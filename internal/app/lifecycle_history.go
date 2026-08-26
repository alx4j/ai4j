package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
)

func (s *lifecycleService) History(_ context.Context, request cli.HistoryRequest) (cli.Response, error) {
	record, present, err := s.state.LoadByID(request.InstallationID().String())
	if err != nil || !present {
		return lifecycleFailure(cli.CommandHistory, result.FailureConflict, "installation_not_found", "the selected installation does not exist", result.UpdateNotChecked, nil)
	}
	entries, err := s.state.LoadHistory(record.InstallationID)
	if err != nil {
		return lifecycleFailure(cli.CommandHistory, result.FailureRecovery, "history_invalid", "installation history could not be read", result.UpdateNotChecked, nil)
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

func (s *lifecycleService) HistoryPurge(ctx context.Context, request cli.HistoryPurgeRequest, commandIO CommandIO) (cli.Response, error) {
	if request.DryRun() {
		return s.planHistoryPurge(request.InstallationID(), request.Selection(), request.OperationID())
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return mutationLockResponse(cli.CommandHistoryPurge, err, result.UpdateNotChecked, nil)
	}
	defer func() { _ = release() }()
	if recovered, recoveryErr := s.reconcileInterrupted(ctx); recoveryErr != nil || !recovered {
		return lifecycleFailure(cli.CommandHistoryPurge, result.FailureRecovery, "recovery_required", "an interrupted operation requires recovery before another mutation", result.UpdateNotChecked, nil)
	}
	plan, err := s.planHistoryPurge(request.InstallationID(), request.Selection(), request.OperationID())
	if err != nil {
		return cli.Response{}, err
	}
	if plan.Result().Status() == result.StatusError {
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
	record, present, err := s.state.LoadByID(request.InstallationID().String())
	if err != nil || !present {
		return lifecycleFailure(cli.CommandHistoryPurge, result.FailureConflict, "installation_not_found", "the selected installation does not exist", result.UpdateNotChecked, nil)
	}
	originalRecord := cloneRecord(record)
	entries, err := s.state.LoadHistory(record.InstallationID)
	if err != nil {
		return lifecycleFailure(cli.CommandHistoryPurge, result.FailureRecovery, "history_invalid", "installation history could not be read", result.UpdateNotChecked, nil)
	}
	ids := selectedHistoryIDs(entries, request.Selection(), request.OperationID(), s.now())
	operationID, err := newOperationID(s.random)
	if err != nil {
		return cli.Response{}, err
	}
	marker, err := installstate.NewResourceMarker("history_purge", operationID.String(), record.InstallationID, recordSourceRevision(record), []string{"history:" + record.InstallationID, "owned:state/installation.json"})
	if err != nil || s.state.SaveMarker(marker) != nil {
		return lifecycleFailure(cli.CommandHistoryPurge, result.FailureInternal, "operation_marker_failed", "history purge could not be prepared", result.UpdateNotChecked, nil)
	}
	remaining := slices.Clone(record.History)
	for _, id := range ids {
		remaining = slices.DeleteFunc(remaining, func(value string) bool { return value == id })
	}
	record.History = remaining
	if err := s.state.DeleteHistory(record.InstallationID, ids); err != nil {
		return s.recovery(cli.CommandHistoryPurge, cli.OperationHistoryPurge, operationID, request.InstallationID(), data.ExpectedFinalState(), data.Actions(), "history_purge_failed")
	}
	if record.Lifecycle == "archived" && len(remaining) == 0 {
		err = s.state.Delete(originalRecord)
	} else {
		err = s.state.Save(record)
	}
	if err != nil {
		return s.recovery(cli.CommandHistoryPurge, cli.OperationHistoryPurge, operationID, request.InstallationID(), data.ExpectedFinalState(), data.Actions(), "history_state_commit_failed")
	}
	if err := s.state.DeleteMarker(); err != nil {
		return s.recovery(cli.CommandHistoryPurge, cli.OperationHistoryPurge, operationID, request.InstallationID(), data.ExpectedFinalState(), data.Actions(), "operation_cleanup_failed")
	}
	return committedResponse(cli.CommandHistoryPurge, cli.OperationHistoryPurge, operationID, ptrInstallation(request.InstallationID()), data.ExpectedFinalState(), result.UpdateNotChecked, nil, data.Actions(), false)
}

func (s *lifecycleService) planHistoryPurge(installationID domain.InstallationID, selection cli.HistoryPurgeSelection, operationID domain.OperationID) (cli.Response, error) {
	record, present, err := s.state.LoadByID(installationID.String())
	if err != nil || !present {
		return lifecycleFailure(cli.CommandHistoryPurge, result.FailureConflict, "installation_not_found", "the selected installation does not exist", result.UpdateNotChecked, nil)
	}
	entries, err := s.state.LoadHistory(record.InstallationID)
	if err != nil {
		return lifecycleFailure(cli.CommandHistoryPurge, result.FailureRecovery, "history_invalid", "installation history could not be read", result.UpdateNotChecked, nil)
	}
	ids := selectedHistoryIDs(entries, selection, operationID, s.now())
	presentCondition, err := cli.NewCondition(cli.ConditionPresent, "")
	if err != nil {
		return cli.Response{}, err
	}
	absentCondition, err := cli.NewCondition(cli.ConditionAbsent, "")
	if err != nil {
		return cli.Response{}, err
	}
	var actions []cli.Action
	if len(ids) != 0 {
		actions, err = makeActions([]planActionSpec{{cli.ActionOwnerAI4J, cli.ActionPrepareRecovery, "history purge journal", absentCondition, presentCondition, cli.RecoveryStructuralInverse}, {cli.ActionOwnerAI4J, cli.ActionRemoveState, "retained history: " + strings.Join(ids, ","), presentCondition, absentCondition, cli.RecoveryNone}, {cli.ActionOwnerAI4J, cli.ActionCommitState, "AI4J history references", presentCondition, presentCondition, cli.RecoveryStructuralInverse}, {cli.ActionOwnerAI4J, cli.ActionCleanup, "history purge journal", presentCondition, absentCondition, cli.RecoveryNone}})
		if err != nil {
			return cli.Response{}, err
		}
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
	return cli.NewResponse(cli.CommandHistoryPurge, commandResult, nil, data)
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
