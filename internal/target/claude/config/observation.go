package config

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/pathsafe"
)

// MappingQualification is a closed authority state for target directory
// mapping. The pure checkpoint can produce only unqualified candidates.
type MappingQualification struct{ value uint8 }

var unqualifiedMapping = MappingQualification{value: 1}

// UnqualifiedMapping returns the only state available before neutral host proof.
func UnqualifiedMapping() MappingQualification { return unqualifiedMapping }

// NewMappingQualification parses the canonical state.
func NewMappingQualification(value string) (MappingQualification, error) {
	if value != "unqualified" {
		return MappingQualification{}, newError(CodeInvalidObservation)
	}
	return unqualifiedMapping, nil
}

// String returns the canonical state name.
func (q MappingQualification) String() string {
	if q == unqualifiedMapping {
		return "unqualified"
	}
	return "invalid"
}

// Valid reports whether the state is registered.
func (q MappingQualification) Valid() bool { return q == unqualifiedMapping }

// CandidateObservation is the immutable result of pure T3 target mapping. It
// deliberately remains unqualified: it carries neither presence facts nor a
// host proof and cannot activate a managed output root.
type CandidateObservation struct {
	home          TrustedHome
	configuration DirectoryCandidate
	rules         DirectoryCandidate
	policy        OverridePolicy
}

// NewCandidateObservation constructs a coherent unqualified mapping. The
// default source is fixed to .claude, and rules is always config/rules.
func NewCandidateObservation(
	home TrustedHome,
	configuration DirectoryCandidate,
	rules DirectoryCandidate,
	policy OverridePolicy,
) (CandidateObservation, error) {
	result := CandidateObservation{home: home, configuration: configuration, rules: rules, policy: policy}
	if !result.Valid() {
		return CandidateObservation{}, newError(CodeInvalidObservation)
	}
	return result, nil
}

// Configuration returns the unqualified effective configuration candidate.
func (o CandidateObservation) Configuration() DirectoryCandidate { return o.configuration }

// Rules returns the unqualified documented rules candidate.
func (o CandidateObservation) Rules() DirectoryCandidate { return o.rules }

// OverridePolicy returns the exact-version policy used during mapping.
func (o CandidateObservation) OverridePolicy() OverridePolicy { return o.policy }

// Qualification returns the fixed non-authoritative state.
func (o CandidateObservation) Qualification() MappingQualification {
	if !o.Valid() {
		return MappingQualification{}
	}
	return unqualifiedMapping
}

// Valid reports whether the default and config/rules relationships are exact.
func (o CandidateObservation) Valid() bool {
	if !o.home.Valid() || !o.configuration.Valid() || !o.rules.Valid() || !o.policy.Valid() ||
		o.configuration.Role() != environment.ClaudeConfigurationDirectory() ||
		o.rules.Role() != environment.ClaudeRulesDirectory() ||
		o.configuration.Source() != o.rules.Source() {
		return false
	}
	if o.configuration.Source() == environment.DefaultDirectorySource() &&
		o.configuration.RelativePath().String() != ".claude" {
		return false
	}
	if o.configuration.Source() == environment.EnvironmentOverrideDirectorySource() &&
		o.policy.Decision() != AllowedOverrideDecision() {
		return false
	}
	wantRules, err := pathsafe.NewRelativePath(o.configuration.RelativePath().String() + "/rules")
	return err == nil && o.rules.RelativePath() == wantRules
}

// Format redacts home and relative locators and emphasizes non-qualification.
func (o CandidateObservation) Format(state fmt.State, _ rune) {
	source := "invalid"
	if o.configuration.Valid() {
		source = o.configuration.Source().String()
	}
	_, _ = io.WriteString(state, "<claude-config-candidate:"+source+":unqualified:redacted>")
}

// MarshalText emits only the safe redacted marker.
func (o CandidateObservation) MarshalText() ([]byte, error) {
	if !o.Valid() {
		return nil, newError(CodeInvalidObservation)
	}
	return []byte(fmt.Sprintf("%v", o)), nil
}

// MarshalJSON exposes only documented roles/source, the exact policy, and the
// explicit unqualified state. No home or effective locator is serialized.
func (o CandidateObservation) MarshalJSON() ([]byte, error) {
	if !o.Valid() {
		return nil, newError(CodeInvalidObservation)
	}
	return json.Marshal(struct {
		Configuration DirectoryCandidate `json:"configuration"`
		Rules         DirectoryCandidate `json:"rules"`
		Policy        OverridePolicy     `json:"override_policy"`
		Qualification string             `json:"qualification"`
	}{
		Configuration: o.configuration,
		Rules:         o.rules,
		Policy:        o.policy,
		Qualification: unqualifiedMapping.String(),
	})
}
