package config_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/target/claude/config"
)

func TestErrorCodesAreClosedAndSafe(t *testing.T) {
	t.Parallel()

	codes := []config.ErrorCode{
		config.CodeInvalidStartupInput,
		config.CodeInvalidTrustedHome,
		config.CodeInvalidOverridePolicy,
		config.CodeInvalidDirectoryCandidate,
		config.CodeInvalidContext,
		config.CodeInvalidObservation,
		config.CodeCancelled,
		config.CodeTimedOut,
	}
	for _, code := range codes {
		if !code.Valid() {
			t.Fatalf("registered code %q is invalid", code)
		}
	}
	if config.ErrorCode("future").Valid() {
		t.Fatal("unknown code is valid")
	}
	zero := config.Error{}
	formatted := fmt.Sprintf("%v|%+v|%#v|%q", zero, zero, zero, zero)
	encoded, err := json.Marshal(zero)
	if err != nil {
		t.Fatal(err)
	}
	if formatted != "claude.config.invalid_error|claude.config.invalid_error|claude.config.invalid_error|claude.config.invalid_error" ||
		string(encoded) != `{"code":"claude.config.invalid_error"}` {
		t.Fatalf("zero error = %s / %s", formatted, encoded)
	}
}

func TestOperationalErrorsPreserveOnlyContextCategories(t *testing.T) {
	t.Parallel()

	version := mustClaudeVersion(t, "2.1.211")
	input := mustInput(t, testHome, true, "", false)
	home := mustHome(t, testHome)
	policy := mustPolicy(t, version, config.AllowedOverrideDecision())
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, cancelledErr := config.ResolveCandidate(cancelled, input, home, version, policy)
	if !errors.Is(cancelledErr, context.Canceled) || errors.Is(cancelledErr, context.DeadlineExceeded) {
		t.Fatalf("cancelled error = %v", cancelledErr)
	}
	deadline, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	_, deadlineErr := config.ResolveCandidate(deadline, input, home, version, policy)
	if !errors.Is(deadlineErr, context.DeadlineExceeded) || errors.Is(deadlineErr, context.Canceled) {
		t.Fatalf("deadline error = %v", deadlineErr)
	}
	for _, err := range []error{cancelledErr, deadlineErr} {
		formatted := fmt.Sprintf("%v|%+v|%#v|%q", err, err, err, err)
		encoded, marshalErr := json.Marshal(err)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		for _, forbidden := range []string{testHome, overridePath, "native-error-canary"} {
			if strings.Contains(formatted, forbidden) || strings.Contains(string(encoded), forbidden) {
				t.Fatalf("error disclosed %q", forbidden)
			}
		}
	}
}

func TestTypedHomeFaultExposesOnlyCanonicalFields(t *testing.T) {
	t.Parallel()

	version := mustClaudeVersion(t, "2.1.211")
	_, err := config.ResolveCandidate(
		t.Context(), mustInput(t, "/Users/untrusted-home-canary", true, "", false),
		mustHome(t, testHome), version, mustPolicy(t, version, config.AllowedOverrideDecision()),
	)
	requireEnvironmentFault(
		t, err, environment.UnsupportedFaultKind(), environment.UntrustedHomeReason(),
		environment.ClaudeConfigurationFact(),
	)
	formatted := fmt.Sprintf("%v|%+v|%#v|%q", err, err, err, err)
	encoded, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	want := `{"kind":"unsupported","reason":"untrusted_home","fact":"claude_configuration"}`
	if string(encoded) != want {
		t.Fatalf("JSON = %s", encoded)
	}
	for _, forbidden := range []string{"untrusted-home-canary", testHome, "/Users/"} {
		if strings.Contains(formatted, forbidden) || strings.Contains(string(encoded), forbidden) {
			t.Fatalf("fault disclosed %q", forbidden)
		}
	}
}
