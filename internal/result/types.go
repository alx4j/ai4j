// Package result defines target-neutral command outcomes and their process
// presentation semantics.
package result

// Status is the stable top-level command status exposed to callers.
type Status string

const (
	StatusOK        Status = "ok"
	StatusNoChange  Status = "no_change"
	StatusDegraded  Status = "degraded"
	StatusCancelled Status = "cancelled"
	StatusError     Status = "error"
)

func (s Status) String() string { return string(s) }

func (s Status) valid() bool {
	switch s {
	case StatusOK, StatusNoChange, StatusDegraded, StatusCancelled, StatusError:
		return true
	default:
		return false
	}
}

// Phase records the durable lifecycle phase independently of process status.
type Phase string

const (
	PhaseNone                     Phase = "none"
	PhasePrepared                 Phase = "prepared"
	PhaseApplying                 Phase = "applying"
	PhaseReconciled               Phase = "reconciled"
	PhaseCompensating             Phase = "compensating"
	PhaseCommittedCleanupPending  Phase = "committed_cleanup_pending"
	PhaseRolledBackCleanupPending Phase = "rolled_back_cleanup_pending"
	PhaseComplete                 Phase = "complete"
	PhaseCompleteRolledBack       Phase = "complete_rolled_back"
)

func (p Phase) String() string { return string(p) }

func (p Phase) valid() bool {
	switch p {
	case PhaseNone, PhasePrepared, PhaseApplying, PhaseReconciled, PhaseCompensating,
		PhaseCommittedCleanupPending, PhaseRolledBackCleanupPending, PhaseComplete,
		PhaseCompleteRolledBack:
		return true
	default:
		return false
	}
}

// Outcome is the operation's terminal-outcome selection. Pending is distinct
// from None so an unresolved modifying operation cannot look like a read-only
// command.
type Outcome string

const (
	OutcomeNone       Outcome = "none"
	OutcomePending    Outcome = "pending"
	OutcomeCommitted  Outcome = "committed"
	OutcomeRolledBack Outcome = "rolled_back"
)

func (o Outcome) String() string { return string(o) }

func (o Outcome) valid() bool {
	switch o {
	case OutcomeNone, OutcomePending, OutcomeCommitted, OutcomeRolledBack:
		return true
	default:
		return false
	}
}

// MutationState records whether the first target, native, adapter-owned, or
// committed-state mutation began. It disambiguates pre-mutation and
// post-mutation complete_rolled_back results.
type MutationState string

const (
	MutationNotStarted MutationState = "not_started"
	MutationStarted    MutationState = "started"
)

func (m MutationState) String() string { return string(m) }

func (m MutationState) valid() bool {
	switch m {
	case MutationNotStarted, MutationStarted:
		return true
	default:
		return false
	}
}

// DurableChange records whether a committed desired state differs from the
// pre-operation state. It is deliberately not a caller-supplied changed bool.
type DurableChange string

const (
	DurableChangeNone           DurableChange = "none"
	DurableCommittedWithoutDiff DurableChange = "committed_no_diff"
	DurableCommittedWithDiff    DurableChange = "committed_diff"
)

func (d DurableChange) String() string { return string(d) }

func (d DurableChange) valid() bool {
	switch d {
	case DurableChangeNone, DurableCommittedWithoutDiff, DurableCommittedWithDiff:
		return true
	default:
		return false
	}
}

// Failure identifies the primary stable failure family used for exit-code
// selection. Problem values carry the bounded user-facing details.
type Failure string

const (
	FailureNone         Failure = "none"
	FailureUsage        Failure = "usage"
	FailureApproval     Failure = "approval"
	FailureEnvironment  Failure = "environment"
	FailureSource       Failure = "source"
	FailureValidation   Failure = "validation"
	FailureConflict     Failure = "conflict"
	FailureRecovery     Failure = "recovery"
	FailureInternal     Failure = "internal"
	FailureCancellation Failure = "cancellation"
)

func (f Failure) String() string { return string(f) }

func (f Failure) valid() bool {
	switch f {
	case FailureNone, FailureUsage, FailureApproval, FailureEnvironment, FailureSource,
		FailureValidation, FailureConflict, FailureRecovery, FailureInternal,
		FailureCancellation:
		return true
	default:
		return false
	}
}

// UpdateDisposition is the stable update-check classification.
type UpdateDisposition string

const (
	UpdateNotChecked   UpdateDisposition = "not_checked"
	UpdateNotInstalled UpdateDisposition = "not_installed"
	UpdateUpToDate     UpdateDisposition = "up_to_date"
	UpdateAvailable    UpdateDisposition = "available"
	UpdatePinned       UpdateDisposition = "pinned"
	UpdateRefRewritten UpdateDisposition = "ref_rewritten"
	UpdateUnknown      UpdateDisposition = "unknown"
)

func (d UpdateDisposition) String() string { return string(d) }

func (d UpdateDisposition) valid() bool {
	switch d {
	case UpdateNotChecked, UpdateNotInstalled, UpdateUpToDate, UpdateAvailable,
		UpdatePinned, UpdateRefRewritten, UpdateUnknown:
		return true
	default:
		return false
	}
}

// ExitCode is the process status selected from a validated Result.
type ExitCode int

const (
	ExitSuccess            ExitCode = 0
	ExitCancelled          ExitCode = 1
	ExitUsageOrApproval    ExitCode = 2
	ExitEnvironment        ExitCode = 3
	ExitSource             ExitCode = 4
	ExitValidation         ExitCode = 5
	ExitConflict           ExitCode = 6
	ExitCompensated        ExitCode = 7
	ExitRecoveryRequired   ExitCode = 8
	ExitUnexpectedInternal ExitCode = 9
)

func (c ExitCode) Int() int { return int(c) }
