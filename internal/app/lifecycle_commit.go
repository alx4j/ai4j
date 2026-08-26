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
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	"github.com/alx4j/ai4j/internal/target/claude/catalog"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func (s *lifecycleService) commitExecution(ctx context.Context, command cli.Command, execution lifecycleExecution, policy cli.ConflictPolicy, commandIO CommandIO) (cli.Response, error) {
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
	beforeArtifacts := s.currentArtifacts(execution.before)
	if len(beforeArtifacts) == 0 {
		beforeArtifacts = cloneNativeArtifacts(execution.beforeArtifacts)
	}
	afterArtifacts := cloneNativeArtifacts(execution.artifacts)
	if desired.Lifecycle == "archived" {
		afterCatalog = nil
		afterRules = nil
		afterArtifacts = nil
	}
	if desired.Scope == "project-shared" {
		afterCatalog = projectSharedOwnedEntry(desired)
	}
	restorable := !execution.nonRestorable
	if !restorable {
		beforeArtifacts = nil
	}
	if (restorable && execution.before != nil && execution.before.Lifecycle == "active" && len(beforeArtifacts) == 0) || (desired.Lifecycle == "active" && len(afterArtifacts) == 0) {
		return lifecycleFailure(command, result.FailureConflict, "rollback_artifact_unavailable", "exact native rollback material is unavailable", execution.disposition, execution.source.Warnings)
	}
	if err := s.preflightProjectSharedTransition(execution.before, desired, policy); err != nil {
		return lifecycleFailure(command, result.FailureConflict, "target_preflight_failed", "project-shared owned state changed before mutation", execution.disposition, execution.source.Warnings)
	}
	entry := installstate.HistoryEntry{SchemaVersion: installstate.HistorySchemaVersion, Operation: execution.operation.String(), OperationID: operationID.String(), InstallationID: installationID.String(), Timestamp: desired.LastOperation.Timestamp, Restorable: restorable, Before: cloneRecordPtr(execution.before), After: cloneRecordPtr(desired), CatalogBefore: beforeCatalog, RulesBefore: beforeRules, CatalogAfter: slices.Clone(afterCatalog), RulesAfter: slices.Clone(afterRules), NativeArtifactsBefore: beforeArtifacts, NativeArtifactsAfter: afterArtifacts}
	resources := []string{"history:" + installationID.String(), "owned:state/installation.json"}
	if execution.before != nil {
		resources = append(resources, execution.before.NativeResources...)
		if execution.before.Catalog.Path != "" {
			resources = append(resources, "owned:"+execution.before.Catalog.Path)
		}
		if execution.before.NativeCatalog.Path != "" {
			resources = append(resources, "owned:"+execution.before.NativeCatalog.Path)
		}
		if execution.before.Rules.Path != "" {
			resources = append(resources, "owned:"+execution.before.Rules.Path)
		}
	}
	resources = append(resources, desired.NativeResources...)
	if desired.Catalog.Path != "" {
		resources = append(resources, "owned:"+desired.Catalog.Path)
	}
	if desired.NativeCatalog.Path != "" {
		resources = append(resources, "owned:"+desired.NativeCatalog.Path)
	}
	if desired.Rules.Path != "" {
		resources = append(resources, "owned:"+desired.Rules.Path)
	}
	sourceRevision := recordSourceRevision(*desired)
	marker, err := installstate.NewResourceMarker(execution.operation.String(), operationID.String(), installationID.String(), sourceRevision, resources)
	if err != nil {
		return lifecycleFailure(command, result.FailureInternal, "operation_marker_failed", "operation could not be prepared", execution.disposition, execution.source.Warnings)
	}
	reportProgress(commandIO, "checking available disk space...")
	if err := s.preflightExecutionCapacity(marker, entry, desired, execution.catalog, execution.rules, execution.artifacts); err != nil {
		if code, message, ok := appDiskCapacityProblem(err); ok {
			return lifecycleFailure(command, result.FailureEnvironment, code, message, execution.disposition, execution.source.Warnings)
		}
		return lifecycleFailure(command, result.FailureInternal, "operation_preflight_failed", "operation storage requirements could not be verified", execution.disposition, execution.source.Warnings)
	}
	if err := s.preflightCatalogDestination(execution.before, *desired, execution.catalog, execution.artifacts); err != nil {
		return lifecycleFailure(command, result.FailureConflict, "catalog_destination_changed", "the catalog destination is occupied, unsafe, or no longer matches retained content", execution.disposition, execution.source.Warnings)
	}
	if err := s.preflightOwnedTransition(execution.before, desired, policy); err != nil {
		return lifecycleFailure(command, result.FailureConflict, "target_preflight_failed", "installation-owned state changed before mutation", execution.disposition, execution.source.Warnings)
	}
	if err := s.state.StageHistory(entry); err != nil {
		return lifecycleFailure(command, result.FailureInternal, "history_prepare_failed", "operation recovery history could not be prepared", execution.disposition, execution.source.Warnings)
	}
	if s.state.SaveMarker(marker) != nil {
		_ = s.state.DeleteHistory(installationID.String(), []string{operationID.String()})
		return lifecycleFailure(command, result.FailureInternal, "operation_marker_failed", "operation could not be prepared", execution.disposition, execution.source.Warnings)
	}
	if err := s.applyTransition(ctx, execution.before, desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifacts, policy, execution.rollback != nil); err != nil {
		return s.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "target_mutation_failed")
	}
	reportProgress(commandIO, "verifying the final installation state...")
	if err := s.verifyTransition(ctx, *desired, execution.before); err != nil {
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

func (s *lifecycleService) preflightExecutionCapacity(marker installstate.Marker, entry installstate.HistoryEntry, desired *installstate.Record, catalogBytes, rulesBytes []byte, artifacts []installstate.NativeArtifact) error {
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
	if desired.Scope == "project-shared" && desired.Lifecycle == "active" {
		nativeCatalog, err := projectSharedNativeCatalog(*desired)
		if err != nil {
			return err
		}
		if err := diskcapacity.Require(filepath.Dir(s.projectSharedNativeCatalogPath(*desired)), uint64(len(nativeCatalog))); err != nil {
			return err
		}
	}
	if len(rulesBytes) != 0 && desired.Rules != (installstate.OwnedFile{}) {
		if err := diskcapacity.Require(filepath.Dir(s.rulesPath(*desired)), uint64(len(rulesBytes))); err != nil {
			return err
		}
	}
	if isLocalBundleCatalog(desired.Catalog.Path) && len(artifacts) != 0 {
		expanded, compressed, err := nativeArtifactsSize(artifacts)
		if err != nil {
			return err
		}
		if err := diskcapacity.Require(filepath.Dir(s.catalogPath(*desired)), expanded+compressed); err != nil {
			return err
		}
	}
	return nil
}

func (s *lifecycleService) applyTransition(ctx context.Context, before *installstate.Record, desired *installstate.Record, catalogBefore, catalogBytes, rulesBytes []byte, artifacts []installstate.NativeArtifact, policy cli.ConflictPolicy, rollback bool) error {
	if desired.Scope == "project-shared" {
		return s.applyProjectSharedTransition(ctx, before, desired, catalogBefore, rulesBytes, policy)
	}
	if desired.Lifecycle == "archived" {
		if before == nil || before.Lifecycle != "active" {
			return nil
		}
		for _, pluginID := range nativePluginIDs(*before) {
			if err := s.runClaudeFor(ctx, *before, []string{"plugin", "uninstall", pluginID, "--scope", nativeScope(*before), "--keep-data"}); err != nil {
				return err
			}
		}
		if err := s.runClaudeFor(ctx, *before, []string{"plugin", "marketplace", "remove", before.MarketplaceID, "--scope", nativeScope(*before)}); err != nil {
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
			return s.restoreArtifacts(ctx, before, desired, catalogBytes, rulesBytes, artifacts, policy)
		}
		if err := s.addProjectLocalExclusion(ctx, *desired); err != nil {
			return err
		}
		if desired.Source.Mode == "development_source" {
			if err := s.writeLocalBundle(*desired, catalogBytes, artifacts); err != nil {
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
		for _, pluginID := range nativePluginIDs(*desired) {
			if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "install", pluginID, "--scope", nativeScope(*desired)}); err != nil {
				return err
			}
		}
		if desired.Rules != (installstate.OwnedFile{}) {
			return writeOwnedNew(s.ownedRoot(s.rulesPath(*desired)), s.rulesPath(*desired), rulesBytes)
		}
		return nil
	}
	if rollback {
		return s.restoreArtifacts(ctx, before, desired, catalogBytes, rulesBytes, artifacts, policy)
	}
	var exclusionErr error
	if before == nil || before.Lifecycle == "archived" || before.Rules == (installstate.OwnedFile{}) && desired.Rules != (installstate.OwnedFile{}) {
		exclusionErr = s.addProjectLocalExclusion(ctx, *desired)
	} else if desired.Rules != (installstate.OwnedFile{}) {
		exclusionErr = s.ensureProjectLocalExclusion(ctx, *desired)
	}
	if exclusionErr != nil {
		return exclusionErr
	}
	catalogPathChanged := before.Catalog.Path != desired.Catalog.Path
	if catalogPathChanged {
		if err := s.preflightCatalogDestination(before, *desired, catalogBytes, artifacts); err != nil {
			return err
		}
		if desired.Source.Mode == "development_source" {
			if err := s.writeLocalBundle(*desired, catalogBytes, artifacts); err != nil {
				return err
			}
		}
		for _, pkg := range before.Packages {
			if err := s.runClaudeFor(ctx, *before, []string{"plugin", "uninstall", nativePluginID(pkg, before.MarketplaceID), "--scope", nativeScope(*before), "--keep-data"}); err != nil {
				return err
			}
		}
		if desired.Source.Mode != "development_source" {
			if err := writeOwnedNew(s.ownedRoot(s.catalogPath(*desired)), s.catalogPath(*desired), catalogBytes); err != nil {
				return err
			}
		}
		if err := s.runClaudeFor(ctx, *before, []string{"plugin", "marketplace", "remove", before.MarketplaceID, "--scope", nativeScope(*before)}); err != nil {
			return err
		}
		if err := mutateOwned(s.ownedRoot(s.catalogPath(*before)), s.catalogPath(*before), before.Catalog.Checksum, nil, policy); err != nil {
			return err
		}
		if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", filepath.Dir(filepath.Dir(s.catalogPath(*desired))), "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
		for _, pluginID := range nativePluginIDs(*desired) {
			if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "install", pluginID, "--scope", nativeScope(*desired)}); err != nil {
				return err
			}
		}
	}
	catalogChanged := catalogTransitionNeeded(s, *before, *desired)
	if catalogChanged && !catalogPathChanged {
		if policy == cli.ConflictKeep && inspectFileDrift(s.catalogPath(*before), before.Catalog.Checksum) != cli.DriftUnchanged {
			return errors.New("catalog drift is preserved by conflict policy")
		}
		for _, pkg := range before.Packages {
			if err := s.runClaudeFor(ctx, *before, []string{"plugin", "uninstall", nativePluginID(pkg, before.MarketplaceID), "--scope", nativeScope(*before), "--keep-data"}); err != nil {
				return err
			}
		}
		if err := mutateOwned(s.ownedRoot(s.catalogPath(*before)), s.catalogPath(*before), before.Catalog.Checksum, catalogBytes, policy); err != nil {
			return err
		}
		if policy != cli.ConflictKeep || inspectFileDrift(s.catalogPath(*before), desired.Catalog.Checksum) == cli.DriftUnchanged {
			if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "update", desired.MarketplaceID, "--scope", nativeScope(*desired)}); err != nil {
				return err
			}
			for _, pkg := range desired.Packages {
				if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "install", nativePluginID(pkg, desired.MarketplaceID), "--scope", nativeScope(*desired)}); err != nil {
					return err
				}
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
		if err := s.removeProjectLocalExclusion(ctx, *before); err != nil {
			return err
		}
	case before.Rules != (installstate.OwnedFile{}) && (before.Rules.Checksum != desired.Rules.Checksum || inspectFileDrift(s.rulesPath(*before), before.Rules.Checksum) != cli.DriftUnchanged):
		if err := mutateOwned(s.ownedRoot(s.rulesPath(*before)), s.rulesPath(*before), before.Rules.Checksum, rulesBytes, policy); err != nil {
			return err
		}
	}
	return nil
}

func (s *lifecycleService) restoreArtifacts(ctx context.Context, before *installstate.Record, desired *installstate.Record, catalogBytes, rulesBytes []byte, artifacts []installstate.NativeArtifact, policy cli.ConflictPolicy) error {
	if len(artifacts) == 0 {
		return errors.New("native rollback artifact is unavailable")
	}
	var exclusionErr error
	if before == nil || before.Lifecycle == "archived" || before.Rules == (installstate.OwnedFile{}) && desired.Rules != (installstate.OwnedFile{}) {
		exclusionErr = s.addProjectLocalExclusion(ctx, *desired)
	} else if desired.Rules != (installstate.OwnedFile{}) {
		exclusionErr = s.ensureProjectLocalExclusion(ctx, *desired)
	}
	if exclusionErr != nil {
		return exclusionErr
	}
	if err := s.writeLocalBundle(*desired, catalogBytes, artifacts); err != nil {
		return err
	}
	if before != nil && before.Lifecycle == "active" {
		for _, pluginID := range nativePluginIDs(*before) {
			if err := s.runClaudeFor(ctx, *before, []string{"plugin", "uninstall", pluginID, "--scope", nativeScope(*before), "--keep-data"}); err != nil {
				return err
			}
		}
		if err := s.runClaudeFor(ctx, *before, []string{"plugin", "marketplace", "remove", before.MarketplaceID, "--scope", nativeScope(*before)}); err != nil {
			return err
		}
		if before.Catalog.Path != desired.Catalog.Path {
			if err := mutateOwned(s.ownedRoot(s.catalogPath(*before)), s.catalogPath(*before), before.Catalog.Checksum, nil, policy); err != nil {
				return err
			}
		}
	}
	if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", filepath.Dir(filepath.Dir(s.catalogPath(*desired))), "--scope", nativeScope(*desired)}); err != nil {
		return err
	}
	for _, pluginID := range nativePluginIDs(*desired) {
		if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "install", pluginID, "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
	}
	switch {
	case before == nil || before.Lifecycle == "archived":
		if desired.Rules != (installstate.OwnedFile{}) {
			return writeOwnedNew(s.ownedRoot(s.rulesPath(*desired)), s.rulesPath(*desired), rulesBytes)
		}
	case before.Rules == (installstate.OwnedFile{}) && desired.Rules != (installstate.OwnedFile{}):
		return writeOwnedNew(s.ownedRoot(s.rulesPath(*desired)), s.rulesPath(*desired), rulesBytes)
	case before.Rules != (installstate.OwnedFile{}) && desired.Rules == (installstate.OwnedFile{}):
		if err := mutateOwned(s.ownedRoot(s.rulesPath(*before)), s.rulesPath(*before), before.Rules.Checksum, nil, policy); err != nil {
			return err
		}
		return s.removeProjectLocalExclusion(ctx, *before)
	case before.Rules != (installstate.OwnedFile{}):
		return mutateOwned(s.ownedRoot(s.rulesPath(*before)), s.rulesPath(*before), before.Rules.Checksum, rulesBytes, policy)
	}
	return nil
}

func (s *lifecycleService) writeLocalBundle(record installstate.Record, catalogBytes []byte, artifacts []installstate.NativeArtifact) error {
	root := filepath.Dir(filepath.Dir(s.catalogPath(record)))
	descriptorBytes, err := localBundleDescriptor(record, artifacts)
	if err != nil {
		return err
	}
	rootInfo, rootErr := os.Lstat(root)
	if rootErr == nil {
		if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || hostPathUnsafe(root) {
			return errors.New("local install-backing bundle destination is occupied")
		}
		catalogPresent, verifyErr := verifyLocalBundleTree(root, record, catalogBytes, descriptorBytes, artifacts)
		if verifyErr != nil {
			return errors.New("local install-backing bundle is not immutable")
		}
		if catalogPresent {
			return nil
		}
		return writeOwnedNew(s.ownedRoot(s.catalogPath(record)), s.catalogPath(record), catalogBytes)
	}
	if !errors.Is(rootErr, os.ErrNotExist) {
		return rootErr
	}
	expanded, compressed, err := nativeArtifactsSize(artifacts)
	if err != nil {
		return err
	}
	if err := diskcapacity.Require(root, expanded+compressed+uint64(len(catalogBytes))+uint64(len(descriptorBytes))); err != nil {
		return err
	}
	parent := filepath.Dir(root)
	if err := ensureOwnedDirectory(s.state.DataRoot(), parent); err != nil {
		return err
	}
	temporaryRoot, err := os.MkdirTemp(parent, ".ai4j-bundle-")
	if err != nil {
		return err
	}
	if err := os.Chmod(temporaryRoot, 0o700); err != nil {
		_ = os.RemoveAll(temporaryRoot)
		return err
	}
	defer func() {
		if temporaryRoot != "" {
			_ = os.RemoveAll(temporaryRoot)
		}
	}()
	for _, artifact := range artifacts {
		if err := unpackNativeArtifact(temporaryRoot, filepath.Join("plugins", artifact.PackageID), artifact.Bytes); err != nil {
			return err
		}
	}
	if err := writeOwnedNew(temporaryRoot, filepath.Join(temporaryRoot, ".ai4j-bundle.json"), descriptorBytes); err != nil {
		return err
	}
	relativeCatalog, err := filepath.Rel(root, s.catalogPath(record))
	if err != nil || relativeCatalog == "." || relativeCatalog == ".." || strings.HasPrefix(relativeCatalog, ".."+string(filepath.Separator)) {
		return errors.New("local bundle catalog path is invalid")
	}
	if err := writeOwnedNew(temporaryRoot, filepath.Join(temporaryRoot, relativeCatalog), catalogBytes); err != nil {
		return err
	}
	if err := os.Rename(temporaryRoot, root); err != nil {
		return err
	}
	temporaryRoot = ""
	return nil
}

func (s *lifecycleService) preflightCatalogDestination(before *installstate.Record, desired installstate.Record, catalogBytes []byte, artifacts []installstate.NativeArtifact) error {
	if desired.Lifecycle != "active" || desired.Scope == "project-shared" {
		return nil
	}
	if before != nil && before.Lifecycle == "active" && before.Catalog.Path == desired.Catalog.Path {
		return nil
	}
	path := s.catalogPath(desired)
	if !isLocalBundleCatalog(desired.Catalog.Path) {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		return errors.New("catalog destination is occupied")
	}
	root := filepath.Dir(filepath.Dir(path))
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || hostPathUnsafe(root) {
		return errors.New("local install-backing bundle destination is occupied")
	}
	descriptorBytes, err := localBundleDescriptor(desired, artifacts)
	if err != nil {
		return err
	}
	_, err = verifyLocalBundleTree(root, desired, catalogBytes, descriptorBytes, artifacts)
	return err
}

func (s *lifecycleService) preflightOwnedTransition(before, desired *installstate.Record, policy cli.ConflictPolicy) error {
	if desired == nil || desired.Scope == "project-shared" {
		return nil
	}
	var beforeCatalogPath, beforeRulesPath string
	if before != nil && before.Lifecycle == "active" {
		if before.Catalog != (installstate.OwnedFile{}) {
			beforeCatalogPath = s.catalogPath(*before)
			if err := preflightOwnedCurrent(s.ownedRoot(beforeCatalogPath), beforeCatalogPath, before.Catalog.Checksum, policy); err != nil {
				return errors.New("catalog does not match installation state")
			}
		}
		if before.Rules != (installstate.OwnedFile{}) {
			beforeRulesPath = s.rulesPath(*before)
			if err := preflightOwnedCurrent(s.ownedRoot(beforeRulesPath), beforeRulesPath, before.Rules.Checksum, policy); err != nil {
				return errors.New("rules do not match installation state")
			}
		}
	}
	if desired.Lifecycle != "active" {
		return nil
	}
	desiredCatalogPath := s.catalogPath(*desired)
	if beforeCatalogPath == "" || filepath.Clean(beforeCatalogPath) != filepath.Clean(desiredCatalogPath) {
		if err := validateOwnedDestination(s.ownedRoot(desiredCatalogPath), desiredCatalogPath); err != nil {
			return errors.New("catalog destination is occupied or unsafe")
		}
	}
	if desired.Rules == (installstate.OwnedFile{}) {
		return nil
	}
	desiredRulesPath := s.rulesPath(*desired)
	if beforeRulesPath == "" || filepath.Clean(beforeRulesPath) != filepath.Clean(desiredRulesPath) {
		if err := validateOwnedDestination(s.ownedRoot(desiredRulesPath), desiredRulesPath); err != nil {
			return errors.New("rules destination is occupied or unsafe")
		}
	}
	return nil
}

func preflightOwnedCurrent(root, path, expected string, policy cli.ConflictPolicy) error {
	drift := inspectFileDrift(path, expected)
	switch drift {
	case cli.DriftUnchanged:
		return validateOwnedPath(root, path)
	case cli.DriftMissing:
		if policy == cli.ConflictReplaceOwned {
			return validateOwnedDestination(root, path)
		}
	case cli.DriftModified:
		if policy == cli.ConflictReplaceOwned {
			return validateOwnedPath(root, path)
		}
	}
	return errors.New("owned file does not match installation state")
}

func validateOwnedDestination(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("owned path is outside its root")
	}
	current := root
	for _, component := range strings.Split(filepath.Dir(relative), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || hostPathUnsafe(current) {
			return errors.New("owned path parent is unsafe")
		}
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("owned destination is occupied")
}

func verifyLocalBundleTree(root string, record installstate.Record, catalogBytes, descriptorBytes []byte, artifacts []installstate.NativeArtifact) (bool, error) {
	catalogPath := filepath.Join(root, ".claude-plugin", "marketplace.json")
	catalogInfo, catalogErr := os.Lstat(catalogPath)
	if catalogErr != nil && !errors.Is(catalogErr, os.ErrNotExist) {
		return false, catalogErr
	}
	catalogPresent := catalogErr == nil
	if catalogPresent && (!catalogInfo.Mode().IsRegular() || catalogInfo.Mode()&os.ModeSymlink != 0) {
		return false, errors.New("local bundle catalog is unsafe")
	}
	expected, err := expectedLocalBundleFiles(record, catalogBytes, descriptorBytes, artifacts, catalogPresent)
	if err != nil {
		return false, err
	}
	seen := make(map[string]struct{}, len(expected))
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || hostPathUnsafe(path) {
			return errors.New("local bundle contains a symbolic link")
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("local bundle contains a non-regular file")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		expectedFile, ok := expected[relative]
		if !ok {
			return errors.New("local bundle contains an unexpected file")
		}
		contents, err := readLocalBundleFile(path)
		if err != nil || sha256.Sum256(contents) != expectedFile.digest {
			return errors.New("local bundle file content does not match")
		}
		if runtime.GOOS != "windows" && (info.Mode()&0o111 != 0) != expectedFile.executable {
			return errors.New("local bundle file mode does not match")
		}
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil || len(seen) != len(expected) {
		if err != nil {
			return false, err
		}
		return false, errors.New("local bundle is incomplete")
	}
	return catalogPresent, nil
}

func readLocalBundleFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(contents) > 16<<20 {
		return nil, errors.New("local bundle file is too large")
	}
	return contents, nil
}

type localBundleFile struct {
	digest     [32]byte
	executable bool
}

func expectedLocalBundleFiles(record installstate.Record, catalogBytes, descriptorBytes []byte, artifacts []installstate.NativeArtifact, includeCatalog bool) (map[string]localBundleFile, error) {
	if len(record.Packages) != len(artifacts) {
		return nil, errors.New("local bundle artifacts do not match packages")
	}
	expected := map[string]localBundleFile{
		".ai4j-bundle.json": {digest: sha256.Sum256(descriptorBytes)},
	}
	if includeCatalog {
		expected[filepath.Join(".claude-plugin", "marketplace.json")] = localBundleFile{digest: sha256.Sum256(catalogBytes)}
	}
	for index, artifact := range artifacts {
		if artifact.PackageID != record.Packages[index].ID {
			return nil, errors.New("local bundle artifacts do not match packages")
		}
		reader, err := zip.NewReader(bytes.NewReader(artifact.Bytes), int64(len(artifact.Bytes)))
		if err != nil || len(reader.File) == 0 || len(reader.File) > 4096 {
			return nil, errors.New("native rollback artifact is invalid")
		}
		var total int
		for _, file := range reader.File {
			clean := filepath.Clean(filepath.FromSlash(file.Name))
			if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || file.FileInfo().IsDir() || !file.Mode().IsRegular() {
				return nil, errors.New("native rollback artifact contains an unsafe path")
			}
			relative := filepath.Join("plugins", artifact.PackageID, clean)
			if _, exists := expected[relative]; exists {
				return nil, errors.New("native rollback artifact contains a duplicate path")
			}
			input, err := file.Open()
			if err != nil {
				return nil, err
			}
			contents, readErr := io.ReadAll(io.LimitReader(input, (16<<20)+1))
			closeErr := input.Close()
			if readErr != nil || closeErr != nil || len(contents) > 16<<20 {
				return nil, errors.New("native rollback artifact could not be read")
			}
			total += len(contents)
			if total > 16<<20 {
				return nil, errors.New("native rollback artifact is too large")
			}
			expected[relative] = localBundleFile{digest: sha256.Sum256(contents), executable: file.Mode()&0o111 != 0}
		}
	}
	return expected, nil
}

func ensureOwnedDirectory(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("owned directory is outside its root")
	}
	current := root
	components := []string{}
	if relative != "." {
		components = strings.Split(relative, string(filepath.Separator))
	}
	for _, component := range components {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
		case statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || hostPathUnsafe(current):
			return errors.New("owned directory is unsafe")
		}
	}
	return nil
}

func retainedRollbackCatalog(record installstate.Record, artifacts []installstate.NativeArtifact) ([]byte, installstate.OwnedFile, error) {
	if len(record.Packages) == 0 || len(record.Packages) != len(artifacts) {
		return nil, installstate.OwnedFile{}, errors.New("retained rollback packages are incomplete")
	}
	packages := make([]catalog.Package, len(record.Packages))
	digestInput := []string{"retained-rollback", record.InstallationID, recordSourceRevision(record), record.Source.RenderedDigest, record.Selection.RequestedBundle}
	for index, pkg := range record.Packages {
		if artifacts[index].PackageID != pkg.ID || len(artifacts[index].Bytes) == 0 {
			return nil, installstate.OwnedFile{}, errors.New("retained rollback packages do not match installation state")
		}
		artifactDigest := sha256.Sum256(artifacts[index].Bytes)
		digestInput = append(digestInput, pkg.ID, pkg.Path, hex.EncodeToString(artifactDigest[:]))
		packages[index] = catalog.Package{ID: pkg.ID, Path: "plugins/" + pkg.ID, Description: "AI4J retained rollback package " + pkg.ID}
	}
	bundleDigest := sha256.Sum256([]byte(strings.Join(digestInput, "\x00")))
	document, err := catalog.RenderLocalPackages(record.MarketplaceID, packages)
	if err != nil {
		return nil, installstate.OwnedFile{}, err
	}
	path := "state/bundles/" + hex.EncodeToString(bundleDigest[:]) + "/.claude-plugin/marketplace.json"
	return document.Bytes(), installstate.OwnedFile{Path: path, Checksum: document.Digest()}, nil
}

func localBundleDescriptor(record installstate.Record, artifacts []installstate.NativeArtifact) ([]byte, error) {
	type packageDescriptor struct {
		ID             string `json:"id"`
		Path           string `json:"path"`
		ArtifactDigest string `json:"artifactDigest"`
	}
	packages := make([]packageDescriptor, len(record.Packages))
	if len(record.Packages) != len(artifacts) {
		return nil, errors.New("local bundle artifacts do not match packages")
	}
	for index, pkg := range record.Packages {
		if artifacts[index].PackageID != pkg.ID {
			return nil, errors.New("local bundle artifacts do not match packages")
		}
		digest := sha256.Sum256(artifacts[index].Bytes)
		packages[index] = packageDescriptor{ID: pkg.ID, Path: pkg.Path, ArtifactDigest: hex.EncodeToString(digest[:])}
	}
	descriptor := struct {
		SchemaVersion   int                 `json:"schemaVersion"`
		BundleDigest    string              `json:"bundleDigest"`
		SourceDigest    string              `json:"sourceDigest"`
		RenderedDigest  string              `json:"renderedDigest"`
		ToolkitID       string              `json:"toolkitId"`
		RequestedBundle string              `json:"requestedBundle"`
		ResolvedBundles []string            `json:"resolvedBundles"`
		ResolvedAssets  []string            `json:"resolvedAssets"`
		Packages        []packageDescriptor `json:"packages"`
		Adapter         string              `json:"adapter"`
	}{2, localBundleID(record), record.Source.SourceDigest, record.Source.RenderedDigest, record.ToolkitID, record.Selection.RequestedBundle, slices.Clone(record.Selection.ResolvedBundles), slices.Clone(record.Selection.ResolvedAssets), packages, "claude-user"}
	contents, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return nil, err
	}
	contents = append(contents, '\n')
	return contents, nil
}

func localBundleID(record installstate.Record) string {
	const prefix = "state/bundles/"
	const suffix = "/.claude-plugin/marketplace.json"
	if strings.HasPrefix(record.Catalog.Path, prefix) && strings.HasSuffix(record.Catalog.Path, suffix) {
		return strings.TrimSuffix(strings.TrimPrefix(record.Catalog.Path, prefix), suffix)
	}
	return record.Source.BundleDigest
}

func isLocalBundleCatalog(path string) bool {
	const prefix = "state/bundles/"
	const suffix = "/.claude-plugin/marketplace.json"
	return strings.HasPrefix(path, prefix) && strings.HasSuffix(path, suffix)
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

func nativeArtifactsSize(artifacts []installstate.NativeArtifact) (expanded, compressed uint64, err error) {
	for _, artifact := range artifacts {
		size, sizeErr := nativeArtifactExpandedBytes(artifact.Bytes)
		if sizeErr != nil || expanded > 16<<20-size || compressed > 16<<20-uint64(len(artifact.Bytes)) {
			return 0, 0, errors.New("native rollback artifacts are too large")
		}
		expanded += size
		compressed += uint64(len(artifact.Bytes))
	}
	return expanded, compressed, nil
}

func (s *lifecycleService) currentArtifacts(record *installstate.Record) []installstate.NativeArtifact {
	if record == nil || record.Lifecycle != "active" {
		return nil
	}
	entry, present, err := s.state.LoadHistoryEntry(record.InstallationID, record.LastOperation.ID)
	if err != nil || !present || entry.After == nil || !sameCurrentState(*record, *entry.After) || len(entry.NativeArtifactsAfter) == 0 {
		return nil
	}
	return cloneNativeArtifacts(entry.NativeArtifactsAfter)
}

func (s *lifecycleService) verifyDesired(ctx context.Context, desired installstate.Record) error {
	native, problem := s.inspectNative(ctx, desired)
	if problem != nil {
		return errors.New("native state could not be verified")
	}
	if desired.Lifecycle == "archived" {
		anyNative, anyProblem := inspectAnyRecordNative(ctx, s.validation, desired)
		if anyProblem != nil || anyNative.MarketplaceRegistered || anyNative.PluginInstalled {
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
		if s.inspectProjectSharedNativeCatalogDrift(desired) != cli.DriftUnchanged {
			return errors.New("native catalog state does not match")
		}
	} else if inspectFileDrift(s.catalogPath(desired), desired.Catalog.Checksum) != cli.DriftUnchanged {
		return errors.New("catalog state does not match")
	}
	if desired.Rules != (installstate.OwnedFile{}) && inspectFileDrift(s.rulesPath(desired), desired.Rules.Checksum) != cli.DriftUnchanged {
		return errors.New("rules state does not match")
	}
	return nil
}

func (s *lifecycleService) verifyTransition(ctx context.Context, desired installstate.Record, before *installstate.Record) error {
	if err := s.verifyDesired(ctx, desired); err != nil {
		return err
	}
	if desired.Lifecycle == "archived" && before != nil && before.Scope == "project-shared" && before.NativeCatalog != (installstate.OwnedFile{}) && !s.projectSharedNativeCatalogAbsent(*before) {
		return errors.New("shared project native catalog is unexpectedly present")
	}
	if !s.removedPackagesAbsent(ctx, desired, before) {
		return errors.New("removed native packages are still installed")
	}
	return nil
}

func (s *lifecycleService) removedPackagesAbsent(ctx context.Context, desired installstate.Record, before *installstate.Record) bool {
	if before == nil {
		return true
	}
	removed, _, _ := packageChanges(before.Packages, desired.Packages)
	for _, pkg := range removed {
		observed, problem := s.validation.InspectNativeStatusAt(ctx, nativeDirectory(*before), before.MarketplaceID, nativePluginID(pkg, before.MarketplaceID))
		if problem != nil || observed.PluginInstalled {
			return false
		}
	}
	return true
}

func (s *lifecycleService) inspectNative(ctx context.Context, record installstate.Record) (validation.NativeStatus, *result.Problem) {
	return inspectRecordNative(ctx, s.validation, record)
}

func mutateOwned(home, path, expected string, contents []byte, policy cli.ConflictPolicy) error {
	return mutateOwnedAfterInspection(home, path, expected, contents, policy, nil)
}

func mutateOwnedAfterInspection(home, path, expected string, contents []byte, policy cli.ConflictPolicy, afterInspection func() error) error {
	drift := inspectFileDrift(path, expected)
	if afterInspection != nil {
		if err := afterInspection(); err != nil {
			return err
		}
	}
	if drift == cli.DriftMissing && policy == cli.ConflictReplaceOwned {
		if contents == nil {
			return nil
		}
		return writeOwnedNew(home, path, contents)
	}
	if drift == cli.DriftConflicting || drift != cli.DriftUnchanged && policy != cli.ConflictReplaceOwned {
		return errors.New("owned file does not match installation state")
	}
	if contents == nil {
		if policy == cli.ConflictReplaceOwned {
			return removeOwnedAny(home, path)
		}
		return removeOwnedMatching(home, path, expected)
	}
	if policy == cli.ConflictReplaceOwned {
		return replaceOwnedAny(home, path, contents)
	}
	return replaceOwnedMatching(home, path, expected, contents)
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
	if err := validateOwnedPath(home, path); err != nil {
		return err
	}
	return commitOwnedReplacement(temporaryPath, path)
}

func removeOwnedAny(home, path string) error {
	if err := validateOwnedPath(home, path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if !ownedFileAbsent(path) {
		return errors.New("owned-file removal could not be verified")
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
