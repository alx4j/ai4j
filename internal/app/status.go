package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	validation "github.com/alx4j/ai4j/internal/validate"
)

const maximumStatusFileBytes = 16 << 20

type statusValidation interface {
	InspectNativeStatus(context.Context) (validation.NativeStatus, *result.Problem)
	InspectNativeStatusAt(context.Context, string, string, string) (validation.NativeStatus, *result.Problem)
	SelectLifecycle(context.Context, cli.SourceOptions, bool, []string, []string) validation.LifecycleSelection
	ValidateUpdate(context.Context, cli.SourceOptions, domain.CommitOID) validation.UpdateReport
}

type nativeStatusInspector interface {
	InspectNativeStatusAt(context.Context, string, string, string) (validation.NativeStatus, *result.Problem)
}

type statusService struct {
	validation statusValidation
	state      installstate.Store
	home       string
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
			var observation validation.NativeStatus
			var problem *result.Problem
			if record.MarketplaceID == "" {
				observation, problem = s.validation.InspectNativeStatus(ctx)
			} else {
				observation, problem = s.validation.InspectNativeStatusAt(ctx, nativeDirectory(record), record.MarketplaceID, record.PluginID+"@"+record.MarketplaceID)
			}
			if problem != nil {
				native, err = unknownNative()
				warning, warningErr := result.NewWarning(problem.Code(), problem.Message(), nil)
				if warningErr != nil {
					return cli.Response{}, warningErr
				}
				warnings = append(warnings, warning)
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
		disposition, updateProblem = s.checkUpdates(ctx, record)
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
	return cli.NewDetailedInstallationSummary(id, record.ToolkitID, cli.BuildTarget(record.Target), cli.Scope(record.Scope), record.ScopeRoot, record.Lifecycle, source, record.Selection.All, record.Selection.Assets, record.Selection.Bundles, record.Selection.Resolved, record.Health, len(record.History), lastOperation)
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
	toolkitVersion := record.ToolkitVersion
	if toolkitVersion == "" {
		toolkitVersion = "unversioned"
	}
	return cli.NewInstallation(installationID, record.ToolkitID, record.PluginID, source, toolkitVersion, record.AI4JVersion, "")
}

func recordedSourceFromRecord(record installstate.Record) (cli.RecordedSource, error) {
	if record.Source.Mode == "development_source" {
		digest, err := domain.NewRenderedDigest(record.Source.SourceDigest)
		if err != nil {
			return cli.RecordedSource{}, err
		}
		return cli.NewRecordedDevelopmentSource(record.Source.Checkout, digest, record.Source.Dirty)
	}
	selection, err := domain.NewSourceSelection(record.Source.Selection)
	if err != nil {
		return cli.RecordedSource{}, err
	}
	repository, err := domain.NewRepositoryIdentity(record.Source.Repository)
	if err != nil {
		return cli.RecordedSource{}, err
	}
	commit, err := domain.NewCommitOID(record.Source.Commit)
	if err != nil {
		return cli.RecordedSource{}, err
	}
	requested := ""
	hasRequested := record.Source.RequestedRef != nil
	if hasRequested {
		requested = *record.Source.RequestedRef
	}
	source, err := cli.NewRecordedSource(selection, repository, requested, hasRequested, cli.RefKind(record.Source.RefKind), commit)
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
	rulesRoot := record.ScopeRoot
	if rulesRoot == "" || record.Scope == "user" && strings.HasPrefix(record.Rules.Path, ".claude/") {
		rulesRoot = s.home
	}
	checks := []struct {
		path     string
		resource string
		checksum string
	}{
		{catalogPath, record.Catalog.Path, record.Catalog.Checksum},
		{filepath.Join(rulesRoot, filepath.FromSlash(record.Rules.Path)), record.Rules.Path, record.Rules.Checksum},
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

func (s statusService) checkUpdates(ctx context.Context, record installstate.Record) (result.UpdateDisposition, *result.Problem) {
	if record.Source.Mode == "development_source" {
		options, err := cli.NewDevelopmentSourceOptions(record.Source.Checkout, true)
		if err != nil {
			return result.UpdateUnknown, statusProblem("update_check_failed", "stored local source could not be checked")
		}
		selection := selectionFromRecord(record)
		report := s.validation.SelectLifecycle(ctx, options, selection.SelectAll(), selection.Assets(), selection.Bundles())
		if len(report.Problems) != 0 {
			problem := report.Problems[0]
			return result.UpdateUnknown, &problem
		}
		if !report.HasSource() {
			return result.UpdateUnknown, statusProblem("update_check_failed", "local source could not be checked")
		}
		if report.Source.SourceDigest().String() == record.Source.SourceDigest {
			return result.UpdateUpToDate, nil
		}
		return result.UpdateAvailable, nil
	}
	if record.Source.RefKind == cli.RefCommit.String() {
		return result.UpdatePinned, nil
	}
	installed, err := domain.NewCommitOID(record.Source.Commit)
	if err != nil {
		return result.UpdateUnknown, statusProblem("update_check_failed", "installed source commit is invalid")
	}
	options, err := updateSourceOptions(record)
	if err != nil {
		return result.UpdateUnknown, statusProblem("update_check_failed", "installed source selection is invalid")
	}
	update := s.validation.ValidateUpdate(ctx, options, installed)
	if len(update.Report.Problems) != 0 {
		problem := update.Report.Problems[0]
		return result.UpdateUnknown, &problem
	}
	if update.Report.Failure != validation.FailureNone || !update.Report.HasSource() {
		return result.UpdateUnknown, statusProblem("update_check_failed", "public GitHub source could not be checked")
	}
	if record.Source.RefKind == cli.RefTag.String() {
		if update.Report.Source.Commit().OID() == installed {
			return result.UpdatePinned, nil
		}
		return result.UpdateRefRewritten, nil
	}
	switch update.Disposition {
	case gitsource.UpdateNoChange:
		return result.UpdateUpToDate, nil
	case gitsource.UpdateAvailable:
		return result.UpdateAvailable, nil
	case gitsource.UpdateRefRewritten:
		return result.UpdateRefRewritten, nil
	default:
		return result.UpdateUnknown, statusProblem("update_check_failed", "public GitHub source could not be checked")
	}
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
	return cli.NewNativeState(
		cli.NativeRegistrationNotObservable, cli.NativeInstallationNotObservable, cli.NativeEnablementNotObservable,
		cli.NativeActivationNotObservable, cli.NativeReloadNotObservable, cli.NativeNextSessionNotObservable,
		cli.NativePolicyNotObservable, "", cli.NativeVersionNotApplicable,
	)
}

func unknownNative() (cli.NativeState, error) {
	return cli.NewNativeState(
		cli.NativeRegistrationUnknown, cli.NativeInstallationUnknown, cli.NativeEnablementUnknown,
		cli.NativeActivationNotObservable, cli.NativeReloadNotObservable, cli.NativeNextSessionNotObservable,
		cli.NativePolicyNotObservable, "", cli.NativeVersionNotApplicable,
	)
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
	return cli.NewNativeState(
		registration, installation, enablement, cli.NativeActivationNotObservable,
		cli.NativeReloadNotObservable, cli.NativeNextSessionNotObservable, cli.NativePolicyNotObservable,
		"", cli.NativeVersionNotApplicable,
	)
}
