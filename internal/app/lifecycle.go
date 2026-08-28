package app

import (
	"context"
	"crypto/rand"
	"io"
	"path/filepath"
	"slices"
	"time"

	"github.com/alx4j/ai4j/internal/buildinfo"
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	validation "github.com/alx4j/ai4j/internal/validate"
)

type lifecycleValidation interface {
	SelectLifecycle(context.Context, cli.SourceOptions, string) validation.LifecycleSelection
	ValidateUpdate(context.Context, cli.SourceOptions, domain.CommitOID) validation.UpdateReport
	InspectNativeStatusAt(context.Context, string, string, string) (validation.NativeStatus, *result.Problem)
}

type lifecycleService struct {
	validation lifecycleValidation
	state      installstate.Store
	runner     validation.ProcessRunner
	home       string
	claudeRoot string
	build      buildinfo.Info
	now        func() time.Time
	random     io.Reader
	acquire    func(context.Context) (func() error, error)
}

type lifecycleExecution struct {
	operation         cli.Operation
	source            validation.LifecycleSelection
	before            *installstate.Record
	desired           *installstate.Record
	beforeArtifacts   []installstate.NativeArtifact
	catalog           []byte
	catalogBefore     []byte
	rules             []byte
	artifacts         []installstate.NativeArtifact
	actions           []cli.Action
	content           []cli.ContentItem
	conflicts         []cli.Conflict
	degradedConflicts []cli.Conflict
	final             cli.FinalState
	disposition       result.UpdateDisposition
	rollback          *installstate.HistoryEntry
	nonRestorable     bool
}

type applyRequest struct {
	command           cli.Command
	output            cli.OutputMode
	approved          bool
	commandIO         CommandIO
	conflictPolicy    cli.ConflictPolicy
	expectedCommit    domain.CommitOID
	hasExpectedCommit bool
	expectedDigest    string
	hasExpectedDigest bool
	prepare           func(cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error)
}

func newLifecycleService(validationService lifecycleValidation, state installstate.Store, runner validation.ProcessRunner, home string, build buildinfo.Info, acquire func(context.Context) (func() error, error)) *lifecycleService {
	return &lifecycleService{
		validation: validationService,
		state:      state,
		runner:     runner,
		home:       home,
		build:      build,
		now:        time.Now,
		random:     rand.Reader,
		acquire:    acquire,
	}
}

func (s *lifecycleService) Install(ctx context.Context, request cli.InstallRequest, commandIO CommandIO) (cli.Response, error) {
	project, hasProject := request.Project()
	if request.DryRun() {
		execution, response, stop, err := s.prepareInstall(ctx, request.Source(), request.Target(), request.Scope(), project, hasProject, request.Selection(), request.InstallationID(), request.HasInstallationID(), cli.ConflictFail)
		if err != nil {
			return cli.Response{}, err
		}
		if stop {
			return response, nil
		}
		return s.planResponse(cli.CommandInstall, execution)
	}
	expectedCommit, hasExpectedCommit := request.ExpectedCommit()
	expectedDigest, hasExpectedDigest := request.ExpectedSourceDigest()
	return s.apply(ctx, applyRequest{
		command: cli.CommandInstall, output: request.OutputMode(), approved: request.Approved(), commandIO: commandIO,
		conflictPolicy: cli.ConflictFail, expectedCommit: expectedCommit, hasExpectedCommit: hasExpectedCommit,
		expectedDigest: expectedDigest, hasExpectedDigest: hasExpectedDigest,
		prepare: func(policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
			return s.prepareInstall(ctx, request.Source(), request.Target(), request.Scope(), project, hasProject, request.Selection(), request.InstallationID(), request.HasInstallationID(), policy)
		},
	})
}

func (s *lifecycleService) Update(ctx context.Context, request cli.UpdateRequest, commandIO CommandIO) (cli.Response, error) {
	if request.DryRun() {
		execution, response, stop, err := s.prepareUpdate(ctx, request.InstallationID(), request.Source(), request.ConflictPolicy())
		if err != nil {
			return cli.Response{}, err
		}
		if stop {
			return response, nil
		}
		return s.planResponse(cli.CommandUpdate, execution)
	}
	expectedCommit, hasExpectedCommit := request.ExpectedCommit()
	if request.Source().HasRepository() || request.Source().HasReference() {
		if !hasExpectedCommit {
			return lifecycleFailure(cli.CommandUpdate, result.FailureConflict, "expected_commit_required", "changing source or reference requires --expected-commit", result.UpdateNotChecked, nil)
		}
	}
	expectedDigest, hasExpectedDigest := request.ExpectedSourceDigest()
	return s.apply(ctx, applyRequest{
		command: cli.CommandUpdate, output: request.OutputMode(), approved: request.Approved(), commandIO: commandIO,
		conflictPolicy: request.ConflictPolicy(), expectedCommit: expectedCommit, hasExpectedCommit: hasExpectedCommit,
		expectedDigest: expectedDigest, hasExpectedDigest: hasExpectedDigest,
		prepare: func(policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
			return s.prepareUpdate(ctx, request.InstallationID(), request.Source(), policy)
		},
	})
}

func (s *lifecycleService) Sync(ctx context.Context, request cli.SyncRequest, commandIO CommandIO) (cli.Response, error) {
	if request.DryRun() {
		execution, response, stop, err := s.prepareSync(ctx, request.InstallationID(), request.Selection(), request.AllowDirty(), request.ConflictPolicy())
		if err != nil {
			return cli.Response{}, err
		}
		if stop {
			return response, nil
		}
		return s.planResponse(cli.CommandSync, execution)
	}
	expectedDigest, hasExpectedDigest := request.ExpectedSourceDigest()
	return s.apply(ctx, applyRequest{
		command: cli.CommandSync, output: request.OutputMode(), approved: request.Approved(), commandIO: commandIO,
		conflictPolicy: request.ConflictPolicy(), expectedDigest: expectedDigest, hasExpectedDigest: hasExpectedDigest,
		prepare: func(policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
			return s.prepareSync(ctx, request.InstallationID(), request.Selection(), request.AllowDirty(), policy)
		},
	})
}

func (s *lifecycleService) Rollback(ctx context.Context, request cli.RollbackRequest, commandIO CommandIO) (cli.Response, error) {
	if request.DryRun() {
		execution, response, stop, err := s.prepareRollback(ctx, request.InstallationID(), request.OperationID(), request.HasOperationID(), request.ConflictPolicy())
		if err != nil {
			return cli.Response{}, err
		}
		if stop {
			return response, nil
		}
		return s.planResponse(cli.CommandRollback, execution)
	}
	return s.apply(ctx, applyRequest{
		command: cli.CommandRollback, output: request.OutputMode(), approved: request.Approved(), commandIO: commandIO,
		conflictPolicy: request.ConflictPolicy(),
		prepare: func(policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
			return s.prepareRollback(ctx, request.InstallationID(), request.OperationID(), request.HasOperationID(), policy)
		},
	})
}

func (s *lifecycleService) Uninstall(ctx context.Context, request cli.UninstallRequest, commandIO CommandIO) (cli.Response, error) {
	if request.DryRun() {
		execution, response, stop, err := s.prepareUninstall(ctx, request.InstallationID(), request.ConflictPolicy())
		if err != nil {
			return cli.Response{}, err
		}
		if stop {
			return response, nil
		}
		return s.planResponse(cli.CommandUninstall, execution)
	}
	return s.apply(ctx, applyRequest{
		command: cli.CommandUninstall, output: request.OutputMode(), approved: request.Approved(), commandIO: commandIO,
		conflictPolicy: request.ConflictPolicy(),
		prepare: func(policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
			return s.prepareUninstall(ctx, request.InstallationID(), policy)
		},
	})
}

func (s *lifecycleService) apply(ctx context.Context, request applyRequest) (cli.Response, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return mutationLockResponse(request.command, err, result.UpdateNotChecked, nil)
	}
	defer func() { _ = release() }()
	if recovered, recoveryErr := s.reconcileInterrupted(ctx); recoveryErr != nil || !recovered {
		return lifecycleFailure(request.command, result.FailureRecovery, "recovery_required", "an interrupted operation requires recovery before another mutation", result.UpdateNotChecked, nil)
	}
	policy, ok, err := resolveInteractivePolicy(request.conflictPolicy, request.output, request.commandIO)
	if err != nil {
		return cli.Response{}, err
	}
	if !ok {
		return lifecycleFailure(request.command, result.FailureApproval, "interactive_terminal_required", "interactive conflict policy requires terminal input and output", result.UpdateNotChecked, nil)
	}
	execution, response, stop, err := request.prepare(policy)
	if err != nil {
		return cli.Response{}, err
	}
	if stop {
		return planAsCommand(response, request.command)
	}
	if len(execution.conflicts) != 0 {
		return lifecycleConflict(request.command, execution.conflicts, execution.disposition, execution.source.Warnings)
	}
	if request.hasExpectedCommit && execution.source.Source.Commit().OID() != request.expectedCommit {
		return lifecycleFailure(request.command, result.FailureConflict, "expected_commit_mismatch", "resolved source commit does not match --expected-commit", execution.disposition, execution.source.Warnings)
	}
	if execution.source.Source.Mode() == cli.SourceDevelopment && execution.source.Source.Dirty() && !request.hasExpectedDigest {
		return lifecycleFailure(request.command, result.FailureConflict, "expected_source_digest_required", "dirty local source apply requires --expected-source-digest", execution.disposition, execution.source.Warnings)
	}
	if request.hasExpectedDigest && (execution.source.Source.Mode() != cli.SourceDevelopment || execution.source.Source.SourceDigest().String() != request.expectedDigest) {
		return lifecycleFailure(request.command, result.FailureConflict, "expected_source_digest_mismatch", "local source digest does not match --expected-source-digest", execution.disposition, execution.source.Warnings)
	}
	plan, err := s.planResponse(request.command, execution)
	if err != nil {
		return cli.Response{}, err
	}
	if len(execution.actions) == 0 {
		return lifecycleNoChange(request.command, execution.operation, recordInstallation(execution.before, execution.desired), execution.final, execution.disposition, execution.source.Warnings)
	}
	approval, err := approveLifecycle(request.approved, request.output, request.commandIO, plan, execution.operation.String())
	if err != nil {
		return cli.Response{}, err
	}
	if approval == approvalDeclined {
		return cancelledResponse(request.command, "operation_cancelled", "operation was declined before mutation", execution.disposition, execution.source.Warnings)
	}
	if approval != approvalGranted {
		return lifecycleFailure(request.command, result.FailureApproval, "approval_required", "operation requires explicit approval", execution.disposition, execution.source.Warnings)
	}
	reportProgress(request.commandIO, "applying the approved changes...")
	return s.commitExecution(ctx, request.command, execution, policy, request.commandIO)
}

func (s *lifecycleService) prepareInstall(ctx context.Context, source cli.SourceOptions, target cli.BuildTarget, scope cli.Scope, project string, hasProject bool, selection cli.BundleSelection, installationID domain.InstallationID, reactivation bool, policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
	var before *installstate.Record
	scopeRoot := s.effectiveClaudeRoot()
	if reactivation {
		record, present, err := s.state.LoadByID(installationID.String())
		if err != nil || !present {
			return stopLifecycle(cli.CommandInstall, result.FailureConflict, "installation_not_found", "the selected archived installation does not exist")
		}
		before = cloneRecordPtr(&record)
		if record.Source.Mode == "development_source" {
			source, err = cli.NewDevelopmentSourceOptions(record.Source.Checkout, source.AllowDirty())
		} else {
			source, err = exactSourceOptions(record)
		}
		if err != nil {
			return stopLifecycle(cli.CommandInstall, result.FailureConflict, "installation_state_invalid", "the selected archived installation source is invalid")
		}
		selection = selectionFromRecord(record)
		target = cli.BuildTarget(record.Target)
		scope = cli.Scope(record.Scope)
		scopeRoot = record.ScopeRoot
	} else {
		if target != cli.BuildTargetClaude || !scope.Valid() {
			return stopLifecycle(cli.CommandInstall, result.FailureValidation, "unsupported_scope", "the requested target or scope is unsupported")
		}
		if scope == cli.ScopeProjectShared && source.HasCheckout() {
			return stopLifecycle(cli.CommandInstall, result.FailureValidation, "unsupported_scope", "local development sources cannot be installed at project-shared scope")
		}
		if scope != cli.ScopeUser {
			var err error
			scopeRoot, err = s.resolveProjectRoot(ctx, project, hasProject)
			if err != nil {
				return stopLifecycle(cli.CommandInstall, result.FailureEnvironment, "project_root_invalid", "a canonical Git project root could not be resolved")
			}
		}
	}
	report := s.validation.SelectLifecycle(ctx, source, selection.Bundle())
	if len(report.Problems) != 0 || !report.HasSource() {
		return stopSelection(cli.CommandInstall, report)
	}
	if !reactivation {
		installationID = installationIDFor(report, scope, scopeRoot)
	}
	desired, document, err := s.recordForSelection(report, selection, installationID, scope, scopeRoot)
	if err != nil {
		return stopLifecycle(cli.CommandInstall, result.FailureInternal, "plan_failed", "installation plan could not be created")
	}
	records, err := s.state.LoadAll()
	if err != nil {
		return stopLifecycle(cli.CommandInstall, result.FailureConflict, "installation_state_invalid", "installation state could not be read")
	}
	if !reactivation {
		for _, record := range records {
			if record.Target == desired.Target && record.Scope == desired.Scope && filepath.Clean(record.ScopeRoot) == filepath.Clean(desired.ScopeRoot) && record.ToolkitID == desired.ToolkitID {
				if record.Lifecycle == "archived" {
					return stopLifecycle(cli.CommandInstall, result.FailureConflict, "archived_installation_exists", "reactivate the archived installation by its installation ID")
				}
				before = cloneRecordPtr(&record)
				desired.InstallationID = record.InstallationID
			}
		}
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
	if before != nil && before.Lifecycle == "active" {
		conflicts = s.existingConflicts(ctx, *before, true)
	}
	visible, degraded := applyConflictPolicy(conflicts, policy, before != nil)
	if before != nil {
		visible, degraded = applyActiveTransitionConflictPolicy(conflicts, policy)
	}
	actions, err := s.transitionActions(cli.OperationInstall, before, &desired, len(report.Rules) != 0)
	if err != nil {
		return stopLifecycle(cli.CommandInstall, result.FailureInternal, "plan_failed", "installation plan actions could not be created")
	}
	if before != nil && recordsEquivalent(*before, desired) {
		actions = nil
	}
	final := mustFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	return lifecycleExecution{operation: cli.OperationInstall, source: report, before: before, desired: &desired, catalog: catalogBytes, catalogBefore: catalogBefore, rules: report.Rules, artifacts: retainedArtifacts(report), actions: actions, content: report.Content, conflicts: visible, degradedConflicts: degraded, final: final, disposition: result.UpdateNotChecked}, cli.Response{}, false, nil
}

func (s *lifecycleService) prepareUpdate(ctx context.Context, installationID domain.InstallationID, requested cli.SourceOptions, policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
	record, present, err := s.state.LoadByID(installationID.String())
	if err != nil || !present || record.Lifecycle != "active" {
		return stopLifecycle(cli.CommandUpdate, result.FailureConflict, "installation_not_active", "the selected installation is not active")
	}
	selection := selectionFromRecord(record)
	if record.Source.Mode == "development_source" {
		if requested.HasRepository() || requested.HasReference() || requested.HasCheckout() {
			return stopLifecycle(cli.CommandUpdate, result.FailureConflict, "source_mode_change_unsupported", "remote Git and local source modes cannot be changed in place")
		}
		options, optionErr := cli.NewDevelopmentSourceOptions(record.Source.Checkout, requested.AllowDirty())
		if optionErr != nil {
			return stopLifecycle(cli.CommandUpdate, result.FailureSource, "source_invalid", "stored local source is invalid")
		}
		report := s.validation.SelectLifecycle(ctx, options, selection.Bundle())
		if len(report.Problems) != 0 || !report.HasSource() {
			return stopSelection(cli.CommandUpdate, report)
		}
		disposition := result.UpdateUpToDate
		if report.Source.SourceDigest().String() != record.Source.SourceDigest {
			disposition = result.UpdateAvailable
		}
		return s.prepareExisting(ctx, cli.CommandUpdate, cli.OperationUpdate, record, report, options, selection, policy, disposition)
	}
	if requested.AllowDirty() || requested.HasCheckout() {
		return stopLifecycle(cli.CommandUpdate, result.FailureConflict, "source_mode_mismatch", "local source options are invalid for a remote Git installation")
	}
	installed, err := domain.NewCommitOID(record.Source.Commit)
	if err != nil {
		return stopLifecycle(cli.CommandUpdate, result.FailureConflict, "installation_state_invalid", "the selected installation source is invalid")
	}
	options, err := updateSourceOptions(record)
	if err != nil {
		return stopLifecycle(cli.CommandUpdate, result.FailureInternal, "source_invalid", "stored source selection is invalid")
	}
	sourceChange := requested.HasRepository() || requested.HasReference()
	if sourceChange {
		repository, repositoryProvided, sourceErr := storedSourceRepository(record)
		if sourceErr != nil {
			return stopLifecycle(cli.CommandUpdate, result.FailureInternal, "source_invalid", "stored source selection is invalid")
		}
		if requested.HasRepository() {
			repository = requested.Repository()
			repositoryProvided = true
		}
		reference := ""
		hasReference := false
		if requested.HasReference() {
			reference, hasReference = requested.Reference(), true
		}
		options, err = cli.NewSourceOptions(repository, repositoryProvided, reference, hasReference)
		if err != nil {
			return stopLifecycle(cli.CommandUpdate, result.FailureSource, "invalid_source", "requested source change is invalid")
		}
		report := s.validation.SelectLifecycle(ctx, options, selection.Bundle())
		if len(report.Problems) != 0 || !report.HasSource() {
			return stopSelection(cli.CommandUpdate, report)
		}
		if report.ToolkitID != record.ToolkitID {
			return stopLifecycle(cli.CommandUpdate, result.FailureConflict, "toolkit_identity_changed", "source changes must retain the toolkit identifier")
		}
		return s.prepareExisting(ctx, cli.CommandUpdate, cli.OperationUpdate, record, report, options, selection, policy, result.UpdateAvailable)
	}
	update := s.validation.ValidateUpdate(ctx, options, installed)
	if len(update.Report.Problems) != 0 {
		return stopValidation(cli.CommandUpdate, update.Report)
	}
	disposition := result.UpdateUnknown
	switch {
	case update.Disposition == gitsource.UpdateNoChange:
		disposition = result.UpdateUpToDate
	case update.Disposition == gitsource.UpdateAvailable:
		disposition = result.UpdateAvailable
	case update.Disposition == gitsource.UpdateRefRewritten:
		return stopLifecycle(cli.CommandUpdate, result.FailureConflict, "ref_rewritten", "the tracked branch is not a fast-forward update")
	default:
		return stopLifecycle(cli.CommandUpdate, result.FailureSource, "update_source_failed", "the stored source could not be checked")
	}
	report := s.validation.SelectLifecycle(ctx, options, selection.Bundle())
	if len(report.Problems) != 0 || !report.HasSource() {
		return stopSelection(cli.CommandUpdate, report)
	}
	return s.prepareExisting(ctx, cli.CommandUpdate, cli.OperationUpdate, record, report, options, selection, policy, disposition)
}

func (s *lifecycleService) prepareSync(ctx context.Context, installationID domain.InstallationID, selection cli.BundleSelection, allowDirty bool, policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
	record, present, err := s.state.LoadByID(installationID.String())
	if err != nil || !present || record.Lifecycle != "active" {
		return stopLifecycle(cli.CommandSync, result.FailureConflict, "installation_not_active", "the selected installation is not active")
	}
	options, err := exactSourceOptions(record)
	if record.Source.Mode == "development_source" {
		options, err = cli.NewDevelopmentSourceOptions(record.Source.Checkout, allowDirty)
	} else if allowDirty {
		return stopLifecycle(cli.CommandSync, result.FailureConflict, "source_mode_mismatch", "--allow-dirty is valid only for local development sources")
	}
	if err != nil {
		return stopLifecycle(cli.CommandSync, result.FailureInternal, "source_invalid", "stored source selection is invalid")
	}
	report := s.validation.SelectLifecycle(ctx, options, selection.Bundle())
	if len(report.Problems) != 0 || !report.HasSource() {
		return stopSelection(cli.CommandSync, report)
	}
	return s.prepareExisting(ctx, cli.CommandSync, cli.OperationSync, record, report, options, selection, policy, result.UpdateNotChecked)
}

func (s *lifecycleService) prepareExisting(ctx context.Context, command cli.Command, operation cli.Operation, record installstate.Record, report validation.LifecycleSelection, sourceOptions cli.SourceOptions, selection cli.BundleSelection, policy cli.ConflictPolicy, disposition result.UpdateDisposition) (lifecycleExecution, cli.Response, bool, error) {
	desired, document, err := s.recordForSelection(report, selection, mustInstallation(record.InstallationID), cli.Scope(record.Scope), record.ScopeRoot)
	if err != nil {
		return stopLifecycle(command, result.FailureInternal, "plan_failed", "lifecycle plan could not be created")
	}
	desired.History = slices.Clone(record.History)
	if err := s.inspectProjectLocal(ctx, &record, desired); err != nil {
		return stopLifecycle(command, result.FailureConflict, "project_local_conflict", "project-local rules cannot be proven safely untracked")
	}
	catalogBytes := document.Bytes()
	var catalogBefore []byte
	if desired.Scope == "project-shared" {
		catalogBefore, catalogBytes, err = s.planProjectShared(&desired, &record)
		if err != nil {
			return stopLifecycle(command, result.FailureConflict, "project_settings_conflict", "the shared project declaration cannot be changed safely")
		}
	}
	conflicts := s.existingConflicts(ctx, record, true)
	visible, degraded := applyActiveTransitionConflictPolicy(conflicts, policy)
	actions, err := s.transitionActions(operation, &record, &desired, len(report.Rules) != 0)
	if err != nil {
		return stopLifecycle(command, result.FailureInternal, "plan_failed", "lifecycle plan actions could not be created")
	}
	var beforeArtifacts []installstate.NativeArtifact
	var content []cli.ContentItem
	installedSelection := selectionFromRecord(record)
	if record.Source.Mode == "development_source" {
		beforeArtifacts = s.currentArtifacts(&record)
		if len(beforeArtifacts) == 0 && len(actions) != 0 {
			return stopLifecycle(command, result.FailureConflict, "rollback_artifact_unavailable", "exact native rollback material is unavailable")
		}
		content, err = recordedTransitionContent(record.Selection.ResolvedAssets, report.Content)
	} else {
		options, optionsErr := exactSourceOptions(record)
		if optionsErr != nil {
			return stopLifecycle(command, result.FailureConflict, "installation_state_invalid", "the selected installation source is invalid")
		}
		installed := s.validation.SelectLifecycle(ctx, options, installedSelection.Bundle())
		if len(installed.Problems) != 0 || !installed.HasSource() {
			return stopSelection(command, installed)
		}
		beforeArtifacts = retainedArtifacts(installed)
		content, err = diffActiveContent(installed.Content, report.Content)
	}
	if err != nil {
		return stopLifecycle(command, result.FailureInternal, "plan_failed", "active content changes could not be created")
	}
	if recordsEquivalent(record, desired) && len(conflicts) == 0 {
		actions = nil
		content, err = contentWithChange(report.Content, cli.ContentUnchanged)
		if err != nil {
			return stopLifecycle(command, result.FailureInternal, "plan_failed", "active content plan could not be created")
		}
	}
	final := mustFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	return lifecycleExecution{operation: operation, source: report, before: &record, desired: &desired, beforeArtifacts: beforeArtifacts, catalog: catalogBytes, catalogBefore: catalogBefore, rules: report.Rules, artifacts: retainedArtifacts(report), actions: actions, content: content, conflicts: visible, degradedConflicts: degraded, final: final, disposition: disposition}, cli.Response{}, false, nil
}

func (s *lifecycleService) prepareUninstall(ctx context.Context, installationID domain.InstallationID, policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
	record, present, err := s.state.LoadByID(installationID.String())
	if err != nil || !present {
		return stopLifecycle(cli.CommandUninstall, result.FailureConflict, "installation_not_found", "the selected installation does not exist")
	}
	final := mustFinalState(cli.StateAbsent, cli.StateAbsent, cli.StateAbsent)
	if record.Lifecycle == "archived" {
		return stopLifecycle(cli.CommandUninstall, result.FailureConflict, "installation_not_active", "the selected installation is not active")
	}
	var report validation.LifecycleSelection
	var beforeArtifacts []installstate.NativeArtifact
	selection := selectionFromRecord(record)
	nonRestorable := false
	if record.Source.Mode == "development_source" {
		beforeArtifacts = s.currentArtifacts(&record)
		report = recordedLifecycleSelection(record)
		if len(beforeArtifacts) == 0 {
			nonRestorable = true
			report.Warnings = append(report.Warnings, rollbackUnavailableWarning())
		}
	} else {
		options, optionsErr := exactSourceOptions(record)
		if optionsErr != nil {
			return stopLifecycle(cli.CommandUninstall, result.FailureConflict, "installation_state_invalid", "the selected installation source is invalid")
		}
		report = s.validation.SelectLifecycle(ctx, options, selection.Bundle())
		if len(report.Problems) != 0 || !report.HasSource() {
			return stopSelection(cli.CommandUninstall, report)
		}
		beforeArtifacts = retainedArtifacts(report)
	}
	desired := record
	var catalogAfter []byte
	if record.Scope == "project-shared" {
		catalogAfter, err = s.planProjectSharedRemoval(record)
		if err != nil {
			return stopLifecycle(cli.CommandUninstall, result.FailureConflict, "project_settings_conflict", "the shared project declaration cannot be removed safely")
		}
	}
	desired.Lifecycle = "archived"
	desired.Health = "healthy"
	desired.Catalog = installstate.OwnedFile{}
	desired.NativeCatalog = installstate.OwnedFile{}
	desired.Rules = installstate.OwnedFile{}
	desired.NativeResources = []string{}
	conflicts := s.existingConflicts(ctx, record, true)
	visible, degraded := applyRemovalConflictPolicy(conflicts, policy)
	actions, err := s.transitionActions(cli.OperationUninstall, &record, &desired, false)
	if err != nil {
		return stopLifecycle(cli.CommandUninstall, result.FailureInternal, "plan_failed", "uninstall plan actions could not be created")
	}
	content, err := contentWithChange(report.Content, cli.ContentRemoved)
	if err != nil {
		return stopLifecycle(cli.CommandUninstall, result.FailureInternal, "plan_failed", "uninstall content plan could not be created")
	}
	return lifecycleExecution{operation: cli.OperationUninstall, source: report, before: &record, desired: &desired, beforeArtifacts: beforeArtifacts, catalog: catalogAfter, actions: actions, content: content, conflicts: visible, degradedConflicts: degraded, final: final, disposition: result.UpdateNotChecked, nonRestorable: nonRestorable}, cli.Response{}, false, nil
}

func (s *lifecycleService) prepareRollback(ctx context.Context, installationID domain.InstallationID, operationID domain.OperationID, selected bool, policy cli.ConflictPolicy) (lifecycleExecution, cli.Response, bool, error) {
	record, present, err := s.state.LoadByID(installationID.String())
	if err != nil || !present {
		return stopLifecycle(cli.CommandRollback, result.FailureConflict, "installation_not_found", "the selected installation does not exist")
	}
	entry, found, err := s.state.LoadHistoryEntry(record.InstallationID, record.LastOperation.ID)
	if err != nil || !found {
		return stopLifecycle(cli.CommandRollback, result.FailureConflict, "rollback_unavailable", "the current installation has no retained rollback point")
	}
	if selected {
		entry, found, err = s.state.LoadHistoryEntry(record.InstallationID, operationID.String())
		if err != nil || !found {
			return stopLifecycle(cli.CommandRollback, result.FailureConflict, "rollback_not_found", "the selected rollback point does not exist")
		}
	}
	if !entry.Restorable || entry.After == nil || !sameCurrentState(record, *entry.After) {
		return stopLifecycle(cli.CommandRollback, result.FailureConflict, "rollback_conflict", "current installation state no longer matches the rollback point")
	}
	desired := cloneRecord(record)
	if entry.Before != nil {
		desired = cloneRecord(*entry.Before)
	} else {
		desired.Lifecycle = "archived"
		desired.Catalog = installstate.OwnedFile{}
		desired.NativeCatalog = installstate.OwnedFile{}
		desired.Rules = installstate.OwnedFile{}
		desired.NativeResources = []string{}
	}
	desired.History = slices.Clone(record.History)
	if desired.Lifecycle == "active" {
		if err := s.inspectProjectLocal(ctx, &record, desired); err != nil {
			return stopLifecycle(cli.CommandRollback, result.FailureConflict, "project_local_conflict", "project-local rules cannot be proven safely untracked")
		}
	}
	selection := selectionFromRecord(desired)
	var report validation.LifecycleSelection
	if desired.Source.Mode == "development_source" {
		report = recordedLifecycleSelection(desired)
	} else {
		options, optionsErr := exactSourceOptions(desired)
		if optionsErr != nil {
			return stopLifecycle(cli.CommandRollback, result.FailureConflict, "installation_state_invalid", "the rollback source is invalid")
		}
		report = s.validation.SelectLifecycle(ctx, options, selection.Bundle())
		if len(report.Problems) != 0 || !report.HasSource() {
			return stopSelection(cli.CommandRollback, report)
		}
	}
	rollbackArtifacts := cloneNativeArtifacts(entry.NativeArtifactsBefore)
	catalogBefore, catalogAfter := slices.Clone(entry.CatalogAfter), slices.Clone(entry.CatalogBefore)
	if desired.Lifecycle == "active" && desired.Scope != "project-shared" {
		catalogAfter, desired.Catalog, err = retainedRollbackCatalog(desired, rollbackArtifacts)
		if err != nil {
			return stopLifecycle(cli.CommandRollback, result.FailureInternal, "rollback_artifact_invalid", "retained rollback packages could not be prepared")
		}
	}
	conflicts := s.existingConflicts(ctx, record, record.Lifecycle == "active")
	visible, degraded := applyActiveTransitionConflictPolicy(conflicts, policy)
	if desired.Lifecycle == "archived" {
		conflicts = s.existingConflicts(ctx, record, true)
		visible, degraded = applyRemovalConflictPolicy(conflicts, policy)
	}
	actions, err := s.transitionActions(cli.OperationRollback, &record, &desired, desired.Rules != (installstate.OwnedFile{}))
	if err != nil {
		return stopLifecycle(cli.CommandRollback, result.FailureInternal, "plan_failed", "rollback plan actions could not be created")
	}
	final := mustFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	if desired.Lifecycle == "archived" {
		final = mustFinalState(cli.StateAbsent, cli.StateAbsent, cli.StateAbsent)
	}
	if record.Scope == "project-shared" {
		catalogBefore, catalogAfter, err = s.planProjectSharedRollback(record, &desired, entry.CatalogBefore)
		if err != nil {
			return stopLifecycle(cli.CommandRollback, result.FailureConflict, "project_settings_conflict", "the shared project declaration cannot be rolled back safely")
		}
	}
	return lifecycleExecution{operation: cli.OperationRollback, source: report, before: &record, desired: &desired, catalog: catalogAfter, catalogBefore: catalogBefore, rules: slices.Clone(entry.RulesBefore), artifacts: rollbackArtifacts, actions: actions, content: report.Content, conflicts: visible, degradedConflicts: degraded, final: final, disposition: result.UpdateNotChecked, rollback: &entry}, cli.Response{}, false, nil
}

func retainedArtifacts(report validation.LifecycleSelection) []installstate.NativeArtifact {
	artifacts := make([]installstate.NativeArtifact, len(report.Packages))
	for index, pkg := range report.Packages {
		artifacts[index] = installstate.NativeArtifact{PackageID: pkg.ID, Bytes: slices.Clone(pkg.NativeArtifact)}
	}
	return artifacts
}
