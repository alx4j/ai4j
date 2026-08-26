package cli

import (
	"fmt"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
)

// Data is the closed set of target-neutral CLI response payloads. Concrete
// wire versions project these values without exposing lifecycle internals.
type Data interface {
	cliData()
}

// UnavailableData represents command data that could not be produced. It is
// serialized as null and is valid only for cancelled or failed commands.
type UnavailableData struct{}

func (UnavailableData) cliData() {}

// Response is an immutable, validated command response. A missing command is
// reserved for usage failures that occur before command dispatch.
type Response struct {
	command     Command
	result      result.Result
	operationID domain.OperationID
	data        Data
}

func NewResponse(command Command, commandResult result.Result, operationID *domain.OperationID, data Data) (Response, error) {
	if !commandResult.Valid() {
		return Response{}, fmt.Errorf("response requires a validated result")
	}
	if data == nil {
		return Response{}, fmt.Errorf("response requires typed data")
	}

	if command == "" {
		usage, ok := data.(UsageData)
		if !ok || !usage.valid() {
			return Response{}, fmt.Errorf("missing command requires usage data")
		}
		if commandResult.Status() != result.StatusError || commandResult.Failure() != result.FailureUsage {
			return Response{}, fmt.Errorf("missing command is valid only for a usage failure")
		}
		if operationID != nil {
			return Response{}, fmt.Errorf("pre-dispatch usage response cannot have an operation ID")
		}
		if !neutralLifecycle(commandResult) {
			return Response{}, fmt.Errorf("usage response requires a neutral lifecycle")
		}
	} else {
		if !command.Valid() {
			return Response{}, fmt.Errorf("unknown command %q", command)
		}
		if err := validateDataPair(command, commandResult, data); err != nil {
			return Response{}, err
		}
	}

	var ownedID domain.OperationID
	if operationID != nil {
		if !operationID.Valid() {
			return Response{}, fmt.Errorf("invalid operation ID")
		}
		ownedID = *operationID
	}

	return Response{command: command, result: commandResult, operationID: ownedID, data: data}, nil
}

func validateDataPair(command Command, commandResult result.Result, data Data) error {
	if _, unavailable := data.(UnavailableData); unavailable {
		if commandResult.Status() != result.StatusError && commandResult.Status() != result.StatusCancelled {
			return fmt.Errorf("unavailable data requires an error or cancelled result")
		}
		switch command {
		case CommandInit, CommandValidate, CommandBuild, CommandHistory, CommandList, CommandStatus, CommandVersion:
			if !neutralLifecycle(commandResult) {
				return fmt.Errorf("read-only command requires a neutral lifecycle")
			}
		}
		if command == CommandStatus && (commandResult.Failure() == result.FailureSource || commandResult.Failure() == result.FailureRecovery) {
			return fmt.Errorf("status source or recovery failure requires typed status data")
		}
		if (command == CommandInstall || command == CommandUpdate || command == CommandSync || command == CommandRollback || command == CommandUninstall || command == CommandHistoryPurge) && commandResult.Phase() != result.PhaseNone {
			return fmt.Errorf("mutation lifecycle disclosure requires typed mutation data")
		}
		return nil
	}

	valid := false
	switch command {
	case CommandInit:
		value, ok := data.(InitData)
		valid = ok && value.valid() && commandResult.Status() == result.StatusOK && commandResult.Phase() == result.PhaseComplete && commandResult.Outcome() == result.OutcomeCommitted && commandResult.Mutation() == result.MutationStarted && commandResult.DurableChange() == result.DurableCommittedWithDiff && commandResult.UpdateDisposition() == result.UpdateNotChecked
	case CommandValidate:
		value, ok := data.(ValidateData)
		valid = ok && value.valid() && neutralLifecycle(commandResult) && commandResult.UpdateDisposition() == result.UpdateNotChecked && validationSemantics(value, commandResult)
	case CommandBuild:
		value, ok := data.(BuildData)
		valid = ok && value.valid() && commandResult.Status() == result.StatusOK && commandResult.Phase() == result.PhaseComplete && commandResult.Outcome() == result.OutcomeCommitted && commandResult.Mutation() == result.MutationStarted && commandResult.DurableChange() == result.DurableCommittedWithDiff && commandResult.UpdateDisposition() == result.UpdateNotChecked
	case CommandInstall, CommandUpdate, CommandSync, CommandRollback, CommandUninstall, CommandHistoryPurge:
		switch value := data.(type) {
		case PlanData:
			valid = value.valid() && neutralLifecycle(commandResult) && value.operation.command() == command && value.disposition == commandResult.UpdateDisposition() && planConflictSemantics(value, commandResult)
		case MutationData:
			valid = value.valid() && value.operation.command() == command && value.disposition == commandResult.UpdateDisposition() && sameLifecycle(value.operationResult, commandResult) && updateDispositionSemantics(commandResult)
		}
	case CommandList:
		value, ok := data.(ListData)
		valid = ok && value.valid() && neutralLifecycle(commandResult) && commandResult.Status() == result.StatusOK && commandResult.UpdateDisposition() == result.UpdateNotChecked
	case CommandHistory:
		value, ok := data.(HistoryData)
		valid = ok && value.valid() && neutralLifecycle(commandResult) && commandResult.Status() == result.StatusOK && commandResult.UpdateDisposition() == result.UpdateNotChecked
	case CommandStatus:
		value, ok := data.(StatusData)
		valid = ok && value.valid() && neutralLifecycle(commandResult) && value.disposition == commandResult.UpdateDisposition() && statusInstallationDispositionSemantics(value.installation, value.recovery, value.disposition) && statusRecoverySemantics(value, commandResult)
	case CommandDoctor:
		value, ok := data.(DoctorData)
		valid = ok && value.valid() && neutralLifecycle(commandResult) && commandResult.UpdateDisposition() == result.UpdateNotChecked
	case CommandVersion:
		value, ok := data.(VersionData)
		valid = ok && value.valid() && neutralLifecycle(commandResult) && commandResult.UpdateDisposition() == result.UpdateNotChecked
	}
	if !valid {
		return fmt.Errorf("data %T does not match command %q", data, command)
	}
	return nil
}

func sameLifecycle(left, right result.Result) bool {
	return left.Valid() && right.Valid() && left.Phase() == right.Phase() && left.Outcome() == right.Outcome() && left.Mutation() == right.Mutation() && left.DurableChange() == right.DurableChange()
}

func neutralLifecycle(value result.Result) bool {
	return value.Phase() == result.PhaseNone && value.Outcome() == result.OutcomeNone && value.Mutation() == result.MutationNotStarted && value.DurableChange() == result.DurableChangeNone
}

func updateDispositionSemantics(value result.Result) bool {
	if value.UpdateDisposition() != result.UpdateUnknown {
		return true
	}
	return neutralLifecycle(value) && value.Status() == result.StatusError && value.Failure() == result.FailureSource && value.ExitCode() == result.ExitSource
}

func planConflictSemantics(data PlanData, value result.Result) bool {
	hasConflicts := len(data.conflicts) != 0
	isConflictFailure := value.Status() == result.StatusError && value.Failure() == result.FailureConflict && value.ExitCode() == result.ExitConflict
	return hasConflicts == isConflictFailure
}

func statusRecoverySemantics(data StatusData, value result.Result) bool {
	if data.recovery.state == "none" {
		if value.Failure() == result.FailureRecovery || value.ExitCode() == result.ExitRecoveryRequired {
			return false
		}
		unknown := data.disposition == result.UpdateUnknown
		sourceFailure := value.Status() == result.StatusError && value.Failure() == result.FailureSource && value.ExitCode() == result.ExitSource
		return unknown == sourceFailure
	}
	return value.Status() == result.StatusError && value.Failure() == result.FailureRecovery && value.ExitCode() == result.ExitRecoveryRequired
}

func validationSemantics(data ValidateData, value result.Result) bool {
	if data.validationValid {
		return value.ExitCode() == result.ExitSuccess && value.Failure() == result.FailureNone
	}
	return value.Status() == result.StatusError && value.Failure() == result.FailureValidation && value.ExitCode() == result.ExitValidation
}

func (r Response) Valid() bool {
	if !r.result.Valid() || r.data == nil {
		return false
	}
	if r.command == "" {
		usage, ok := r.data.(UsageData)
		return ok && usage.valid() && !r.operationID.Valid() && neutralLifecycle(r.result) && r.result.Status() == result.StatusError && r.result.Failure() == result.FailureUsage
	}
	return validateDataPair(r.command, r.result, r.data) == nil
}

func (r Response) Command() Command                { return r.command }
func (r Response) Result() result.Result           { return r.result }
func (r Response) Data() Data                      { return r.data }
func (r Response) HasOperationID() bool            { return r.operationID.Valid() }
func (r Response) OperationID() domain.OperationID { return r.operationID }
