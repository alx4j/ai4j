package result_test

import (
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/result"
)

func TestAllowedResultSemantics(t *testing.T) {
	t.Parallel()

	problem := mustProblem(t, "operation_failed", "the operation failed")
	warning := mustWarning(t, "compatibility_warning", "compatibility is reduced")

	cases := []struct {
		name        string
		facts       result.Facts
		wantExit    result.ExitCode
		wantChanged bool
	}{
		{
			name:     "read-only success",
			facts:    successFacts(),
			wantExit: result.ExitSuccess,
		},
		{
			name: "complete committed state without diff",
			facts: with(successFacts(), func(f *result.Facts) {
				f.Phase = result.PhaseComplete
				f.Outcome = result.OutcomeCommitted
				f.Mutation = result.MutationStarted
				f.DurableChange = result.DurableCommittedWithoutDiff
			}),
			wantExit: result.ExitSuccess,
		},
		{
			name: "complete committed state with diff",
			facts: with(successFacts(), func(f *result.Facts) {
				f.Phase = result.PhaseComplete
				f.Outcome = result.OutcomeCommitted
				f.Mutation = result.MutationStarted
				f.DurableChange = result.DurableCommittedWithDiff
			}),
			wantExit:    result.ExitSuccess,
			wantChanged: true,
		},
		{
			name: "no change",
			facts: with(successFacts(), func(f *result.Facts) {
				f.Status = result.StatusNoChange
				f.UpdateDisposition = result.UpdateUpToDate
			}),
			wantExit: result.ExitSuccess,
		},
		{
			name: "pinned is typed no change",
			facts: with(successFacts(), func(f *result.Facts) {
				f.Status = result.StatusNoChange
				f.UpdateDisposition = result.UpdatePinned
			}),
			wantExit: result.ExitSuccess,
		},
		{
			name: "accepted degraded result",
			facts: with(successFacts(), func(f *result.Facts) {
				f.Status = result.StatusDegraded
				f.Warnings = []result.Warning{warning}
			}),
			wantExit: result.ExitSuccess,
		},
		{
			name:     "cancelled before preparation",
			facts:    cancelledFacts(problem),
			wantExit: result.ExitCancelled,
		},
		{
			name: "cancelled empty recovery completed",
			facts: with(cancelledFacts(problem), func(f *result.Facts) {
				f.Phase = result.PhaseCompleteRolledBack
				f.Outcome = result.OutcomeRolledBack
			}),
			wantExit: result.ExitCancelled,
		},
		{
			name: "committed cleanup pending without diff",
			facts: with(errorFacts(result.FailureRecovery, problem), func(f *result.Facts) {
				f.Phase = result.PhaseCommittedCleanupPending
				f.Outcome = result.OutcomeCommitted
				f.Mutation = result.MutationStarted
				f.DurableChange = result.DurableCommittedWithoutDiff
			}),
			wantExit: result.ExitRecoveryRequired,
		},
		{
			name: "update check source failure is unknown",
			facts: with(errorFacts(result.FailureSource, problem), func(f *result.Facts) {
				f.UpdateDisposition = result.UpdateUnknown
			}),
			wantExit: result.ExitSource,
		},
		{
			name: "committed cleanup pending with diff",
			facts: with(errorFacts(result.FailureRecovery, problem), func(f *result.Facts) {
				f.Phase = result.PhaseCommittedCleanupPending
				f.Outcome = result.OutcomeCommitted
				f.Mutation = result.MutationStarted
				f.DurableChange = result.DurableCommittedWithDiff
			}),
			wantExit:    result.ExitRecoveryRequired,
			wantChanged: true,
		},
		{
			name: "rolled back cleanup pending before mutation",
			facts: with(errorFacts(result.FailureRecovery, problem), func(f *result.Facts) {
				f.Phase = result.PhaseRolledBackCleanupPending
				f.Outcome = result.OutcomeRolledBack
			}),
			wantExit: result.ExitRecoveryRequired,
		},
		{
			name: "rolled back cleanup pending after mutation",
			facts: with(errorFacts(result.FailureRecovery, problem), func(f *result.Facts) {
				f.Phase = result.PhaseRolledBackCleanupPending
				f.Outcome = result.OutcomeRolledBack
				f.Mutation = result.MutationStarted
			}),
			wantExit: result.ExitRecoveryRequired,
		},
	}

	preMutationFailures := []struct {
		failure result.Failure
		exit    result.ExitCode
	}{
		{result.FailureUsage, result.ExitUsageOrApproval},
		{result.FailureApproval, result.ExitUsageOrApproval},
		{result.FailureEnvironment, result.ExitEnvironment},
		{result.FailureSource, result.ExitSource},
		{result.FailureValidation, result.ExitValidation},
		{result.FailureConflict, result.ExitConflict},
		{result.FailureRecovery, result.ExitRecoveryRequired},
		{result.FailureInternal, result.ExitUnexpectedInternal},
	}
	preMutationPhases := []struct {
		name    string
		phase   result.Phase
		outcome result.Outcome
	}{
		{"before journal", result.PhaseNone, result.OutcomeNone},
		{"empty recovery completed", result.PhaseCompleteRolledBack, result.OutcomeRolledBack},
	}
	for _, failureCase := range preMutationFailures {
		for _, phaseCase := range preMutationPhases {
			cases = append(cases, struct {
				name        string
				facts       result.Facts
				wantExit    result.ExitCode
				wantChanged bool
			}{
				name: "pre-mutation " + failureCase.failure.String() + " failure " + phaseCase.name,
				facts: with(errorFacts(failureCase.failure, problem), func(f *result.Facts) {
					f.Phase = phaseCase.phase
					f.Outcome = phaseCase.outcome
				}),
				wantExit: failureCase.exit,
			})
		}
		cases = append(cases, struct {
			name        string
			facts       result.Facts
			wantExit    result.ExitCode
			wantChanged bool
		}{
			name: "incomplete prepared journal after " + failureCase.failure.String(),
			facts: with(errorFacts(failureCase.failure, problem), func(f *result.Facts) {
				f.Phase = result.PhasePrepared
				f.Outcome = result.OutcomePending
			}),
			wantExit: result.ExitRecoveryRequired,
		})
	}

	postMutationPhases := []result.Phase{
		result.PhaseApplying,
		result.PhaseReconciled,
		result.PhaseCompensating,
	}
	postMutationFailures := append([]struct {
		failure result.Failure
		exit    result.ExitCode
	}{}, preMutationFailures...)
	postMutationFailures = append(postMutationFailures, struct {
		failure result.Failure
		exit    result.ExitCode
	}{result.FailureCancellation, result.ExitRecoveryRequired})
	for _, failureCase := range postMutationFailures {
		for _, phase := range postMutationPhases {
			cases = append(cases, struct {
				name        string
				facts       result.Facts
				wantExit    result.ExitCode
				wantChanged bool
			}{
				name: "unresolved " + phase.String() + " after " + failureCase.failure.String(),
				facts: with(errorFacts(failureCase.failure, problem), func(f *result.Facts) {
					f.Phase = phase
					f.Outcome = result.OutcomePending
					f.Mutation = result.MutationStarted
				}),
				wantExit: result.ExitRecoveryRequired,
			})
		}

		cases = append(cases, struct {
			name        string
			facts       result.Facts
			wantExit    result.ExitCode
			wantChanged bool
		}{
			name: "fully compensated after " + failureCase.failure.String(),
			facts: with(errorFacts(failureCase.failure, problem), func(f *result.Facts) {
				f.Phase = result.PhaseCompleteRolledBack
				f.Outcome = result.OutcomeRolledBack
				f.Mutation = result.MutationStarted
			}),
			wantExit: result.ExitCompensated,
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := result.New(tc.facts)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if got.ExitCode() != tc.wantExit {
				t.Fatalf("ExitCode() = %d, want %d", got.ExitCode(), tc.wantExit)
			}
			if got.Changed() != tc.wantChanged {
				t.Fatalf("Changed() = %t, want %t", got.Changed(), tc.wantChanged)
			}
			if !got.Valid() {
				t.Fatal("Valid() = false for constructed Result")
			}
		})
	}
}

func TestNewRejectsContradictoryFacts(t *testing.T) {
	t.Parallel()

	problem := mustProblem(t, "operation_failed", "the operation failed")
	warning := mustWarning(t, "compatibility_warning", "compatibility is reduced")

	cases := []struct {
		name  string
		facts result.Facts
	}{
		{
			name: "compensated result claiming durable diff",
			facts: with(errorFacts(result.FailureValidation, problem), func(f *result.Facts) {
				f.Phase = result.PhaseCompleteRolledBack
				f.Outcome = result.OutcomeRolledBack
				f.Mutation = result.MutationStarted
				f.DurableChange = result.DurableCommittedWithDiff
			}),
		},
		{
			name: "committed cleanup without durable classification",
			facts: with(errorFacts(result.FailureRecovery, problem), func(f *result.Facts) {
				f.Phase = result.PhaseCommittedCleanupPending
				f.Outcome = result.OutcomeCommitted
				f.Mutation = result.MutationStarted
			}),
		},
		{
			name:  "error without typed error",
			facts: errorFacts(result.FailureValidation, result.Problem{}),
		},
		{
			name: "error without failure family",
			facts: with(errorFacts(result.FailureValidation, problem), func(f *result.Facts) {
				f.Failure = result.FailureNone
			}),
		},
		{
			name: "pinned with ok status",
			facts: with(successFacts(), func(f *result.Facts) {
				f.UpdateDisposition = result.UpdatePinned
			}),
		},
		{
			name: "cancelled after mutation",
			facts: with(cancelledFacts(problem), func(f *result.Facts) {
				f.Phase = result.PhaseApplying
				f.Outcome = result.OutcomePending
				f.Mutation = result.MutationStarted
			}),
		},
		{
			name: "cancelled with incomplete prepared journal",
			facts: with(cancelledFacts(problem), func(f *result.Facts) {
				f.Phase = result.PhasePrepared
				f.Outcome = result.OutcomePending
			}),
		},
		{
			name: "phase and outcome disagree",
			facts: with(successFacts(), func(f *result.Facts) {
				f.Phase = result.PhasePrepared
			}),
		},
		{
			name: "committed completion before mutation",
			facts: with(successFacts(), func(f *result.Facts) {
				f.Phase = result.PhaseComplete
				f.Outcome = result.OutcomeCommitted
				f.DurableChange = result.DurableCommittedWithoutDiff
			}),
		},
		{
			name: "cleanup pending with non-recovery failure",
			facts: with(errorFacts(result.FailureConflict, problem), func(f *result.Facts) {
				f.Phase = result.PhaseRolledBackCleanupPending
				f.Outcome = result.OutcomeRolledBack
				f.Mutation = result.MutationStarted
			}),
		},
		{
			name: "degraded without warning",
			facts: with(successFacts(), func(f *result.Facts) {
				f.Status = result.StatusDegraded
			}),
		},
		{
			name: "degraded with an invalid warning",
			facts: with(successFacts(), func(f *result.Facts) {
				f.Status = result.StatusDegraded
				f.Warnings = []result.Warning{{}}
			}),
		},
		{
			name: "success with a failure",
			facts: with(successFacts(), func(f *result.Facts) {
				f.Failure = result.FailureInternal
				f.Errors = []result.Problem{problem}
			}),
		},
		{
			name: "degraded with valid warning but invalid phase",
			facts: with(successFacts(), func(f *result.Facts) {
				f.Status = result.StatusDegraded
				f.Phase = result.PhasePrepared
				f.Outcome = result.OutcomePending
				f.Warnings = []result.Warning{warning}
			}),
		},
		{
			name: "missing update disposition",
			facts: with(successFacts(), func(f *result.Facts) {
				f.UpdateDisposition = ""
			}),
		},
		{
			name: "unknown update disposition without source error",
			facts: with(successFacts(), func(f *result.Facts) {
				f.UpdateDisposition = result.UpdateUnknown
			}),
		},
		{
			name: "unknown update disposition with wrong failure",
			facts: with(errorFacts(result.FailureConflict, problem), func(f *result.Facts) {
				f.UpdateDisposition = result.UpdateUnknown
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := result.New(tc.facts); err == nil {
				t.Fatal("New() error = nil, want contradiction rejected")
			}
		})
	}
}

func TestUpdateUnknownRequiresNeutralPreMutationSourceFailure(t *testing.T) {
	t.Parallel()

	problem := mustProblem(t, "source_unavailable", "source update check failed")
	neutral := with(errorFacts(result.FailureSource, problem), func(f *result.Facts) {
		f.UpdateDisposition = result.UpdateUnknown
	})
	got, err := result.New(neutral)
	if err != nil {
		t.Fatalf("New(neutral source failure) error = %v", err)
	}
	if got.ExitCode() != result.ExitSource {
		t.Fatalf("ExitCode() = %d, want %d", got.ExitCode(), result.ExitSource)
	}

	cases := []struct {
		name     string
		phase    result.Phase
		outcome  result.Outcome
		mutation result.MutationState
		durable  result.DurableChange
	}{
		{
			name:     "prepared journal",
			phase:    result.PhasePrepared,
			outcome:  result.OutcomePending,
			mutation: result.MutationNotStarted,
			durable:  result.DurableChangeNone,
		},
		{
			name:     "applying",
			phase:    result.PhaseApplying,
			outcome:  result.OutcomePending,
			mutation: result.MutationStarted,
			durable:  result.DurableChangeNone,
		},
		{
			name:     "reconciled but uncommitted",
			phase:    result.PhaseReconciled,
			outcome:  result.OutcomePending,
			mutation: result.MutationStarted,
			durable:  result.DurableChangeNone,
		},
		{
			name:     "compensating",
			phase:    result.PhaseCompensating,
			outcome:  result.OutcomePending,
			mutation: result.MutationStarted,
			durable:  result.DurableChangeNone,
		},
		{
			name:     "committed cleanup pending without diff",
			phase:    result.PhaseCommittedCleanupPending,
			outcome:  result.OutcomeCommitted,
			mutation: result.MutationStarted,
			durable:  result.DurableCommittedWithoutDiff,
		},
		{
			name:     "committed cleanup pending with diff",
			phase:    result.PhaseCommittedCleanupPending,
			outcome:  result.OutcomeCommitted,
			mutation: result.MutationStarted,
			durable:  result.DurableCommittedWithDiff,
		},
		{
			name:     "pre-mutation rolled back cleanup pending",
			phase:    result.PhaseRolledBackCleanupPending,
			outcome:  result.OutcomeRolledBack,
			mutation: result.MutationNotStarted,
			durable:  result.DurableChangeNone,
		},
		{
			name:     "post-mutation rolled back cleanup pending",
			phase:    result.PhaseRolledBackCleanupPending,
			outcome:  result.OutcomeRolledBack,
			mutation: result.MutationStarted,
			durable:  result.DurableChangeNone,
		},
		{
			name:     "pre-mutation complete rolled back",
			phase:    result.PhaseCompleteRolledBack,
			outcome:  result.OutcomeRolledBack,
			mutation: result.MutationNotStarted,
			durable:  result.DurableChangeNone,
		},
		{
			name:     "post-mutation complete rolled back",
			phase:    result.PhaseCompleteRolledBack,
			outcome:  result.OutcomeRolledBack,
			mutation: result.MutationStarted,
			durable:  result.DurableChangeNone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			facts := neutral
			facts.Phase = tc.phase
			facts.Outcome = tc.outcome
			facts.Mutation = tc.mutation
			facts.DurableChange = tc.durable
			if _, newErr := result.New(facts); newErr == nil {
				t.Fatal("New() error = nil, want non-neutral update-unknown result rejected")
			}
		})
	}
}

func TestDiagnosticsAreBoundedOwnedAndDeterministic(t *testing.T) {
	t.Parallel()

	resource := mustContext(t, "resource", "catalog/ai4j")
	operation := mustContext(t, "operation", "install")
	inputContext := []result.Context{resource, operation}
	problem, err := result.NewProblem("ownership_conflict", "owned state does not match", inputContext)
	if err != nil {
		t.Fatalf("NewProblem() error = %v", err)
	}
	inputContext[0] = result.Context{}

	gotContext := problem.Context()
	if gotContext[0].Field() != "operation" || gotContext[1].Field() != "resource" {
		t.Fatalf("Context() = %#v, want stable field order", gotContext)
	}
	gotContext[0] = result.Context{}
	if problem.Context()[0].Field() != "operation" {
		t.Fatal("Problem context changed through returned slice")
	}

	first := mustProblem(t, "z_failure", "last by code")
	second := mustProblem(t, "a_failure", "first by code")
	facts := errorFacts(result.FailureValidation, first)
	facts.Errors = []result.Problem{first, second}
	got, err := result.New(facts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	facts.Errors[0] = result.Problem{}
	problems := got.Errors()
	if problems[0].Code() != "a_failure" || problems[1].Code() != "z_failure" {
		t.Fatalf("Errors() codes = [%q %q], want deterministic order", problems[0].Code(), problems[1].Code())
	}
	problems[0] = result.Problem{}
	if got.Errors()[0].Code() != "a_failure" {
		t.Fatal("Result errors changed through returned slice")
	}
}

func TestDiagnosticsWithEqualCodeAndMessageUseCanonicalContextOrder(t *testing.T) {
	t.Parallel()

	alphaContext := mustContext(t, "resource", "alpha")
	zuluContext := mustContext(t, "resource", "zulu")
	alpha, err := result.NewProblem("ownership_conflict", "owned state does not match", []result.Context{alphaContext})
	if err != nil {
		t.Fatalf("NewProblem(alpha) error = %v", err)
	}
	zulu, err := result.NewProblem("ownership_conflict", "owned state does not match", []result.Context{zuluContext})
	if err != nil {
		t.Fatalf("NewProblem(zulu) error = %v", err)
	}

	build := func(values []result.Problem) result.Result {
		t.Helper()
		facts := errorFacts(result.FailureConflict, values[0])
		facts.Errors = values
		got, newErr := result.New(facts)
		if newErr != nil {
			t.Fatalf("New() error = %v", newErr)
		}
		return got
	}

	forward := build([]result.Problem{alpha, zulu}).Errors()
	reversed := build([]result.Problem{zulu, alpha}).Errors()
	for index := range forward {
		gotForward := forward[index].Context()[0].Value()
		gotReversed := reversed[index].Context()[0].Value()
		if gotForward != gotReversed {
			t.Fatalf("context order differs at %d: forward %q, reversed %q", index, gotForward, gotReversed)
		}
	}
	if forward[0].Context()[0].Value() != "alpha" {
		t.Fatalf("first context value = %q, want alpha", forward[0].Context()[0].Value())
	}
}

func TestDiagnosticConstructorsRejectUnsafeOrUnboundedValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		make func() error
	}{
		{
			name: "invalid code",
			make: func() error {
				_, err := result.NewProblem("Not Canonical", "safe message", nil)
				return err
			},
		},
		{
			name: "message control character",
			make: func() error {
				_, err := result.NewWarning("unsafe_message", "line one\nline two", nil)
				return err
			},
		},
		{
			name: "oversized message",
			make: func() error {
				_, err := result.NewProblem("oversized", strings.Repeat("x", 513), nil)
				return err
			},
		},
		{
			name: "invalid context field",
			make: func() error {
				_, err := result.NewContext("Bad Field", "value")
				return err
			},
		},
		{
			name: "context control character",
			make: func() error {
				_, err := result.NewContext("resource", "unsafe\tvalue")
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.make(); err == nil {
				t.Fatal("constructor error = nil, want unsafe value rejected")
			}
		})
	}
}

func TestZeroResultFailsClosed(t *testing.T) {
	t.Parallel()

	var got result.Result
	if got.Valid() {
		t.Fatal("Valid() = true for zero Result")
	}
	if got.ExitCode() != result.ExitUnexpectedInternal {
		t.Fatalf("ExitCode() = %d, want %d", got.ExitCode(), result.ExitUnexpectedInternal)
	}
	if got.Changed() {
		t.Fatal("Changed() = true for zero Result")
	}
}

func successFacts() result.Facts {
	return result.Facts{
		Status:            result.StatusOK,
		Phase:             result.PhaseNone,
		Outcome:           result.OutcomeNone,
		Mutation:          result.MutationNotStarted,
		DurableChange:     result.DurableChangeNone,
		Failure:           result.FailureNone,
		UpdateDisposition: result.UpdateNotChecked,
	}
}

func cancelledFacts(problem result.Problem) result.Facts {
	return result.Facts{
		Status:            result.StatusCancelled,
		Phase:             result.PhaseNone,
		Outcome:           result.OutcomeNone,
		Mutation:          result.MutationNotStarted,
		DurableChange:     result.DurableChangeNone,
		Failure:           result.FailureCancellation,
		UpdateDisposition: result.UpdateNotChecked,
		Errors:            []result.Problem{problem},
	}
}

func errorFacts(failure result.Failure, problem result.Problem) result.Facts {
	return result.Facts{
		Status:            result.StatusError,
		Phase:             result.PhaseNone,
		Outcome:           result.OutcomeNone,
		Mutation:          result.MutationNotStarted,
		DurableChange:     result.DurableChangeNone,
		Failure:           failure,
		UpdateDisposition: result.UpdateNotChecked,
		Errors:            []result.Problem{problem},
	}
}

func with(facts result.Facts, change func(*result.Facts)) result.Facts {
	change(&facts)
	return facts
}

func mustContext(t *testing.T, field, value string) result.Context {
	t.Helper()
	got, err := result.NewContext(field, value)
	if err != nil {
		t.Fatalf("NewContext() error = %v", err)
	}
	return got
}

func mustWarning(t *testing.T, code, message string) result.Warning {
	t.Helper()
	got, err := result.NewWarning(code, message, nil)
	if err != nil {
		t.Fatalf("NewWarning() error = %v", err)
	}
	return got
}

func mustProblem(t *testing.T, code, message string) result.Problem {
	t.Helper()
	got, err := result.NewProblem(code, message, nil)
	if err != nil {
		t.Fatalf("NewProblem() error = %v", err)
	}
	return got
}
