package environment_test

import (
	"encoding/json"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/environment"
)

func TestProfileIDIsExactBoundedAndCanonical(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"claude-v1", "claude.2.1.234-darwin_arm64", "a", strings.Repeat("a", 64)} {
		id, err := environment.NewProfileID(value)
		if err != nil || !id.Valid() || id.String() != value {
			t.Fatalf("NewProfileID(%q) = %v, %v", value, id, err)
		}
	}
	for _, value := range []string{"", "Claude-v1", "1claude", "claude/v1", "claude v1", "claude\nv1", strings.Repeat("a", 65), string([]byte{0xff})} {
		_, err := environment.NewProfileID(value)
		requireCode(t, err, environment.CodeInvalidProfileID)
	}
	if (environment.ProfileID{}).Valid() {
		t.Fatal("zero profile ID must be invalid")
	}
}

func TestCandidateCapabilityFactAcceptsOnlyExactMVPValues(t *testing.T) {
	t.Parallel()

	for _, capability := range domain.MVPCapabilities().Values() {
		fact, err := environment.NewCandidateCapabilityFact(capability)
		if err != nil || !fact.Valid() || fact.Capability() != capability {
			t.Fatalf("NewCandidateCapabilityFact(%s) = %v, %v", capability.String(), fact, err)
		}
	}
	future, _ := domain.NewCapability("future")
	for _, capability := range []domain.Capability{{}, future} {
		_, err := environment.NewCandidateCapabilityFact(capability)
		requireCode(t, err, environment.CodeInvalidCapabilityFact)
	}
	if (environment.CandidateCapabilityFact{}).Valid() {
		t.Fatal("zero capability fact must be invalid")
	}
}

func TestCapabilityProfileIsOrderIndependentCompleteAndUnqualified(t *testing.T) {
	t.Parallel()

	id, _ := environment.NewProfileID("claude-2.1.234-darwin-arm64-v1")
	baseline, err := environment.NewCapabilityProfile(id, candidateFacts(t))
	if err != nil {
		t.Fatal(err)
	}
	if !baseline.Valid() || baseline.MutationQualification() != environment.MutationUnqualified() {
		t.Fatalf("profile = %v", baseline)
	}
	for seed := int64(0); seed < 64; seed++ {
		facts := candidateFacts(t)
		rand.New(rand.NewSource(seed)).Shuffle(len(facts), func(i, j int) { facts[i], facts[j] = facts[j], facts[i] })
		got, constructErr := environment.NewCapabilityProfile(id, facts)
		if constructErr != nil || got != baseline {
			t.Fatalf("seed %d profile = %v, %v", seed, got, constructErr)
		}
	}
	capabilities := baseline.CandidateCapabilities()
	if !capabilities.Valid() || !slices.Equal(capabilities.Values(), domain.MVPCapabilities().Values()) {
		t.Fatal("candidate capabilities differ from the exact MVP set")
	}
	for _, capability := range domain.MVPCapabilities().Values() {
		if !capabilities.Contains(capability) {
			t.Fatalf("candidate capabilities do not contain %s", capability.String())
		}
	}
	facts := baseline.CandidateFacts()
	if !slices.IsSortedFunc(facts, func(a, b environment.CandidateCapabilityFact) int {
		return strings.Compare(a.Capability().String(), b.Capability().String())
	}) {
		t.Fatalf("facts are not sorted: %v", facts)
	}
}

func TestCapabilityProfileCopiesInputsAndOutputs(t *testing.T) {
	t.Parallel()

	id, _ := environment.NewProfileID("claude-v1")
	facts := candidateFacts(t)
	profile, err := environment.NewCapabilityProfile(id, facts)
	if err != nil {
		t.Fatal(err)
	}
	facts[0] = environment.CandidateCapabilityFact{}
	if !profile.Valid() {
		t.Fatal("mutating constructor input changed profile")
	}
	returned := profile.CandidateFacts()
	returned[0] = environment.CandidateCapabilityFact{}
	if !profile.Valid() || !profile.CandidateFacts()[0].Valid() {
		t.Fatal("mutating accessor output changed profile")
	}
	values := profile.CandidateCapabilities().Values()
	values[0] = domain.Capability{}
	if !profile.Valid() || !profile.CandidateCapabilities().Values()[0].Valid() {
		t.Fatal("mutating candidate-set output changed profile")
	}
}

func TestCapabilityProfileRejectsMissingDuplicateAndUnknownFacts(t *testing.T) {
	t.Parallel()

	id, _ := environment.NewProfileID("claude-v1")
	facts := candidateFacts(t)
	for _, candidate := range [][]environment.CandidateCapabilityFact{
		facts[:len(facts)-1],
		append(append([]environment.CandidateCapabilityFact(nil), facts[:len(facts)-1]...), facts[0]),
		append(append([]environment.CandidateCapabilityFact(nil), facts[:len(facts)-1]...), environment.CandidateCapabilityFact{}),
	} {
		_, err := environment.NewCapabilityProfile(id, candidate)
		requireCode(t, err, environment.CodeInvalidCapabilityProfile)
	}
	_, err := environment.NewCapabilityProfile(environment.ProfileID{}, facts)
	requireCode(t, err, environment.CodeInvalidCapabilityProfile)
	if (environment.CapabilityProfile{}).Valid() || (environment.CandidateCapabilitySet{}).Valid() || (environment.MutationQualification{}).Valid() {
		t.Fatal("zero profile values must be invalid")
	}
	_, marshalErr := json.Marshal(environment.CandidateCapabilitySet{})
	requireCode(t, marshalErr, environment.CodeInvalidCapabilityProfile)
}

func TestCapabilityProfileJSONPublishesNoMutationAuthority(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(validProfile(t))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"mutationQualification":"unqualified"`) || strings.Contains(text, `"qualified"`) && !strings.Contains(text, `"unqualified"`) {
		t.Fatalf("MarshalJSON() = %s", encoded)
	}
	candidates, candidateErr := json.Marshal(validProfile(t).CandidateCapabilities())
	if candidateErr != nil || !strings.Contains(string(candidates), `"native_validation"`) {
		t.Fatalf("candidate MarshalJSON() = %s, %v", candidates, candidateErr)
	}
}
