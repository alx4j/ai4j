package result

import (
	"fmt"
	"sort"
)

const maxDiagnostics = 64

// Facts are validated lifecycle and diagnostic facts used to construct a
// Result. Changed and ExitCode are intentionally derived instead of supplied.
type Facts struct {
	Status            Status
	Phase             Phase
	Outcome           Outcome
	Mutation          MutationState
	DurableChange     DurableChange
	Failure           Failure
	UpdateDisposition UpdateDisposition
	Warnings          []Warning
	Errors            []Problem
}

// Result is an immutable validated command outcome.
type Result struct {
	valid             bool
	status            Status
	phase             Phase
	outcome           Outcome
	mutation          MutationState
	durableChange     DurableChange
	failure           Failure
	updateDisposition UpdateDisposition
	warnings          []Warning
	errors            []Problem
}

func New(facts Facts) (Result, error) {
	if !facts.Status.valid() {
		return Result{}, fmt.Errorf("unknown result status %q", facts.Status)
	}
	if !facts.Phase.valid() {
		return Result{}, fmt.Errorf("unknown operation phase %q", facts.Phase)
	}
	if !facts.Outcome.valid() {
		return Result{}, fmt.Errorf("unknown operation outcome %q", facts.Outcome)
	}
	if !facts.Mutation.valid() {
		return Result{}, fmt.Errorf("unknown mutation state %q", facts.Mutation)
	}
	if !facts.DurableChange.valid() {
		return Result{}, fmt.Errorf("unknown durable-change value %q", facts.DurableChange)
	}
	if !facts.Failure.valid() {
		return Result{}, fmt.Errorf("unknown failure family %q", facts.Failure)
	}
	if !facts.UpdateDisposition.valid() {
		return Result{}, fmt.Errorf("unknown update disposition %q", facts.UpdateDisposition)
	}
	if len(facts.Warnings) > maxDiagnostics {
		return Result{}, fmt.Errorf("warnings exceed %d entries", maxDiagnostics)
	}
	if len(facts.Errors) > maxDiagnostics {
		return Result{}, fmt.Errorf("errors exceed %d entries", maxDiagnostics)
	}
	for _, warning := range facts.Warnings {
		if !warning.valid() {
			return Result{}, fmt.Errorf("warnings contain an invalid diagnostic")
		}
	}
	for _, problem := range facts.Errors {
		if !problem.valid() {
			return Result{}, fmt.Errorf("errors contain an invalid diagnostic")
		}
	}
	if err := validateLifecycle(facts); err != nil {
		return Result{}, err
	}
	if err := validatePresentation(facts); err != nil {
		return Result{}, err
	}

	warnings := append([]Warning(nil), facts.Warnings...)
	errors := append([]Problem(nil), facts.Errors...)
	sortWarnings(warnings)
	sortProblems(errors)

	return Result{
		valid:             true,
		status:            facts.Status,
		phase:             facts.Phase,
		outcome:           facts.Outcome,
		mutation:          facts.Mutation,
		durableChange:     facts.DurableChange,
		failure:           facts.Failure,
		updateDisposition: facts.UpdateDisposition,
		warnings:          warnings,
		errors:            errors,
	}, nil
}

// Valid reports whether Result was produced by New.
func (r Result) Valid() bool { return r.valid }

func validateLifecycle(facts Facts) error {
	wantOutcome := outcomeForPhase(facts.Phase)
	if facts.Outcome != wantOutcome {
		return fmt.Errorf("phase %q requires outcome %q", facts.Phase, wantOutcome)
	}

	if facts.Outcome == OutcomeCommitted {
		if facts.DurableChange == DurableChangeNone {
			return fmt.Errorf("committed outcome requires a durable-change classification")
		}
	} else if facts.DurableChange != DurableChangeNone {
		return fmt.Errorf("durable change requires a committed outcome")
	}

	switch facts.Phase {
	case PhaseNone, PhasePrepared:
		if facts.Mutation != MutationNotStarted {
			return fmt.Errorf("phase %q cannot follow mutation", facts.Phase)
		}
	case PhaseApplying, PhaseReconciled, PhaseCompensating,
		PhaseCommittedCleanupPending, PhaseComplete:
		if facts.Mutation != MutationStarted {
			return fmt.Errorf("phase %q requires mutation to have started", facts.Phase)
		}
	case PhaseRolledBackCleanupPending, PhaseCompleteRolledBack:
		// Both phases are valid in the pre-mutation empty-recovery branch and
		// in the post-mutation compensation branch.
	}

	return nil
}

func validatePresentation(facts Facts) error {
	if facts.UpdateDisposition == UpdateUnknown &&
		(facts.Status != StatusError ||
			facts.Failure != FailureSource ||
			facts.Phase != PhaseNone ||
			facts.Outcome != OutcomeNone ||
			facts.Mutation != MutationNotStarted ||
			facts.DurableChange != DurableChangeNone) {
		return fmt.Errorf("unknown update disposition requires a neutral pre-mutation source error")
	}

	switch facts.Status {
	case StatusOK:
		if facts.Failure != FailureNone || len(facts.Errors) != 0 {
			return fmt.Errorf("ok result cannot contain a failure")
		}
		if facts.Phase != PhaseNone && facts.Phase != PhaseComplete {
			return fmt.Errorf("ok result cannot use phase %q", facts.Phase)
		}
		if facts.UpdateDisposition == UpdatePinned {
			return fmt.Errorf("pinned disposition requires no_change status")
		}
	case StatusNoChange:
		if facts.Failure != FailureNone || len(facts.Errors) != 0 {
			return fmt.Errorf("no_change result cannot contain a failure")
		}
		if facts.Phase != PhaseNone {
			return fmt.Errorf("no_change result cannot use phase %q", facts.Phase)
		}
	case StatusDegraded:
		if facts.Failure != FailureNone || len(facts.Errors) != 0 {
			return fmt.Errorf("degraded result cannot contain a failure")
		}
		if facts.Phase != PhaseNone && facts.Phase != PhaseComplete {
			return fmt.Errorf("degraded result cannot use phase %q", facts.Phase)
		}
		if len(facts.Warnings) == 0 {
			return fmt.Errorf("degraded result requires a warning")
		}
		if facts.UpdateDisposition == UpdatePinned && facts.Phase != PhaseNone {
			return fmt.Errorf("degraded pinned result requires a neutral lifecycle")
		}
	case StatusCancelled:
		if facts.Failure != FailureCancellation || len(facts.Errors) == 0 {
			return fmt.Errorf("cancelled result requires a typed cancellation error")
		}
		if facts.Mutation != MutationNotStarted {
			return fmt.Errorf("cancelled result cannot follow mutation")
		}
		if facts.Phase != PhaseNone && facts.Phase != PhaseCompleteRolledBack {
			return fmt.Errorf("cancelled result cannot use phase %q", facts.Phase)
		}
		if facts.UpdateDisposition == UpdatePinned {
			return fmt.Errorf("pinned disposition requires no_change status")
		}
	case StatusError:
		if facts.Failure == FailureNone || len(facts.Errors) == 0 {
			return fmt.Errorf("error result requires a typed error")
		}
		if facts.Phase == PhaseComplete {
			return fmt.Errorf("error result cannot use complete phase")
		}
		if facts.Mutation == MutationNotStarted && facts.Failure == FailureCancellation {
			return fmt.Errorf("pre-mutation cancellation requires cancelled status")
		}
		if facts.Phase == PhaseCommittedCleanupPending || facts.Phase == PhaseRolledBackCleanupPending {
			if facts.Failure != FailureRecovery {
				return fmt.Errorf("cleanup-pending result requires recovery failure")
			}
		}
		if facts.UpdateDisposition == UpdatePinned {
			return fmt.Errorf("pinned disposition requires no_change status")
		}
	}

	return nil
}

func outcomeForPhase(phase Phase) Outcome {
	switch phase {
	case PhaseNone:
		return OutcomeNone
	case PhasePrepared, PhaseApplying, PhaseReconciled, PhaseCompensating:
		return OutcomePending
	case PhaseCommittedCleanupPending, PhaseComplete:
		return OutcomeCommitted
	case PhaseRolledBackCleanupPending, PhaseCompleteRolledBack:
		return OutcomeRolledBack
	default:
		return OutcomeNone
	}
}

func (r Result) Status() Status                       { return r.status }
func (r Result) Phase() Phase                         { return r.phase }
func (r Result) Outcome() Outcome                     { return r.outcome }
func (r Result) Mutation() MutationState              { return r.mutation }
func (r Result) DurableChange() DurableChange         { return r.durableChange }
func (r Result) Failure() Failure                     { return r.failure }
func (r Result) UpdateDisposition() UpdateDisposition { return r.updateDisposition }
func (r Result) Warnings() []Warning                  { return append([]Warning(nil), r.warnings...) }
func (r Result) Errors() []Problem                    { return append([]Problem(nil), r.errors...) }

// Changed is true only for a committed final desired state that differs from
// the pre-operation state.
func (r Result) Changed() bool {
	return r.outcome == OutcomeCommitted && r.durableChange == DurableCommittedWithDiff
}

// ExitCode returns the single normative process mapping. The zero Result and
// any otherwise unconstructable value fail closed as an internal error.
func (r Result) ExitCode() ExitCode {
	switch r.status {
	case StatusOK, StatusNoChange, StatusDegraded:
		return ExitSuccess
	case StatusCancelled:
		return ExitCancelled
	case StatusError:
		if r.phase == PhaseCommittedCleanupPending || r.phase == PhaseRolledBackCleanupPending {
			return ExitRecoveryRequired
		}
		switch r.phase {
		case PhasePrepared, PhaseApplying, PhaseReconciled, PhaseCompensating:
			return ExitRecoveryRequired
		case PhaseCompleteRolledBack:
			if r.mutation == MutationStarted && r.outcome == OutcomeRolledBack {
				return ExitCompensated
			}
		}
		return exitCodeForFailure(r.failure)
	default:
		return ExitUnexpectedInternal
	}
}

func exitCodeForFailure(failure Failure) ExitCode {
	switch failure {
	case FailureUsage, FailureApproval:
		return ExitUsageOrApproval
	case FailureEnvironment:
		return ExitEnvironment
	case FailureSource:
		return ExitSource
	case FailureValidation:
		return ExitValidation
	case FailureConflict:
		return ExitConflict
	case FailureRecovery:
		return ExitRecoveryRequired
	case FailureCancellation:
		return ExitCancelled
	case FailureInternal, FailureNone:
		return ExitUnexpectedInternal
	default:
		return ExitUnexpectedInternal
	}
}

func sortWarnings(values []Warning) {
	sort.SliceStable(values, func(i, j int) bool {
		return diagnosticLess(
			diagnostic{code: values[i].code, message: values[i].message, context: values[i].context},
			diagnostic{code: values[j].code, message: values[j].message, context: values[j].context},
		)
	})
}

func sortProblems(values []Problem) {
	sort.SliceStable(values, func(i, j int) bool {
		return diagnosticLess(
			diagnostic{code: values[i].code, message: values[i].message, context: values[i].context},
			diagnostic{code: values[j].code, message: values[j].message, context: values[j].context},
		)
	})
}

func diagnosticLess(left, right diagnostic) bool {
	if left.code != right.code {
		return left.code < right.code
	}
	if left.message != right.message {
		return left.message < right.message
	}
	for index := 0; index < len(left.context) && index < len(right.context); index++ {
		if left.context[index].field != right.context[index].field {
			return left.context[index].field < right.context[index].field
		}
		if left.context[index].value != right.context[index].value {
			return left.context[index].value < right.context[index].value
		}
	}
	return len(left.context) < len(right.context)
}
