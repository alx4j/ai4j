package app

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/installstate"
)

// reconcileInterrupted completes only transaction states that are fully
// explained by the operation's marker and structural history. Mixed, drifted,
// unsupported, or changing observations remain fail-closed.
func (s *lifecycleService) reconcileInterrupted(ctx context.Context) (bool, error) {
	marker, present, err := s.state.LoadMarker()
	if err != nil {
		return false, err
	}
	if !present {
		return s.reconcileOrphanedHistory(ctx)
	}
	if marker.Operation == "history_purge" {
		return s.reconcileHistoryPurge(marker)
	}
	if !slices.Contains(marker.Resources, "history:"+marker.InstallationID) {
		return false, nil
	}
	entry, historyPresent, err := s.state.LoadOperationHistory(marker.InstallationID, marker.OperationID)
	if err != nil {
		return false, err
	}
	if !historyPresent {
		current, statePresent, loadErr := s.state.LoadByID(marker.InstallationID)
		if loadErr != nil {
			return false, loadErr
		}
		if !statePresent || current.Lifecycle != "active" || current.LastOperation.ID == marker.OperationID ||
			!s.recoveryTargetMatches(ctx, &current, nil) || !s.recoverySnapshotMatches(ctx, marker.InstallationID, &current, nil, &current) {
			return false, nil
		}
		if err := s.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	}
	if entry.Operation != marker.Operation || entry.OperationID != marker.OperationID || entry.InstallationID != marker.InstallationID ||
		entry.After == nil || recordSourceRevision(*entry.After) != marker.Commit {
		return false, nil
	}
	current, statePresent, err := s.state.LoadByID(marker.InstallationID)
	if err != nil {
		return false, err
	}
	afterState := statePresent && reflect.DeepEqual(current, *entry.After)
	beforeState := entry.Before == nil && !statePresent || entry.Before != nil && statePresent && reflect.DeepEqual(current, *entry.Before)
	afterTarget := s.recoveryTargetMatches(ctx, entry.After, entry.Before)
	beforeTarget := s.recoveryTargetMatches(ctx, entry.Before, entry.After)
	if !entry.Committed && beforeState && !beforeTarget && entry.After.Scope == "project-local" &&
		(entry.Before == nil || entry.Before.Lifecycle == "archived" || entry.Before.Rules == (installstate.OwnedFile{})) &&
		s.recoveryTargetMatchesWithoutExclusion(ctx, entry.Before, entry.After) {
		if err := s.removeProjectLocalExclusion(ctx, *entry.After); err != nil {
			return false, err
		}
		beforeTarget = s.recoveryTargetMatches(ctx, entry.Before, entry.After)
	}
	if !entry.Committed && beforeState && !afterTarget && !beforeTarget {
		resumed, resumeErr := s.resumePartialPackageTransition(ctx, entry)
		if resumeErr != nil || !resumed {
			return false, resumeErr
		}
		afterTarget = s.recoveryTargetMatches(ctx, entry.After, entry.Before)
		beforeTarget = s.recoveryTargetMatches(ctx, entry.Before, entry.After)
	}

	switch {
	case afterState && afterTarget:
		if !s.recoverySnapshotMatches(ctx, marker.InstallationID, entry.After, entry.Before, entry.After) {
			return false, nil
		}
		if !entry.Committed {
			if err := s.state.CommitHistory(entry); err != nil {
				return false, err
			}
		}
		if err := s.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	case !entry.Committed && beforeState && afterTarget:
		// Target mutation completed and verified but installation state did not.
		if !s.recoverySnapshotMatches(ctx, marker.InstallationID, entry.Before, entry.Before, entry.After) {
			return false, nil
		}
		if entry.Before == nil {
			err = s.state.SaveNew(*entry.After)
		} else {
			err = s.state.Replace(*entry.Before, *entry.After)
		}
		if err != nil {
			return false, err
		}
		if err := s.state.CommitHistory(entry); err != nil {
			return false, err
		}
		if err := s.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	case !entry.Committed && beforeState && beforeTarget:
		// No durable mutation remains, so the staged journal can be discarded.
		if (entry.Before == nil || entry.Before.Lifecycle == "archived") && entry.After.Scope == "project-local" {
			if err := s.removeProjectLocalExclusion(ctx, *entry.After); err != nil {
				return false, err
			}
		}
		if !s.recoverySnapshotMatches(ctx, marker.InstallationID, entry.Before, entry.After, entry.Before) {
			return false, nil
		}
		if err := s.state.DeleteHistory(marker.InstallationID, []string{marker.OperationID}); err != nil {
			return false, err
		}
		if err := s.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func (s *lifecycleService) reconcileHistoryPurge(marker installstate.Marker) (bool, error) {
	before, desired, ok := historyPurgeRecords(marker)
	if !ok || recordSourceRevision(before) != marker.Commit {
		return false, nil
	}
	current, present, err := s.state.LoadByID(marker.InstallationID)
	if err != nil {
		return false, err
	}
	if !historyPurgeStateMatches(current, present, before, desired) {
		return false, nil
	}
	entries, err := s.state.LoadHistory(marker.InstallationID)
	if err != nil {
		return false, err
	}
	desiredHistory := []string{}
	if desired != nil {
		desiredHistory = desired.History
	}
	if !validPartialHistoryPurge(before.History, desiredHistory, marker.HistoryPurge.OperationIDs, historyEntryIDs(entries)) {
		return false, nil
	}
	switch {
	case present && reflect.DeepEqual(current, before):
		if desired == nil {
			err = s.state.Delete(before)
		} else {
			err = s.state.Replace(before, *desired)
		}
		if errors.Is(err, installstate.ErrStateChanged) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	case desired != nil && present && reflect.DeepEqual(current, *desired):
	case desired == nil && !present:
	default:
		return false, nil
	}
	if err := s.state.DeleteHistory(marker.InstallationID, marker.HistoryPurge.OperationIDs); err != nil {
		return false, err
	}
	entries, err = s.state.LoadHistory(marker.InstallationID)
	if err != nil {
		return false, err
	}
	if !slices.Equal(historyEntryIDs(entries), desiredHistory) {
		return false, nil
	}
	current, present, err = s.state.LoadByID(marker.InstallationID)
	if err != nil {
		return false, err
	}
	if desired != nil && (!present || !reflect.DeepEqual(current, *desired)) || desired == nil && present {
		return false, nil
	}
	if err := s.state.DeleteMarker(); err != nil {
		return false, err
	}
	return true, nil
}

func historyPurgeRecords(marker installstate.Marker) (installstate.Record, *installstate.Record, bool) {
	journal := marker.HistoryPurge
	if journal == nil {
		return installstate.Record{}, nil, false
	}
	switch journal.DesiredState {
	case installstate.HistoryPurgeStatePresent:
		if journal.DesiredRecord == nil {
			return installstate.Record{}, nil, false
		}
		desired := cloneRecord(*journal.DesiredRecord)
		before := cloneRecord(desired)
		before.History = append(before.History, journal.OperationIDs...)
		slices.Sort(before.History)
		return before, &desired, before.Validate() == nil
	case installstate.HistoryPurgeStateAbsent:
		if journal.ExpectedRecord == nil {
			return installstate.Record{}, nil, false
		}
		before := cloneRecord(*journal.ExpectedRecord)
		return before, nil, before.Validate() == nil
	default:
		return installstate.Record{}, nil, false
	}
}

func historyPurgeStateMatches(current installstate.Record, present bool, before installstate.Record, desired *installstate.Record) bool {
	return present && reflect.DeepEqual(current, before) ||
		desired != nil && present && reflect.DeepEqual(current, *desired) ||
		desired == nil && !present
}

func validPartialHistoryPurge(before, desired, selected, observed []string) bool {
	expectedBefore := append(slices.Clone(desired), selected...)
	slices.Sort(expectedBefore)
	if !slices.Equal(before, expectedBefore) {
		return false
	}
	beforeSet := make(map[string]struct{}, len(before))
	observedSet := make(map[string]struct{}, len(observed))
	for _, operationID := range before {
		beforeSet[operationID] = struct{}{}
	}
	for _, operationID := range observed {
		if _, expected := beforeSet[operationID]; !expected {
			return false
		}
		observedSet[operationID] = struct{}{}
	}
	for _, operationID := range desired {
		if _, present := observedSet[operationID]; !present {
			return false
		}
	}
	return true
}

type recoveryPluginState struct {
	installed bool
	enabled   bool
}

func (s *lifecycleService) resumePartialPackageTransition(ctx context.Context, entry installstate.HistoryEntry) (bool, error) {
	if entry.After == nil {
		return false, nil
	}
	if entry.After.Lifecycle == "archived" {
		return s.resumePartialRemoval(ctx, entry)
	}
	if entry.After.Lifecycle != "active" {
		return false, nil
	}
	return s.resumePartialActivation(ctx, entry)
}

func (s *lifecycleService) resumePartialActivation(ctx context.Context, entry installstate.HistoryEntry) (bool, error) {
	desired := *entry.After
	if recoveryEndpointBytesValid(desired, entry.CatalogAfter, entry.RulesAfter) &&
		recoveryArtifactsComplete(desired, entry.NativeArtifactsAfter) &&
		s.recoveryRulesTransitionKnown(entry.Before, &desired) {
		catalogReady := s.recoveryCatalogMatches(desired, entry.CatalogAfter, entry.NativeArtifactsAfter)
		if !catalogReady && desired.Scope == "project-shared" {
			marketplace, plugins, known := s.recoveryNativeProgress(ctx, entry.Before, &desired)
			if !known || !recoveryPluginStatesConsistent(plugins) || recoveryAnyPluginInstalled(plugins) {
				return s.compensatePartialActivation(ctx, entry)
			}
			var err error
			catalogReady, err = s.resumeProjectSharedCatalog(ctx, entry, marketplace)
			if err != nil {
				return false, err
			}
		}
		if !catalogReady {
			return s.compensatePartialActivation(ctx, entry)
		}
		resumed, err := s.convergePartialActivation(ctx, entry)
		if err != nil || resumed {
			return resumed, err
		}
	}
	return s.compensatePartialActivation(ctx, entry)
}

func (s *lifecycleService) resumeProjectSharedCatalog(ctx context.Context, entry installstate.HistoryEntry, marketplace bool) (bool, error) {
	desired := *entry.After
	if s.inspectProjectSharedNativeCatalogDrift(desired) != cli.DriftUnchanged {
		return false, nil
	}
	settings, _, err := readProjectSettings(projectSettingsPath(desired))
	if err != nil {
		return false, nil
	}
	current, present, err := projectMarketplaceFromSettings(settings, desired.DeclarationID)
	if err != nil {
		return false, nil
	}
	if present {
		if !marketplace {
			return false, nil
		}
		digest, digestErr := canonicalJSONDigest(current)
		if digestErr == nil && digest == desired.Catalog.Checksum {
			return true, nil
		}
		if entry.Before != nil && entry.Before.Scope == "project-shared" && digestErr == nil && digest == entry.Before.Catalog.Checksum {
			if err := replaceProjectMarketplace(desired, entry.CatalogAfter, entry.Before.Catalog.Checksum, ""); err != nil {
				return false, err
			}
			return true, nil
		}
		root := recoveryMarketplaceRoot(s, desired)
		temporary := []byte(`{"source":{"source":"directory","path":` + quotedJSON(root) + `}}`)
		if !jsonEqual(current, temporary) {
			return false, nil
		}
		if err := replaceProjectMarketplace(desired, entry.CatalogAfter, "", root); err != nil {
			return false, err
		}
		return true, nil
	}
	if entry.Before != nil && entry.Before.Lifecycle == "active" {
		return false, nil
	}
	if marketplace {
		return false, nil
	}
	root := recoveryMarketplaceRoot(s, desired)
	if err := s.runClaudeFor(ctx, desired, []string{"plugin", "marketplace", "add", root, "--scope", nativeScope(desired)}); err != nil {
		return false, err
	}
	if err := replaceProjectMarketplace(desired, entry.CatalogAfter, "", root); err != nil {
		return false, err
	}
	return true, nil
}

func (s *lifecycleService) convergePartialActivation(ctx context.Context, entry installstate.HistoryEntry) (bool, error) {
	desired := *entry.After
	return s.recoverPartialActivation(ctx, partialActivationDirection{
		desired:              desired,
		counterpart:          entry.Before,
		desiredRules:         entry.RulesAfter,
		counterpartCatalog:   entry.CatalogBefore,
		counterpartRules:     entry.RulesBefore,
		counterpartArtifacts: entry.NativeArtifactsBefore,
	})
}

func (s *lifecycleService) compensatePartialActivation(ctx context.Context, entry installstate.HistoryEntry) (bool, error) {
	if entry.Before == nil || entry.Before.Lifecycle != "active" {
		return false, nil
	}
	before := *entry.Before
	if !recoveryEndpointBytesValid(before, entry.CatalogBefore, entry.RulesBefore) ||
		!recoveryArtifactsComplete(before, entry.NativeArtifactsBefore) ||
		!s.recoveryCatalogMatches(before, entry.CatalogBefore, entry.NativeArtifactsBefore) ||
		!s.recoveryRulesTransitionKnown(entry.After, &before) {
		return false, nil
	}
	return s.recoverPartialActivation(ctx, partialActivationDirection{
		desired:              before,
		counterpart:          entry.After,
		desiredRules:         entry.RulesBefore,
		counterpartCatalog:   entry.CatalogAfter,
		counterpartRules:     entry.RulesAfter,
		counterpartArtifacts: entry.NativeArtifactsAfter,
	})
}

type partialActivationDirection struct {
	desired              installstate.Record
	counterpart          *installstate.Record
	desiredRules         []byte
	counterpartCatalog   []byte
	counterpartRules     []byte
	counterpartArtifacts []installstate.NativeArtifact
}

func (s *lifecycleService) recoverPartialActivation(ctx context.Context, direction partialActivationDirection) (bool, error) {
	desired := direction.desired
	marketplace, plugins, known := s.recoveryNativeProgress(ctx, direction.counterpart, &desired)
	if !known || !recoveryPluginStatesConsistent(plugins) || !marketplace && recoveryAnyPluginInstalled(plugins) {
		return false, nil
	}
	desiredPackages := make(map[string]struct{}, len(desired.Packages))
	for _, pkg := range desired.Packages {
		desiredPackages[pkg.ID] = struct{}{}
	}
	for packageID, state := range plugins {
		_, wanted := desiredPackages[packageID]
		if state.installed && !wanted {
			return false, nil
		}
	}
	pathChanged := recoveryCatalogPathChanged(s, direction.counterpart, &desired)
	if pathChanged {
		if direction.counterpart == nil ||
			!recoveryEndpointBytesValid(*direction.counterpart, direction.counterpartCatalog, direction.counterpartRules) ||
			!s.recoveryCatalogRemovalKnown(*direction.counterpart, direction.counterpartCatalog, direction.counterpartArtifacts) {
			return false, nil
		}
	} else if !marketplace && direction.counterpart != nil && direction.counterpart.Lifecycle == "active" {
		return false, nil
	}
	if err := s.ensureProjectLocalExclusion(ctx, desired); err != nil {
		return false, err
	}
	if err := s.completeRecoveryPackages(ctx, direction.counterpart, &desired, marketplace, plugins, direction.counterpartCatalog, direction.counterpartArtifacts); err != nil {
		return false, err
	}
	if err := s.resumeRecoveryRules(direction.counterpart, &desired, direction.desiredRules); err != nil {
		return false, err
	}
	if desired.Scope == "project-local" && desired.Rules == (installstate.OwnedFile{}) &&
		direction.counterpart != nil && direction.counterpart.Rules != (installstate.OwnedFile{}) {
		if err := s.removeProjectLocalExclusion(ctx, *direction.counterpart); err != nil {
			return false, err
		}
	}
	if err := s.verifyTransition(ctx, desired, direction.counterpart); err != nil {
		return false, nil
	}
	return true, nil
}

func (s *lifecycleService) completeRecoveryPackages(ctx context.Context, counterpart, desired *installstate.Record, marketplace bool, plugins map[string]recoveryPluginState, counterpartCatalog []byte, counterpartArtifacts []installstate.NativeArtifact) error {
	pathChanged := counterpart != nil && !recoveryCatalogResourceEqual(s, counterpart, desired)
	if pathChanged {
		for _, pkg := range recoveryPackages(counterpart, desired) {
			if !plugins[pkg.ID].installed {
				continue
			}
			if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "uninstall", nativePluginID(pkg, desired.MarketplaceID), "--scope", nativeScope(*desired), "--keep-data"}); err != nil {
				return err
			}
		}
		if err := s.removeRecoveryCatalog(*counterpart, counterpartCatalog, counterpartArtifacts); err != nil {
			return err
		}
		for packageID := range plugins {
			plugins[packageID] = recoveryPluginState{}
		}
	}
	if pathChanged || !marketplace {
		endpointRoot := recoveryMarketplaceRoot(s, *desired)
		if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", endpointRoot, "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
		if desired.Scope == "project-shared" {
			entry, entryErr := projectMarketplaceEntry(*desired)
			if entryErr != nil {
				return entryErr
			}
			if err := replaceProjectMarketplace(*desired, entry, "", endpointRoot); err != nil {
				return err
			}
		}
	} else if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "update", desired.MarketplaceID, "--scope", nativeScope(*desired)}); err != nil {
		return err
	}
	for _, pkg := range desired.Packages {
		if plugins[pkg.ID].installed {
			continue
		}
		if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "install", nativePluginID(pkg, desired.MarketplaceID), "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
	}
	return nil
}

func recoveryMarketplaceRoot(s *lifecycleService, record installstate.Record) string {
	if record.Scope == "project-shared" {
		return filepath.Dir(filepath.Dir(s.projectSharedNativeCatalogPath(record)))
	}
	return filepath.Dir(filepath.Dir(s.catalogPath(record)))
}

func (s *lifecycleService) resumePartialRemoval(ctx context.Context, entry installstate.HistoryEntry) (bool, error) {
	if entry.Before == nil || entry.Before.Lifecycle != "active" ||
		!recoveryEndpointBytesValid(*entry.Before, entry.CatalogBefore, entry.RulesBefore) ||
		!s.recoveryCatalogRemovalKnown(*entry.Before, entry.CatalogBefore, entry.NativeArtifactsBefore) ||
		!s.recoveryRulesTransitionKnown(entry.Before, entry.After) {
		return false, nil
	}
	marketplace, plugins, known := s.recoveryNativeProgress(ctx, entry.Before, entry.After)
	if !known || !recoveryPluginStatesConsistent(plugins) || !marketplace && recoveryAnyPluginInstalled(plugins) {
		return false, nil
	}
	for _, pkg := range entry.Before.Packages {
		if !plugins[pkg.ID].installed {
			continue
		}
		if err := s.runClaudeFor(ctx, *entry.Before, []string{"plugin", "uninstall", nativePluginID(pkg, entry.Before.MarketplaceID), "--scope", nativeScope(*entry.Before), "--keep-data"}); err != nil {
			return false, err
		}
	}
	if marketplace {
		if err := s.runClaudeFor(ctx, *entry.Before, []string{"plugin", "marketplace", "remove", entry.Before.MarketplaceID, "--scope", nativeScope(*entry.Before)}); err != nil {
			return false, err
		}
	}
	if err := s.removeRecoveryCatalog(*entry.Before, entry.CatalogBefore, entry.NativeArtifactsBefore); err != nil {
		return false, err
	}
	if err := s.resumeRecoveryRules(entry.Before, entry.After, nil); err != nil {
		return false, err
	}
	if err := s.removeProjectLocalExclusion(ctx, *entry.Before); err != nil {
		return false, err
	}
	if err := s.verifyTransition(ctx, *entry.After, entry.Before); err != nil {
		return false, nil
	}
	return true, nil
}

func (s *lifecycleService) recoveryNativeProgress(ctx context.Context, before, after *installstate.Record) (bool, map[string]recoveryPluginState, bool) {
	reference := after
	if reference == nil || reference.Lifecycle != "active" {
		reference = before
	}
	if reference == nil || len(reference.Packages) == 0 {
		return false, nil, false
	}
	for _, record := range []*installstate.Record{before, after} {
		if record != nil && (record.MarketplaceID != reference.MarketplaceID || nativeDirectory(*record) != nativeDirectory(*reference) || nativeScope(*record) != nativeScope(*reference)) {
			return false, nil, false
		}
	}
	packages := make(map[string]installstate.NativePackage, len(reference.Packages))
	for _, record := range []*installstate.Record{before, after} {
		if record == nil {
			continue
		}
		for _, pkg := range record.Packages {
			packages[pkg.ID] = pkg
		}
	}
	ids := make([]string, 0, len(packages))
	for id := range packages {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	states := make(map[string]recoveryPluginState, len(ids))
	marketplace := false
	for index, id := range ids {
		observed, problem := s.validation.InspectNativeStatusAt(ctx, nativeDirectory(*reference), reference.MarketplaceID, nativePluginID(packages[id], reference.MarketplaceID))
		if problem != nil || index > 0 && observed.MarketplaceRegistered != marketplace {
			return false, nil, false
		}
		marketplace = observed.MarketplaceRegistered
		states[id] = recoveryPluginState{installed: observed.PluginInstalled, enabled: observed.PluginEnabled}
	}
	return marketplace, states, true
}

func recoveryPluginStatesConsistent(plugins map[string]recoveryPluginState) bool {
	for _, state := range plugins {
		if state.installed != state.enabled {
			return false
		}
	}
	return true
}

func recoveryAnyPluginInstalled(plugins map[string]recoveryPluginState) bool {
	for _, state := range plugins {
		if state.installed {
			return true
		}
	}
	return false
}

func recoveryPackages(records ...*installstate.Record) []installstate.NativePackage {
	byID := make(map[string]installstate.NativePackage)
	for _, record := range records {
		if record == nil {
			continue
		}
		for _, pkg := range record.Packages {
			byID[pkg.ID] = pkg
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	packages := make([]installstate.NativePackage, len(ids))
	for index, id := range ids {
		packages[index] = byID[id]
	}
	return packages
}

func recoveryArtifactsComplete(record installstate.Record, artifacts []installstate.NativeArtifact) bool {
	if len(artifacts) != len(record.Packages) {
		return false
	}
	for index, artifact := range artifacts {
		if artifact.PackageID != record.Packages[index].ID || len(artifact.Bytes) == 0 {
			return false
		}
		if _, err := nativeArtifactExpandedBytes(artifact.Bytes); err != nil {
			return false
		}
	}
	return true
}

func recoveryCatalogTransitionNeeded(before, after *installstate.Record) bool {
	if before == nil || after == nil || before.Lifecycle != "active" || after.Lifecycle != "active" {
		return false
	}
	return before.Catalog != after.Catalog || before.NativeCatalog != after.NativeCatalog
}

func recoveryCatalogPathChanged(s *lifecycleService, before, after *installstate.Record) bool {
	if before == nil || after == nil || before.Lifecycle != "active" || after.Lifecycle != "active" {
		return false
	}
	return !recoveryCatalogResourceEqual(s, before, after)
}

func recoveryCatalogResourceEqual(s *lifecycleService, first, second *installstate.Record) bool {
	if first == nil || second == nil || first.Lifecycle != "active" || second.Lifecycle != "active" || first.Scope != second.Scope {
		return false
	}
	if first.Scope == "project-shared" {
		return filepath.Clean(s.projectSharedNativeCatalogPath(*first)) == filepath.Clean(s.projectSharedNativeCatalogPath(*second))
	}
	return filepath.Clean(s.catalogPath(*first)) == filepath.Clean(s.catalogPath(*second))
}

func recoveryEndpointBytesValid(record installstate.Record, catalogBytes, rulesBytes []byte) bool {
	if record.Lifecycle != "active" {
		return len(catalogBytes) == 0 && len(rulesBytes) == 0
	}
	if record.Scope == "project-shared" {
		digest, err := canonicalJSONDigest(catalogBytes)
		if err != nil || digest != record.Catalog.Checksum {
			return false
		}
	} else if len(catalogBytes) == 0 || sha256Digest(catalogBytes) != record.Catalog.Checksum {
		return false
	}
	if record.Rules == (installstate.OwnedFile{}) {
		return len(rulesBytes) == 0
	}
	return len(rulesBytes) != 0 && sha256Digest(rulesBytes) == record.Rules.Checksum
}

func (s *lifecycleService) recoveryCatalogMatches(record installstate.Record, catalogBytes []byte, artifacts []installstate.NativeArtifact) bool {
	if record.Scope == "project-shared" {
		return inspectProjectMarketplaceDrift(record) == cli.DriftUnchanged && s.inspectProjectSharedNativeCatalogDrift(record) == cli.DriftUnchanged
	}
	if !isLocalBundleCatalog(record.Catalog.Path) {
		return inspectFileDrift(s.catalogPath(record), record.Catalog.Checksum) == cli.DriftUnchanged
	}
	descriptor, err := localBundleDescriptor(record, artifacts)
	if err != nil {
		return false
	}
	root := filepath.Dir(filepath.Dir(s.catalogPath(record)))
	present, err := verifyLocalBundleTree(root, record, catalogBytes, descriptor, artifacts)
	return err == nil && present
}

func (s *lifecycleService) recoveryCatalogRemovalKnown(record installstate.Record, catalogBytes []byte, artifacts []installstate.NativeArtifact) bool {
	if record.Scope == "project-shared" {
		declarationKnown := inspectProjectMarketplaceDrift(record) == cli.DriftUnchanged || projectMarketplaceAbsent(record)
		nativeKnown := s.inspectProjectSharedNativeCatalogDrift(record) == cli.DriftUnchanged || s.projectSharedNativeCatalogAbsent(record)
		return declarationKnown && nativeKnown
	}
	if ownedFileAbsent(s.catalogPath(record)) {
		return true
	}
	return inspectFileDrift(s.catalogPath(record), record.Catalog.Checksum) == cli.DriftUnchanged
}

func (s *lifecycleService) recoveryRulesTransitionKnown(before, after *installstate.Record) bool {
	var beforeOwned, afterOwned installstate.OwnedFile
	var beforePath, afterPath string
	if before != nil {
		beforeOwned, beforePath = before.Rules, s.rulesPath(*before)
	}
	if after != nil {
		afterOwned, afterPath = after.Rules, s.rulesPath(*after)
	}
	if beforeOwned != (installstate.OwnedFile{}) && afterOwned != (installstate.OwnedFile{}) && beforePath != afterPath {
		return false
	}
	switch {
	case beforeOwned == (installstate.OwnedFile{}) && afterOwned == (installstate.OwnedFile{}):
		return true
	case beforeOwned == (installstate.OwnedFile{}):
		drift := inspectFileDrift(afterPath, afterOwned.Checksum)
		return drift == cli.DriftUnchanged || drift == cli.DriftMissing
	case afterOwned == (installstate.OwnedFile{}):
		drift := inspectFileDrift(beforePath, beforeOwned.Checksum)
		return drift == cli.DriftUnchanged || drift == cli.DriftMissing
	default:
		return inspectFileDrift(afterPath, afterOwned.Checksum) == cli.DriftUnchanged || inspectFileDrift(beforePath, beforeOwned.Checksum) == cli.DriftUnchanged || ownedFileAbsent(afterPath)
	}
}

func (s *lifecycleService) resumeRecoveryRules(before, after *installstate.Record, afterBytes []byte) error {
	var beforeOwned, afterOwned installstate.OwnedFile
	var beforePath, afterPath string
	if before != nil {
		beforeOwned, beforePath = before.Rules, s.rulesPath(*before)
	}
	if after != nil {
		afterOwned, afterPath = after.Rules, s.rulesPath(*after)
	}
	if afterOwned == (installstate.OwnedFile{}) {
		if beforeOwned == (installstate.OwnedFile{}) || ownedFileAbsent(beforePath) {
			return nil
		}
		return mutateOwned(s.ownedRoot(beforePath), beforePath, beforeOwned.Checksum, nil, cli.ConflictFail)
	}
	if len(afterBytes) == 0 || sha256Digest(afterBytes) != afterOwned.Checksum {
		return errors.New("recovery rules do not match staged ownership")
	}
	if inspectFileDrift(afterPath, afterOwned.Checksum) == cli.DriftUnchanged {
		return nil
	}
	if ownedFileAbsent(afterPath) {
		return writeOwnedNew(s.ownedRoot(afterPath), afterPath, afterBytes)
	}
	if beforeOwned != (installstate.OwnedFile{}) && beforePath == afterPath && inspectFileDrift(beforePath, beforeOwned.Checksum) == cli.DriftUnchanged {
		return mutateOwned(s.ownedRoot(beforePath), beforePath, beforeOwned.Checksum, afterBytes, cli.ConflictFail)
	}
	return errors.New("recovery rules changed outside the staged operation")
}

func (s *lifecycleService) removeRecoveryCatalog(record installstate.Record, catalogBytes []byte, artifacts []installstate.NativeArtifact) error {
	if record.Scope == "project-shared" {
		if !projectMarketplaceAbsent(record) {
			if inspectProjectMarketplaceDrift(record) != cli.DriftUnchanged {
				return errors.New("project marketplace changed during recovery")
			}
			if err := removeProjectMarketplace(record); err != nil {
				return err
			}
		}
		if !s.projectSharedNativeCatalogAbsent(record) {
			if s.inspectProjectSharedNativeCatalogDrift(record) != cli.DriftUnchanged {
				return errors.New("project native catalog changed during recovery")
			}
			if err := s.removeProjectSharedNativeCatalog(record, cli.ConflictFail); err != nil {
				return err
			}
		}
		return removeCreatedProjectSettingsIfEmpty(record)
	}
	if ownedFileAbsent(s.catalogPath(record)) {
		return nil
	}
	if inspectFileDrift(s.catalogPath(record), record.Catalog.Checksum) != cli.DriftUnchanged {
		return errors.New("catalog changed during recovery")
	}
	return mutateOwned(s.ownedRoot(s.catalogPath(record)), s.catalogPath(record), record.Catalog.Checksum, nil, cli.ConflictFail)
}

func (s *lifecycleService) reconcileOrphanedHistory(ctx context.Context) (bool, error) {
	entries, err := s.state.LoadStagedHistory()
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		current, present, err := s.state.LoadByID(entry.InstallationID)
		if err != nil {
			return false, err
		}
		beforeState := entry.Before == nil && !present || entry.Before != nil && present && reflect.DeepEqual(current, *entry.Before)
		targetMatches := s.recoveryTargetMatches(ctx, entry.Before, entry.After)
		if !targetMatches && entry.After != nil && entry.After.Scope == "project-local" &&
			(entry.Before == nil || entry.Before.Lifecycle == "archived" || entry.Before.Rules == (installstate.OwnedFile{})) {
			targetMatches = s.recoveryTargetMatchesWithoutExclusion(ctx, entry.Before, entry.After)
		}
		if !beforeState || !targetMatches {
			return false, nil
		}
	}
	for _, entry := range entries {
		if (entry.Before == nil || entry.Before.Lifecycle == "archived" || entry.Before.Rules == (installstate.OwnedFile{})) &&
			entry.After != nil && entry.After.Scope == "project-local" && entry.After.Rules != (installstate.OwnedFile{}) {
			if err := s.removeProjectLocalExclusion(ctx, *entry.After); err != nil {
				return false, err
			}
			if !s.recoverySnapshotMatches(ctx, entry.InstallationID, entry.Before, entry.After, entry.Before) {
				return false, nil
			}
		}
		if err := s.state.DeleteHistory(entry.InstallationID, []string{entry.OperationID}); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *lifecycleService) recoverySnapshotMatches(ctx context.Context, installationID string, expectedState, counterpart, expectedTarget *installstate.Record) bool {
	current, present, err := s.state.LoadByID(installationID)
	if err != nil || expectedState == nil && present || expectedState != nil && (!present || !reflect.DeepEqual(current, *expectedState)) {
		return false
	}
	return s.recoveryTargetMatches(ctx, expectedTarget, counterpart)
}

func (s *lifecycleService) recoveryTargetMatches(ctx context.Context, expected, counterpart *installstate.Record) bool {
	return s.recoveryTargetMatchesWithoutExclusion(ctx, expected, counterpart) && s.recoveryProjectLocalExclusionMatches(ctx, expected, counterpart)
}

func (s *lifecycleService) recoveryTargetMatchesWithoutExclusion(ctx context.Context, expected, counterpart *installstate.Record) bool {
	if expected == nil {
		if counterpart == nil {
			return false
		}
		native, problem := inspectAnyRecordNative(ctx, s.validation, *counterpart)
		if problem != nil || native.MarketplaceRegistered || native.PluginInstalled {
			return false
		}
		return recoveryOwnedAbsent(s, *counterpart)
	}
	if expected.Health == "drifted" || s.verifyTransition(ctx, *expected, counterpart) != nil {
		return false
	}
	if expected.Lifecycle == "archived" && counterpart != nil {
		return recoveryOwnedAbsent(s, *counterpart)
	}
	return recoveryCounterpartOwnedAbsent(s, *expected, counterpart)
}

func (s *lifecycleService) recoveryProjectLocalExclusionMatches(ctx context.Context, expected, counterpart *installstate.Record) bool {
	var reference *installstate.Record
	for _, record := range []*installstate.Record{expected, counterpart} {
		if record != nil && record.Scope == "project-local" && record.Rules != (installstate.OwnedFile{}) {
			reference = record
			break
		}
	}
	if reference == nil {
		return true
	}
	path, err := s.projectExcludePath(ctx, *reference)
	if err != nil {
		return false
	}
	contents, _, err := readProjectMetadata(path)
	if err != nil {
		return false
	}
	present := slices.Contains(strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n"), projectExcludeLine(*reference))
	want := expected != nil && expected.Lifecycle == "active" && expected.Rules != (installstate.OwnedFile{})
	return present == want
}

func recoveryOwnedAbsent(s *lifecycleService, record installstate.Record) bool {
	if record.Scope == "project-shared" {
		return projectMarketplaceAbsent(record) && s.projectSharedNativeCatalogAbsent(record) &&
			(record.Rules == (installstate.OwnedFile{}) || ownedFileAbsent(s.rulesPath(record)))
	}
	return (record.Catalog == (installstate.OwnedFile{}) || ownedFileAbsent(s.catalogPath(record))) &&
		(record.Rules == (installstate.OwnedFile{}) || ownedFileAbsent(s.rulesPath(record)))
}

func recoveryCounterpartOwnedAbsent(s *lifecycleService, expected installstate.Record, counterpart *installstate.Record) bool {
	if counterpart == nil {
		return true
	}
	if counterpart.Catalog != (installstate.OwnedFile{}) &&
		(expected.Catalog == (installstate.OwnedFile{}) || filepath.Clean(s.catalogPath(expected)) != filepath.Clean(s.catalogPath(*counterpart))) &&
		!ownedFileAbsent(s.catalogPath(*counterpart)) {
		return false
	}
	if counterpart.NativeCatalog != (installstate.OwnedFile{}) &&
		(expected.NativeCatalog == (installstate.OwnedFile{}) || filepath.Clean(s.projectSharedNativeCatalogPath(expected)) != filepath.Clean(s.projectSharedNativeCatalogPath(*counterpart))) &&
		!s.projectSharedNativeCatalogAbsent(*counterpart) {
		return false
	}
	return counterpart.Rules == (installstate.OwnedFile{}) ||
		expected.Rules != (installstate.OwnedFile{}) && filepath.Clean(s.rulesPath(expected)) == filepath.Clean(s.rulesPath(*counterpart)) ||
		ownedFileAbsent(s.rulesPath(*counterpart))
}
