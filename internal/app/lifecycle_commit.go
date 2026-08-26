package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	"github.com/alx4j/ai4j/internal/target/claude/catalog"
	validation "github.com/alx4j/ai4j/internal/validate"
	"github.com/alx4j/ai4j/internal/workspace"
)

func (s *lifecycleService) commitExecution(ctx context.Context, command cli.Command, execution lifecycleExecution, policy cli.ConflictPolicy) (cli.Response, error) {
	operationID, err := newOperationID(s.random)
	if err != nil {
		return lifecycleFailure(command, result.FailureInternal, "operation_id_unavailable", "operation could not be prepared", execution.disposition, execution.source.Warnings)
	}
	installationID := recordInstallation(execution.before, execution.desired)
	if installationID == nil {
		return cli.Response{}, errors.New("operation installation identity is unavailable")
	}
	desired := cloneRecordPtr(execution.desired)
	if desired == nil {
		return cli.Response{}, errors.New("operation desired state is unavailable")
	}
	desired.LastOperation = installstate.LastOperation{ID: operationID.String(), Timestamp: s.now().UTC().Truncate(time.Second).Format(time.RFC3339)}
	desired.History = appendUnique(desired.History, operationID.String())
	if len(execution.degradedConflicts) != 0 && policy == cli.ConflictKeep {
		desired.Health = "drifted"
	}
	beforeCatalog, beforeRules, err := s.captureOwned(execution.before)
	if err != nil {
		return lifecycleFailure(command, result.FailureConflict, "rollback_capture_failed", "owned rollback material could not be captured", execution.disposition, execution.source.Warnings)
	}
	if execution.desired.Scope == "project-shared" {
		beforeCatalog = projectSharedOwnedEntry(execution.before)
	}
	afterCatalog, afterRules := execution.catalog, execution.rules
	beforeArtifact := s.currentArtifact(execution.before)
	afterArtifact := slices.Clone(execution.artifact)
	if desired.Lifecycle == "archived" {
		afterCatalog = nil
		afterRules = nil
		afterArtifact = nil
	}
	if desired.Scope == "project-shared" {
		afterCatalog = projectSharedOwnedEntry(desired)
	}
	if execution.before != nil && execution.before.Lifecycle == "active" && len(beforeArtifact) == 0 || desired.Lifecycle == "active" && len(afterArtifact) == 0 {
		return lifecycleFailure(command, result.FailureConflict, "rollback_artifact_unavailable", "exact native rollback material is unavailable", execution.disposition, execution.source.Warnings)
	}
	entry := installstate.HistoryEntry{SchemaVersion: installstate.HistorySchemaVersion, Operation: execution.operation.String(), OperationID: operationID.String(), InstallationID: installationID.String(), Timestamp: desired.LastOperation.Timestamp, Restorable: true, Before: cloneRecordPtr(execution.before), After: cloneRecordPtr(desired), CatalogBefore: beforeCatalog, RulesBefore: beforeRules, CatalogAfter: slices.Clone(afterCatalog), RulesAfter: slices.Clone(afterRules), NativeArtifactBefore: beforeArtifact, NativeArtifactAfter: afterArtifact}
	resources := []string{"history:" + installationID.String(), "owned:state/installation.json"}
	if execution.before != nil {
		resources = append(resources, execution.before.NativeResources...)
		if execution.before.Catalog.Path != "" {
			resources = append(resources, "owned:"+execution.before.Catalog.Path)
		}
		if execution.before.Rules.Path != "" {
			resources = append(resources, "owned:"+execution.before.Rules.Path)
		}
	}
	resources = append(resources, desired.NativeResources...)
	marker, err := installstate.NewResourceMarker(execution.operation.String(), operationID.String(), installationID.String(), cliSourceRevision(execution.source.Source), resources)
	if err != nil {
		return lifecycleFailure(command, result.FailureInternal, "operation_marker_failed", "operation could not be prepared", execution.disposition, execution.source.Warnings)
	}
	if err := s.preflightExecutionCapacity(marker, entry, desired, execution.catalog, execution.rules, execution.artifact); err != nil {
		if code, message, ok := appDiskCapacityProblem(err); ok {
			return lifecycleFailure(command, result.FailureEnvironment, code, message, execution.disposition, execution.source.Warnings)
		}
		return lifecycleFailure(command, result.FailureInternal, "operation_preflight_failed", "operation storage requirements could not be verified", execution.disposition, execution.source.Warnings)
	}
	if s.state.SaveMarker(marker) != nil {
		return lifecycleFailure(command, result.FailureInternal, "operation_marker_failed", "operation could not be prepared", execution.disposition, execution.source.Warnings)
	}
	if err := s.state.StageHistory(entry); err != nil {
		return s.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "history_prepare_failed")
	}
	if err := s.applyTransition(ctx, execution.before, desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifact, policy, execution.rollback != nil); err != nil {
		return s.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "target_mutation_failed")
	}
	if err := s.verifyDesired(ctx, *desired); err != nil {
		return s.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "target_verification_failed")
	}
	if execution.before == nil {
		err = s.state.SaveNew(*desired)
	} else {
		err = s.state.Save(*desired)
	}
	if err != nil {
		return s.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "state_commit_failed")
	}
	entry.After = cloneRecordPtr(desired)
	if err := s.state.CommitHistory(entry); err != nil {
		return s.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "history_commit_failed")
	}
	if err := s.state.DeleteMarker(); err != nil {
		return s.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "operation_cleanup_failed")
	}
	warnings := slices.Clone(execution.source.Warnings)
	if len(execution.degradedConflicts) != 0 {
		warnings = append(warnings, conflictWarnings(execution.degradedConflicts)...)
	}
	return committedResponse(command, execution.operation, operationID, installationID, execution.final, execution.disposition, warnings, execution.actions, len(execution.degradedConflicts) != 0)
}

func appDiskCapacityProblem(err error) (string, string, bool) {
	switch {
	case errors.Is(err, diskcapacity.ErrInsufficient):
		return "insufficient_disk_space", "the destination filesystem does not have enough space for the bounded operation", true
	case errors.Is(err, diskcapacity.ErrUnavailable):
		return "disk_capacity_unavailable", "destination disk capacity could not be verified", true
	default:
		return "", "", false
	}
}

func (s *lifecycleService) preflightExecutionCapacity(marker installstate.Marker, entry installstate.HistoryEntry, desired *installstate.Record, catalogBytes, rulesBytes, artifact []byte) error {
	markerBytes, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	historyBytes, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	stateBytes, err := json.Marshal(desired)
	if err != nil {
		return err
	}
	required := uint64(len(markerBytes) + len(stateBytes) + 2*len(historyBytes))
	if err := diskcapacity.Require(s.state.DataRoot(), required); err != nil {
		return err
	}
	if len(catalogBytes) != 0 {
		if err := diskcapacity.Require(filepath.Dir(s.catalogPath(*desired)), uint64(len(catalogBytes))); err != nil {
			return err
		}
	}
	if len(rulesBytes) != 0 && desired.Rules != (installstate.OwnedFile{}) {
		if err := diskcapacity.Require(filepath.Dir(s.rulesPath(*desired)), uint64(len(rulesBytes))); err != nil {
			return err
		}
	}
	if desired.Source.Mode == "development_source" && len(artifact) != 0 {
		expanded, err := nativeArtifactExpandedBytes(artifact)
		if err != nil {
			return err
		}
		if err := diskcapacity.Require(filepath.Dir(s.catalogPath(*desired)), expanded+uint64(len(artifact))); err != nil {
			return err
		}
	}
	return nil
}

func (s *lifecycleService) applyTransition(ctx context.Context, before *installstate.Record, desired *installstate.Record, catalogBefore, catalogBytes, rulesBytes, artifact []byte, policy cli.ConflictPolicy, rollback bool) error {
	if desired.Scope == "project-shared" {
		return s.applyProjectSharedTransition(ctx, before, desired, catalogBefore, rulesBytes, policy)
	}
	if desired.Lifecycle == "archived" {
		if before == nil || before.Lifecycle != "active" {
			return nil
		}
		if err := s.runClaudeFor(ctx, *before, []string{"plugin", "uninstall", nativePluginID(*before), "--scope", nativeScope(*before), "--keep-data"}); err != nil && policy != cli.ConflictKeep {
			return err
		}
		if err := s.runClaudeFor(ctx, *before, []string{"plugin", "marketplace", "remove", before.MarketplaceID, "--scope", nativeScope(*before)}); err != nil && policy != cli.ConflictKeep {
			return err
		}
		if err := mutateOwned(s.ownedRoot(s.catalogPath(*before)), s.catalogPath(*before), before.Catalog.Checksum, nil, policy); err != nil {
			return err
		}
		if before.Rules != (installstate.OwnedFile{}) {
			if err := mutateOwned(s.ownedRoot(s.rulesPath(*before)), s.rulesPath(*before), before.Rules.Checksum, nil, policy); err != nil {
				return err
			}
		}
		if err := s.removeProjectLocalExclusion(ctx, *before); err != nil {
			return err
		}
		return nil
	}
	if before == nil || before.Lifecycle == "archived" {
		if rollback {
			return s.restoreArtifact(ctx, before, desired, catalogBytes, rulesBytes, artifact, policy)
		}
		if err := s.ensureProjectLocalExclusion(ctx, *desired); err != nil {
			return err
		}
		if desired.Source.Mode == "development_source" {
			if err := s.writeLocalBundle(*desired, catalogBytes, artifact); err != nil {
				return err
			}
		} else {
			if err := writeOwnedNew(s.ownedRoot(s.catalogPath(*desired)), s.catalogPath(*desired), catalogBytes); err != nil {
				return err
			}
		}
		if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", filepath.Dir(filepath.Dir(s.catalogPath(*desired))), "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
		if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "install", nativePluginID(*desired), "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
		if desired.Rules != (installstate.OwnedFile{}) {
			return writeOwnedNew(s.ownedRoot(s.rulesPath(*desired)), s.rulesPath(*desired), rulesBytes)
		}
		return nil
	}
	if rollback {
		return s.restoreArtifact(ctx, before, desired, catalogBytes, rulesBytes, artifact, policy)
	}
	if err := s.ensureProjectLocalExclusion(ctx, *desired); err != nil {
		return err
	}
	if desired.Source.Mode == "development_source" && before.Catalog.Path != desired.Catalog.Path {
		if err := s.writeLocalBundle(*desired, catalogBytes, artifact); err != nil {
			return err
		}
		if err := s.runClaudeFor(ctx, *before, []string{"plugin", "marketplace", "remove", before.MarketplaceID, "--scope", nativeScope(*before)}); err != nil {
			return err
		}
		if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", filepath.Dir(filepath.Dir(s.catalogPath(*desired))), "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
		if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "update", nativePluginID(*desired), "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
	}
	catalogChanged := before.Catalog.Checksum != desired.Catalog.Checksum || inspectFileDrift(s.catalogPath(*before), before.Catalog.Checksum) != cli.DriftUnchanged
	if catalogChanged && before.Catalog.Path == desired.Catalog.Path {
		if err := mutateOwned(s.ownedRoot(s.catalogPath(*before)), s.catalogPath(*before), before.Catalog.Checksum, catalogBytes, policy); err != nil {
			return err
		}
		if policy != cli.ConflictKeep || inspectFileDrift(s.catalogPath(*before), desired.Catalog.Checksum) == cli.DriftUnchanged {
			if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "update", desired.MarketplaceID, "--scope", nativeScope(*desired)}); err != nil {
				return err
			}
			if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "update", nativePluginID(*desired), "--scope", nativeScope(*desired)}); err != nil {
				return err
			}
		}
	}
	switch {
	case before.Rules == (installstate.OwnedFile{}) && desired.Rules != (installstate.OwnedFile{}):
		if err := writeOwnedNew(s.ownedRoot(s.rulesPath(*desired)), s.rulesPath(*desired), rulesBytes); err != nil {
			return err
		}
	case before.Rules != (installstate.OwnedFile{}) && desired.Rules == (installstate.OwnedFile{}):
		if err := mutateOwned(s.ownedRoot(s.rulesPath(*before)), s.rulesPath(*before), before.Rules.Checksum, nil, policy); err != nil {
			return err
		}
	case before.Rules != (installstate.OwnedFile{}) && (before.Rules.Checksum != desired.Rules.Checksum || inspectFileDrift(s.rulesPath(*before), before.Rules.Checksum) != cli.DriftUnchanged):
		if err := mutateOwned(s.ownedRoot(s.rulesPath(*before)), s.rulesPath(*before), before.Rules.Checksum, rulesBytes, policy); err != nil {
			return err
		}
	}
	return nil
}

func (s *lifecycleService) restoreArtifact(ctx context.Context, before *installstate.Record, desired *installstate.Record, catalogBytes, rulesBytes, artifact []byte, policy cli.ConflictPolicy) (returnErr error) {
	if len(artifact) == 0 {
		return errors.New("native rollback artifact is unavailable")
	}
	if err := s.ensureProjectLocalExclusion(ctx, *desired); err != nil {
		return err
	}
	stateRoot := filepath.Dir(s.state.Path())
	recoveryBytes, err := nativeArtifactExpandedBytes(artifact)
	if err != nil {
		return err
	}
	if err := diskcapacity.Require(stateRoot, recoveryBytes+uint64(len(catalogBytes))+uint64(len(rulesBytes))); err != nil {
		return err
	}
	recoveryWorkspace, err := workspace.Create(stateRoot, workspace.Recovery)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, recoveryWorkspace.Close()) }()
	root := recoveryWorkspace.Path()
	if err := unpackNativeArtifact(root, "plugin", artifact); err != nil {
		return err
	}
	if desired.Source.Mode == "development_source" {
		if err := s.writeLocalBundle(*desired, catalogBytes, artifact); err != nil {
			return err
		}
	}
	localCatalog, err := catalog.RenderLocalPackage(desired.MarketplaceID, desired.PluginID, "plugin", "AI4J retained rollback package "+desired.PluginID)
	if err != nil {
		return err
	}
	localCatalogPath := filepath.Join(root, ".claude-plugin", "marketplace.json")
	if err := writeOwnedNew(s.ownedRoot(localCatalogPath), localCatalogPath, localCatalog.Bytes()); err != nil {
		return err
	}
	if before != nil && before.Lifecycle == "active" {
		if err := s.runClaudeFor(ctx, *before, []string{"plugin", "uninstall", nativePluginID(*before), "--scope", nativeScope(*before), "--keep-data"}); err != nil {
			return err
		}
		if err := s.runClaudeFor(ctx, *before, []string{"plugin", "marketplace", "remove", before.MarketplaceID, "--scope", nativeScope(*before)}); err != nil {
			return err
		}
		if before.Catalog.Path == desired.Catalog.Path {
			if err := mutateOwned(s.ownedRoot(s.catalogPath(*before)), s.catalogPath(*before), before.Catalog.Checksum, catalogBytes, policy); err != nil {
				return err
			}
		} else if desired.Source.Mode != "development_source" {
			if err := writeOwnedNew(s.ownedRoot(s.catalogPath(*desired)), s.catalogPath(*desired), catalogBytes); err != nil {
				return err
			}
		}
	} else if desired.Source.Mode != "development_source" {
		if err := writeOwnedNew(s.ownedRoot(s.catalogPath(*desired)), s.catalogPath(*desired), catalogBytes); err != nil {
			return err
		}
	}
	if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", root, "--scope", nativeScope(*desired)}); err != nil {
		return err
	}
	if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "install", nativePluginID(*desired), "--scope", nativeScope(*desired)}); err != nil {
		return err
	}
	if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "remove", desired.MarketplaceID, "--scope", nativeScope(*desired)}); err != nil {
		return err
	}
	if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", filepath.Dir(filepath.Dir(s.catalogPath(*desired))), "--scope", nativeScope(*desired)}); err != nil {
		return err
	}
	switch {
	case before == nil || before.Lifecycle == "archived":
		if desired.Rules != (installstate.OwnedFile{}) {
			return writeOwnedNew(s.ownedRoot(s.rulesPath(*desired)), s.rulesPath(*desired), rulesBytes)
		}
	case before.Rules == (installstate.OwnedFile{}) && desired.Rules != (installstate.OwnedFile{}):
		return writeOwnedNew(s.ownedRoot(s.rulesPath(*desired)), s.rulesPath(*desired), rulesBytes)
	case before.Rules != (installstate.OwnedFile{}) && desired.Rules == (installstate.OwnedFile{}):
		return mutateOwned(s.ownedRoot(s.rulesPath(*before)), s.rulesPath(*before), before.Rules.Checksum, nil, policy)
	case before.Rules != (installstate.OwnedFile{}):
		return mutateOwned(s.ownedRoot(s.rulesPath(*before)), s.rulesPath(*before), before.Rules.Checksum, rulesBytes, policy)
	}
	return nil
}

func (s *lifecycleService) writeLocalBundle(record installstate.Record, catalogBytes, artifact []byte) error {
	root := filepath.Dir(filepath.Dir(s.catalogPath(record)))
	descriptorBytes, err := localBundleDescriptor(record, artifact)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(s.catalogPath(record)); err == nil && info.Mode().IsRegular() {
		descriptorPath := filepath.Join(root, ".ai4j-bundle.json")
		descriptor, readErr := os.ReadFile(descriptorPath)
		pluginInfo, pluginErr := os.Lstat(filepath.Join(root, "plugin", ".claude-plugin", "plugin.json"))
		if inspectFileDrift(s.catalogPath(record), record.Catalog.Checksum) == cli.DriftUnchanged && readErr == nil && bytes.Equal(descriptor, descriptorBytes) && pluginErr == nil && pluginInfo.Mode().IsRegular() {
			return nil
		}
		return errors.New("local install-backing bundle is not immutable")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Lstat(root); err == nil {
		return errors.New("local install-backing bundle destination is occupied")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	expanded, err := nativeArtifactExpandedBytes(artifact)
	if err != nil {
		return err
	}
	if err := diskcapacity.Require(root, expanded+uint64(len(catalogBytes))+uint64(len(descriptorBytes))); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := unpackNativeArtifact(root, "plugin", artifact); err != nil {
		return err
	}
	if err := writeOwnedNew(s.ownedRoot(filepath.Join(root, ".ai4j-bundle.json")), filepath.Join(root, ".ai4j-bundle.json"), descriptorBytes); err != nil {
		return err
	}
	return writeOwnedNew(s.ownedRoot(s.catalogPath(record)), s.catalogPath(record), catalogBytes)
}

func localBundleDescriptor(record installstate.Record, artifact []byte) ([]byte, error) {
	artifactDigest := sha256.Sum256(artifact)
	descriptor := struct {
		SchemaVersion  int      `json:"schemaVersion"`
		BundleDigest   string   `json:"bundleDigest"`
		SourceDigest   string   `json:"sourceDigest"`
		RenderedDigest string   `json:"renderedDigest"`
		ToolkitID      string   `json:"toolkitId"`
		PackageID      string   `json:"packageId"`
		Selection      []string `json:"selection"`
		ArtifactDigest string   `json:"artifactDigest"`
		Adapter        string   `json:"adapter"`
	}{1, record.Source.BundleDigest, record.Source.SourceDigest, record.Source.RenderedDigest, record.ToolkitID, record.PluginID, slices.Clone(record.Selection.Resolved), hex.EncodeToString(artifactDigest[:]), "claude-user"}
	contents, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return nil, err
	}
	contents = append(contents, '\n')
	return contents, nil
}

func unpackNativeArtifact(root, destination string, artifact []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(artifact), int64(len(artifact)))
	if err != nil || len(reader.File) == 0 || len(reader.File) > 4096 {
		return errors.New("native rollback artifact is invalid")
	}
	var total int
	for _, file := range reader.File {
		clean := filepath.Clean(filepath.FromSlash(file.Name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || file.FileInfo().IsDir() || !file.Mode().IsRegular() {
			return errors.New("native rollback artifact contains an unsafe path")
		}
		path := filepath.Join(root, destination, clean)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		input, err := file.Open()
		if err != nil {
			return err
		}
		content, readErr := io.ReadAll(io.LimitReader(input, 16<<20))
		closeErr := input.Close()
		if readErr != nil || closeErr != nil {
			return errors.New("native rollback artifact could not be read")
		}
		total += len(content)
		if total > 16<<20 {
			return errors.New("native rollback artifact is too large")
		}
		mode := os.FileMode(0o600)
		if file.Mode()&0o111 != 0 {
			mode = 0o700
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			return err
		}
	}
	return nil
}

func nativeArtifactExpandedBytes(artifact []byte) (uint64, error) {
	reader, err := zip.NewReader(bytes.NewReader(artifact), int64(len(artifact)))
	if err != nil || len(reader.File) == 0 || len(reader.File) > 4096 {
		return 0, errors.New("native rollback artifact is invalid")
	}
	var total uint64
	for _, file := range reader.File {
		if file.UncompressedSize64 > 16<<20 || total > 16<<20-file.UncompressedSize64 {
			return 0, errors.New("native rollback artifact is too large")
		}
		total += file.UncompressedSize64
	}
	return total, nil
}

func (s *lifecycleService) currentArtifact(record *installstate.Record) []byte {
	if record == nil || record.Lifecycle != "active" {
		return nil
	}
	entries, err := s.state.LoadHistory(record.InstallationID)
	if err != nil {
		return nil
	}
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.After != nil && recordSourceRevision(*entry.After) == recordSourceRevision(*record) && entry.After.Source.RenderedDigest == record.Source.RenderedDigest && len(entry.NativeArtifactAfter) != 0 {
			return slices.Clone(entry.NativeArtifactAfter)
		}
	}
	return nil
}

func (s *lifecycleService) verifyDesired(ctx context.Context, desired installstate.Record) error {
	native, problem := s.inspectNative(ctx, desired)
	if problem != nil {
		return errors.New("native state could not be verified")
	}
	if desired.Lifecycle == "archived" {
		if native.MarketplaceRegistered || native.PluginInstalled {
			return errors.New("archived native state is still present")
		}
		if desired.Scope == "project-shared" {
			if !projectMarketplaceAbsent(desired) {
				return errors.New("shared project marketplace is unexpectedly present")
			}
		}
		return nil
	}
	if !native.MarketplaceRegistered || !native.PluginInstalled || !native.PluginEnabled {
		return errors.New("active native state is incomplete")
	}
	if desired.Health == "drifted" {
		return nil
	}
	if desired.Scope == "project-shared" {
		if inspectProjectMarketplaceDrift(desired) != cli.DriftUnchanged {
			return errors.New("catalog state does not match")
		}
	} else if inspectFileDrift(s.catalogPath(desired), desired.Catalog.Checksum) != cli.DriftUnchanged {
		return errors.New("catalog state does not match")
	}
	if desired.Rules != (installstate.OwnedFile{}) && inspectFileDrift(s.rulesPath(desired), desired.Rules.Checksum) != cli.DriftUnchanged {
		return errors.New("rules state does not match")
	}
	return nil
}

func (s *lifecycleService) inspectNative(ctx context.Context, record installstate.Record) (validation.NativeStatus, *result.Problem) {
	return s.validation.InspectNativeStatusAt(ctx, nativeDirectory(record), record.MarketplaceID, nativePluginID(record))
}

func mutateOwned(home, path, expected string, contents []byte, policy cli.ConflictPolicy) error {
	drift := inspectFileDrift(path, expected)
	if drift != cli.DriftUnchanged {
		switch policy {
		case cli.ConflictKeep:
			return nil
		case cli.ConflictReplaceOwned:
			if errors.Is(fileRegular(path), os.ErrNotExist) {
				if contents == nil {
					return nil
				}
				return writeOwnedNew(home, path, contents)
			}
		default:
			return errors.New("owned file does not match installation state")
		}
	}
	if err := validateOwnedPath(home, path); err != nil {
		return err
	}
	if contents == nil {
		return os.Remove(path)
	}
	return replaceOwnedAny(home, path, contents)
}

func replaceOwnedAny(home, path string, contents []byte) error {
	if err := validateOwnedPath(home, path); err != nil {
		return err
	}
	if err := diskcapacity.Require(filepath.Dir(path), uint64(len(contents))); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ai4j-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if temporary.Chmod(0o600) != nil {
		return errors.New("owned replacement could not be secured")
	}
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return commitOwnedReplacement(temporaryPath, path)
}

func fileRegular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("owned path is not a regular file")
	}
	return nil
}

func (s *lifecycleService) captureOwned(record *installstate.Record) ([]byte, []byte, error) {
	if record == nil || record.Lifecycle != "active" {
		return nil, nil, nil
	}
	catalogBytes, err := readOwnedOpaque(s.catalogPath(*record))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	var rulesBytes []byte
	if record.Rules != (installstate.OwnedFile{}) {
		rulesBytes, err = readOwnedOpaque(s.rulesPath(*record))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
	}
	return catalogBytes, rulesBytes, nil
}

func readOwnedOpaque(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, errors.New("owned rollback material is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, 16<<20))
}
