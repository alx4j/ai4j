package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	validation "github.com/alx4j/ai4j/internal/validate"
)

const maximumStatusFileBytes = 16 << 20

type statusValidation interface {
	InspectNativeStatusAt(context.Context, string, string, string) (validation.NativeStatus, *result.Problem)
	SelectLifecycle(context.Context, cli.SourceOptions, string) validation.LifecycleSelection
	ValidateUpdate(context.Context, cli.SourceOptions, domain.CommitOID) validation.UpdateReport
}

type nativeStatusInspector interface {
	InspectNativeStatusAt(context.Context, string, string, string) (validation.NativeStatus, *result.Problem)
}

type statusService struct {
	validation statusValidation
	state      installstate.Store
}

func inspectRecordNative(ctx context.Context, inspector nativeStatusInspector, record installstate.Record) (validation.NativeStatus, *result.Problem) {
	status, _, problem := inspectRecordNativeDetailed(ctx, inspector, record)
	return status, problem
}

func inspectRecordNativeDetailed(ctx context.Context, inspector nativeStatusInspector, record installstate.Record) (validation.NativeStatus, []result.Warning, *result.Problem) {
	status := validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}
	var warnings []result.Warning
	var firstProblem *result.Problem
	warningKeys := make(map[string]struct{})
	addWarning := func(key, code, message, component, resourceValue string) {
		if _, present := warningKeys[key]; present {
			return
		}
		warningKeys[key] = struct{}{}
		context := compositionContext(component)
		resource, _ := result.NewContext("resource", resourceValue)
		context = append(context, resource)
		warning, _ := result.NewWarning(code, message, context)
		warnings = append(warnings, warning)
	}
	for _, pkg := range record.Packages {
		pluginID := nativePluginID(pkg, record.MarketplaceID)
		observed, problem := inspector.InspectNativeStatusAt(ctx, nativeDirectory(record), record.MarketplaceID, pluginID)
		if problem != nil {
			if pkg.Component == "" {
				return validation.NativeStatus{}, nil, problem
			}
			annotated := annotateCompositionProblem(*problem, pkg.Component)
			resource, _ := result.NewContext("resource", pluginID)
			context := append(annotated.Context(), resource)
			annotated, _ = result.NewProblem(annotated.Code(), annotated.Message(), context)
			addWarning(pkg.Component+"\x00inspection", annotated.Code(), annotated.Message(), pkg.Component, pluginID)
			if firstProblem == nil {
				firstProblem = &annotated
			}
			continue
		}
		status.MarketplaceRegistered = status.MarketplaceRegistered && observed.MarketplaceRegistered
		status.PluginInstalled = status.PluginInstalled && observed.PluginInstalled
		status.PluginEnabled = status.PluginEnabled && observed.PluginEnabled
		if pkg.Component != "" {
			for _, issue := range []struct {
				unhealthy bool
				code      string
				message   string
			}{
				{!observed.MarketplaceRegistered, "native_registration_missing", "Claude does not have the component marketplace registered"},
				{!observed.PluginInstalled, "native_plugin_missing", "Claude does not have the component plugin installed"},
				{observed.PluginInstalled && !observed.PluginEnabled, "native_plugin_disabled", "Claude has the component plugin disabled"},
			} {
				if issue.unhealthy {
					addWarning(pkg.Component+"\x00"+issue.code, issue.code, issue.message, pkg.Component, pluginID)
				}
			}
		}
	}
	return status, warnings, firstProblem
}

func inspectAnyRecordNative(ctx context.Context, inspector nativeStatusInspector, record installstate.Record) (validation.NativeStatus, *result.Problem) {
	var status validation.NativeStatus
	for _, pluginID := range nativePluginIDs(record) {
		observed, problem := inspector.InspectNativeStatusAt(ctx, nativeDirectory(record), record.MarketplaceID, pluginID)
		if problem != nil {
			return validation.NativeStatus{}, problem
		}
		status.MarketplaceRegistered = status.MarketplaceRegistered || observed.MarketplaceRegistered
		status.PluginInstalled = status.PluginInstalled || observed.PluginInstalled
		status.PluginEnabled = status.PluginEnabled || observed.PluginEnabled
	}
	return status, nil
}

func (s statusService) Status(ctx context.Context, request cli.StatusRequest) (cli.Response, error) {
	record, installed, stateErr := s.state.LoadByID(request.InstallationID().String())
	_, markerPresent, markerErr := s.state.LoadMarker()
	recovery := recoveryFromState(stateErr, markerPresent, markerErr)

	disposition := result.UpdateNotChecked
	if !installed && recovery.State() == cli.RecoveryStateNone {
		disposition = result.UpdateNotInstalled
	}
	native, err := unobservableNative()
	if err != nil {
		return cli.Response{}, err
	}
	var installation *cli.Installation
	var summary *cli.InstallationSummary
	var drift []cli.Drift
	var warnings []result.Warning
	if installed {
		value, valueErr := installationFromRecord(record)
		if valueErr != nil {
			return cli.Response{}, valueErr
		}
		installation = &value
		if record.Lifecycle == "active" {
			drift, err = s.inspectDrift(record)
			if err != nil {
				return cli.Response{}, err
			}
			observation, nativeWarnings, problem := inspectRecordNativeDetailed(ctx, s.validation, record)
			warnings = append(warnings, nativeWarnings...)
			if problem != nil {
				native, err = unknownNative()
				if len(record.Components) == 0 {
					warning, warningErr := result.NewWarning(problem.Code(), problem.Message(), nil)
					if warningErr != nil {
						return cli.Response{}, warningErr
					}
					warnings = append(warnings, warning)
				}
			} else {
				if record.Scope == "project-shared" && inspectProjectMarketplaceDrift(record) == cli.DriftUnchanged {
					observation.MarketplaceRegistered = true
				}
				native, err = observedNative(observation)
			}
			if err != nil {
				return cli.Response{}, err
			}
		}
		current := record
		current.Health = observedStatusHealth(record, native, drift, recovery)
		summaryValue, valueErr := summaryFromRecord(current)
		if valueErr != nil {
			return cli.Response{}, valueErr
		}
		summary = &summaryValue
	}

	var updateProblem *result.Problem
	if installed && record.Lifecycle == "active" && recovery.State() == cli.RecoveryStateNone {
		var updateWarnings []result.Warning
		disposition, updateProblem, updateWarnings = s.checkUpdates(ctx, record)
		warnings = append(warnings, updateWarnings...)
	}
	data, err := cli.NewDetailedStatusData(installation, summary, native, drift, recovery, disposition)
	if err != nil {
		return cli.Response{}, err
	}
	if !installed && recovery.State() == cli.RecoveryStateNone {
		return statusNotFoundResponse(data, request.InstallationID())
	}
	if record.Lifecycle == "active" {
		warnings, err = appendStatusWarnings(warnings, data)
		if err != nil {
			return cli.Response{}, err
		}
	}
	return statusResponse(data, warnings, updateProblem)
}

func (s statusService) List(_ context.Context, request cli.ListRequest) (cli.Response, error) {
	snapshot, stateErr := s.state.Snapshot()
	recovery := recoveryFromState(stateErr, false, nil)
	if recovery.State() != cli.RecoveryStateNone {
		problem := statusProblem("recovery_required", "installation state requires attention before it can be listed")
		commandResult, err := result.New(result.Facts{Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone, Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone, Failure: result.FailureRecovery, UpdateDisposition: result.UpdateNotChecked, Errors: []result.Problem{*problem}})
		if err != nil {
			return cli.Response{}, err
		}
		return cli.NewResponse(cli.CommandList, commandResult, nil, cli.UnavailableData{})
	}
	summaries := make([]cli.InstallationSummary, 0, len(snapshot.Installations))
	for _, record := range snapshot.Installations {
		if request.HasTarget() && string(request.Target()) != record.Target || request.HasScope() && string(request.Scope()) != record.Scope {
			continue
		}
		summary, err := summaryFromRecord(record)
		if err != nil {
			return cli.Response{}, err
		}
		summaries = append(summaries, summary)
	}
	data, err := cli.NewListData(summaries)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := result.New(result.Facts{Status: result.StatusOK, Phase: result.PhaseNone, Outcome: result.OutcomeNone, Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone, Failure: result.FailureNone, UpdateDisposition: result.UpdateNotChecked})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandList, commandResult, nil, data)
}

func summaryFromRecord(record installstate.Record) (cli.InstallationSummary, error) {
	source, err := recordedSourceFromRecord(record)
	if err != nil {
		return cli.InstallationSummary{}, err
	}
	id, err := domain.NewInstallationID(record.InstallationID)
	if err != nil {
		return cli.InstallationSummary{}, err
	}
	lastOperation, err := domain.NewOperationID(record.LastOperation.ID)
	if err != nil {
		return cli.InstallationSummary{}, err
	}
	if len(record.Components) != 0 {
		components, componentErr := recordedComponentsFromRecord(record)
		if componentErr != nil {
			return cli.InstallationSummary{}, componentErr
		}
		return cli.NewDetailedCompositionSummary(
			id, cli.BuildTarget(record.Target), cli.Scope(record.Scope), record.ScopeRoot, record.Lifecycle, components,
			packageIDs(record.Packages), record.Selection.ResolvedAssets, record.Health, len(record.History), lastOperation,
		)
	}
	return cli.NewInstallationSummary(cli.InstallationSummaryInput{
		ID:              id,
		ToolkitID:       record.ToolkitID,
		Target:          cli.BuildTarget(record.Target),
		Scope:           cli.Scope(record.Scope),
		ScopeRoot:       record.ScopeRoot,
		Lifecycle:       record.Lifecycle,
		Source:          source,
		RequestedBundle: record.Selection.RequestedBundle,
		ResolvedBundles: record.Selection.ResolvedBundles,
		Packages:        packageIDs(record.Packages),
		ResolvedAssets:  record.Selection.ResolvedAssets,
		Health:          record.Health,
		HistoryCount:    len(record.History),
		LastOperation:   lastOperation,
	})
}

func recoveryFromState(stateErr error, markerPresent bool, markerErr error) cli.RecoveryState {
	kind := cli.RecoveryStateNone
	switch {
	case errors.Is(stateErr, installstate.ErrUnsupportedSchema), errors.Is(markerErr, installstate.ErrUnsupportedMarkerSchema):
		kind = cli.RecoveryUnsupportedSchema
	case stateErr != nil || markerErr != nil:
		kind = cli.RecoveryUnknown
	case markerPresent:
		kind = cli.RecoveryIncompleteJournal
	}
	recovery, err := cli.NewRecoveryState(kind, "")
	if err != nil {
		panic(err)
	}
	return recovery
}

func installationFromRecord(record installstate.Record) (cli.Installation, error) {
	source, err := recordedSourceFromRecord(record)
	if err != nil {
		return cli.Installation{}, err
	}
	installationID, err := domain.NewInstallationID(record.InstallationID)
	if err != nil {
		return cli.Installation{}, err
	}
	if len(record.Components) != 0 {
		components, componentErr := recordedComponentsFromRecord(record)
		if componentErr != nil {
			return cli.Installation{}, componentErr
		}
		return cli.NewComposedInstallation(installationID, packageIDs(record.Packages), components, record.AI4JVersion)
	}
	return cli.NewInstallation(installationID, record.ToolkitID, packageIDs(record.Packages), source, record.ToolkitVersion, record.AI4JVersion, "")
}

func recordedComponentsFromRecord(record installstate.Record) ([]cli.RecordedComponent, error) {
	components := make([]cli.RecordedComponent, len(record.Components))
	for index, component := range record.Components {
		source, err := recordedSourceFromStateSource(component.Source)
		if err != nil {
			return nil, err
		}
		components[index], err = cli.NewRecordedComponent(
			component.Name, component.Tag, source, component.ToolkitVersion,
			component.Selection.ResolvedBundles, component.Packages, component.Selection.ResolvedAssets,
		)
		if err != nil {
			return nil, err
		}
	}
	return components, nil
}

func packageIDs(packages []installstate.NativePackage) []string {
	ids := make([]string, len(packages))
	for index, pkg := range packages {
		ids[index] = pkg.ID
	}
	return ids
}

func recordedSourceFromRecord(record installstate.Record) (cli.RecordedSource, error) {
	if len(record.Components) != 0 {
		return recordedSourceFromStateSource(record.Components[0].Source)
	}
	return recordedSourceFromStateSource(record.Source)
}

func recordedSourceFromStateSource(sourceState installstate.Source) (cli.RecordedSource, error) {
	if sourceState.Mode == "development_source" {
		digest, err := domain.NewRenderedDigest(sourceState.SourceDigest)
		if err != nil {
			return cli.RecordedSource{}, err
		}
		return cli.NewRecordedDevelopmentSource(sourceState.Checkout, digest, sourceState.Dirty)
	}
	selection, err := domain.NewSourceSelection(sourceState.Selection)
	if err != nil {
		return cli.RecordedSource{}, err
	}
	repository, err := domain.NewRepositoryIdentity(sourceState.Repository)
	if err != nil {
		return cli.RecordedSource{}, err
	}
	commit, err := domain.NewCommitOID(sourceState.Commit)
	if err != nil {
		return cli.RecordedSource{}, err
	}
	requested := ""
	hasRequested := sourceState.RequestedRef != nil
	if hasRequested {
		requested = *sourceState.RequestedRef
	}
	transport, err := domain.NewGitTransport(sourceState.Transport)
	if err != nil {
		return cli.RecordedSource{}, err
	}
	source, err := cli.NewRecordedSource(selection, repository, transport, requested, hasRequested, cli.RefKind(sourceState.RefKind), commit)
	if err != nil {
		return cli.RecordedSource{}, err
	}
	return source, nil
}

func (s statusService) inspectDrift(record installstate.Record) ([]cli.Drift, error) {
	catalogPath := filepath.Join(s.state.DataRoot(), filepath.FromSlash(record.Catalog.Path))
	if record.Scope == "project-shared" {
		catalogPath = filepath.Join(record.ScopeRoot, filepath.FromSlash(record.Catalog.Path))
	}
	checks := []struct {
		path     string
		resource string
		checksum string
	}{
		{catalogPath, record.Catalog.Path, record.Catalog.Checksum},
		{filepath.Join(record.ScopeRoot, filepath.FromSlash(record.Rules.Path)), record.Rules.Path, record.Rules.Checksum},
	}
	if record.Scope == "project-shared" && record.NativeCatalog != (installstate.OwnedFile{}) {
		nativeCatalog, err := projectSharedNativeCatalogFile(record)
		if err != nil {
			return nil, err
		}
		checks = append(checks, struct {
			path     string
			resource string
			checksum string
		}{filepath.Join(s.state.DataRoot(), filepath.FromSlash(nativeCatalog.Path)), nativeCatalog.Path, nativeCatalog.Checksum})
	}
	items := make([]cli.Drift, 0, len(checks))
	for _, check := range checks {
		if check.resource == "" {
			continue
		}
		state := inspectFileDrift(check.path, check.checksum)
		if record.Scope == "project-shared" && check.resource == ".claude/settings.json" {
			state = inspectProjectMarketplaceDrift(record)
		}
		item, err := cli.NewDrift(check.resource, state)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func inspectFileDrift(path, expectedChecksum string) cli.DriftState {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return cli.DriftMissing
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximumStatusFileBytes {
		return cli.DriftConflicting
	}
	file, err := os.Open(path)
	if err != nil {
		return cli.DriftConflicting
	}
	defer file.Close()
	digest := sha256.New()
	read, err := io.Copy(digest, io.LimitReader(file, maximumStatusFileBytes+1))
	if err != nil || read > maximumStatusFileBytes {
		return cli.DriftConflicting
	}
	if fmt.Sprintf("%x", digest.Sum(nil)) != expectedChecksum {
		return cli.DriftModified
	}
	return cli.DriftUnchanged
}

func (s statusService) checkUpdates(ctx context.Context, record installstate.Record) (result.UpdateDisposition, *result.Problem, []result.Warning) {
	if len(record.Components) != 0 {
		return s.checkCompositionUpdates(ctx, record)
	}
	selection := selectionFromRecord(record)
	if record.Source.Mode == "development_source" {
		options, err := cli.NewDevelopmentSourceOptions(record.Source.Checkout, true)
		if err != nil {
			return result.UpdateUnknown, statusProblem("update_check_failed", "stored local source could not be checked"), nil
		}
		report := s.validation.SelectLifecycle(ctx, options, selection.Bundle())
		if len(report.Problems) != 0 {
			problem := report.Problems[0]
			return result.UpdateUnknown, &problem, nil
		}
		if !report.HasSource() {
			return result.UpdateUnknown, statusProblem("update_check_failed", "local source could not be checked"), nil
		}
		if report.Source.SourceDigest().String() == record.Source.SourceDigest {
			return result.UpdateUpToDate, nil, nil
		}
		return result.UpdateAvailable, nil, nil
	}
	if record.Source.RefKind == cli.RefCommit.String() {
		return result.UpdatePinned, nil, nil
	}
	installed, err := domain.NewCommitOID(record.Source.Commit)
	if err != nil {
		return result.UpdateUnknown, statusProblem("update_check_failed", "installed source commit is invalid"), nil
	}
	options, err := updateSourceOptions(record)
	if err != nil {
		return result.UpdateUnknown, statusProblem("update_check_failed", "installed source selection is invalid"), nil
	}
	update := s.validation.ValidateUpdate(ctx, options, installed)
	if len(update.Report.Problems) != 0 {
		problem := update.Report.Problems[0]
		return result.UpdateUnknown, &problem, nil
	}
	if update.Report.Failure != validation.FailureNone || !update.Report.HasSource() {
		return result.UpdateUnknown, statusProblem("update_check_failed", "Git source could not be checked"), nil
	}
	if record.Source.RefKind == cli.RefTag.String() {
		if update.Report.Source.Commit().OID() == installed {
			return result.UpdatePinned, nil, nil
		}
		return result.UpdateRefRewritten, nil, nil
	}
	switch update.Disposition {
	case gitsource.UpdateNoChange:
		return result.UpdateUpToDate, nil, nil
	case gitsource.UpdateAvailable:
		return result.UpdateAvailable, nil, nil
	case gitsource.UpdateRefRewritten:
		return result.UpdateRefRewritten, nil, nil
	default:
		return result.UpdateUnknown, statusProblem("update_check_failed", "Git source could not be checked"), nil
	}
}

func (s statusService) checkCompositionUpdates(ctx context.Context, record installstate.Record) (result.UpdateDisposition, *result.Problem, []result.Warning) {
	var unavailable []string
	var rewritten []string
	for _, component := range record.Components {
		remote, err := storedSourceRemote(component.Source)
		if err != nil {
			unavailable = append(unavailable, component.Name)
			continue
		}
		options, err := cli.NewSourceOptions(remote.Endpoint(), true, "refs/tags/"+component.Tag, true)
		if err != nil {
			unavailable = append(unavailable, component.Name)
			continue
		}
		installed, err := domain.NewCommitOID(component.Source.Commit)
		if err != nil {
			unavailable = append(unavailable, component.Name)
			continue
		}
		update := s.validation.ValidateUpdate(ctx, options, installed)
		if len(update.Report.Problems) != 0 || update.Report.Failure != validation.FailureNone || !update.Report.HasSource() {
			unavailable = append(unavailable, component.Name)
			continue
		}
		if update.Report.Source.Commit().OID() != installed {
			rewritten = append(rewritten, component.Name)
		}
	}
	warnings := make([]result.Warning, len(rewritten))
	for index, component := range rewritten {
		warnings[index], _ = result.NewWarning("component_ref_rewritten", "a pinned component tag now resolves to a different commit", compositionContext(component))
	}
	if len(unavailable) != 0 {
		problem, _ := result.NewProblem("update_check_failed", "one or more composition components could not be checked", compositionContexts(unavailable))
		return result.UpdateUnknown, &problem, warnings
	}
	if len(rewritten) != 0 {
		return result.UpdateRefRewritten, nil, warnings
	}
	return result.UpdatePinned, nil, nil
}

func compositionContexts(components []string) []result.Context {
	contexts := make([]result.Context, len(components))
	for index, component := range components {
		contexts[index], _ = result.NewContext("component", component)
	}
	return contexts
}

func statusResponse(data cli.StatusData, warnings []result.Warning, updateProblem *result.Problem) (cli.Response, error) {
	status := result.StatusOK
	failure := result.FailureNone
	var problems []result.Problem
	recovery := data.RecoveryState()
	switch {
	case recovery.State() != cli.RecoveryStateNone:
		status = result.StatusError
		failure = result.FailureRecovery
		problem := statusProblem("recovery_required", "installation state requires attention before another modifying command")
		problems = []result.Problem{*problem}
	case updateProblem != nil:
		status = result.StatusError
		failure = result.FailureSource
		problems = []result.Problem{*updateProblem}
	case statusIsArchived(data):
		status = result.StatusNoChange
	case len(warnings) != 0:
		status = result.StatusDegraded
	case data.UpdateDisposition() == result.UpdatePinned || data.UpdateDisposition() == result.UpdateUpToDate:
		status = result.StatusNoChange
	}
	commandResult, err := result.New(result.Facts{
		Status: status, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: failure, UpdateDisposition: data.UpdateDisposition(), Warnings: warnings, Errors: problems,
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandStatus, commandResult, nil, data)
}

func observedStatusHealth(record installstate.Record, native cli.NativeState, drift []cli.Drift, recovery cli.RecoveryState) string {
	if recovery.State() != cli.RecoveryStateNone {
		return "recovery_required"
	}
	if record.Lifecycle == "archived" {
		return "healthy"
	}
	for _, item := range drift {
		if item.State() != cli.DriftUnchanged {
			return "drifted"
		}
	}
	if native.Registration() == cli.NativeNotRegistered || native.Installation() == cli.NativeNotInstalled || native.Enablement() == cli.NativeDisabled {
		return "drifted"
	}
	if native.Registration() != cli.NativeRegistered || native.Installation() != cli.NativeInstalled || native.Enablement() != cli.NativeEnabled {
		return "unknown"
	}
	return "healthy"
}

func statusIsArchived(data cli.StatusData) bool {
	summary, ok := data.Summary()
	return ok && summary.Lifecycle() == "archived"
}

func statusNotFoundResponse(data cli.StatusData, installationID domain.InstallationID) (cli.Response, error) {
	context, err := result.NewContext("installation", installationID.String())
	if err != nil {
		return cli.Response{}, err
	}
	problem, err := result.NewProblem("installation_not_found", "selected installation was not found", []result.Context{context})
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureConflict, UpdateDisposition: data.UpdateDisposition(), Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandStatus, commandResult, nil, data)
}

func appendStatusWarnings(warnings []result.Warning, data cli.StatusData) ([]result.Warning, error) {
	for _, drift := range data.Drift() {
		if drift.State() == cli.DriftUnchanged {
			continue
		}
		resource, err := result.NewContext("resource", drift.Resource())
		if err != nil {
			return nil, err
		}
		state, err := result.NewContext("state", string(drift.State()))
		if err != nil {
			return nil, err
		}
		warning, err := result.NewWarning("managed_resource_drift", "managed installation content needs attention", []result.Context{resource, state})
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, warning)
	}
	native := data.NativeState()
	if installation, present := data.Installation(); present && len(installation.Components()) != 0 {
		return warnings, nil
	}
	checks := []struct {
		unhealthy bool
		code      string
		message   string
	}{
		{native.Registration() == cli.NativeNotRegistered, "native_registration_missing", "Claude does not have the toolkit marketplace registered"},
		{native.Installation() == cli.NativeNotInstalled, "native_plugin_missing", "Claude does not have the toolkit plugin installed"},
		{native.Enablement() == cli.NativeDisabled, "native_plugin_disabled", "Claude has the toolkit plugin disabled"},
	}
	for _, check := range checks {
		if !check.unhealthy {
			continue
		}
		warning, err := result.NewWarning(check.code, check.message, nil)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, warning)
	}
	return warnings, nil
}

func statusProblem(code, message string) *result.Problem {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return nil
	}
	return &problem
}

func unobservableNative() (cli.NativeState, error) {
	return cli.NewNativeState(cli.NativeStateInput{
		Registration:  cli.NativeRegistrationNotObservable,
		Installation:  cli.NativeInstallationNotObservable,
		Enablement:    cli.NativeEnablementNotObservable,
		Activation:    cli.NativeActivationNotObservable,
		Reload:        cli.NativeReloadNotObservable,
		NextSession:   cli.NativeNextSessionNotObservable,
		Policy:        cli.NativePolicyNotObservable,
		VersionStatus: cli.NativeVersionNotApplicable,
	})
}

func unknownNative() (cli.NativeState, error) {
	return cli.NewNativeState(cli.NativeStateInput{
		Registration:  cli.NativeRegistrationUnknown,
		Installation:  cli.NativeInstallationUnknown,
		Enablement:    cli.NativeEnablementUnknown,
		Activation:    cli.NativeActivationNotObservable,
		Reload:        cli.NativeReloadNotObservable,
		NextSession:   cli.NativeNextSessionNotObservable,
		Policy:        cli.NativePolicyNotObservable,
		VersionStatus: cli.NativeVersionNotApplicable,
	})
}

func observedNative(observation validation.NativeStatus) (cli.NativeState, error) {
	registration := cli.NativeNotRegistered
	if observation.MarketplaceRegistered {
		registration = cli.NativeRegistered
	}
	installation := cli.NativeNotInstalled
	enablement := cli.NativeEnablementNotObservable
	if observation.PluginInstalled {
		installation = cli.NativeInstalled
		enablement = cli.NativeDisabled
		if observation.PluginEnabled {
			enablement = cli.NativeEnabled
		}
	}
	return cli.NewNativeState(cli.NativeStateInput{
		Registration:  registration,
		Installation:  installation,
		Enablement:    enablement,
		Activation:    cli.NativeActivationNotObservable,
		Reload:        cli.NativeReloadNotObservable,
		NextSession:   cli.NativeNextSessionNotObservable,
		Policy:        cli.NativePolicyNotObservable,
		VersionStatus: cli.NativeVersionNotApplicable,
	})
}
