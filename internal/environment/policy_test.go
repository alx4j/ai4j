package environment_test

import (
	"encoding/json"
	"testing"

	"github.com/alx4j/ai4j/internal/environment"
)

func TestPolicyObservationIsClosedIndependentAndSerializable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  environment.PolicyObservation
	}{
		{"allowed", environment.PolicyAllowed()},
		{"policy_blocked", environment.PolicyBlocked()},
		{"unknown", environment.PolicyUnknown()},
		{"not_observable", environment.PolicyNotObservable()},
	}
	for _, test := range tests {
		got, err := environment.NewPolicyObservation(test.value)
		if err != nil || got != test.want || !got.Valid() || got.String() != test.value {
			t.Fatalf("NewPolicyObservation(%q) = %v, %v", test.value, got, err)
		}
		encoded, marshalErr := json.Marshal(got)
		if marshalErr != nil || string(encoded) != `"`+test.value+`"` {
			t.Fatalf("MarshalJSON(%q) = %s, %v", test.value, encoded, marshalErr)
		}
	}
	for _, value := range []string{"", "blocked", "Allowed", "not-observable", "allowed\n", string([]byte{0xff})} {
		_, err := environment.NewPolicyObservation(value)
		requireCode(t, err, environment.CodeInvalidPolicyObservation)
	}
	if (environment.PolicyObservation{}).Valid() {
		t.Fatal("zero policy observation must be invalid")
	}
	_, err := json.Marshal(environment.PolicyObservation{})
	requireCode(t, err, environment.CodeInvalidPolicyObservation)
}

func TestObservationRetainsOnlyExplicitPolicy(t *testing.T) {
	t.Parallel()

	base := validObservation(t)
	if base.Policy() != environment.PolicyNotObservable() {
		t.Fatalf("Policy() = %s, want not_observable", base.Policy().String())
	}
	_, err := environment.NewObservation(base.Host(), base.Executables(), base.Directories(), base.Profile(), environment.PolicyObservation{})
	requireCode(t, err, environment.CodeInvalidObservation)
	for _, policy := range []environment.PolicyObservation{
		environment.PolicyAllowed(), environment.PolicyBlocked(), environment.PolicyUnknown(), environment.PolicyNotObservable(),
	} {
		observation, constructErr := environment.NewObservation(base.Host(), base.Executables(), base.Directories(), base.Profile(), policy)
		if constructErr != nil || observation.Policy() != policy {
			t.Fatalf("explicit policy %s = %s, %v", policy.String(), observation.Policy().String(), constructErr)
		}
	}
}
