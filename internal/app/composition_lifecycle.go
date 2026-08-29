package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	gitremote "github.com/alx4j/ai4j/internal/source/gitremote"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func (s *lifecycleService) prepareCompositionInstall(ctx context.Context, rootValue string, coordinates []cli.BundleCoordinate, target cli.BuildTarget, scope cli.Scope, project string, hasProject bool, policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
	if target != cli.BuildTargetClaude || !scope.Valid() {
		return stopLifecycle(cli.CommandInstall, result.FailureValidation, "unsupported_scope", "the requested target or scope is unsupported")
	}
	scopeRoot := s.effectiveClaudeRoot()
	if scope != cli.ScopeUser {
		var err error
		scopeRoot, err = s.resolveProjectRoot(ctx, project, hasProject)
		if err != nil {
			return stopLifecycle(cli.CommandInstall, result.FailureEnvironment, "project_root_invalid", "a canonical Git project root could not be resolved")
		}
	}
	root, err := gitremote.ParseRoot(rootValue)
	if err != nil {
		return stopLifecycle(cli.CommandInstall, result.FailureSource, "invalid_git_root", "the Git root is not in a supported credential-free form")
	}
	report := s.selectComposition(ctx, root, coordinates)
	if len(report.Problems) != 0 || len(report.Components) == 0 {
		return stopSelection(cli.CommandInstall, report)
	}
	installationID := installationIDForComposition(scope, scopeRoot)
	desired, document, err := s.recordForComposition(report, installationID, scope, scopeRoot)
	if err != nil {
		return stopLifecycle(cli.CommandInstall, result.FailureInternal, "plan_failed", "composition plan could not be created")
	}
	records, err := s.state.LoadAll()
	if err != nil {
		return stopLifecycle(cli.CommandInstall, result.FailureConflict, "installation_state_invalid", "installation state could not be read")
	}
	var before *installstate.Record
	for _, record := range records {
		if record.InstallationID == desired.InstallationID {
			before = cloneRecordPtr(&record)
			continue
		}
		if record.Lifecycle == "active" && record.ToolkitID == "composition" && record.Target == desired.Target && record.Scope == desired.Scope && filepath.Clean(record.ScopeRoot) == filepath.Clean(desired.ScopeRoot) {
			return stopLifecycle(cli.CommandInstall, result.FailureConflict, "composition_exists", "a different composition already owns this target scope")
		}
	}
	if before != nil {
		desired.History = slices.Clone(before.History)
	}
	if err := s.inspectProjectLocal(ctx, before, desired); err != nil {
		return stopLifecycle(cli.CommandInstall, result.FailureConflict, "project_local_conflict", "project-local rules cannot be proven safely untracked")
	}
	catalogBytes := document.Bytes()
	var catalogBefore []byte
	if desired.Scope == "project-shared" {
		catalogBefore, catalogBytes, err = s.planProjectShared(&desired, before)
		if err != nil {
			return stopLifecycle(cli.CommandInstall, result.FailureConflict, "project_settings_conflict", "the shared project declaration cannot be changed safely")
		}
	}
	conflicts := s.installConflicts(ctx, desired)
	if before != nil {
		conflicts = s.existingConflicts(ctx, *before, true)
		conflicts = append(conflicts, s.compositionAdditionConflicts(ctx, *before, desired)...)
	}
	visible, degraded := applyConflictPolicy(conflicts, policy, before != nil)
	if before != nil {
		visible, degraded = applyActiveTransitionConflictPolicy(conflicts, policy)
	}
	actions, err := s.transitionActions(cli.OperationInstall, before, &desired, len(report.Rules) != 0)
	if err != nil {
		return stopLifecycle(cli.CommandInstall, result.FailureInternal, "plan_failed", "composition plan actions could not be created")
	}
	content := report.Content
	var beforeArtifacts []installstate.NativeArtifact
	if before != nil && before.Lifecycle == "active" {
		beforeArtifacts = s.currentArtifacts(before)
		content, err = recordedTransitionContent(before.Selection.ResolvedAssets, report.Content)
		if err != nil {
			return stopLifecycle(cli.CommandInstall, result.FailureInternal, "plan_failed", "active content changes could not be created")
		}
		if recordsEquivalent(*before, desired) && len(conflicts) == 0 {
			actions = nil
			content, err = contentWithChange(report.Content, cli.ContentUnchanged)
			if err != nil {
				return stopLifecycle(cli.CommandInstall, result.FailureInternal, "plan_failed", "active content plan could not be created")
			}
		} else if len(beforeArtifacts) == 0 {
			return stopLifecycle(cli.CommandInstall, result.FailureConflict, "rollback_artifact_unavailable", "exact native rollback material is unavailable")
		}
	}
	final := mustFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	transition, err := newPreparedTransition(before, &desired, preparedTransitionMaterial{
		projectSettingsBefore: catalogBefore,
		desiredTarget:         catalogBytes,
		desiredRules:          report.Rules,
		retainedBefore:        beforeArtifacts,
		desiredArtifacts:      retainedArtifacts(report),
	})
	if err != nil {
		return stopLifecycle(cli.CommandInstall, result.FailureInternal, "plan_failed", "composition plan could not be created")
	}
	return lifecycleExecution{
		operation: cli.OperationInstall, source: report, transition: transition, actions: actions,
		content: content, conflicts: visible, degradedConflicts: degraded, final: final, disposition: result.UpdateNotChecked,
	}, cli.Response{}, false, nil
}

func (s *lifecycleService) compositionAdditionConflicts(ctx context.Context, before, desired installstate.Record) []cli.Conflict {
	existing := make(map[string]struct{}, len(before.Packages))
	for _, pkg := range before.Packages {
		existing[nativePluginID(pkg, before.MarketplaceID)] = struct{}{}
	}
	var conflicts []cli.Conflict
	if before.Rules == (installstate.OwnedFile{}) && desired.Rules != (installstate.OwnedFile{}) {
		if _, err := os.Lstat(s.rulesPath(desired)); err == nil {
			conflicts = append(conflicts, mustCLIConflict("rules_destination_occupied", desired.Rules.Path, "destination is already occupied"))
		} else if !errors.Is(err, os.ErrNotExist) {
			conflicts = append(conflicts, mustCLIConflict("owned_state_inspection_failed", desired.Rules.Path, "destination could not be inspected"))
		}
	}
	for _, pkg := range desired.Packages {
		pluginID := nativePluginID(pkg, desired.MarketplaceID)
		if _, present := existing[pluginID]; present {
			continue
		}
		observed, problem := s.validation.InspectNativeStatusAt(ctx, nativeDirectory(desired), desired.MarketplaceID, pluginID)
		if problem != nil {
			conflicts = append(conflicts, mustCLIConflict(problem.Code(), pluginID, problem.Message()))
			continue
		}
		if observed.PluginInstalled || observed.PluginEnabled {
			conflicts = append(conflicts, mustCLIConflict("plugin_identity_conflict", pluginID, "the new component plugin identity already exists"))
		}
	}
	return conflicts
}

func (s *lifecycleService) selectComposition(ctx context.Context, root gitremote.Root, coordinates []cli.BundleCoordinate) validation.LifecycleSelection {
	ordered := append([]cli.BundleCoordinate(nil), coordinates...)
	slices.SortFunc(ordered, func(left, right cli.BundleCoordinate) int { return strings.Compare(left.Name(), right.Name()) })
	selected := make([]selectedCompositionComponent, 0, len(ordered))
	for _, coordinate := range ordered {
		remote, err := root.Repository(coordinate.Name())
		if err != nil {
			return compositionFailure("invalid_component_source", "a composition repository could not be derived safely")
		}
		options, err := cli.NewSourceOptions(remote.Endpoint(), true, "refs/tags/"+coordinate.Tag(), true)
		if err != nil {
			return compositionFailure("invalid_component_source", "a composition source could not be constructed")
		}
		report := s.validation.SelectLifecycle(ctx, options, coordinate.Name())
		if len(report.Problems) != 0 {
			return annotateCompositionSelection(report, coordinate.Name())
		}
		if !report.HasSource() {
			return compositionFailureFor(coordinate.Name(), "component_source_failed", "the component source did not resolve to an exact commit")
		}
		selected = append(selected, selectedCompositionComponent{coordinate: coordinate, report: report})
	}
	return combineComposition(selected)
}

func (s *lifecycleService) selectRecordedComposition(ctx context.Context, record installstate.Record) validation.LifecycleSelection {
	selected := make([]selectedCompositionComponent, 0, len(record.Components))
	for _, component := range record.Components {
		remote, err := storedSourceRemote(component.Source)
		if err != nil {
			return compositionFailure("invalid_component_source", "stored composition source is invalid")
		}
		options, err := cli.NewSourceOptions(remote.Endpoint(), true, "refs/tags/"+component.Tag, true)
		if err != nil {
			return compositionFailure("invalid_component_source", "stored composition source is invalid")
		}
		report := s.validation.SelectLifecycle(ctx, options, component.Name)
		if len(report.Problems) != 0 {
			return annotateCompositionSelection(report, component.Name)
		}
		if !report.HasSource() {
			return compositionFailureFor(component.Name, "component_source_failed", "the component source did not resolve to an exact commit")
		}
		if report.Source.Commit().OID().String() != component.Source.Commit {
			return compositionFailure("ref_rewritten", "the recorded tag for component "+component.Name+" no longer resolves to its installed commit")
		}
		coordinate, err := cli.NewBundleCoordinate(component.Name, component.Tag)
		if err != nil {
			return compositionFailure("invalid_component_source", "stored composition coordinate is invalid")
		}
		selected = append(selected, selectedCompositionComponent{coordinate: coordinate, report: report})
	}
	return combineComposition(selected)
}

func (s *lifecycleService) prepareCompositionUpdate(ctx context.Context, record installstate.Record, requested cli.SourceOptions, policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
	if requested.HasRepository() || requested.HasReference() || requested.HasCheckout() || requested.AllowDirty() {
		return stopLifecycle(cli.CommandUpdate, result.FailureConflict, "composition_source_change_unsupported", "composition sources are changed by reinstalling the complete coordinate set")
	}
	report := s.selectRecordedComposition(ctx, record)
	if len(report.Problems) != 0 || len(report.Components) == 0 {
		return stopSelection(cli.CommandUpdate, report)
	}
	selection, _ := cli.NewBundleSelection("composition")
	return s.prepareExisting(ctx, cli.CommandUpdate, cli.OperationUpdate, record, report, cli.SourceOptions{}, selection, policy, result.UpdatePinned)
}

func installationIDForComposition(scope cli.Scope, scopeRoot string) domain.InstallationID {
	digest := sha256.Sum256([]byte(strings.Join([]string{"composition", string(scope), filepath.Clean(scopeRoot)}, "\x00")))
	id, _ := domain.NewInstallationID(hex.EncodeToString(digest[:4]))
	return id
}
