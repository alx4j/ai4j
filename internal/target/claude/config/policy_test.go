package config_test

import (
	"encoding/json"
	"testing"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/target/claude/config"
)

func TestOverrideDecisionsAreClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want config.OverrideDecision
	}{
		{"allowed", config.AllowedOverrideDecision()},
		{"unsupported_version", config.UnsupportedVersionOverrideDecision()},
		{"policy_prohibited", config.PolicyProhibitedOverrideDecision()},
	}
	for _, test := range tests {
		got, err := config.NewOverrideDecision(test.name)
		if err != nil || got != test.want || got.String() != test.name || !got.Valid() {
			t.Fatalf("NewOverrideDecision(%q) = %v, %v", test.name, got, err)
		}
	}
	if _, err := config.NewOverrideDecision("unknown"); err == nil {
		t.Fatal("unknown decision accepted")
	}
	if (config.OverrideDecision{}).Valid() || (config.OverrideDecision{}).String() != "invalid" {
		t.Fatal("zero decision is valid")
	}
}

func TestOverridePolicyBindsOneExactClaudeVersion(t *testing.T) {
	t.Parallel()

	version := mustClaudeVersion(t, "2.1.211")
	for _, decision := range []config.OverrideDecision{
		config.AllowedOverrideDecision(),
		config.UnsupportedVersionOverrideDecision(),
		config.PolicyProhibitedOverrideDecision(),
	} {
		policy, err := config.NewOverridePolicy(version, decision)
		if err != nil || !policy.Valid() || policy.Version() != version || policy.Decision() != decision {
			t.Fatalf("NewOverridePolicy() = %v, %v", policy, err)
		}
	}

	gitSemantic, err := environment.NewSemanticVersion("2.1.211")
	if err != nil {
		t.Fatal(err)
	}
	gitVersion, err := environment.NewSemanticToolVersion(environment.GitTool(), gitSemantic)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		version  environment.ToolVersion
		decision config.OverrideDecision
	}{
		{environment.ToolVersion{}, config.AllowedOverrideDecision()},
		{gitVersion, config.AllowedOverrideDecision()},
		{version, config.OverrideDecision{}},
	} {
		if policy, policyErr := config.NewOverridePolicy(test.version, test.decision); policyErr == nil || policy.Valid() {
			t.Fatalf("invalid policy accepted: %v", policy)
		}
	}
	if (config.OverridePolicy{}).Valid() {
		t.Fatal("zero policy is valid")
	}
}

func TestOverridePolicySerializationIsExactAndSafe(t *testing.T) {
	t.Parallel()

	policy := mustPolicy(t, mustClaudeVersion(t, "2.1.211"), config.AllowedOverrideDecision())
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"claude_version":"2.1.211","decision":"allowed"}` {
		t.Fatalf("JSON = %s", encoded)
	}
	if _, err := json.Marshal(config.OverridePolicy{}); err == nil {
		t.Fatal("zero policy serialized")
	}
}
