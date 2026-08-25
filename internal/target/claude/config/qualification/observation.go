package qualification

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/lifecycle"
	claudeconfig "github.com/alx4j/ai4j/internal/target/claude/config"
)

// State is the closed result of the read-only proof join. It is not target
// mutation eligibility.
type State struct{ value uint8 }

var readOnlyQualifiedState = State{value: 1}

func ReadOnlyQualified() State { return readOnlyQualifiedState }

func NewState(value string) (State, error) {
	if value != "read_only_qualified" {
		return State{}, newError(CodeInvalidObservation)
	}
	return readOnlyQualifiedState, nil
}

func (s State) String() string {
	if s == readOnlyQualifiedState {
		return "read_only_qualified"
	}
	return "invalid"
}

func (s State) Valid() bool { return s == readOnlyQualifiedState }

// Observation retains the pure mapping and its exact neutral proof pairing.
// It contains facts only and cannot inspect or mutate the host.
type Observation struct {
	candidate           claudeconfig.CandidateObservation
	home                lifecycle.UserHomeProof
	configurationProof  lifecycle.DirectoryLeafProof
	rulesProof          lifecycle.DirectoryLeafProof
	configuration       environment.Directory
	rules               environment.Directory
	rulesAbsenceDerived bool
}

func newObservation(
	candidate claudeconfig.CandidateObservation,
	home lifecycle.UserHomeProof,
	configurationProof lifecycle.DirectoryLeafProof,
	rulesProof lifecycle.DirectoryLeafProof,
	configuration environment.Directory,
	rules environment.Directory,
	rulesAbsenceDerived bool,
) (Observation, error) {
	result := Observation{
		candidate: candidate, home: home, configurationProof: configurationProof, rulesProof: rulesProof,
		configuration: configuration, rules: rules, rulesAbsenceDerived: rulesAbsenceDerived,
	}
	if !result.Valid() {
		return Observation{}, newError(CodeInvalidObservation)
	}
	return result, nil
}

func (o Observation) Candidate() claudeconfig.CandidateObservation { return o.candidate }
func (o Observation) HomeProof() lifecycle.UserHomeProof           { return o.home }
func (o Observation) ConfigurationProof() lifecycle.DirectoryLeafProof {
	return o.configurationProof
}
func (o Observation) Configuration() environment.Directory { return o.configuration }
func (o Observation) Rules() environment.Directory         { return o.rules }
func (o Observation) Qualification() State {
	if !o.Valid() {
		return State{}
	}
	return readOnlyQualifiedState
}

func (o Observation) RulesProof() (lifecycle.DirectoryLeafProof, bool) {
	if !o.Valid() || o.rulesAbsenceDerived {
		return lifecycle.DirectoryLeafProof{}, false
	}
	return o.rulesProof, true
}

func (o Observation) RulesAbsenceDerived() bool {
	return o.Valid() && o.rulesAbsenceDerived
}

func (o Observation) Valid() bool {
	if !o.candidate.Valid() || !o.home.Valid() || !o.configurationProof.Valid() ||
		!o.configuration.Valid() || !o.rules.Valid() ||
		o.configuration.Role() != environment.ClaudeConfigurationDirectory() ||
		o.rules.Role() != environment.ClaudeRulesDirectory() ||
		o.configuration.Source() != o.candidate.Configuration().Source() ||
		o.rules.Source() != o.candidate.Rules().Source() ||
		o.configurationProof.HomeProof() != o.home ||
		o.configurationProof.RelativePath() != o.candidate.Configuration().RelativePath() ||
		o.configuration.AbsolutePath() != o.configurationProof.Locator().Value() {
		return false
	}
	configurationPresence, ok := environmentPresence(o.configurationProof.Presence())
	if !ok || o.configuration.Presence() != configurationPresence {
		return false
	}
	wantRules, ok := absoluteLocator(o.home, o.candidate.Rules().RelativePath())
	if !ok || o.rules.AbsolutePath() != wantRules {
		return false
	}
	if o.configurationProof.Presence() == lifecycle.AbsentDirectoryLeaf() {
		return o.rulesAbsenceDerived && o.rules.Presence() == environment.AbsentDirectory() &&
			o.rulesProof == (lifecycle.DirectoryLeafProof{})
	}
	if o.rulesAbsenceDerived || !o.rulesProof.Valid() || o.rulesProof.HomeProof() != o.home ||
		o.rulesProof.RelativePath() != o.candidate.Rules().RelativePath() ||
		o.rulesProof.Locator().Value() != wantRules {
		return false
	}
	configurationLeaf, present := o.configurationProof.Leaf()
	if !present || o.rulesProof.Parent() != configurationLeaf {
		return false
	}
	rulesPresence, ok := environmentPresence(o.rulesProof.Presence())
	return ok && o.rules.Presence() == rulesPresence
}

func (o Observation) Format(state fmt.State, _ rune) {
	source := "invalid"
	if o.configuration.Valid() {
		source = o.configuration.Source().String()
	}
	_, _ = io.WriteString(state, "<claude-config-qualification:"+source+":read-only:redacted>")
}

func (o Observation) MarshalText() ([]byte, error) {
	if !o.Valid() {
		return nil, newError(CodeInvalidObservation)
	}
	return []byte(fmt.Sprintf("%v", o)), nil
}

func (o Observation) MarshalJSON() ([]byte, error) {
	if !o.Valid() {
		return nil, newError(CodeInvalidObservation)
	}
	rulesEvidence := "qualified"
	if o.rulesAbsenceDerived {
		rulesEvidence = "derived_absent"
	}
	return json.Marshal(struct {
		Configuration environment.Directory       `json:"configuration"`
		Rules         environment.Directory       `json:"rules"`
		Policy        claudeconfig.OverridePolicy `json:"override_policy"`
		Qualification string                      `json:"qualification"`
		RulesEvidence string                      `json:"rules_evidence"`
	}{
		Configuration: o.configuration,
		Rules:         o.rules,
		Policy:        o.candidate.OverridePolicy(),
		Qualification: readOnlyQualifiedState.String(),
		RulesEvidence: rulesEvidence,
	})
}
