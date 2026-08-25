package fault_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/fault"
)

func TestFaultCategoriesRetainSafeTypedDetailsAndCauses(t *testing.T) {
	t.Parallel()

	invalid, err := fault.NewInvalidDetail("source.repository", fault.ReasonInvalidFormat)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("private adapter detail")
	got, err := fault.New(fault.InvalidInput, invalid, cause)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(got, fault.ErrInvalidInput) || !errors.Is(got, cause) {
		t.Fatalf("fault does not retain category/cause: %v", got)
	}
	var typed *fault.Error
	if !errors.As(got, &typed) || typed.Category() != fault.InvalidInput {
		t.Fatalf("errors.As() = %#v", typed)
	}
	detail, ok := typed.Detail().(fault.InvalidDetail)
	if !ok || detail.Field() != "source.repository" || detail.Reason() != fault.ReasonInvalidFormat {
		t.Fatalf("detail = %#v", typed.Detail())
	}
}

func TestCancellationAndTimeoutRetainContextCauses(t *testing.T) {
	t.Parallel()

	detail, err := fault.NewOperationDetail("source_acquisition")
	if err != nil {
		t.Fatal(err)
	}
	cancelled := fault.MustNew(fault.Cancelled, detail, context.Canceled)
	timedOut := fault.MustNew(fault.Timeout, detail, context.DeadlineExceeded)
	if !errors.Is(cancelled, fault.ErrCancelled) || !errors.Is(cancelled, context.Canceled) {
		t.Fatalf("cancelled fault = %v", cancelled)
	}
	if !errors.Is(timedOut, fault.ErrTimeout) || !errors.Is(timedOut, context.DeadlineExceeded) {
		t.Fatalf("timeout fault = %v", timedOut)
	}
}

func TestFaultConstructionCannotExposeOpaqueSecrets(t *testing.T) {
	t.Parallel()

	const secret = "token-super-secret"
	detail, err := fault.NewOperationDetail("native_validation")
	if err != nil {
		t.Fatal(err)
	}
	got := fault.MustNew(fault.Internal, detail, errors.New(secret+` C:\Users\person\.claude\private.json`))
	if strings.Contains(got.Error(), secret) || strings.Contains(got.Error(), ".claude") {
		t.Fatalf("safe error leaked cause: %s", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), ".claude") {
		t.Fatalf("JSON leaked opaque cause: %s", encoded)
	}
	for _, unsafe := range []string{"../../escape", strings.Repeat("a", 129), `C:\Users\private`, "UPPER"} {
		if _, err := fault.NewOperationDetail(unsafe); err == nil {
			t.Errorf("NewOperationDetail(%q) succeeded", unsafe)
		}
	}
}

func TestAllStableCategories(t *testing.T) {
	t.Parallel()

	operation, err := fault.NewOperationDetail("operation")
	if err != nil {
		t.Fatal(err)
	}
	invalid, _ := fault.NewInvalidDetail("field", fault.ReasonEmpty)
	unsupported, _ := fault.NewUnsupportedDetail("target", "future", "mutation")
	conflict, _ := fault.NewConflictDetail("state", "sha256-deadbeef")
	for _, test := range []struct {
		category fault.Category
		detail   fault.Detail
	}{
		{category: fault.InvalidInput, detail: invalid},
		{category: fault.UnsupportedCapability, detail: unsupported},
		{category: fault.Conflict, detail: conflict},
		{category: fault.Cancelled, detail: operation},
		{category: fault.Timeout, detail: operation},
		{category: fault.Internal, detail: operation},
	} {
		if _, err := fault.New(test.category, test.detail, nil); err != nil {
			t.Errorf("New(%q) error = %v", test.category, err)
		}
	}
	if _, err := fault.New(fault.Category("future"), operation, nil); err == nil {
		t.Error("unknown category succeeded")
	}
	if _, err := fault.New(fault.Internal, invalid, nil); err == nil {
		t.Error("mismatched category/detail succeeded")
	}
}
