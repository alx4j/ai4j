package config_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/pathsafe"
	"github.com/alx4j/ai4j/internal/target/claude/config"
)

func TestCandidateObservationConstructorEnforcesDefaultAndRulesRelationship(t *testing.T) {
	t.Parallel()

	version := mustClaudeVersion(t, "2.1.211")
	allowed := mustPolicy(t, version, config.AllowedOverrideDecision())
	unsupported := mustPolicy(t, version, config.UnsupportedVersionOverrideDecision())
	home := mustHome(t, testHome)
	defaultConfig := mustCandidate(t, environment.ClaudeConfigurationDirectory(), environment.DefaultDirectorySource(), ".claude")
	defaultRules := mustCandidate(t, environment.ClaudeRulesDirectory(), environment.DefaultDirectorySource(), ".claude/rules")
	overrideConfig := mustCandidate(t, environment.ClaudeConfigurationDirectory(), environment.EnvironmentOverrideDirectorySource(), "work")
	overrideRules := mustCandidate(t, environment.ClaudeRulesDirectory(), environment.EnvironmentOverrideDirectorySource(), "work/rules")

	valid := []struct {
		configuration config.DirectoryCandidate
		rules         config.DirectoryCandidate
		policy        config.OverridePolicy
	}{
		{defaultConfig, defaultRules, unsupported},
		{overrideConfig, overrideRules, allowed},
	}
	for _, test := range valid {
		observation, err := config.NewCandidateObservation(home, test.configuration, test.rules, test.policy)
		if err != nil || !observation.Valid() || observation.Qualification() != config.UnqualifiedMapping() {
			t.Fatalf("NewCandidateObservation() = %v, %v", observation, err)
		}
	}

	invalid := []struct {
		configuration config.DirectoryCandidate
		rules         config.DirectoryCandidate
		policy        config.OverridePolicy
	}{
		{mustCandidate(t, environment.ClaudeConfigurationDirectory(), environment.DefaultDirectorySource(), "other"), defaultRules, allowed},
		{defaultConfig, mustCandidate(t, environment.ClaudeRulesDirectory(), environment.DefaultDirectorySource(), ".claude/not-rules"), allowed},
		{defaultConfig, overrideRules, allowed},
		{overrideConfig, overrideRules, unsupported},
		{defaultRules, defaultConfig, allowed},
		{defaultConfig, defaultRules, config.OverridePolicy{}},
	}
	for _, test := range invalid {
		if observation, err := config.NewCandidateObservation(home, test.configuration, test.rules, test.policy); err == nil || observation.Valid() {
			t.Fatalf("invalid observation accepted: %v", observation)
		}
	}
	if (config.CandidateObservation{}).Valid() || (config.CandidateObservation{}).Qualification().Valid() {
		t.Fatal("zero observation is valid")
	}
}

func TestDirectoryCandidateUsesPathsafeAndRejectsUnrelatedRoles(t *testing.T) {
	t.Parallel()

	validPath, err := pathsafe.NewRelativePath("custom/config")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []environment.DirectoryRole{
		environment.ClaudeConfigurationDirectory(),
		environment.ClaudeRulesDirectory(),
	} {
		for _, source := range []environment.DirectorySource{
			environment.DefaultDirectorySource(),
			environment.EnvironmentOverrideDirectorySource(),
		} {
			candidate, candidateErr := config.NewDirectoryCandidate(role, source, validPath)
			if candidateErr != nil || !candidate.Valid() || candidate.RelativePath() != validPath {
				t.Fatalf("candidate = %v, %v", candidate, candidateErr)
			}
		}
	}
	for _, test := range []struct {
		role   environment.DirectoryRole
		source environment.DirectorySource
		path   pathsafe.RelativePath
	}{
		{environment.DirectoryRole{}, environment.DefaultDirectorySource(), validPath},
		{environment.AI4JStateDirectory(), environment.DefaultDirectorySource(), validPath},
		{environment.ClaudeConfigurationDirectory(), environment.PrivateRuntimeDirectorySource(), validPath},
		{environment.ClaudeConfigurationDirectory(), environment.DefaultDirectorySource(), pathsafe.RelativePath{}},
	} {
		if candidate, candidateErr := config.NewDirectoryCandidate(test.role, test.source, test.path); candidateErr == nil || candidate.Valid() {
			t.Fatalf("invalid candidate accepted: %v", candidate)
		}
	}
	if (config.DirectoryCandidate{}).Valid() {
		t.Fatal("zero candidate is valid")
	}
}

func TestMappingQualificationIsClosed(t *testing.T) {
	t.Parallel()

	got, err := config.NewMappingQualification("unqualified")
	if err != nil || got != config.UnqualifiedMapping() || !got.Valid() || got.String() != "unqualified" {
		t.Fatalf("qualification = %v, %v", got, err)
	}
	if _, err := config.NewMappingQualification("qualified"); err == nil {
		t.Fatal("qualified mapping accepted before host proof")
	}
	if (config.MappingQualification{}).Valid() || (config.MappingQualification{}).String() != "invalid" {
		t.Fatal("zero qualification is valid")
	}
}

func TestTrustedHomeAndObservationSerializationRedactAllLocators(t *testing.T) {
	t.Parallel()

	version := mustClaudeVersion(t, "2.1.211")
	observation, err := config.ResolveCandidate(
		t.Context(), mustInput(t, testHome, true, overridePath, true), mustHome(t, testHome), version,
		mustPolicy(t, version, config.AllowedOverrideDecision()),
	)
	if err != nil {
		t.Fatal(err)
	}
	values := []any{
		mustHome(t, testHome), observation.Configuration(), observation.Rules(), observation,
	}
	for _, value := range values {
		formatted := fmt.Sprintf("%v|%+v|%#v|%q", value, value, value, value)
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		for _, forbidden := range []string{testHome, overridePath, "alex-private-canary", "work-config-canary"} {
			if strings.Contains(formatted, forbidden) || strings.Contains(string(encoded), forbidden) {
				t.Fatalf("%T disclosed %q: %s / %s", value, forbidden, formatted, encoded)
			}
		}
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"configuration":{"role":"claude_configuration","source":"environment_override","path":"redacted","qualification":"unqualified"},"rules":{"role":"claude_rules","source":"environment_override","path":"redacted","qualification":"unqualified"},"override_policy":{"claude_version":"2.1.211","decision":"allowed"},"qualification":"unqualified"}`
	if string(encoded) != want {
		t.Fatalf("JSON = %s\nwant = %s", encoded, want)
	}
	for _, zero := range []any{config.TrustedHome{}, config.DirectoryCandidate{}, config.CandidateObservation{}} {
		if _, marshalErr := json.Marshal(zero); marshalErr == nil {
			t.Fatalf("zero %T serialized", zero)
		}
	}
}

func TestTrustedHomeRejectsUnsafeAbsoluteSpellings(t *testing.T) {
	t.Parallel()

	invalidUTF8 := string([]byte{'/', 0xff})
	for _, value := range []string{
		"", "/", "relative", "/Users/alex/..", "/Users//alex", "/Users/alex/", "/Users/alex\nother",
		invalidUTF8, strings.Repeat("a", 4097),
	} {
		if home, err := config.NewTrustedHome(value); err == nil || home.Valid() {
			t.Fatalf("trusted home %q accepted", value)
		}
	}
	if (config.TrustedHome{}).Valid() {
		t.Fatal("zero trusted home is valid")
	}
}

func TestTrustedHomeCopiesAliasedInput(t *testing.T) {
	t.Parallel()

	bytes := []byte(testHome)
	alias := unsafe.String(unsafe.SliceData(bytes), len(bytes))
	home := mustHome(t, alias)
	for index := range bytes {
		bytes[index] = 'x'
	}
	version := mustClaudeVersion(t, "2.1.211")
	observation, err := config.ResolveCandidate(
		t.Context(), mustInput(t, testHome, true, "", false), home, version,
		mustPolicy(t, version, config.AllowedOverrideDecision()),
	)
	if err != nil || !observation.Valid() {
		t.Fatalf("copied trusted home changed: %v, %v", observation, err)
	}
}

func mustCandidate(
	t *testing.T,
	role environment.DirectoryRole,
	source environment.DirectorySource,
	spelling string,
) config.DirectoryCandidate {
	t.Helper()
	relative, err := pathsafe.NewRelativePath(spelling)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := config.NewDirectoryCandidate(role, source, relative)
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}
