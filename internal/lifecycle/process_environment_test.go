package lifecycle_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestProcessEnvironmentProfileID(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"isolated", "git_hardened", "profile9"} {
		profile, err := lifecycle.NewProcessEnvironmentProfileID(value)
		if err != nil || !profile.Valid() || profile.String() != value {
			t.Fatalf("profile %q = %q, %v", value, profile.String(), err)
		}
	}
	for _, value := range []string{"", "Upper", "-option", "two words", "with.dot", strings.Repeat("a", 65), "bad\x00value"} {
		profile, err := lifecycle.NewProcessEnvironmentProfileID(value)
		if err == nil || profile.Valid() {
			t.Fatalf("invalid profile %q accepted", value)
		}
	}
	if (lifecycle.ProcessEnvironmentProfileID{}).Valid() {
		t.Fatal("zero profile identifier accepted")
	}
}

func TestEnvironmentBindingRedactsValue(t *testing.T) {
	t.Parallel()

	const canary = "ENVIRONMENT_CANARY"
	binding := lifecycle.EnvironmentBinding{Name: "TOKEN", Value: canary}
	encoded, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	text, err := binding.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{
		fmt.Sprintf("%v", binding),
		fmt.Sprintf("%+v", binding),
		fmt.Sprintf("%#v", binding),
	} {
		if rendered != "<environment-binding:redacted>" {
			t.Fatalf("formatted environment binding = %q", rendered)
		}
	}
	if string(text) != "<environment-binding:redacted>" || string(encoded) != `{"environment":"redacted"}` {
		t.Fatalf("marshaled environment binding = text %q, JSON %s", text, encoded)
	}
	if strings.Contains(string(text), canary) || strings.Contains(string(encoded), canary) {
		t.Fatal("marshaled environment binding leaked its value")
	}
}
