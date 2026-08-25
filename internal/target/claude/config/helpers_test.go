package config_test

import (
	"errors"
	"testing"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/target/claude/config"
)

const (
	testHome     = "/Users/alex-private-canary"
	overridePath = "/Users/alex-private-canary/work-config-canary"
)

func mustClaudeVersion(t *testing.T, value string) environment.ToolVersion {
	t.Helper()
	semantic, err := environment.NewSemanticVersion(value)
	if err != nil {
		t.Fatal(err)
	}
	version, err := environment.NewSemanticToolVersion(environment.ClaudeTool(), semantic)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func mustPolicy(t *testing.T, version environment.ToolVersion, decision config.OverrideDecision) config.OverridePolicy {
	t.Helper()
	policy, err := config.NewOverridePolicy(version, decision)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func mustHome(t *testing.T, value string) config.TrustedHome {
	t.Helper()
	home, err := config.NewTrustedHome(value)
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func mustInput(t *testing.T, home string, homePresent bool, override string, overridePresent bool) config.StartupInput {
	t.Helper()
	input, err := config.NewStartupInput(home, homePresent, override, overridePresent)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func requireCode(t *testing.T, err error, want config.ErrorCode) {
	t.Helper()
	var typed config.Error
	if !errors.As(err, &typed) || typed.Code() != want {
		t.Fatalf("error = %T %v, want code %s", err, err, want)
	}
}

func requireEnvironmentFault(
	t *testing.T,
	err error,
	wantKind environment.FaultKind,
	wantReason environment.FaultReason,
	wantFact environment.EnvironmentFact,
) {
	t.Helper()
	var typed environment.EnvironmentFault
	if !errors.As(err, &typed) || typed.Kind() != wantKind || typed.Reason() != wantReason || typed.Fact() != wantFact {
		t.Fatalf("error = %T %v, want %s:%s:%s", err, err, wantKind.String(), wantReason.String(), wantFact.String())
	}
}
