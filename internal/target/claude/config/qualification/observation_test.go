package qualification_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/lifecycle"
	claudeconfig "github.com/alx4j/ai4j/internal/target/claude/config"
	"github.com/alx4j/ai4j/internal/target/claude/config/qualification"
)

func TestQualifiedObservationSerializationIsSafeAndCopyStable(t *testing.T) {
	t.Parallel()

	source, _ := newProofFixture(t, lifecycle.PresentDirectoryLeaf(), lifecycle.AbsentDirectoryLeaf())
	service, err := qualification.NewService(source)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := service.ResolveAndQualify(
		t.Context(), mustStartup(t, "", false), mustVersion(t), mustPolicy(t, claudeconfig.AllowedOverrideDecision()),
	)
	if err != nil {
		t.Fatal(err)
	}
	copyOfObservation := observation
	if copyOfObservation != observation || !copyOfObservation.Valid() || copyOfObservation.Candidate() != observation.Candidate() ||
		copyOfObservation.ConfigurationProof() != observation.ConfigurationProof() {
		t.Fatal("observation copy changed immutable facts")
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	formatted := fmt.Sprintf("%v|%+v|%#v|%q|%s", observation, observation, observation, observation, observation)
	for _, forbidden := range []string{qualificationHomeCanary, "qualification-secret-canary", "Filesystem", "Object", "issuer"} {
		if strings.Contains(string(encoded), forbidden) || strings.Contains(formatted, forbidden) {
			t.Fatalf("qualified observation disclosed %q", forbidden)
		}
	}
	for _, required := range []string{`"qualification":"read_only_qualified"`, `"rules_evidence":"qualified"`, `"path":"redacted"`} {
		if !strings.Contains(string(encoded), required) {
			t.Fatalf("qualified JSON %s lacks %s", encoded, required)
		}
	}
}

func TestQualificationStateIsClosedAndZeroObservationFails(t *testing.T) {
	t.Parallel()

	state, err := qualification.NewState("read_only_qualified")
	if err != nil || state != qualification.ReadOnlyQualified() || !state.Valid() {
		t.Fatalf("NewState() = %v, %v", state, err)
	}
	for _, value := range []string{"", "qualified", "mutation_qualified"} {
		if state, stateErr := qualification.NewState(value); stateErr == nil || state.Valid() {
			t.Fatalf("invalid state accepted: %q", value)
		}
	}
	if (qualification.Observation{}).Valid() || (qualification.Observation{}).Qualification().Valid() {
		t.Fatal("zero observation is valid")
	}
	if _, err := json.Marshal(qualification.Observation{}); err == nil {
		t.Fatal("zero observation serialized")
	}
}
