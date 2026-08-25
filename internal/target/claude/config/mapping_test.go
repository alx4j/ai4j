package config_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/target/claude/config"
)

func TestResolveCandidateMapsDocumentedDefaultAndRulesExactly(t *testing.T) {
	t.Parallel()

	version := mustClaudeVersion(t, "2.1.211")
	observation, err := config.ResolveCandidate(
		t.Context(),
		mustInput(t, testHome, true, "", false),
		mustHome(t, testHome),
		version,
		mustPolicy(t, version, config.UnsupportedVersionOverrideDecision()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Valid() || observation.Qualification() != config.UnqualifiedMapping() ||
		observation.Configuration().Role() != environment.ClaudeConfigurationDirectory() ||
		observation.Configuration().Source() != environment.DefaultDirectorySource() ||
		observation.Configuration().RelativePath().String() != ".claude" ||
		observation.Rules().Role() != environment.ClaudeRulesDirectory() ||
		observation.Rules().Source() != environment.DefaultDirectorySource() ||
		observation.Rules().RelativePath().String() != ".claude/rules" {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestResolveCandidateMapsAllowedOverrideBelowExactHome(t *testing.T) {
	t.Parallel()

	version := mustClaudeVersion(t, "2.1.211")
	observation, err := config.ResolveCandidate(
		t.Context(),
		mustInput(t, testHome, true, overridePath+"/nested", true),
		mustHome(t, testHome),
		version,
		mustPolicy(t, version, config.AllowedOverrideDecision()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Valid() || observation.Configuration().Source() != environment.EnvironmentOverrideDirectorySource() ||
		observation.Configuration().RelativePath().String() != "work-config-canary/nested" ||
		observation.Rules().RelativePath().String() != "work-config-canary/nested/rules" ||
		observation.OverridePolicy().Version() != version {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestResolveCandidateClassifiesHomeAndOverrideFaults(t *testing.T) {
	t.Parallel()

	version := mustClaudeVersion(t, "2.1.211")
	home := mustHome(t, testHome)
	tests := []struct {
		name     string
		input    config.StartupInput
		decision config.OverrideDecision
		kind     environment.FaultKind
		reason   environment.FaultReason
		fact     environment.EnvironmentFact
	}{
		{
			name: "home absent", input: mustInput(t, "", false, "", false), decision: config.AllowedOverrideDecision(),
			kind: environment.IncompleteEnvironmentFaultKind(), reason: environment.MissingRequiredFactReason(),
			fact: environment.ClaudeConfigurationFact(),
		},
		{
			name: "home explicit empty", input: mustInput(t, "", true, "", false), decision: config.AllowedOverrideDecision(),
			kind: environment.IncompleteEnvironmentFaultKind(), reason: environment.MissingRequiredFactReason(),
			fact: environment.ClaudeConfigurationFact(),
		},
		{
			name: "home relative", input: mustInput(t, "Users/alex", true, "", false), decision: config.AllowedOverrideDecision(),
			kind: environment.UnsupportedFaultKind(), reason: environment.UntrustedHomeReason(),
			fact: environment.ClaudeConfigurationFact(),
		},
		{
			name: "home mismatch", input: mustInput(t, "/Users/other", true, "", false), decision: config.AllowedOverrideDecision(),
			kind: environment.UnsupportedFaultKind(), reason: environment.UntrustedHomeReason(),
			fact: environment.ClaudeConfigurationFact(),
		},
		{
			name: "override empty", input: mustInput(t, testHome, true, "", true), decision: config.UnsupportedVersionOverrideDecision(),
			kind: environment.UnsupportedFaultKind(), reason: environment.EmptyConfigOverrideReason(),
			fact: environment.ClaudeConfigurationOverrideFact(),
		},
		{
			name: "override relative", input: mustInput(t, testHome, true, ".claude-work", true), decision: config.UnsupportedVersionOverrideDecision(),
			kind: environment.UnsupportedFaultKind(), reason: environment.RelativeConfigOverrideReason(),
			fact: environment.ClaudeConfigurationOverrideFact(),
		},
		{
			name: "override unsupported version", input: mustInput(t, testHome, true, overridePath, true), decision: config.UnsupportedVersionOverrideDecision(),
			kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedVersionConfigOverrideReason(),
			fact: environment.ClaudeConfigurationOverrideFact(),
		},
		{
			name: "override policy prohibited", input: mustInput(t, testHome, true, overridePath, true), decision: config.PolicyProhibitedOverrideDecision(),
			kind: environment.UnsupportedFaultKind(), reason: environment.PolicyProhibitedConfigOverrideReason(),
			fact: environment.ClaudeConfigurationOverrideFact(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := config.ResolveCandidate(t.Context(), test.input, home, version, mustPolicy(t, version, test.decision))
			requireEnvironmentFault(t, err, test.kind, test.reason, test.fact)
		})
	}
}

func TestResolveCandidateRejectsNonCanonicalOutsideAndNonPortableOverrides(t *testing.T) {
	t.Parallel()

	version := mustClaudeVersion(t, "2.1.211")
	policy := mustPolicy(t, version, config.AllowedOverrideDecision())
	tooManyComponents := strings.Repeat("a/", 128) + "a"
	tests := []string{
		testHome,
		"/Users/outside",
		testHome + "-sibling/config",
		testHome + "/a/../config",
		testHome + "//config",
		testHome + "/CON",
		testHome + "/bad\\name",
		testHome + "/bad:name",
		testHome + "/trailing.",
		testHome + "/" + tooManyComponents,
	}
	for _, override := range tests {
		override := override
		t.Run(override, func(t *testing.T) {
			t.Parallel()
			input, inputErr := config.NewStartupInput(testHome, true, override, true)
			if inputErr != nil {
				return
			}
			_, err := config.ResolveCandidate(t.Context(), input, mustHome(t, testHome), version, policy)
			requireEnvironmentFault(
				t, err, environment.UnsupportedFaultKind(),
				environment.PolicyProhibitedConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact(),
			)
		})
	}
}

func TestResolveCandidateRejectsOverrideWhoseRulesChildExceedsPathsafeBound(t *testing.T) {
	t.Parallel()

	components := make([]string, 16)
	for index := range components {
		components[index] = strings.Repeat("a", 255)
	}
	components[len(components)-1] = strings.Repeat("a", 252)
	relative := strings.Join(components, "/")
	if len(relative) != 4092 {
		t.Fatalf("fixture length = %d", len(relative))
	}
	home := "/u"
	input := mustInput(t, home, true, home+"/"+relative, true)
	version := mustClaudeVersion(t, "2.1.211")
	_, err := config.ResolveCandidate(
		t.Context(), input, mustHome(t, home), version,
		mustPolicy(t, version, config.AllowedOverrideDecision()),
	)
	requireEnvironmentFault(
		t, err, environment.UnsupportedFaultKind(),
		environment.PolicyProhibitedConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact(),
	)
}

func TestResolveCandidateRejectsMisbindingAndHonorsContext(t *testing.T) {
	t.Parallel()

	version := mustClaudeVersion(t, "2.1.211")
	otherVersion := mustClaudeVersion(t, "2.1.212")
	input := mustInput(t, testHome, true, "", false)
	home := mustHome(t, testHome)
	if _, err := config.ResolveCandidate(t.Context(), input, home, version, mustPolicy(t, otherVersion, config.AllowedOverrideDecision())); err == nil {
		t.Fatal("mismatched policy version accepted")
	} else {
		requireCode(t, err, config.CodeInvalidOverridePolicy)
	}
	if _, err := config.ResolveCandidate(nil, input, home, version, mustPolicy(t, version, config.AllowedOverrideDecision())); err == nil {
		t.Fatal("nil context accepted")
	} else {
		requireCode(t, err, config.CodeInvalidContext)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := config.ResolveCandidate(cancelled, input, home, version, mustPolicy(t, version, config.AllowedOverrideDecision())); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
	timedOut, cancelTimeout := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelTimeout()
	if _, err := config.ResolveCandidate(timedOut, input, home, version, mustPolicy(t, version, config.AllowedOverrideDecision())); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	if _, err := config.ResolveCandidate(t.Context(), config.StartupInput{}, home, version, mustPolicy(t, version, config.AllowedOverrideDecision())); err == nil {
		t.Fatal("zero input accepted")
	} else {
		requireCode(t, err, config.CodeInvalidStartupInput)
	}
	if _, err := config.ResolveCandidate(t.Context(), input, config.TrustedHome{}, version, mustPolicy(t, version, config.AllowedOverrideDecision())); err == nil {
		t.Fatal("zero home accepted")
	} else {
		requireCode(t, err, config.CodeInvalidTrustedHome)
	}
}
