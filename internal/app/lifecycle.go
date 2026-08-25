package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/human"
	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	"github.com/alx4j/ai4j/internal/target/claude/catalog"
	validation "github.com/alx4j/ai4j/internal/validate"
)

type lifecycleValidation interface {
	Validate(context.Context, cli.SourceOptions) validation.Report
	ValidateUpdate(context.Context, cli.SourceOptions, domain.CommitOID) validation.UpdateReport
	InspectPlanExisting(context.Context, string, string) ([]cli.Conflict, *result.Problem)
	InspectUninstall(context.Context, string, string) ([]cli.Conflict, *result.Problem)
	InspectNativeStatus(context.Context) (validation.NativeStatus, *result.Problem)
}

type lifecycleService struct {
	base       *installer
	validation lifecycleValidation
}

func newLifecycleService(base *installer, validationService lifecycleValidation) *lifecycleService {
	return &lifecycleService{base: base, validation: validationService}
}

func (l *lifecycleService) Update(ctx context.Context, request cli.UpdateRequest, commandIO CommandIO) (cli.Response, error) {
	release, err := l.base.acquire(ctx)
	if err != nil {
		return lifecycleFailure(cli.CommandUpdate, result.FailureConflict, "mutation_locked", "another AI4J modifying command is running", result.UpdateNotChecked, nil)
	}
	defer func() { _ = release() }()

	final := mustFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	if response, stop, inspectErr := l.interruptedResponse(cli.CommandUpdate, cli.OperationUpdate, final, result.UpdateNotChecked); inspectErr != nil || stop {
		return response, inspectErr
	}
	record, present, err := l.base.state.Load()
	if err != nil {
		return lifecycleFailure(cli.CommandUpdate, result.FailureConflict, "installation_state_invalid", "installation state could not be read", result.UpdateNotChecked, nil)
	}
	if !present {
		return lifecycleFailure(cli.CommandUpdate, result.FailureConflict, "not_installed", "AI4J is not installed", result.UpdateNotInstalled, nil)
	}
	installationID, err := domain.NewInstallationID(record.InstallationID)
	if err != nil {
		return cli.Response{}, err
	}
	if record.Source.RefKind == cli.RefTag.String() || record.Source.RefKind == cli.RefCommit.String() {
		return lifecycleNoChange(cli.CommandUpdate, cli.OperationUpdate, &installationID, final, result.UpdatePinned, nil)
	}
	installedCommit, err := domain.NewCommitOID(record.Source.Commit)
	if err != nil {
		return cli.Response{}, err
	}
	options, err := updateSourceOptions(record)
	if err != nil {
		return cli.Response{}, err
	}
	update := l.validation.ValidateUpdate(ctx, options, installedCommit)
	if len(update.Report.Problems) != 0 || !update.Report.HasSource() {
		return lifecycleValidationUnavailable(cli.CommandUpdate, update.Report)
	}
	switch update.Disposition {
	case gitsource.UpdateNoChange:
		return lifecycleNoChange(cli.CommandUpdate, cli.OperationUpdate, &installationID, final, result.UpdateUpToDate, update.Report.Warnings)
	case gitsource.UpdateRefRewritten:
		return lifecycleFailure(cli.CommandUpdate, result.FailureConflict, "ref_rewritten", "the tracked branch is not a fast-forward update", result.UpdateRefRewritten, update.Report.Warnings)
	case gitsource.UpdateAvailable:
	default:
		return lifecycleFailure(cli.CommandUpdate, result.FailureSource, "update_source_failed", "the stored source could not be checked", result.UpdateUnknown, update.Report.Warnings)
	}
	conflicts, problem := l.validation.InspectPlanExisting(ctx, record.Catalog.Checksum, record.Rules.Checksum)
	if problem != nil {
		return lifecycleFailure(cli.CommandUpdate, result.FailureEnvironment, problem.Code(), problem.Message(), result.UpdateAvailable, update.Report.Warnings)
	}
	if len(conflicts) != 0 {
		return lifecycleConflict(cli.CommandUpdate, conflicts, result.UpdateAvailable, update.Report.Warnings)
	}
	if expected, supplied := request.ExpectedCommit(); supplied && expected != update.Report.Source.Commit().OID() {
		return lifecycleFailure(cli.CommandUpdate, result.FailureConflict, "expected_commit_mismatch", "resolved source commit does not match --expected-commit", result.UpdateAvailable, update.Report.Warnings)
	}
	exactOptions, err := exactSourceOptions(record)
	if err != nil {
		return cli.Response{}, err
	}
	installedReport := l.validation.Validate(ctx, exactOptions)
	if len(installedReport.Problems) != 0 || !installedReport.HasSource() {
		return lifecycleValidationUnavailable(cli.CommandUpdate, installedReport)
	}
	content, err := diffActiveContent(installedReport.Content, update.Report.Content)
	if err != nil {
		return cli.Response{}, err
	}
	actions, err := updateActions(record, update.Report)
	if err != nil {
		return cli.Response{}, err
	}
	planData, err := cli.NewPlanData(cli.OperationUpdate, update.Report.Source, installationID, actions, content, nil, final, result.UpdateAvailable)
	if err != nil {
		return cli.Response{}, err
	}
	planResponse, err := planResponse(cli.CommandUpdate, planData, update.Report.Warnings, nil, result.UpdateAvailable)
	if err != nil {
		return cli.Response{}, err
	}
	approval, err := approveLifecycle(request.Approved(), request.OutputMode(), commandIO, planResponse, "update")
	if err != nil {
		return cli.Response{}, err
	}
	if approval == approvalDeclined {
		return lifecycleCancelled(cli.CommandUpdate, "update_cancelled", "update was declined before mutation", result.UpdateAvailable, update.Report.Warnings)
	}
	if approval != approvalGranted {
		return lifecycleFailure(cli.CommandUpdate, result.FailureApproval, "approval_required", "update requires explicit approval", result.UpdateAvailable, update.Report.Warnings)
	}

	operationID, err := newOperationID(l.base.random)
	if err != nil {
		return lifecycleFailure(cli.CommandUpdate, result.FailureInternal, "operation_id_unavailable", "update operation could not be prepared", result.UpdateAvailable, update.Report.Warnings)
	}
	marker, err := installstate.NewOperationMarker("update", operationID.String(), installationID.String(), update.Report.Source.Commit().OID().String())
	if err != nil {
		return cli.Response{}, err
	}
	if err := l.base.state.SaveMarker(marker); err != nil {
		return lifecycleFailure(cli.CommandUpdate, result.FailureInternal, "operation_marker_failed", "update operation could not be prepared", result.UpdateAvailable, update.Report.Warnings)
	}
	newCatalog, err := catalog.Render(update.Report.Source.Repository(), update.Report.Source.Commit().OID())
	if err != nil {
		return l.recovery(cli.CommandUpdate, cli.OperationUpdate, operationID, &installationID, final, result.UpdateAvailable, "catalog_render_failed", "update requires recovery", update.Report.Warnings, actions, result.PhaseApplying)
	}
	if err := replaceOwnedMatching(l.base.home, l.base.catalogPath(), record.Catalog.Checksum, newCatalog.Bytes()); err != nil {
		return l.recovery(cli.CommandUpdate, cli.OperationUpdate, operationID, &installationID, final, result.UpdateAvailable, "catalog_replace_failed", "update requires recovery", update.Report.Warnings, actions, result.PhaseApplying)
	}
	if err := l.base.runClaude(ctx, []string{"plugin", "marketplace", "update", "ai4j"}); err != nil {
		return l.recovery(cli.CommandUpdate, cli.OperationUpdate, operationID, &installationID, final, result.UpdateAvailable, "marketplace_update_failed", "update requires recovery", update.Report.Warnings, actions, result.PhaseApplying)
	}
	if err := l.base.runClaude(ctx, []string{"plugin", "update", "ai4j-default@ai4j", "--scope", "user"}); err != nil {
		return l.recovery(cli.CommandUpdate, cli.OperationUpdate, operationID, &installationID, final, result.UpdateAvailable, "plugin_update_failed", "update requires recovery", update.Report.Warnings, actions, result.PhaseApplying)
	}
	if err := replaceOwnedMatching(l.base.home, l.base.rulesPath(), record.Rules.Checksum, update.Report.Rules); err != nil {
		return l.recovery(cli.CommandUpdate, cli.OperationUpdate, operationID, &installationID, final, result.UpdateAvailable, "rules_replace_failed", "update requires recovery", update.Report.Warnings, actions, result.PhaseApplying)
	}
	conflicts, problem = l.validation.InspectPlanExisting(ctx, newCatalog.Digest(), update.Report.RulesChecksum)
	if problem != nil || len(conflicts) != 0 {
		return l.recovery(cli.CommandUpdate, cli.OperationUpdate, operationID, &installationID, final, result.UpdateAvailable, "update_verification_failed", "updated state could not be verified; update requires recovery", update.Report.Warnings, actions, result.PhaseApplying)
	}
	nextRecord := l.base.newRecord(operationID, installationID, update.Report, newCatalog.Digest())
	if err := l.base.state.Save(nextRecord); err != nil {
		return l.recovery(cli.CommandUpdate, cli.OperationUpdate, operationID, &installationID, final, result.UpdateAvailable, "state_commit_failed", "update state could not be committed; update requires recovery", update.Report.Warnings, actions, result.PhaseApplying)
	}
	if err := l.base.state.DeleteMarker(); err != nil {
		return l.recovery(cli.CommandUpdate, cli.OperationUpdate, operationID, &installationID, final, result.UpdateAvailable, "operation_cleanup_failed", "update committed but operation cleanup is required", update.Report.Warnings, actions, result.PhaseCommittedCleanupPending)
	}
	return lifecycleCommitted(cli.CommandUpdate, cli.OperationUpdate, operationID, &installationID, final, result.UpdateAvailable, update.Report.Warnings, actions)
}

func (l *lifecycleService) Uninstall(ctx context.Context, request cli.UninstallRequest, commandIO CommandIO) (cli.Response, error) {
	release, err := l.base.acquire(ctx)
	if err != nil {
		return lifecycleFailure(cli.CommandUninstall, result.FailureConflict, "mutation_locked", "another AI4J modifying command is running", result.UpdateNotChecked, nil)
	}
	defer func() { _ = release() }()

	final := mustFinalState(cli.StateAbsent, cli.StateAbsent, cli.StateAbsent)
	if response, stop, inspectErr := l.interruptedResponse(cli.CommandUninstall, cli.OperationUninstall, final, result.UpdateNotChecked); inspectErr != nil || stop {
		return response, inspectErr
	}
	record, present, err := l.base.state.Load()
	if err != nil {
		return lifecycleFailure(cli.CommandUninstall, result.FailureConflict, "installation_state_invalid", "installation state could not be read", result.UpdateNotChecked, nil)
	}
	if !present {
		return lifecycleNoChange(cli.CommandUninstall, cli.OperationUninstall, nil, final, result.UpdateNotChecked, nil)
	}
	installationID, err := domain.NewInstallationID(record.InstallationID)
	if err != nil {
		return cli.Response{}, err
	}
	exactOptions, err := exactSourceOptions(record)
	if err != nil {
		return cli.Response{}, err
	}
	report := l.validation.Validate(ctx, exactOptions)
	if len(report.Problems) != 0 || !report.HasSource() {
		return lifecycleValidationUnavailable(cli.CommandUninstall, report)
	}
	conflicts, problem := l.validation.InspectUninstall(ctx, record.Catalog.Checksum, record.Rules.Checksum)
	if problem != nil {
		return lifecycleFailure(cli.CommandUninstall, result.FailureEnvironment, problem.Code(), problem.Message(), result.UpdateNotChecked, report.Warnings)
	}
	if len(conflicts) != 0 {
		return lifecycleConflict(cli.CommandUninstall, conflicts, result.UpdateNotChecked, report.Warnings)
	}
	actions, err := uninstallActions(record)
	if err != nil {
		return cli.Response{}, err
	}
	content, err := contentWithChange(report.Content, cli.ContentRemoved)
	if err != nil {
		return cli.Response{}, err
	}
	planData, err := cli.NewPlanData(cli.OperationUninstall, report.Source, installationID, actions, content, nil, final, result.UpdateNotChecked)
	if err != nil {
		return cli.Response{}, err
	}
	planResponse, err := planResponse(cli.CommandUninstall, planData, report.Warnings, nil, result.UpdateNotChecked)
	if err != nil {
		return cli.Response{}, err
	}
	approval, err := approveLifecycle(request.Approved(), request.OutputMode(), commandIO, planResponse, "uninstall")
	if err != nil {
		return cli.Response{}, err
	}
	if approval == approvalDeclined {
		return lifecycleCancelled(cli.CommandUninstall, "uninstall_cancelled", "uninstall was declined before mutation", result.UpdateNotChecked, report.Warnings)
	}
	if approval != approvalGranted {
		return lifecycleFailure(cli.CommandUninstall, result.FailureApproval, "approval_required", "uninstall requires explicit approval", result.UpdateNotChecked, report.Warnings)
	}

	operationID, err := newOperationID(l.base.random)
	if err != nil {
		return lifecycleFailure(cli.CommandUninstall, result.FailureInternal, "operation_id_unavailable", "uninstall operation could not be prepared", result.UpdateNotChecked, report.Warnings)
	}
	marker, err := installstate.NewOperationMarker("uninstall", operationID.String(), installationID.String(), record.Source.Commit)
	if err != nil {
		return cli.Response{}, err
	}
	if err := l.base.state.SaveMarker(marker); err != nil {
		return lifecycleFailure(cli.CommandUninstall, result.FailureInternal, "operation_marker_failed", "uninstall operation could not be prepared", result.UpdateNotChecked, report.Warnings)
	}
	if err := l.base.runClaude(ctx, []string{"plugin", "uninstall", "ai4j-default@ai4j", "--scope", "user", "--keep-data"}); err != nil {
		return l.recovery(cli.CommandUninstall, cli.OperationUninstall, operationID, &installationID, final, result.UpdateNotChecked, "plugin_uninstall_failed", "uninstall requires recovery", report.Warnings, actions, result.PhaseApplying)
	}
	native, nativeProblem := l.validation.InspectNativeStatus(ctx)
	if nativeProblem != nil || native.PluginInstalled {
		return l.recovery(cli.CommandUninstall, cli.OperationUninstall, operationID, &installationID, final, result.UpdateNotChecked, "plugin_uninstall_unverified", "plugin removal could not be verified; uninstall requires recovery", report.Warnings, actions, result.PhaseApplying)
	}
	if native.MarketplaceRegistered {
		if err := l.base.runClaude(ctx, []string{"plugin", "marketplace", "remove", "ai4j", "--scope", "user"}); err != nil {
			return l.recovery(cli.CommandUninstall, cli.OperationUninstall, operationID, &installationID, final, result.UpdateNotChecked, "marketplace_remove_failed", "uninstall requires recovery", report.Warnings, actions, result.PhaseApplying)
		}
	}
	native, nativeProblem = l.validation.InspectNativeStatus(ctx)
	if nativeProblem != nil || native.PluginInstalled || native.MarketplaceRegistered {
		return l.recovery(cli.CommandUninstall, cli.OperationUninstall, operationID, &installationID, final, result.UpdateNotChecked, "native_removal_unverified", "native removal could not be verified; uninstall requires recovery", report.Warnings, actions, result.PhaseApplying)
	}
	if err := removeOwnedMatching(l.base.home, l.base.catalogPath(), record.Catalog.Checksum); err != nil {
		return l.recovery(cli.CommandUninstall, cli.OperationUninstall, operationID, &installationID, final, result.UpdateNotChecked, "catalog_remove_failed", "uninstall requires recovery", report.Warnings, actions, result.PhaseApplying)
	}
	if err := removeOwnedMatching(l.base.home, l.base.rulesPath(), record.Rules.Checksum); err != nil {
		return l.recovery(cli.CommandUninstall, cli.OperationUninstall, operationID, &installationID, final, result.UpdateNotChecked, "rules_remove_failed", "uninstall requires recovery", report.Warnings, actions, result.PhaseApplying)
	}
	if !ownedFileAbsent(l.base.catalogPath()) || !ownedFileAbsent(l.base.rulesPath()) {
		return l.recovery(cli.CommandUninstall, cli.OperationUninstall, operationID, &installationID, final, result.UpdateNotChecked, "owned_removal_unverified", "owned-file removal could not be verified; uninstall requires recovery", report.Warnings, actions, result.PhaseApplying)
	}
	if err := l.base.state.Delete(record); err != nil {
		return l.recovery(cli.CommandUninstall, cli.OperationUninstall, operationID, &installationID, final, result.UpdateNotChecked, "state_remove_failed", "installation state could not be removed; uninstall requires recovery", report.Warnings, actions, result.PhaseApplying)
	}
	if err := l.base.state.DeleteMarker(); err != nil {
		return l.recovery(cli.CommandUninstall, cli.OperationUninstall, operationID, nil, final, result.UpdateNotChecked, "operation_cleanup_failed", "uninstall committed but operation cleanup is required", report.Warnings, actions, result.PhaseCommittedCleanupPending)
	}
	return lifecycleCommitted(cli.CommandUninstall, cli.OperationUninstall, operationID, nil, final, result.UpdateNotChecked, report.Warnings, actions)
}

func (l *lifecycleService) interruptedResponse(command cli.Command, operation cli.Operation, final cli.FinalState, disposition result.UpdateDisposition) (cli.Response, bool, error) {
	marker, present, err := l.base.state.LoadMarker()
	if err != nil {
		response, responseErr := lifecycleRecoveryWithoutIdentity(command, operation, final, disposition, "operation_marker_invalid", "an interrupted operation requires manual recovery")
		return response, true, responseErr
	}
	if !present {
		return cli.Response{}, false, nil
	}
	operationID, _ := domain.NewOperationID(marker.OperationID)
	installationID, _ := domain.NewInstallationID(marker.InstallationID)
	response, responseErr := l.recovery(command, operation, operationID, &installationID, final, disposition, "recovery_required", "an interrupted operation requires manual recovery", nil, nil, result.PhaseApplying)
	return response, true, responseErr
}

func (l *lifecycleService) recovery(command cli.Command, operation cli.Operation, operationID domain.OperationID, installationID *domain.InstallationID, final cli.FinalState, disposition result.UpdateDisposition, code, message string, warnings []result.Warning, actions []cli.Action, phase result.Phase) (cli.Response, error) {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return cli.Response{}, err
	}
	durable := result.DurableChangeNone
	outcome := result.OutcomePending
	if phase == result.PhaseCommittedCleanupPending {
		durable = result.DurableCommittedWithDiff
		outcome = result.OutcomeCommitted
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: phase, Outcome: outcome, Mutation: result.MutationStarted,
		DurableChange: durable, Failure: result.FailureRecovery, UpdateDisposition: disposition,
		Warnings: warnings, Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewMutationData(operation, commandResult, installationID, actions, final, disposition)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, &operationID, data)
}

func approveLifecycle(approved bool, outputMode cli.OutputMode, commandIO CommandIO, plan cli.Response, operation string) (approvalDecision, error) {
	if approved {
		return approvalGranted, nil
	}
	if outputMode == cli.OutputJSON || !commandIO.Interactive || commandIO.Input == nil || commandIO.Output == nil {
		return approvalMissing, nil
	}
	if _, err := human.Render(commandIO.Output, plan); err != nil {
		return approvalMissing, err
	}
	if _, err := io.WriteString(commandIO.Output, "Proceed with "+operation+"? [y/N]: "); err != nil {
		return approvalMissing, err
	}
	line, err := bufio.NewReader(io.LimitReader(commandIO.Input, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return approvalMissing, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return approvalGranted, nil
	}
	return approvalDeclined, nil
}

func lifecycleValidationUnavailable(command cli.Command, report validation.Report) (cli.Response, error) {
	commandResult, err := validationCommandResult(report)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}

func lifecycleFailure(command cli.Command, failure result.Failure, code, message string, disposition result.UpdateDisposition, warnings []result.Warning) (cli.Response, error) {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: failure, UpdateDisposition: disposition, Warnings: warnings, Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}

func lifecycleConflict(command cli.Command, conflicts []cli.Conflict, disposition result.UpdateDisposition, warnings []result.Warning) (cli.Response, error) {
	problems := make([]result.Problem, 0, len(conflicts))
	for _, conflict := range conflicts {
		item, _ := result.NewContext("resource", conflict.Resource())
		problem, _ := result.NewProblem(conflict.Code(), conflict.Message(), []result.Context{item})
		problems = append(problems, problem)
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureConflict, UpdateDisposition: disposition, Warnings: warnings, Errors: problems,
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}

func lifecycleCancelled(command cli.Command, code, message string, disposition result.UpdateDisposition, warnings []result.Warning) (cli.Response, error) {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusCancelled, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureCancellation, UpdateDisposition: disposition, Warnings: warnings, Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}

func lifecycleNoChange(command cli.Command, operation cli.Operation, installationID *domain.InstallationID, final cli.FinalState, disposition result.UpdateDisposition, warnings []result.Warning) (cli.Response, error) {
	commandResult, err := result.New(result.Facts{
		Status: result.StatusNoChange, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureNone, UpdateDisposition: disposition, Warnings: warnings,
	})
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewMutationData(operation, commandResult, installationID, nil, final, disposition)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, data)
}

func lifecycleCommitted(command cli.Command, operation cli.Operation, operationID domain.OperationID, installationID *domain.InstallationID, final cli.FinalState, disposition result.UpdateDisposition, warnings []result.Warning, actions []cli.Action) (cli.Response, error) {
	commandResult, err := result.New(result.Facts{
		Status: result.StatusOK, Phase: result.PhaseComplete, Outcome: result.OutcomeCommitted,
		Mutation: result.MutationStarted, DurableChange: result.DurableCommittedWithDiff,
		Failure: result.FailureNone, UpdateDisposition: disposition, Warnings: warnings,
	})
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewMutationData(operation, commandResult, installationID, actions, final, disposition)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, &operationID, data)
}

func lifecycleRecoveryWithoutIdentity(command cli.Command, operation cli.Operation, final cli.FinalState, disposition result.UpdateDisposition, code, message string) (cli.Response, error) {
	problem, _ := result.NewProblem(code, message, nil)
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseApplying, Outcome: result.OutcomePending,
		Mutation: result.MutationStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureRecovery, UpdateDisposition: disposition, Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewMutationData(operation, commandResult, nil, nil, final, disposition)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, data)
}

func mustFinalState(installation, native, owned cli.StatePresence) cli.FinalState {
	final, err := cli.NewFinalState(installation, native, owned)
	if err != nil {
		panic(err)
	}
	return final
}

func replaceOwnedMatching(home, path, expectedChecksum string, contents []byte) error {
	if err := validateOwnedPath(home, path); err != nil || inspectFileDrift(path, expectedChecksum) != cli.DriftUnchanged {
		return errors.New("owned file does not match installation state")
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
	if err := temporary.Chmod(0o600); err != nil {
		return err
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
	if err := validateOwnedPath(home, path); err != nil || inspectFileDrift(path, expectedChecksum) != cli.DriftUnchanged {
		return errors.New("owned file changed during replacement")
	}
	if err := commitOwnedReplacement(temporaryPath, path); err != nil {
		return err
	}
	digest := sha256Digest(contents)
	if inspectFileDrift(path, digest) != cli.DriftUnchanged {
		return errors.New("owned-file replacement could not be verified")
	}
	return nil
}

func removeOwnedMatching(home, path, expectedChecksum string) error {
	if err := validateOwnedPath(home, path); err != nil || inspectFileDrift(path, expectedChecksum) != cli.DriftUnchanged {
		return errors.New("owned file does not match installation state")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if !ownedFileAbsent(path) {
		return errors.New("owned-file removal could not be verified")
	}
	return nil
}

func validateOwnedPath(home, path string) error {
	relative, err := filepath.Rel(home, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("owned path is outside the user home")
	}
	current := home
	for _, component := range strings.Split(filepath.Dir(relative), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || hostPathUnsafe(current) {
			return errors.New("owned path parent is unsafe")
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || hostPathUnsafe(path) {
		return errors.New("owned path is unsafe")
	}
	return nil
}

func ownedFileAbsent(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

func sha256Digest(contents []byte) string {
	digest := sha256.Sum256(contents)
	return fmt.Sprintf("%x", digest)
}
