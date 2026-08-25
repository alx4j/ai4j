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
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	"github.com/alx4j/ai4j/internal/target/claude/catalog"
	validation "github.com/alx4j/ai4j/internal/validate"
	"github.com/alx4j/ai4j/internal/workspace"
)

type v1LifecycleValidation interface {
	SelectLifecycle(context.Context, cli.SourceOptions, bool, []string, []string) validation.LifecycleSelection
	ValidateUpdate(context.Context, cli.SourceOptions, domain.CommitOID) validation.UpdateReport
	InspectNativeStatusAt(context.Context, string, string, string) (validation.NativeStatus, *result.Problem)
}

type v1LifecycleService struct {
	base       *installer
	validation v1LifecycleValidation
}

type v1Execution struct {
	operation         cli.Operation
	source            validation.LifecycleSelection
	before            *installstate.Record
	desired           *installstate.Record
	catalog           []byte
	catalogBefore     []byte
	rules             []byte
	artifact          []byte
	actions           []cli.Action
	content           []cli.ContentItem
	conflicts         []cli.Conflict
	degradedConflicts []cli.Conflict
	final             cli.FinalState
	disposition       result.UpdateDisposition
	rollback          *installstate.HistoryEntry
}

func newV1LifecycleService(base *installer, validationService v1LifecycleValidation) *v1LifecycleService {
	return &v1LifecycleService{base: base, validation: validationService}
}

func (v *v1LifecycleService) PlanInstall(ctx context.Context, request cli.PlanInstallRequest) (cli.Response, error) {
	project, hasProject := request.Project()
	execution, response, stop := v.prepareInstall(ctx, request.Source(), request.Target(), request.Scope(), project, hasProject, request.Selection(), request.InstallationID(), request.HasInstallationID(), cli.ConflictFail)
	if stop {
		return response, nil
	}
	return v.planResponse(cli.CommandPlanInstall, execution)
}

func (v *v1LifecycleService) Install(ctx context.Context, request cli.InstallRequest, commandIO CommandIO) (cli.Response, error) {
	project, hasProject := request.Project()
	return v.apply(ctx, cli.CommandInstall, request.OutputMode(), request.Approved(), commandIO, cli.ConflictFail, func(policy cli.ConflictPolicy) (v1Execution, cli.Response, bool) {
		return v.prepareInstall(ctx, request.Source(), request.Target(), request.Scope(), project, hasProject, request.Selection(), request.InstallationID(), request.HasInstallationID(), policy)
	}, request.ExpectedCommit, request.ExpectedSourceDigest)
}

func (v *v1LifecycleService) PlanUpdate(ctx context.Context, request cli.PlanUpdateRequest) (cli.Response, error) {
	execution, response, stop := v.prepareUpdate(ctx, request.InstallationID(), request.Source(), request.ConflictPolicy())
	if stop {
		return response, nil
	}
	return v.planResponse(cli.CommandPlanUpdate, execution)
}

func (v *v1LifecycleService) Update(ctx context.Context, request cli.UpdateRequest, commandIO CommandIO) (cli.Response, error) {
	if request.Source().HasRepository() || request.Source().HasReference() {
		if _, supplied := request.ExpectedCommit(); !supplied {
			return lifecycleFailure(cli.CommandUpdate, result.FailureConflict, "expected_commit_required", "source or reference migration requires --expected-commit", result.UpdateNotChecked, nil)
		}
	}
	return v.apply(ctx, cli.CommandUpdate, request.OutputMode(), request.Approved(), commandIO, request.ConflictPolicy(), func(policy cli.ConflictPolicy) (v1Execution, cli.Response, bool) {
		return v.prepareUpdate(ctx, request.InstallationID(), request.Source(), policy)
	}, request.ExpectedCommit, request.ExpectedSourceDigest)
}

func (v *v1LifecycleService) PlanSync(ctx context.Context, request cli.PlanSyncRequest) (cli.Response, error) {
	execution, response, stop := v.prepareSync(ctx, request.InstallationID(), request.Selection(), request.AllowDirty(), request.ConflictPolicy())
	if stop {
		return response, nil
	}
	return v.planResponse(cli.CommandPlanSync, execution)
}

func (v *v1LifecycleService) Sync(ctx context.Context, request cli.SyncRequest, commandIO CommandIO) (cli.Response, error) {
	return v.apply(ctx, cli.CommandSync, request.OutputMode(), request.Approved(), commandIO, request.ConflictPolicy(), func(policy cli.ConflictPolicy) (v1Execution, cli.Response, bool) {
		return v.prepareSync(ctx, request.InstallationID(), request.Selection(), request.AllowDirty(), policy)
	}, func() (domain.CommitOID, bool) { return domain.CommitOID{}, false }, request.ExpectedSourceDigest)
}

func (v *v1LifecycleService) PlanRollback(ctx context.Context, request cli.PlanRollbackRequest) (cli.Response, error) {
	execution, response, stop := v.prepareRollback(ctx, request.InstallationID(), request.OperationID(), request.HasOperationID(), request.ConflictPolicy())
	if stop {
		return response, nil
	}
	return v.planResponse(cli.CommandPlanRollback, execution)
}

func (v *v1LifecycleService) Rollback(ctx context.Context, request cli.RollbackRequest, commandIO CommandIO) (cli.Response, error) {
	return v.apply(ctx, cli.CommandRollback, request.OutputMode(), request.Approved(), commandIO, request.ConflictPolicy(), func(policy cli.ConflictPolicy) (v1Execution, cli.Response, bool) {
		return v.prepareRollback(ctx, request.InstallationID(), request.OperationID(), request.HasOperationID(), policy)
	}, func() (domain.CommitOID, bool) { return domain.CommitOID{}, false }, noExpectedDigest)
}

func (v *v1LifecycleService) PlanUninstall(ctx context.Context, request cli.PlanUninstallRequest) (cli.Response, error) {
	execution, response, stop := v.prepareUninstall(ctx, request.InstallationID(), request.ConflictPolicy())
	if stop {
		return response, nil
	}
	return v.planResponse(cli.CommandPlanUninstall, execution)
}

func (v *v1LifecycleService) Uninstall(ctx context.Context, request cli.UninstallRequest, commandIO CommandIO) (cli.Response, error) {
	return v.apply(ctx, cli.CommandUninstall, request.OutputMode(), request.Approved(), commandIO, request.ConflictPolicy(), func(policy cli.ConflictPolicy) (v1Execution, cli.Response, bool) {
		return v.prepareUninstall(ctx, request.InstallationID(), policy)
	}, func() (domain.CommitOID, bool) { return domain.CommitOID{}, false }, noExpectedDigest)
}

func (v *v1LifecycleService) History(_ context.Context, request cli.HistoryRequest) (cli.Response, error) {
	record, present, err := v.base.state.LoadByID(request.InstallationID().String())
	if err != nil || !present {
		return v1ReadFailure(cli.CommandHistory, result.FailureConflict, "installation_not_found", "the selected installation does not exist")
	}
	entries, err := v.base.state.LoadHistory(record.InstallationID)
	if err != nil {
		return v1ReadFailure(cli.CommandHistory, result.FailureRecovery, "history_invalid", "installation history could not be read")
	}
	descriptors := make([]cli.HistoryDescriptor, 0, len(entries))
	for _, entry := range entries {
		operationID, _ := domain.NewOperationID(entry.OperationID)
		timestamp, _ := time.Parse(time.RFC3339, entry.Timestamp)
		descriptor, descriptorErr := cli.NewHistoryDescriptor(operationID, cli.Operation(entry.Operation), timestamp, entry.Restorable)
		if descriptorErr != nil {
			return cli.Response{}, descriptorErr
		}
		descriptors = append(descriptors, descriptor)
	}
	installationID, _ := domain.NewInstallationID(record.InstallationID)
	data, err := cli.NewHistoryData(installationID, descriptors)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := neutralResult(result.StatusOK, result.FailureNone, nil)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandHistory, commandResult, nil, data)
}

func (v *v1LifecycleService) PlanHistoryPurge(_ context.Context, request cli.PlanHistoryPurgeRequest) (cli.Response, error) {
	return v.planHistoryPurge(request.InstallationID(), request.Selection(), request.OperationID())
}

func (v *v1LifecycleService) HistoryPurge(ctx context.Context, request cli.HistoryPurgeRequest, commandIO CommandIO) (cli.Response, error) {
	release, err := v.base.acquire(ctx)
	if err != nil {
		return lifecycleFailure(cli.CommandHistoryPurge, result.FailureConflict, "mutation_locked", "another AI4J modifying command is running", result.UpdateNotChecked, nil)
	}
	defer func() { _ = release() }()
	if recovered, recoveryErr := v.reconcileInterrupted(ctx); recoveryErr != nil || !recovered {
		return lifecycleFailure(cli.CommandHistoryPurge, result.FailureRecovery, "recovery_required", "an interrupted operation requires recovery before another mutation", result.UpdateNotChecked, nil)
	}
	plan, err := v.planHistoryPurge(request.InstallationID(), request.Selection(), request.OperationID())
	if err != nil || plan.Result().Status() == result.StatusError {
		return planAsCommand(plan, cli.CommandHistoryPurge)
	}
	data := plan.Data().(cli.PlanData)
	if len(data.Actions()) == 0 {
		return lifecycleNoChange(cli.CommandHistoryPurge, cli.OperationHistoryPurge, ptrInstallation(request.InstallationID()), data.ExpectedFinalState(), result.UpdateNotChecked, nil)
	}
	approval, err := approveLifecycle(request.Approved(), request.OutputMode(), commandIO, plan, "history purge")
	if err != nil {
		return cli.Response{}, err
	}
	if approval != approvalGranted {
		return lifecycleFailure(cli.CommandHistoryPurge, result.FailureApproval, "approval_required", "history purge requires explicit approval", result.UpdateNotChecked, nil)
	}
	record, _, _ := v.base.state.LoadByID(request.InstallationID().String())
	originalRecord := cloneRecord(record)
	entries, _ := v.base.state.LoadHistory(record.InstallationID)
	ids := selectedHistoryIDs(entries, request.Selection(), request.OperationID(), v.base.now())
	operationID, err := newOperationID(v.base.random)
	if err != nil {
		return cli.Response{}, err
	}
	marker, err := installstate.NewResourceMarker("history_purge", operationID.String(), record.InstallationID, recordSourceRevision(record), []string{"history:" + record.InstallationID, "owned:state/installation.json"})
	if err != nil || v.base.state.SaveMarker(marker) != nil {
		return lifecycleFailure(cli.CommandHistoryPurge, result.FailureInternal, "operation_marker_failed", "history purge could not be prepared", result.UpdateNotChecked, nil)
	}
	remaining := slices.Clone(record.History)
	for _, id := range ids {
		remaining = slices.DeleteFunc(remaining, func(value string) bool { return value == id })
	}
	record.History = remaining
	if err := v.base.state.DeleteHistory(record.InstallationID, ids); err != nil {
		return v.recovery(cli.CommandHistoryPurge, cli.OperationHistoryPurge, operationID, request.InstallationID(), data.ExpectedFinalState(), data.Actions(), "history_purge_failed")
	}
	if record.Lifecycle == "archived" && len(remaining) == 0 {
		err = v.base.state.Delete(originalRecord)
	} else {
		err = v.base.state.Save(record)
	}
	if err != nil {
		return v.recovery(cli.CommandHistoryPurge, cli.OperationHistoryPurge, operationID, request.InstallationID(), data.ExpectedFinalState(), data.Actions(), "history_state_commit_failed")
	}
	if err := v.base.state.DeleteMarker(); err != nil {
		return v.recovery(cli.CommandHistoryPurge, cli.OperationHistoryPurge, operationID, request.InstallationID(), data.ExpectedFinalState(), data.Actions(), "operation_cleanup_failed")
	}
	return lifecycleCommitted(cli.CommandHistoryPurge, cli.OperationHistoryPurge, operationID, ptrInstallation(request.InstallationID()), data.ExpectedFinalState(), result.UpdateNotChecked, nil, data.Actions())
}

type expectedCommitFunc func() (domain.CommitOID, bool)
type expectedDigestFunc func() (string, bool)

func noExpectedDigest() (string, bool) { return "", false }

func (v *v1LifecycleService) apply(ctx context.Context, command cli.Command, output cli.OutputMode, approved bool, commandIO CommandIO, requestedPolicy cli.ConflictPolicy, prepare func(cli.ConflictPolicy) (v1Execution, cli.Response, bool), expected expectedCommitFunc, expectedDigest expectedDigestFunc) (cli.Response, error) {
	release, err := v.base.acquire(ctx)
	if err != nil {
		return lifecycleFailure(command, result.FailureConflict, "mutation_locked", "another AI4J modifying command is running", result.UpdateNotChecked, nil)
	}
	defer func() { _ = release() }()
	if recovered, recoveryErr := v.reconcileInterrupted(ctx); recoveryErr != nil || !recovered {
		return lifecycleFailure(command, result.FailureRecovery, "recovery_required", "an interrupted operation requires recovery before another mutation", result.UpdateNotChecked, nil)
	}
	policy, ok, err := resolveInteractivePolicy(requestedPolicy, output, commandIO)
	if err != nil {
		return cli.Response{}, err
	}
	if !ok {
		return lifecycleFailure(command, result.FailureApproval, "interactive_terminal_required", "interactive conflict policy requires terminal input and output", result.UpdateNotChecked, nil)
	}
	execution, response, stop := prepare(policy)
	if stop {
		return planAsCommand(response, command)
	}
	if len(execution.conflicts) != 0 {
		return lifecycleConflict(command, execution.conflicts, execution.disposition, execution.source.Warnings)
	}
	if commit, supplied := expected(); supplied && execution.source.Source.Commit().OID() != commit {
		return lifecycleFailure(command, result.FailureConflict, "expected_commit_mismatch", "resolved source commit does not match --expected-commit", execution.disposition, execution.source.Warnings)
	}
	digest, digestSupplied := expectedDigest()
	if execution.source.Source.Mode() == cli.SourceDevelopment && execution.source.Source.Dirty() && !digestSupplied {
		return lifecycleFailure(command, result.FailureConflict, "expected_source_digest_required", "dirty local source apply requires --expected-source-digest", execution.disposition, execution.source.Warnings)
	}
	if digestSupplied && (execution.source.Source.Mode() != cli.SourceDevelopment || execution.source.Source.SourceDigest().String() != digest) {
		return lifecycleFailure(command, result.FailureConflict, "expected_source_digest_mismatch", "local source digest does not match --expected-source-digest", execution.disposition, execution.source.Warnings)
	}
	plan, err := v.planResponse(commandForPlan(command), execution)
	if err != nil {
		return cli.Response{}, err
	}
	if len(execution.actions) == 0 {
		return lifecycleNoChange(command, execution.operation, recordInstallation(execution.before, execution.desired), execution.final, execution.disposition, execution.source.Warnings)
	}
	approval, err := approveLifecycle(approved, output, commandIO, plan, execution.operation.String())
	if err != nil {
		return cli.Response{}, err
	}
	if approval == approvalDeclined {
		return lifecycleCancelled(command, "operation_cancelled", "operation was declined before mutation", execution.disposition, execution.source.Warnings)
	}
	if approval != approvalGranted {
		return lifecycleFailure(command, result.FailureApproval, "approval_required", "operation requires explicit approval", execution.disposition, execution.source.Warnings)
	}
	return v.commitExecution(ctx, command, execution, policy)
}

// reconcileInterrupted completes only transaction states that are fully
// explained by the operation's marker and structural history. Mixed, drifted,
// unsupported, or changing observations remain fail-closed.
func (v *v1LifecycleService) reconcileInterrupted(ctx context.Context) (bool, error) {
	marker, present, err := v.base.state.LoadMarker()
	if err != nil || !present {
		return !present && err == nil, err
	}
	if marker.Operation == "history_purge" || !slices.Contains(marker.Resources, "history:"+marker.InstallationID) {
		return false, nil
	}
	entry, historyPresent, err := v.base.state.LoadOperationHistory(marker.InstallationID, marker.OperationID)
	if err != nil {
		return false, err
	}
	if !historyPresent {
		current, statePresent, loadErr := v.base.state.LoadByID(marker.InstallationID)
		if loadErr != nil {
			return false, loadErr
		}
		if !statePresent || current.Lifecycle != "active" || current.LastOperation.ID == marker.OperationID ||
			!v.recoveryTargetMatches(ctx, &current, nil) || !v.recoverySnapshotMatches(ctx, marker.InstallationID, &current, nil, &current) {
			return false, nil
		}
		if err := v.base.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	}
	if entry.Operation != marker.Operation || entry.OperationID != marker.OperationID || entry.InstallationID != marker.InstallationID ||
		entry.After == nil || recordSourceRevision(*entry.After) != marker.Commit {
		return false, nil
	}
	current, statePresent, err := v.base.state.LoadByID(marker.InstallationID)
	if err != nil {
		return false, err
	}
	afterState := statePresent && reflect.DeepEqual(current, *entry.After)
	beforeState := entry.Before == nil && !statePresent || entry.Before != nil && statePresent && reflect.DeepEqual(current, *entry.Before)
	afterTarget := v.recoveryTargetMatches(ctx, entry.After, entry.Before)
	beforeTarget := v.recoveryTargetMatches(ctx, entry.Before, entry.After)

	switch {
	case afterState && afterTarget:
		if !v.recoverySnapshotMatches(ctx, marker.InstallationID, entry.After, entry.Before, entry.After) {
			return false, nil
		}
		if !entry.Committed {
			if err := v.base.state.CommitHistory(entry); err != nil {
				return false, err
			}
		}
		if err := v.base.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	case !entry.Committed && beforeState && afterTarget:
		// Target mutation completed and verified but installation state did not.
		if !v.recoverySnapshotMatches(ctx, marker.InstallationID, entry.Before, entry.Before, entry.After) {
			return false, nil
		}
		if entry.Before == nil {
			err = v.base.state.SaveNew(*entry.After)
		} else {
			err = v.base.state.Save(*entry.After)
		}
		if err != nil {
			return false, err
		}
		if err := v.base.state.CommitHistory(entry); err != nil {
			return false, err
		}
		if err := v.base.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	case !entry.Committed && beforeState && beforeTarget:
		// No durable mutation remains, so the staged journal can be discarded.
		if !v.recoverySnapshotMatches(ctx, marker.InstallationID, entry.Before, entry.After, entry.Before) {
			return false, nil
		}
		if err := v.base.state.DeleteHistory(marker.InstallationID, []string{marker.OperationID}); err != nil {
			return false, err
		}
		if err := v.base.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func (v *v1LifecycleService) recoverySnapshotMatches(ctx context.Context, installationID string, expectedState, counterpart, expectedTarget *installstate.Record) bool {
	current, present, err := v.base.state.LoadByID(installationID)
	if err != nil || expectedState == nil && present || expectedState != nil && (!present || !reflect.DeepEqual(current, *expectedState)) {
		return false
	}
	return v.recoveryTargetMatches(ctx, expectedTarget, counterpart)
}

func (v *v1LifecycleService) recoveryTargetMatches(ctx context.Context, expected, counterpart *installstate.Record) bool {
	if expected == nil {
		if counterpart == nil {
			return false
		}
		native, problem := v.inspectNative(ctx, *counterpart)
		if problem != nil || native.MarketplaceRegistered || native.PluginInstalled {
			return false
		}
		return recoveryOwnedAbsent(v, *counterpart)
	}
	if expected.Health == "drifted" || v.verifyDesired(ctx, *expected) != nil {
		return false
	}
	if expected.Lifecycle == "archived" && counterpart != nil {
		return recoveryOwnedAbsent(v, *counterpart)
	}
	return true
}

func recoveryOwnedAbsent(v *v1LifecycleService, record installstate.Record) bool {
	if record.Scope == "project-shared" {
		return projectMarketplaceAbsent(record) && (record.Rules == (installstate.OwnedFile{}) || ownedFileAbsent(v.rulesPath(record)))
	}
	return (record.Catalog == (installstate.OwnedFile{}) || ownedFileAbsent(v.catalogPath(record))) &&
		(record.Rules == (installstate.OwnedFile{}) || ownedFileAbsent(v.rulesPath(record)))
}

func (v *v1LifecycleService) prepareInstall(ctx context.Context, source cli.SourceOptions, target cli.BuildTarget, scope cli.Scope, project string, hasProject bool, selection cli.SelectionOptions, installationID domain.InstallationID, reactivation bool, policy cli.ConflictPolicy) (v1Execution, cli.Response, bool) {
	var before *installstate.Record
	scopeRoot := v.base.effectiveClaudeRoot()
	if reactivation {
		record, present, err := v.base.state.LoadByID(installationID.String())
		if err != nil || !present {
			return v1Stop(cli.CommandPlanInstall, result.FailureConflict, "installation_not_found", "the selected archived installation does not exist")
		}
		before = cloneRecordPtr(&record)
		if record.Source.Mode == "development_source" {
			source, _ = cli.NewDevelopmentSourceOptions(record.Source.Checkout, source.AllowDirty())
		} else {
			source, _ = exactSourceOptions(record)
		}
		selection = selectionFromRecord(record)
		target = cli.BuildTarget(record.Target)
		scope = cli.Scope(record.Scope)
		scopeRoot = record.ScopeRoot
	} else {
		if target != cli.BuildTargetClaude || !scope.Valid() {
			return v1Stop(cli.CommandPlanInstall, result.FailureValidation, "unsupported_scope", "the requested target or scope is unsupported")
		}
		if scope == cli.ScopeProjectShared && source.HasCheckout() {
			return v1Stop(cli.CommandPlanInstall, result.FailureValidation, "unsupported_scope", "local development sources cannot be installed at project-shared scope")
		}
		if scope != cli.ScopeUser {
			var err error
			scopeRoot, err = v.resolveProjectRoot(ctx, project, hasProject)
			if err != nil {
				return v1Stop(cli.CommandPlanInstall, result.FailureEnvironment, "project_root_invalid", "a canonical Git project root could not be resolved")
			}
		}
	}
	report := v.validation.SelectLifecycle(ctx, source, selection.SelectAll(), selection.Assets(), selection.Bundles())
	if len(report.Problems) != 0 || !report.HasSource() {
		return v1SelectionStop(cli.CommandPlanInstall, report)
	}
	if !reactivation {
		installationID = installationIDFor(report, scope, scopeRoot)
	}
	desired, document, err := v.recordForSelection(report, selection, installationID, scope, scopeRoot)
	if err != nil {
		return v1Stop(cli.CommandPlanInstall, result.FailureInternal, "plan_failed", "installation plan could not be created")
	}
	if err := v.inspectProjectLocal(ctx, desired); err != nil {
		return v1Stop(cli.CommandPlanInstall, result.FailureConflict, "project_local_conflict", "project-local rules cannot be proven safely untracked")
	}
	records, err := v.base.state.LoadAll()
	if err != nil {
		return v1Stop(cli.CommandPlanInstall, result.FailureConflict, "installation_state_invalid", "installation state could not be read")
	}
	if !reactivation {
		for _, record := range records {
			if record.Target == desired.Target && record.Scope == desired.Scope && filepath.Clean(record.ScopeRoot) == filepath.Clean(desired.ScopeRoot) && record.ToolkitID == desired.ToolkitID {
				if record.Lifecycle == "archived" {
					return v1Stop(cli.CommandPlanInstall, result.FailureConflict, "archived_installation_exists", "reactivate the archived installation by its installation ID")
				}
				before = cloneRecordPtr(&record)
				desired.InstallationID = record.InstallationID
			}
		}
	}
	catalogBytes := document.Bytes()
	var catalogBefore []byte
	if desired.Scope == "project-shared" {
		catalogBefore, catalogBytes, err = v.planProjectShared(&desired, before)
		if err != nil {
			return v1Stop(cli.CommandPlanInstall, result.FailureConflict, "project_settings_conflict", "the shared project declaration cannot be changed safely")
		}
	}
	conflicts := v.installConflicts(ctx, desired)
	if before != nil && before.Lifecycle == "active" {
		conflicts = v.existingConflicts(ctx, *before, true)
	}
	visible, degraded := applyConflictPolicy(conflicts, policy, before != nil)
	actions, _ := v.transitionActions(cli.OperationInstall, before, &desired, len(report.Rules) != 0)
	if before != nil && recordsEquivalent(*before, desired) {
		actions = nil
	}
	final := mustFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	return v1Execution{operation: cli.OperationInstall, source: report, before: before, desired: &desired, catalog: catalogBytes, catalogBefore: catalogBefore, rules: report.Rules, artifact: report.NativeArtifact, actions: actions, content: report.Content, conflicts: visible, degradedConflicts: degraded, final: final, disposition: result.UpdateNotChecked}, cli.Response{}, false
}

func (v *v1LifecycleService) prepareUpdate(ctx context.Context, installationID domain.InstallationID, requested cli.SourceOptions, policy cli.ConflictPolicy) (v1Execution, cli.Response, bool) {
	record, present, err := v.base.state.LoadByID(installationID.String())
	if err != nil || !present || record.Lifecycle != "active" {
		return v1Stop(cli.CommandPlanUpdate, result.FailureConflict, "installation_not_active", "the selected installation is not active")
	}
	if record.Source.Mode == "development_source" {
		if requested.HasRepository() || requested.HasReference() || requested.HasCheckout() {
			return v1Stop(cli.CommandPlanUpdate, result.FailureConflict, "source_mode_migration_unsupported", "GitHub and local source modes cannot be migrated in place")
		}
		options, optionErr := cli.NewDevelopmentSourceOptions(record.Source.Checkout, requested.AllowDirty())
		if optionErr != nil {
			return v1Stop(cli.CommandPlanUpdate, result.FailureSource, "source_invalid", "stored local source is invalid")
		}
		selection := selectionFromRecord(record)
		report := v.validation.SelectLifecycle(ctx, options, selection.SelectAll(), selection.Assets(), selection.Bundles())
		if len(report.Problems) != 0 || !report.HasSource() {
			return v1SelectionStop(cli.CommandPlanUpdate, report)
		}
		disposition := result.UpdateUpToDate
		if report.Source.SourceDigest().String() != record.Source.SourceDigest {
			disposition = result.UpdateAvailable
		}
		return v.prepareExisting(ctx, cli.CommandPlanUpdate, cli.OperationUpdate, record, report, selection, policy, disposition)
	}
	if requested.AllowDirty() || requested.HasCheckout() {
		return v1Stop(cli.CommandPlanUpdate, result.FailureConflict, "source_mode_mismatch", "local source options are invalid for a GitHub installation")
	}
	installed, _ := domain.NewCommitOID(record.Source.Commit)
	options, err := updateSourceOptions(record)
	if err != nil {
		return v1Stop(cli.CommandPlanUpdate, result.FailureInternal, "source_invalid", "stored source selection is invalid")
	}
	migration := requested.HasRepository() || requested.HasReference()
	if migration {
		repository := record.Source.Repository
		if requested.HasRepository() {
			repository = requested.Repository()
		}
		reference := ""
		hasReference := false
		if requested.HasReference() {
			reference, hasReference = requested.Reference(), true
		}
		options, err = cli.NewSourceOptions(repository, true, reference, hasReference)
		if err != nil {
			return v1Stop(cli.CommandPlanUpdate, result.FailureSource, "invalid_source", "requested source migration is invalid")
		}
		selection := selectionFromRecord(record)
		report := v.validation.SelectLifecycle(ctx, options, selection.SelectAll(), selection.Assets(), selection.Bundles())
		if len(report.Problems) != 0 || !report.HasSource() {
			return v1SelectionStop(cli.CommandPlanUpdate, report)
		}
		if report.ToolkitID != record.ToolkitID {
			return v1Stop(cli.CommandPlanUpdate, result.FailureConflict, "toolkit_identity_changed", "source migration must retain the toolkit identifier")
		}
		return v.prepareExisting(ctx, cli.CommandPlanUpdate, cli.OperationUpdate, record, report, selection, policy, result.UpdateAvailable)
	}
	update := v.validation.ValidateUpdate(ctx, options, installed)
	if len(update.Report.Problems) != 0 {
		return v1ReportStop(cli.CommandPlanUpdate, update.Report)
	}
	disposition := result.UpdateUnknown
	switch {
	case update.Disposition == gitsource.UpdateNoChange:
		disposition = result.UpdateUpToDate
	case update.Disposition == gitsource.UpdateAvailable:
		disposition = result.UpdateAvailable
	case update.Disposition == gitsource.UpdateRefRewritten:
		return v1Stop(cli.CommandPlanUpdate, result.FailureConflict, "ref_rewritten", "the tracked branch is not a fast-forward update")
	default:
		return v1Stop(cli.CommandPlanUpdate, result.FailureSource, "update_source_failed", "the stored source could not be checked")
	}
	selection := selectionFromRecord(record)
	report := v.validation.SelectLifecycle(ctx, options, selection.SelectAll(), selection.Assets(), selection.Bundles())
	if len(report.Problems) != 0 || !report.HasSource() {
		return v1SelectionStop(cli.CommandPlanUpdate, report)
	}
	return v.prepareExisting(ctx, cli.CommandPlanUpdate, cli.OperationUpdate, record, report, selection, policy, disposition)
}

func (v *v1LifecycleService) prepareSync(ctx context.Context, installationID domain.InstallationID, selection cli.SelectionOptions, allowDirty bool, policy cli.ConflictPolicy) (v1Execution, cli.Response, bool) {
	record, present, err := v.base.state.LoadByID(installationID.String())
	if err != nil || !present || record.Lifecycle != "active" {
		return v1Stop(cli.CommandPlanSync, result.FailureConflict, "installation_not_active", "the selected installation is not active")
	}
	options, err := exactSourceOptions(record)
	if record.Source.Mode == "development_source" {
		options, err = cli.NewDevelopmentSourceOptions(record.Source.Checkout, allowDirty)
	} else if allowDirty {
		return v1Stop(cli.CommandPlanSync, result.FailureConflict, "source_mode_mismatch", "--allow-dirty is valid only for local development sources")
	}
	if err != nil {
		return v1Stop(cli.CommandPlanSync, result.FailureInternal, "source_invalid", "stored source selection is invalid")
	}
	report := v.validation.SelectLifecycle(ctx, options, selection.SelectAll(), selection.Assets(), selection.Bundles())
	if len(report.Problems) != 0 || !report.HasSource() {
		return v1SelectionStop(cli.CommandPlanSync, report)
	}
	return v.prepareExisting(ctx, cli.CommandPlanSync, cli.OperationSync, record, report, selection, policy, result.UpdateNotChecked)
}

func (v *v1LifecycleService) prepareExisting(ctx context.Context, command cli.Command, operation cli.Operation, record installstate.Record, report validation.LifecycleSelection, selection cli.SelectionOptions, policy cli.ConflictPolicy, disposition result.UpdateDisposition) (v1Execution, cli.Response, bool) {
	desired, document, err := v.recordForSelection(report, selection, mustInstallation(record.InstallationID), cli.Scope(record.Scope), record.ScopeRoot)
	if err != nil {
		return v1Stop(command, result.FailureInternal, "plan_failed", "lifecycle plan could not be created")
	}
	desired.History = slices.Clone(record.History)
	if err := v.inspectProjectLocal(ctx, desired); err != nil {
		return v1Stop(command, result.FailureConflict, "project_local_conflict", "project-local rules cannot be proven safely untracked")
	}
	catalogBytes := document.Bytes()
	var catalogBefore []byte
	if desired.Scope == "project-shared" {
		catalogBefore, catalogBytes, err = v.planProjectShared(&desired, &record)
		if err != nil {
			return v1Stop(command, result.FailureConflict, "project_settings_conflict", "the shared project declaration cannot be changed safely")
		}
	}
	conflicts := v.existingConflicts(ctx, record, true)
	visible, degraded := applyConflictPolicy(conflicts, policy, true)
	actions, _ := v.transitionActions(operation, &record, &desired, len(report.Rules) != 0)
	installedContent := []cli.ContentItem{}
	if options, sourceErr := exactSourceOptions(record); sourceErr == nil {
		installedSelection := selectionFromRecord(record)
		installed := v.validation.SelectLifecycle(ctx, options, installedSelection.SelectAll(), installedSelection.Assets(), installedSelection.Bundles())
		if len(installed.Problems) != 0 || !installed.HasSource() {
			return v1SelectionStop(command, installed)
		}
		installedContent = installed.Content
	}
	content, _ := diffActiveContent(installedContent, report.Content)
	if recordsEquivalent(record, desired) && len(conflicts) == 0 {
		actions = nil
		content, _ = contentWithChange(report.Content, cli.ContentUnchanged)
	}
	final := mustFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	return v1Execution{operation: operation, source: report, before: &record, desired: &desired, catalog: catalogBytes, catalogBefore: catalogBefore, rules: report.Rules, artifact: report.NativeArtifact, actions: actions, content: content, conflicts: visible, degradedConflicts: degraded, final: final, disposition: disposition}, cli.Response{}, false
}

func (v *v1LifecycleService) prepareUninstall(ctx context.Context, installationID domain.InstallationID, policy cli.ConflictPolicy) (v1Execution, cli.Response, bool) {
	record, present, err := v.base.state.LoadByID(installationID.String())
	if err != nil || !present {
		return v1Stop(cli.CommandPlanUninstall, result.FailureConflict, "installation_not_found", "the selected installation does not exist")
	}
	final := mustFinalState(cli.StateAbsent, cli.StateAbsent, cli.StateAbsent)
	if record.Lifecycle == "archived" {
		return v1Execution{operation: cli.OperationUninstall, before: &record, desired: &record, final: final}, cli.Response{}, false
	}
	options, err := exactSourceOptions(record)
	if err != nil {
		return v1Stop(cli.CommandPlanUninstall, result.FailureConflict, "installation_state_invalid", "the selected installation source is invalid")
	}
	selection := selectionFromRecord(record)
	report := v.validation.SelectLifecycle(ctx, options, selection.SelectAll(), selection.Assets(), selection.Bundles())
	if len(report.Problems) != 0 || !report.HasSource() {
		return v1SelectionStop(cli.CommandPlanUninstall, report)
	}
	desired := record
	var catalogAfter []byte
	if record.Scope == "project-shared" {
		catalogAfter, err = v.planProjectSharedRemoval(record)
		if err != nil {
			return v1Stop(cli.CommandPlanUninstall, result.FailureConflict, "project_settings_conflict", "the shared project declaration cannot be removed safely")
		}
	}
	desired.Lifecycle = "archived"
	desired.Health = "healthy"
	desired.Catalog = installstate.OwnedFile{}
	desired.Rules = installstate.OwnedFile{}
	desired.NativeResources = []string{}
	conflicts := v.existingConflicts(ctx, record, false)
	visible, degraded := applyConflictPolicy(conflicts, policy, true)
	actions, _ := v.transitionActions(cli.OperationUninstall, &record, &desired, false)
	content, _ := contentWithChange(report.Content, cli.ContentRemoved)
	return v1Execution{operation: cli.OperationUninstall, source: report, before: &record, desired: &desired, catalog: catalogAfter, actions: actions, content: content, conflicts: visible, degradedConflicts: degraded, final: final, disposition: result.UpdateNotChecked}, cli.Response{}, false
}

func (v *v1LifecycleService) prepareRollback(ctx context.Context, installationID domain.InstallationID, operationID domain.OperationID, selected bool, policy cli.ConflictPolicy) (v1Execution, cli.Response, bool) {
	record, present, err := v.base.state.LoadByID(installationID.String())
	if err != nil || !present {
		return v1Stop(cli.CommandPlanRollback, result.FailureConflict, "installation_not_found", "the selected installation does not exist")
	}
	entries, err := v.base.state.LoadHistory(record.InstallationID)
	if err != nil || len(entries) == 0 {
		return v1Stop(cli.CommandPlanRollback, result.FailureConflict, "rollback_unavailable", "no retained rollback point is available")
	}
	entry := entries[len(entries)-1]
	if selected {
		var found bool
		entry, found, err = v.base.state.LoadHistoryEntry(record.InstallationID, operationID.String())
		if err != nil || !found {
			return v1Stop(cli.CommandPlanRollback, result.FailureConflict, "rollback_not_found", "the selected rollback point does not exist")
		}
	}
	if !entry.Restorable || entry.After == nil || !sameCurrentState(record, *entry.After) {
		return v1Stop(cli.CommandPlanRollback, result.FailureConflict, "rollback_conflict", "current installation state no longer matches the rollback point")
	}
	desired := cloneRecord(record)
	if entry.Before != nil {
		desired = cloneRecord(*entry.Before)
	} else {
		desired.Lifecycle = "archived"
		desired.Catalog = installstate.OwnedFile{}
		desired.Rules = installstate.OwnedFile{}
		desired.NativeResources = []string{}
	}
	desired.History = slices.Clone(record.History)
	selection := selectionFromRecord(desired)
	options, _ := exactSourceOptions(desired)
	report := v.validation.SelectLifecycle(ctx, options, selection.SelectAll(), selection.Assets(), selection.Bundles())
	if len(report.Problems) != 0 || !report.HasSource() {
		return v1SelectionStop(cli.CommandPlanRollback, report)
	}
	conflicts := v.existingConflicts(ctx, record, record.Lifecycle == "active")
	visible, degraded := applyConflictPolicy(conflicts, policy, true)
	actions, _ := v.transitionActions(cli.OperationRollback, &record, &desired, desired.Rules != (installstate.OwnedFile{}))
	final := mustFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	if desired.Lifecycle == "archived" {
		final = mustFinalState(cli.StateAbsent, cli.StateAbsent, cli.StateAbsent)
	}
	catalogBefore, catalogAfter := slices.Clone(entry.CatalogAfter), slices.Clone(entry.CatalogBefore)
	if record.Scope == "project-shared" {
		catalogBefore, catalogAfter, err = v.planProjectSharedRollback(record, &desired, entry.CatalogBefore)
		if err != nil {
			return v1Stop(cli.CommandPlanRollback, result.FailureConflict, "project_settings_conflict", "the shared project declaration cannot be rolled back safely")
		}
	}
	return v1Execution{operation: cli.OperationRollback, source: report, before: &record, desired: &desired, catalog: catalogAfter, catalogBefore: catalogBefore, rules: slices.Clone(entry.RulesBefore), artifact: slices.Clone(entry.NativeArtifactBefore), actions: actions, content: report.Content, conflicts: visible, degradedConflicts: degraded, final: final, disposition: result.UpdateNotChecked, rollback: &entry}, cli.Response{}, false
}

func (v *v1LifecycleService) recordForSelection(report validation.LifecycleSelection, selection cli.SelectionOptions, installationID domain.InstallationID, scope cli.Scope, scopeRoot string) (installstate.Record, catalog.Document, error) {
	marketplaceID := marketplaceIDFor(installationID)
	if scope == cli.ScopeProjectShared {
		marketplaceID = report.DeclarationID
		if marketplaceID == "" {
			marketplaceID = report.ToolkitID
		}
	}
	var document catalog.Document
	var err error
	if report.Source.Mode() == cli.SourceDevelopment {
		document, err = catalog.RenderLocalPackage(marketplaceID, report.PackageID, "plugin", "AI4J local toolkit package "+report.PackageID)
	} else {
		document, err = catalog.RenderPackage(marketplaceID, report.PackageID, report.PackagePath, "AI4J toolkit package "+report.PackageID, report.Source.Repository(), report.Source.Commit().OID())
	}
	if err != nil {
		return installstate.Record{}, catalog.Document{}, err
	}
	var requested *string
	if report.Source.HasRequestedRef() {
		value := report.Source.RequestedRef()
		requested = &value
	}
	stateSource := installstate.Source{Mode: "github", Selection: report.Source.Selection().String(), Repository: report.Source.Repository().String(), RequestedRef: requested, RefKind: report.Source.ResolvedRefKind().String(), Commit: report.Source.Commit().OID().String(), RenderedDigest: report.Source.RenderedDigest().String()}
	catalogPath := "state/catalogs/" + installationID.String() + "/.claude-plugin/marketplace.json"
	if report.Source.Mode() == cli.SourceDevelopment {
		stateSource = installstate.Source{Mode: "development_source", Selection: domain.ExplicitSource().String(), Checkout: report.Source.Checkout(), SourceDigest: report.Source.SourceDigest().String(), RenderedDigest: report.Source.RenderedDigest().String(), Dirty: report.Source.Dirty()}
		stateSource.BundleDigest = localBundleDigest(stateSource, selection, report, document.Digest())
		catalogPath = "state/bundles/" + stateSource.BundleDigest + "/.claude-plugin/marketplace.json"
	}
	record := installstate.Record{
		SchemaVersion: installstate.SchemaVersion, InstallationID: installationID.String(), ToolkitID: report.ToolkitID, DeclarationID: report.DeclarationID, ToolkitVersion: report.ToolkitVersion,
		PluginID: report.PackageID, PackagePath: report.PackagePath, MarketplaceID: marketplaceID,
		Source: stateSource,
		Target: "claude", Host: v.base.host(), Scope: string(scope), ScopeRoot: scopeRoot, Lifecycle: "active",
		Selection:       installstate.Selection{All: selection.SelectAll(), Assets: selection.Assets(), Bundles: selection.Bundles(), Resolved: slices.Clone(report.Resolved)},
		NativeResources: []string{"claude:" + report.PackageID + "@" + marketplaceID, "claude:marketplace:" + marketplaceID},
		Health:          "healthy", AI4JVersion: v.base.build.Version(),
		Catalog:       installstate.OwnedFile{Path: catalogPath, Checksum: document.Digest()},
		LastOperation: installstate.LastOperation{ID: "operation-pending", Timestamp: time.Unix(0, 0).UTC().Format(time.RFC3339)},
	}
	if len(report.Rules) != 0 {
		rulesID := "ai4j-" + installationID.String()
		rulesRoot := "rules/"
		if scope != cli.ScopeUser {
			rulesRoot = ".claude/rules/"
		}
		if scope == cli.ScopeProjectShared {
			rulesID = marketplaceID
		}
		record.Rules = installstate.OwnedFile{Path: rulesRoot + rulesID + ".md", Checksum: report.RulesChecksum}
	}
	slices.Sort(record.Selection.Assets)
	slices.Sort(record.Selection.Bundles)
	slices.Sort(record.Selection.Resolved)
	slices.Sort(record.NativeResources)
	return record, document, record.Validate()
}

func localBundleDigest(source installstate.Source, selection cli.SelectionOptions, report validation.LifecycleSelection, catalogDigest string) string {
	artifact := sha256.Sum256(report.NativeArtifact)
	parts := []string{"claude-local-bundle-v1", source.Checkout, source.SourceDigest, source.RenderedDigest, report.ToolkitID, report.PackageID, report.PackagePath, catalogDigest, hex.EncodeToString(artifact[:]), strings.Join(report.Resolved, ",")}
	parts = append(parts, strings.Join(selection.Assets(), ","), strings.Join(selection.Bundles(), ","))
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func (v *v1LifecycleService) planResponse(command cli.Command, execution v1Execution) (cli.Response, error) {
	installation := recordInstallation(execution.before, execution.desired)
	if installation == nil {
		return cli.Response{}, errors.New("plan installation identity is unavailable")
	}
	data, err := cli.NewPlanData(execution.operation, execution.source.Source, *installation, execution.actions, execution.content, execution.conflicts, execution.final, execution.disposition)
	if err != nil {
		return cli.Response{}, err
	}
	status := result.StatusOK
	failure := result.FailureNone
	var problems []result.Problem
	warnings := slices.Clone(execution.source.Warnings)
	if len(execution.conflicts) != 0 {
		status = result.StatusError
		failure = result.FailureConflict
		problems = conflictProblems(execution.conflicts)
	} else if len(execution.actions) == 0 {
		status = result.StatusNoChange
	} else if len(execution.degradedConflicts) != 0 {
		status = result.StatusDegraded
		warnings = append(warnings, conflictWarnings(execution.degradedConflicts)...)
	}
	commandResult, err := result.New(result.Facts{Status: status, Phase: result.PhaseNone, Outcome: result.OutcomeNone, Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone, Failure: failure, UpdateDisposition: execution.disposition, Warnings: warnings, Errors: problems})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, data)
}

func (v *v1LifecycleService) transitionActions(operation cli.Operation, before, desired *installstate.Record, hasRules bool) ([]cli.Action, error) {
	present, _ := cli.NewCondition(cli.ConditionPresent, "")
	absent, _ := cli.NewCondition(cli.ConditionAbsent, "")
	specs := []planActionSpec{{cli.ActionOwnerAI4J, cli.ActionValidateSource, "toolkit source", present, present, cli.RecoveryNone}, {cli.ActionOwnerAI4J, cli.ActionPrepareRecovery, "durable structural history", absent, present, cli.RecoveryStructuralInverse}}
	switch {
	case desired != nil && desired.Lifecycle == "active" && (before == nil || before.Lifecycle == "archived"):
		catalogChecksum, _ := cli.NewCondition(cli.ConditionMatchesChecksum, desired.Catalog.Checksum)
		specs = append(specs,
			planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, desired.Catalog.Path, absent, catalogChecksum, cli.RecoveryStructuralInverse},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionRegisterMarketplace, desired.MarketplaceID, absent, present, cli.RecoveryExactHandle},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionInstallPlugin, nativePluginID(*desired), absent, present, cli.RecoveryNativeArtifact},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionEnablePlugin, nativePluginID(*desired), present, present, cli.RecoveryExactHandle})
		if hasRules {
			rulesChecksum, _ := cli.NewCondition(cli.ConditionMatchesChecksum, desired.Rules.Checksum)
			specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteRules, desired.Rules.Path, absent, rulesChecksum, cli.RecoveryStructuralInverse})
		}
	case desired != nil && desired.Lifecycle == "active":
		oldCatalog, _ := cli.NewCondition(cli.ConditionMatchesChecksum, before.Catalog.Checksum)
		newCatalog, _ := cli.NewCondition(cli.ConditionMatchesChecksum, desired.Catalog.Checksum)
		specs = append(specs,
			planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, desired.Catalog.Path, oldCatalog, newCatalog, cli.RecoveryStructuralInverse},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionRefreshMarketplace, desired.MarketplaceID, present, present, cli.RecoveryExactHandle},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionUpdatePlugin, nativePluginID(*desired), present, present, cli.RecoveryNativeArtifact},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionEnablePlugin, nativePluginID(*desired), present, present, cli.RecoveryExactHandle})
		if hasRules {
			beforeRules := absent
			if before.Rules != (installstate.OwnedFile{}) {
				beforeRules, _ = cli.NewCondition(cli.ConditionMatchesChecksum, before.Rules.Checksum)
			}
			afterRules, _ := cli.NewCondition(cli.ConditionMatchesChecksum, desired.Rules.Checksum)
			specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteRules, desired.Rules.Path, beforeRules, afterRules, cli.RecoveryStructuralInverse})
		} else if before.Rules != (installstate.OwnedFile{}) {
			beforeRules, _ := cli.NewCondition(cli.ConditionMatchesChecksum, before.Rules.Checksum)
			specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionRemoveRules, before.Rules.Path, beforeRules, absent, cli.RecoveryStructuralInverse})
		}
	case desired != nil && desired.Lifecycle == "archived":
		specs = append(specs,
			planActionSpec{cli.ActionOwnerClaude, cli.ActionUninstallPlugin, nativePluginID(*before), present, absent, cli.RecoveryNativeArtifact},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionRemoveMarketplace, before.MarketplaceID, present, absent, cli.RecoveryExactHandle},
			planActionSpec{cli.ActionOwnerAI4J, cli.ActionRemoveCatalog, before.Catalog.Path, present, absent, cli.RecoveryStructuralInverse})
		if before.Rules != (installstate.OwnedFile{}) {
			specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionRemoveRules, before.Rules.Path, present, absent, cli.RecoveryStructuralInverse})
		}
	}
	specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionCommitState, "AI4J installation state and history references", present, present, cli.RecoveryStructuralInverse}, planActionSpec{cli.ActionOwnerAI4J, cli.ActionCleanup, "operation journal", present, absent, cli.RecoveryNone})
	return makeActions(specs)
}

func (v *v1LifecycleService) installConflicts(ctx context.Context, desired installstate.Record) []cli.Conflict {
	var conflicts []cli.Conflict
	for _, item := range []struct{ path, code, resource string }{{v.catalogPath(desired), "catalog_destination_occupied", desired.Catalog.Path}, {v.rulesPath(desired), "rules_destination_occupied", desired.Rules.Path}} {
		if item.resource == "" {
			continue
		}
		if desired.Scope == "project-shared" && item.resource == ".claude/settings.json" {
			continue
		}
		if _, err := os.Lstat(item.path); err == nil {
			conflicts = append(conflicts, mustCLIConflict(item.code, item.resource, "destination is already occupied"))
		} else if !errors.Is(err, os.ErrNotExist) {
			conflicts = append(conflicts, mustCLIConflict("owned_state_inspection_failed", item.resource, "destination could not be inspected"))
		}
	}
	native, problem := v.inspectNative(ctx, desired)
	if problem != nil {
		conflicts = append(conflicts, mustCLIConflict(problem.Code(), "Claude native state", problem.Message()))
	} else {
		if native.MarketplaceRegistered {
			conflicts = append(conflicts, mustCLIConflict("marketplace_identity_conflict", desired.MarketplaceID, "marketplace identity already exists"))
		}
		if native.PluginInstalled {
			conflicts = append(conflicts, mustCLIConflict("plugin_identity_conflict", nativePluginID(desired), "plugin identity already exists"))
		}
	}
	return conflicts
}

func (v *v1LifecycleService) existingConflicts(ctx context.Context, record installstate.Record, requireEnabled bool) []cli.Conflict {
	var conflicts []cli.Conflict
	if record.Lifecycle == "archived" {
		native, problem := v.inspectNative(ctx, record)
		if problem != nil {
			return []cli.Conflict{mustCLIConflict(problem.Code(), "Claude native state", problem.Message())}
		}
		if native.MarketplaceRegistered || native.PluginInstalled {
			return []cli.Conflict{mustCLIConflict("archived_native_present", nativePluginID(record), "archived installation still has native state")}
		}
		if record.Scope == "project-shared" {
			if !projectMarketplaceAbsent(record) {
				return []cli.Conflict{mustCLIConflict("catalog_drift", ".claude/settings.json", "shared project marketplace appeared after uninstall")}
			}
		}
		return nil
	}
	for _, item := range []struct{ path, checksum, code, resource string }{{v.catalogPath(record), record.Catalog.Checksum, "catalog_drift", record.Catalog.Path}, {v.rulesPath(record), record.Rules.Checksum, "rules_drift", record.Rules.Path}} {
		if item.resource != "" && inspectFileDrift(item.path, item.checksum) != cli.DriftUnchanged {
			if record.Scope == "project-shared" && item.resource == ".claude/settings.json" && inspectProjectMarketplaceDrift(record) == cli.DriftUnchanged {
				continue
			}
			conflicts = append(conflicts, mustCLIConflict(item.code, item.resource, "installation-owned content is missing or modified"))
		}
	}
	native, problem := v.inspectNative(ctx, record)
	if problem != nil {
		conflicts = append(conflicts, mustCLIConflict(problem.Code(), "Claude native state", problem.Message()))
	} else {
		if !native.MarketplaceRegistered {
			conflicts = append(conflicts, mustCLIConflict("marketplace_missing", record.MarketplaceID, "marketplace registration is missing"))
		}
		if !native.PluginInstalled {
			conflicts = append(conflicts, mustCLIConflict("plugin_missing", nativePluginID(record), "plugin installation is missing"))
		} else if requireEnabled && !native.PluginEnabled {
			conflicts = append(conflicts, mustCLIConflict("plugin_disabled", nativePluginID(record), "plugin is disabled"))
		}
	}
	return conflicts
}

func applyConflictPolicy(conflicts []cli.Conflict, policy cli.ConflictPolicy, owned bool) ([]cli.Conflict, []cli.Conflict) {
	if len(conflicts) == 0 || policy == cli.ConflictFail || !owned {
		return slices.Clone(conflicts), nil
	}
	return nil, slices.Clone(conflicts)
}

func (v *v1LifecycleService) commitExecution(ctx context.Context, command cli.Command, execution v1Execution, policy cli.ConflictPolicy) (cli.Response, error) {
	operationID, err := newOperationID(v.base.random)
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
	desired.LastOperation = installstate.LastOperation{ID: operationID.String(), Timestamp: v.base.now().UTC().Truncate(time.Second).Format(time.RFC3339)}
	desired.History = appendUnique(desired.History, operationID.String())
	if len(execution.degradedConflicts) != 0 && policy == cli.ConflictKeep {
		desired.Health = "drifted"
	}
	beforeCatalog, beforeRules, err := v.captureOwned(execution.before)
	if err != nil {
		return lifecycleFailure(command, result.FailureConflict, "rollback_capture_failed", "owned rollback material could not be captured", execution.disposition, execution.source.Warnings)
	}
	if execution.desired.Scope == "project-shared" {
		beforeCatalog = projectSharedOwnedEntry(execution.before)
	}
	afterCatalog, afterRules := execution.catalog, execution.rules
	beforeArtifact := v.currentArtifact(execution.before)
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
	if err := v.preflightExecutionCapacity(marker, entry, desired, execution.catalog, execution.rules, execution.artifact); err != nil {
		if code, message, ok := appDiskCapacityProblem(err); ok {
			return lifecycleFailure(command, result.FailureEnvironment, code, message, execution.disposition, execution.source.Warnings)
		}
		return lifecycleFailure(command, result.FailureInternal, "operation_preflight_failed", "operation storage requirements could not be verified", execution.disposition, execution.source.Warnings)
	}
	if v.base.state.SaveMarker(marker) != nil {
		return lifecycleFailure(command, result.FailureInternal, "operation_marker_failed", "operation could not be prepared", execution.disposition, execution.source.Warnings)
	}
	if err := v.base.state.StageHistory(entry); err != nil {
		return v.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "history_prepare_failed")
	}
	if err := v.applyTransition(ctx, execution.before, desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifact, policy, execution.rollback != nil); err != nil {
		return v.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "target_mutation_failed")
	}
	if err := v.verifyDesired(ctx, *desired); err != nil {
		return v.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "target_verification_failed")
	}
	if execution.before == nil {
		err = v.base.state.SaveNew(*desired)
	} else {
		err = v.base.state.Save(*desired)
	}
	if err != nil {
		return v.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "state_commit_failed")
	}
	entry.After = cloneRecordPtr(desired)
	if err := v.base.state.CommitHistory(entry); err != nil {
		return v.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "history_commit_failed")
	}
	if err := v.base.state.DeleteMarker(); err != nil {
		return v.recovery(command, execution.operation, operationID, *installationID, execution.final, execution.actions, "operation_cleanup_failed")
	}
	warnings := slices.Clone(execution.source.Warnings)
	if len(execution.degradedConflicts) != 0 {
		warnings = append(warnings, conflictWarnings(execution.degradedConflicts)...)
	}
	return committedV1(command, execution.operation, operationID, installationID, execution.final, execution.disposition, warnings, execution.actions, len(execution.degradedConflicts) != 0)
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

func (v *v1LifecycleService) preflightExecutionCapacity(marker installstate.Marker, entry installstate.HistoryEntry, desired *installstate.Record, catalogBytes, rulesBytes, artifact []byte) error {
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
	if err := diskcapacity.Require(v.base.state.DataRoot(), required); err != nil {
		return err
	}
	if len(catalogBytes) != 0 {
		if err := diskcapacity.Require(filepath.Dir(v.catalogPath(*desired)), uint64(len(catalogBytes))); err != nil {
			return err
		}
	}
	if len(rulesBytes) != 0 && desired.Rules != (installstate.OwnedFile{}) {
		if err := diskcapacity.Require(filepath.Dir(v.rulesPath(*desired)), uint64(len(rulesBytes))); err != nil {
			return err
		}
	}
	if desired.Source.Mode == "development_source" && len(artifact) != 0 {
		expanded, err := nativeArtifactExpandedBytes(artifact)
		if err != nil {
			return err
		}
		if err := diskcapacity.Require(filepath.Dir(v.catalogPath(*desired)), expanded+uint64(len(artifact))); err != nil {
			return err
		}
	}
	return nil
}

func (v *v1LifecycleService) applyTransition(ctx context.Context, before *installstate.Record, desired *installstate.Record, catalogBefore, catalogBytes, rulesBytes, artifact []byte, policy cli.ConflictPolicy, rollback bool) error {
	if desired.Scope == "project-shared" {
		return v.applyProjectSharedTransition(ctx, before, desired, catalogBefore, rulesBytes, policy)
	}
	if desired.Lifecycle == "archived" {
		if before == nil || before.Lifecycle != "active" {
			return nil
		}
		if err := v.runClaudeFor(ctx, *before, []string{"plugin", "uninstall", nativePluginID(*before), "--scope", nativeScope(*before), "--keep-data"}); err != nil && policy != cli.ConflictKeep {
			return err
		}
		if err := v.runClaudeFor(ctx, *before, []string{"plugin", "marketplace", "remove", before.MarketplaceID, "--scope", nativeScope(*before)}); err != nil && policy != cli.ConflictKeep {
			return err
		}
		if err := mutateOwned(v.ownedRoot(v.catalogPath(*before)), v.catalogPath(*before), before.Catalog.Checksum, nil, policy); err != nil {
			return err
		}
		if before.Rules != (installstate.OwnedFile{}) {
			if err := mutateOwned(v.ownedRoot(v.rulesPath(*before)), v.rulesPath(*before), before.Rules.Checksum, nil, policy); err != nil {
				return err
			}
		}
		if err := v.removeProjectLocalExclusion(ctx, *before); err != nil {
			return err
		}
		return nil
	}
	if before == nil || before.Lifecycle == "archived" {
		if rollback {
			return v.restoreArtifact(ctx, before, desired, catalogBytes, rulesBytes, artifact, policy)
		}
		if err := v.ensureProjectLocalExclusion(ctx, *desired); err != nil {
			return err
		}
		if desired.Source.Mode == "development_source" {
			if err := v.writeLocalBundle(*desired, catalogBytes, artifact); err != nil {
				return err
			}
		} else {
			if err := writeOwnedNew(v.ownedRoot(v.catalogPath(*desired)), v.catalogPath(*desired), catalogBytes); err != nil {
				return err
			}
		}
		if err := v.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", filepath.Dir(filepath.Dir(v.catalogPath(*desired))), "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
		if err := v.runClaudeFor(ctx, *desired, []string{"plugin", "install", nativePluginID(*desired), "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
		if desired.Rules != (installstate.OwnedFile{}) {
			return writeOwnedNew(v.ownedRoot(v.rulesPath(*desired)), v.rulesPath(*desired), rulesBytes)
		}
		return nil
	}
	if rollback {
		return v.restoreArtifact(ctx, before, desired, catalogBytes, rulesBytes, artifact, policy)
	}
	if err := v.ensureProjectLocalExclusion(ctx, *desired); err != nil {
		return err
	}
	if desired.Source.Mode == "development_source" && before.Catalog.Path != desired.Catalog.Path {
		if err := v.writeLocalBundle(*desired, catalogBytes, artifact); err != nil {
			return err
		}
		if err := v.runClaudeFor(ctx, *before, []string{"plugin", "marketplace", "remove", before.MarketplaceID, "--scope", nativeScope(*before)}); err != nil {
			return err
		}
		if err := v.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", filepath.Dir(filepath.Dir(v.catalogPath(*desired))), "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
		if err := v.runClaudeFor(ctx, *desired, []string{"plugin", "update", nativePluginID(*desired), "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
	}
	catalogChanged := before.Catalog.Checksum != desired.Catalog.Checksum || inspectFileDrift(v.catalogPath(*before), before.Catalog.Checksum) != cli.DriftUnchanged
	if catalogChanged && before.Catalog.Path == desired.Catalog.Path {
		if err := mutateOwned(v.ownedRoot(v.catalogPath(*before)), v.catalogPath(*before), before.Catalog.Checksum, catalogBytes, policy); err != nil {
			return err
		}
		if policy != cli.ConflictKeep || inspectFileDrift(v.catalogPath(*before), desired.Catalog.Checksum) == cli.DriftUnchanged {
			if err := v.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "update", desired.MarketplaceID, "--scope", nativeScope(*desired)}); err != nil {
				return err
			}
			if err := v.runClaudeFor(ctx, *desired, []string{"plugin", "update", nativePluginID(*desired), "--scope", nativeScope(*desired)}); err != nil {
				return err
			}
		}
	}
	switch {
	case before.Rules == (installstate.OwnedFile{}) && desired.Rules != (installstate.OwnedFile{}):
		if err := writeOwnedNew(v.ownedRoot(v.rulesPath(*desired)), v.rulesPath(*desired), rulesBytes); err != nil {
			return err
		}
	case before.Rules != (installstate.OwnedFile{}) && desired.Rules == (installstate.OwnedFile{}):
		if err := mutateOwned(v.ownedRoot(v.rulesPath(*before)), v.rulesPath(*before), before.Rules.Checksum, nil, policy); err != nil {
			return err
		}
	case before.Rules != (installstate.OwnedFile{}) && (before.Rules.Checksum != desired.Rules.Checksum || inspectFileDrift(v.rulesPath(*before), before.Rules.Checksum) != cli.DriftUnchanged):
		if err := mutateOwned(v.ownedRoot(v.rulesPath(*before)), v.rulesPath(*before), before.Rules.Checksum, rulesBytes, policy); err != nil {
			return err
		}
	}
	return nil
}

func (v *v1LifecycleService) restoreArtifact(ctx context.Context, before *installstate.Record, desired *installstate.Record, catalogBytes, rulesBytes, artifact []byte, policy cli.ConflictPolicy) error {
	if len(artifact) == 0 {
		return errors.New("native rollback artifact is unavailable")
	}
	if err := v.ensureProjectLocalExclusion(ctx, *desired); err != nil {
		return err
	}
	stateRoot := filepath.Dir(v.base.state.Path())
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
	defer func() { _ = recoveryWorkspace.Close() }()
	root := recoveryWorkspace.Path()
	if err := unpackNativeArtifact(root, "plugin", artifact); err != nil {
		return err
	}
	if desired.Source.Mode == "development_source" {
		if err := v.writeLocalBundle(*desired, catalogBytes, artifact); err != nil {
			return err
		}
	}
	localCatalog, err := catalog.RenderLocalPackage(desired.MarketplaceID, desired.PluginID, "plugin", "AI4J retained rollback package "+desired.PluginID)
	if err != nil {
		return err
	}
	localCatalogPath := filepath.Join(root, ".claude-plugin", "marketplace.json")
	if err := writeOwnedNew(v.ownedRoot(localCatalogPath), localCatalogPath, localCatalog.Bytes()); err != nil {
		return err
	}
	if before != nil && before.Lifecycle == "active" {
		if err := v.runClaudeFor(ctx, *before, []string{"plugin", "uninstall", nativePluginID(*before), "--scope", nativeScope(*before), "--keep-data"}); err != nil {
			return err
		}
		if err := v.runClaudeFor(ctx, *before, []string{"plugin", "marketplace", "remove", before.MarketplaceID, "--scope", nativeScope(*before)}); err != nil {
			return err
		}
		if before.Catalog.Path == desired.Catalog.Path {
			if err := mutateOwned(v.ownedRoot(v.catalogPath(*before)), v.catalogPath(*before), before.Catalog.Checksum, catalogBytes, policy); err != nil {
				return err
			}
		} else if desired.Source.Mode != "development_source" {
			if err := writeOwnedNew(v.ownedRoot(v.catalogPath(*desired)), v.catalogPath(*desired), catalogBytes); err != nil {
				return err
			}
		}
	} else if desired.Source.Mode != "development_source" {
		if err := writeOwnedNew(v.ownedRoot(v.catalogPath(*desired)), v.catalogPath(*desired), catalogBytes); err != nil {
			return err
		}
	}
	if err := v.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", root, "--scope", nativeScope(*desired)}); err != nil {
		return err
	}
	if err := v.runClaudeFor(ctx, *desired, []string{"plugin", "install", nativePluginID(*desired), "--scope", nativeScope(*desired)}); err != nil {
		return err
	}
	if err := v.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "remove", desired.MarketplaceID, "--scope", nativeScope(*desired)}); err != nil {
		return err
	}
	if err := v.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", filepath.Dir(filepath.Dir(v.catalogPath(*desired))), "--scope", nativeScope(*desired)}); err != nil {
		return err
	}
	switch {
	case before == nil || before.Lifecycle == "archived":
		if desired.Rules != (installstate.OwnedFile{}) {
			return writeOwnedNew(v.ownedRoot(v.rulesPath(*desired)), v.rulesPath(*desired), rulesBytes)
		}
	case before.Rules == (installstate.OwnedFile{}) && desired.Rules != (installstate.OwnedFile{}):
		return writeOwnedNew(v.ownedRoot(v.rulesPath(*desired)), v.rulesPath(*desired), rulesBytes)
	case before.Rules != (installstate.OwnedFile{}) && desired.Rules == (installstate.OwnedFile{}):
		return mutateOwned(v.ownedRoot(v.rulesPath(*before)), v.rulesPath(*before), before.Rules.Checksum, nil, policy)
	case before.Rules != (installstate.OwnedFile{}):
		return mutateOwned(v.ownedRoot(v.rulesPath(*before)), v.rulesPath(*before), before.Rules.Checksum, rulesBytes, policy)
	}
	return nil
}

func (v *v1LifecycleService) writeLocalBundle(record installstate.Record, catalogBytes, artifact []byte) error {
	root := filepath.Dir(filepath.Dir(v.catalogPath(record)))
	descriptorBytes, err := localBundleDescriptor(record, artifact)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(v.catalogPath(record)); err == nil && info.Mode().IsRegular() {
		descriptorPath := filepath.Join(root, ".ai4j-bundle.json")
		descriptor, readErr := os.ReadFile(descriptorPath)
		pluginInfo, pluginErr := os.Lstat(filepath.Join(root, "plugin", ".claude-plugin", "plugin.json"))
		if inspectFileDrift(v.catalogPath(record), record.Catalog.Checksum) == cli.DriftUnchanged && readErr == nil && bytes.Equal(descriptor, descriptorBytes) && pluginErr == nil && pluginInfo.Mode().IsRegular() {
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
	if err := writeOwnedNew(v.ownedRoot(filepath.Join(root, ".ai4j-bundle.json")), filepath.Join(root, ".ai4j-bundle.json"), descriptorBytes); err != nil {
		return err
	}
	return writeOwnedNew(v.ownedRoot(v.catalogPath(record)), v.catalogPath(record), catalogBytes)
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
	}{1, record.Source.BundleDigest, record.Source.SourceDigest, record.Source.RenderedDigest, record.ToolkitID, record.PluginID, slices.Clone(record.Selection.Resolved), hex.EncodeToString(artifactDigest[:]), "claude-user-v1"}
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

func (v *v1LifecycleService) currentArtifact(record *installstate.Record) []byte {
	if record == nil || record.Lifecycle != "active" {
		return nil
	}
	entries, err := v.base.state.LoadHistory(record.InstallationID)
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

func (v *v1LifecycleService) verifyDesired(ctx context.Context, desired installstate.Record) error {
	native, problem := v.inspectNative(ctx, desired)
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
	} else if inspectFileDrift(v.catalogPath(desired), desired.Catalog.Checksum) != cli.DriftUnchanged {
		return errors.New("catalog state does not match")
	}
	if desired.Rules != (installstate.OwnedFile{}) && inspectFileDrift(v.rulesPath(desired), desired.Rules.Checksum) != cli.DriftUnchanged {
		return errors.New("rules state does not match")
	}
	return nil
}

func (v *v1LifecycleService) inspectNative(ctx context.Context, record installstate.Record) (validation.NativeStatus, *result.Problem) {
	return v.validation.InspectNativeStatusAt(ctx, nativeDirectory(record), record.MarketplaceID, nativePluginID(record))
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

func (v *v1LifecycleService) captureOwned(record *installstate.Record) ([]byte, []byte, error) {
	if record == nil || record.Lifecycle != "active" {
		return nil, nil, nil
	}
	catalogBytes, err := readOwnedOpaque(v.catalogPath(*record))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	var rulesBytes []byte
	if record.Rules != (installstate.OwnedFile{}) {
		rulesBytes, err = readOwnedOpaque(v.rulesPath(*record))
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

func (v *v1LifecycleService) planHistoryPurge(installationID domain.InstallationID, selection cli.HistoryPurgeSelection, operationID domain.OperationID) (cli.Response, error) {
	record, present, err := v.base.state.LoadByID(installationID.String())
	if err != nil || !present {
		return v1ReadFailure(cli.CommandPlanHistoryPurge, result.FailureConflict, "installation_not_found", "the selected installation does not exist")
	}
	entries, err := v.base.state.LoadHistory(record.InstallationID)
	if err != nil {
		return v1ReadFailure(cli.CommandPlanHistoryPurge, result.FailureRecovery, "history_invalid", "installation history could not be read")
	}
	ids := selectedHistoryIDs(entries, selection, operationID, v.base.now())
	presentCondition, _ := cli.NewCondition(cli.ConditionPresent, "")
	absentCondition, _ := cli.NewCondition(cli.ConditionAbsent, "")
	var actions []cli.Action
	if len(ids) != 0 {
		actions, _ = makeActions([]planActionSpec{{cli.ActionOwnerAI4J, cli.ActionPrepareRecovery, "history purge journal", absentCondition, presentCondition, cli.RecoveryStructuralInverse}, {cli.ActionOwnerAI4J, cli.ActionRemoveState, "retained history: " + strings.Join(ids, ","), presentCondition, absentCondition, cli.RecoveryNone}, {cli.ActionOwnerAI4J, cli.ActionCommitState, "AI4J history references", presentCondition, presentCondition, cli.RecoveryStructuralInverse}, {cli.ActionOwnerAI4J, cli.ActionCleanup, "history purge journal", presentCondition, absentCondition, cli.RecoveryNone}})
	}
	remaining := len(entries) - len(ids)
	final := mustFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	if record.Lifecycle == "archived" {
		final = mustFinalState(cli.StateAbsent, cli.StateAbsent, cli.StateAbsent)
		if remaining > 0 {
			final = mustFinalState(cli.StatePresent, cli.StateAbsent, cli.StateAbsent)
		}
	}
	data, err := cli.NewOfflinePlanData(cli.OperationHistoryPurge, installationID, actions, nil, final)
	if err != nil {
		return cli.Response{}, err
	}
	status := result.StatusOK
	if len(actions) == 0 {
		status = result.StatusNoChange
	}
	commandResult, err := neutralResult(status, result.FailureNone, nil)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandPlanHistoryPurge, commandResult, nil, data)
}

func selectedHistoryIDs(entries []installstate.HistoryEntry, selection cli.HistoryPurgeSelection, operationID domain.OperationID, now time.Time) []string {
	var ids []string
	switch selection {
	case cli.HistoryPurgeOperation:
		for _, entry := range entries {
			if entry.OperationID == operationID.String() {
				ids = append(ids, entry.OperationID)
			}
		}
	case cli.HistoryPurgeExpired:
		cutoff := now.UTC().Add(-90 * 24 * time.Hour)
		for index, entry := range entries {
			timestamp, _ := time.Parse(time.RFC3339, entry.Timestamp)
			if timestamp.Before(cutoff) && index != len(entries)-1 {
				ids = append(ids, entry.OperationID)
			}
		}
	case cli.HistoryPurgeAll:
		for _, entry := range entries {
			ids = append(ids, entry.OperationID)
		}
	}
	slices.Sort(ids)
	return ids
}

func (v *v1LifecycleService) catalogPath(record installstate.Record) string {
	if record.Scope == "project-shared" {
		return projectSettingsPath(record)
	}
	return filepath.Join(v.base.state.DataRoot(), filepath.FromSlash(record.Catalog.Path))
}

func (v *v1LifecycleService) ownedRoot(path string) string {
	root := v.base.state.DataRoot()
	relative, err := filepath.Rel(root, path)
	if err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return root
	}
	rulesRoot := filepath.Dir(path)
	if filepath.Base(rulesRoot) == "rules" && filepath.Base(filepath.Dir(rulesRoot)) == ".claude" {
		return filepath.Dir(filepath.Dir(rulesRoot))
	}
	if filepath.Base(path) == "settings.json" && filepath.Base(filepath.Dir(path)) == ".claude" {
		return filepath.Dir(filepath.Dir(path))
	}
	return v.base.home
}

func (v *v1LifecycleService) rulesPath(record installstate.Record) string {
	if record.Rules.Path == "" {
		return ""
	}
	return filepath.Join(record.ScopeRoot, filepath.FromSlash(record.Rules.Path))
}

func (v *v1LifecycleService) recovery(command cli.Command, operation cli.Operation, operationID domain.OperationID, installationID domain.InstallationID, final cli.FinalState, actions []cli.Action, code string) (cli.Response, error) {
	problem, _ := result.NewProblem(code, "operation requires recovery before another mutation", nil)
	commandResult, err := result.New(result.Facts{Status: result.StatusError, Phase: result.PhaseApplying, Outcome: result.OutcomePending, Mutation: result.MutationStarted, DurableChange: result.DurableChangeNone, Failure: result.FailureRecovery, UpdateDisposition: result.UpdateNotChecked, Errors: []result.Problem{problem}})
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewMutationData(operation, commandResult, &installationID, actions, final, result.UpdateNotChecked)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, &operationID, data)
}

func committedV1(command cli.Command, operation cli.Operation, operationID domain.OperationID, installationID *domain.InstallationID, final cli.FinalState, disposition result.UpdateDisposition, warnings []result.Warning, actions []cli.Action, degraded bool) (cli.Response, error) {
	status := result.StatusOK
	if degraded {
		status = result.StatusDegraded
	}
	commandResult, err := result.New(result.Facts{Status: status, Phase: result.PhaseComplete, Outcome: result.OutcomeCommitted, Mutation: result.MutationStarted, DurableChange: result.DurableCommittedWithDiff, Failure: result.FailureNone, UpdateDisposition: disposition, Warnings: warnings})
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewMutationData(operation, commandResult, installationID, actions, final, disposition)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, &operationID, data)
}

func resolveInteractivePolicy(policy cli.ConflictPolicy, output cli.OutputMode, commandIO CommandIO) (cli.ConflictPolicy, bool, error) {
	if policy != cli.ConflictInteractive {
		return policy, true, nil
	}
	if output == cli.OutputJSON || !commandIO.Interactive || commandIO.Input == nil || commandIO.Output == nil {
		return "", false, nil
	}
	if _, err := io.WriteString(commandIO.Output, "Replace only resources proven owned by this installation? [y/N]: "); err != nil {
		return "", false, err
	}
	buffer := make([]byte, 16)
	count, err := commandIO.Input.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	answer := strings.ToLower(strings.TrimSpace(string(buffer[:count])))
	if answer == "y" || answer == "yes" {
		return cli.ConflictReplaceOwned, true, nil
	}
	return cli.ConflictKeep, true, nil
}

func conflictProblems(conflicts []cli.Conflict) []result.Problem {
	problems := make([]result.Problem, 0, len(conflicts))
	for _, conflict := range conflicts {
		resource, _ := result.NewContext("resource", conflict.Resource())
		problem, _ := result.NewProblem(conflict.Code(), conflict.Message(), []result.Context{resource})
		problems = append(problems, problem)
	}
	return problems
}

func conflictWarnings(conflicts []cli.Conflict) []result.Warning {
	warnings := make([]result.Warning, 0, len(conflicts))
	for _, conflict := range conflicts {
		resource, _ := result.NewContext("resource", conflict.Resource())
		warning, _ := result.NewWarning("kept_"+conflict.Code(), "conflicting installation-owned state was preserved and the installation is degraded", []result.Context{resource})
		warnings = append(warnings, warning)
	}
	return warnings
}

func v1ReadFailure(command cli.Command, failure result.Failure, code, message string) (cli.Response, error) {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := neutralResult(result.StatusError, failure, []result.Problem{problem})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}

func v1Stop(command cli.Command, failure result.Failure, code, message string) (v1Execution, cli.Response, bool) {
	response, _ := v1ReadFailure(command, failure, code, message)
	return v1Execution{}, response, true
}

func v1SelectionStop(command cli.Command, report validation.LifecycleSelection) (v1Execution, cli.Response, bool) {
	failure := result.FailureValidation
	switch report.Failure {
	case validation.FailureEnvironment:
		failure = result.FailureEnvironment
	case validation.FailureSource:
		failure = result.FailureSource
	case validation.FailureConflict:
		failure = result.FailureConflict
	case validation.FailureInternal:
		failure = result.FailureInternal
	}
	commandResult, _ := result.New(result.Facts{Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone, Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone, Failure: failure, UpdateDisposition: result.UpdateNotChecked, Warnings: report.Warnings, Errors: report.Problems})
	response, _ := cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
	return v1Execution{}, response, true
}

func v1ReportStop(command cli.Command, report validation.Report) (v1Execution, cli.Response, bool) {
	commandResult, _ := validationCommandResult(report)
	response, _ := cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
	return v1Execution{}, response, true
}

func planAsCommand(response cli.Response, command cli.Command) (cli.Response, error) {
	if response.Command() == command {
		return response, nil
	}
	return cli.NewResponse(command, response.Result(), nil, response.Data())
}

func commandForPlan(command cli.Command) cli.Command {
	switch command {
	case cli.CommandInstall:
		return cli.CommandPlanInstall
	case cli.CommandUpdate:
		return cli.CommandPlanUpdate
	case cli.CommandSync:
		return cli.CommandPlanSync
	case cli.CommandRollback:
		return cli.CommandPlanRollback
	case cli.CommandUninstall:
		return cli.CommandPlanUninstall
	case cli.CommandHistoryPurge:
		return cli.CommandPlanHistoryPurge
	default:
		return ""
	}
}

func selectionFromRecord(record installstate.Record) cli.SelectionOptions {
	return cli.NewSelectionOptions(record.Selection.All, record.Selection.Assets, record.Selection.Bundles)
}

func installationIDFor(report validation.LifecycleSelection, scope cli.Scope, scopeRoot string) domain.InstallationID {
	sourceIdentity := report.Source.Checkout()
	if report.Source.Mode() == cli.SourceGitHub {
		sourceIdentity = report.Source.Repository().String()
	}
	digest := sha256.Sum256([]byte(report.ToolkitID + "\x00" + sourceIdentity + "\x00" + string(scope) + "\x00" + filepath.Clean(scopeRoot)))
	id, _ := domain.NewInstallationID("install-" + hex.EncodeToString(digest[:8]))
	return id
}

func marketplaceIDFor(installationID domain.InstallationID) string {
	value := strings.TrimPrefix(installationID.String(), "install-")
	return "ai4j-" + value
}

func nativePluginID(record installstate.Record) string {
	return record.PluginID + "@" + record.MarketplaceID
}

func mustInstallation(value string) domain.InstallationID {
	id, _ := domain.NewInstallationID(value)
	return id
}

func ptrInstallation(value domain.InstallationID) *domain.InstallationID { return &value }

func recordInstallation(records ...*installstate.Record) *domain.InstallationID {
	for _, record := range records {
		if record != nil {
			id := mustInstallation(record.InstallationID)
			return &id
		}
	}
	return nil
}

func appendUnique(values []string, value string) []string {
	result := slices.Clone(values)
	if !slices.Contains(result, value) {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func cloneRecordPtr(record *installstate.Record) *installstate.Record {
	if record == nil {
		return nil
	}
	copy := cloneRecord(*record)
	return &copy
}

func cloneRecord(record installstate.Record) installstate.Record {
	record.Selection.Assets = slices.Clone(record.Selection.Assets)
	record.Selection.Bundles = slices.Clone(record.Selection.Bundles)
	record.Selection.Resolved = slices.Clone(record.Selection.Resolved)
	record.NativeResources = slices.Clone(record.NativeResources)
	record.History = slices.Clone(record.History)
	if record.Source.RequestedRef != nil {
		value := *record.Source.RequestedRef
		record.Source.RequestedRef = &value
	}
	return record
}

func recordsEquivalent(left, right installstate.Record) bool {
	left.LastOperation, right.LastOperation = installstate.LastOperation{}, installstate.LastOperation{}
	left.History, right.History = nil, nil
	left.Health, right.Health = "healthy", "healthy"
	return reflect.DeepEqual(left, right)
}

func sameCurrentState(current, expected installstate.Record) bool {
	return current.InstallationID == expected.InstallationID && current.Lifecycle == expected.Lifecycle && recordSourceRevision(current) == recordSourceRevision(expected) && current.Catalog.Checksum == expected.Catalog.Checksum && current.Rules.Checksum == expected.Rules.Checksum && slices.Equal(current.NativeResources, expected.NativeResources)
}

func recordSourceRevision(record installstate.Record) string {
	if record.Source.Mode == "development_source" {
		return record.Source.SourceDigest
	}
	return record.Source.Commit
}

func cliSourceRevision(source cli.Source) string {
	if source.Mode() == cli.SourceDevelopment {
		return source.SourceDigest().String()
	}
	return source.Commit().OID().String()
}

func mustCLIConflict(code, resource, message string) cli.Conflict {
	conflict, err := cli.NewConflict(code, resource, message)
	if err != nil {
		conflict, _ = cli.NewConflict("lifecycle_conflict", "installation resource", "installation resource is in conflict")
	}
	return conflict
}
