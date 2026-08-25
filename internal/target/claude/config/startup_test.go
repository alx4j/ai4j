package config_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/alx4j/ai4j/internal/target/claude/config"
)

func TestStartupValueStatesAreClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want config.StartupValueState
	}{
		{"absent", config.AbsentStartupValue()},
		{"explicit_empty", config.ExplicitEmptyStartupValue()},
		{"present", config.PresentStartupValue()},
	}
	for _, test := range tests {
		got, err := config.NewStartupValueState(test.name)
		if err != nil || got != test.want || got.String() != test.name || !got.Valid() {
			t.Fatalf("NewStartupValueState(%q) = %v, %v", test.name, got, err)
		}
	}
	if _, err := config.NewStartupValueState("future"); err == nil {
		t.Fatal("unknown state accepted")
	}
	if (config.StartupValueState{}).Valid() || (config.StartupValueState{}).String() != "invalid" {
		t.Fatal("zero state is valid")
	}
}

func TestStartupInputPreservesAbsentAndExplicitEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		home            string
		homePresent     bool
		override        string
		overridePresent bool
		wantHome        config.StartupValueState
		wantOverride    config.StartupValueState
	}{
		{"both absent", "", false, "", false, config.AbsentStartupValue(), config.AbsentStartupValue()},
		{"home empty", "", true, "", false, config.ExplicitEmptyStartupValue(), config.AbsentStartupValue()},
		{"override empty", testHome, true, "", true, config.PresentStartupValue(), config.ExplicitEmptyStartupValue()},
		{"both present", testHome, true, overridePath, true, config.PresentStartupValue(), config.PresentStartupValue()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input, err := config.NewStartupInput(test.home, test.homePresent, test.override, test.overridePresent)
			if err != nil || !input.Valid() || input.HomeState() != test.wantHome || input.OverrideState() != test.wantOverride {
				t.Fatalf("NewStartupInput() = %v, %v", input, err)
			}
		})
	}
	if (config.StartupInput{}).Valid() {
		t.Fatal("zero input is valid")
	}
}

func TestStartupInputRejectsUnsafeOrIncoherentValues(t *testing.T) {
	t.Parallel()

	invalidUTF8 := string([]byte{0xff})
	tooLong := strings.Repeat("a", 4097)
	tests := []struct {
		home            string
		homePresent     bool
		override        string
		overridePresent bool
	}{
		{"retained-while-absent", false, "", false},
		{"", false, "retained-while-absent", false},
		{"bad\nvalue", true, "", false},
		{testHome, true, "bad\x00value", true},
		{invalidUTF8, true, "", false},
		{testHome, true, invalidUTF8, true},
		{tooLong, true, "", false},
		{testHome, true, tooLong, true},
	}
	for _, test := range tests {
		if input, err := config.NewStartupInput(test.home, test.homePresent, test.override, test.overridePresent); err == nil || input.Valid() {
			t.Fatalf("unsafe input accepted: %#v", test)
		}
	}
}

func TestStartupInputCopiesAliasedStrings(t *testing.T) {
	t.Parallel()

	homeBytes := []byte(testHome)
	overrideBytes := []byte(overridePath)
	homeAlias := unsafe.String(unsafe.SliceData(homeBytes), len(homeBytes))
	overrideAlias := unsafe.String(unsafe.SliceData(overrideBytes), len(overrideBytes))
	input := mustInput(t, homeAlias, true, overrideAlias, true)
	for index := range homeBytes {
		homeBytes[index] = 'x'
	}
	for index := range overrideBytes {
		overrideBytes[index] = 'y'
	}
	version := mustClaudeVersion(t, "2.1.211")
	observation, err := config.ResolveCandidate(
		t.Context(), input, mustHome(t, testHome), version,
		mustPolicy(t, version, config.AllowedOverrideDecision()),
	)
	if err != nil || observation.Configuration().RelativePath().String() != "work-config-canary" {
		t.Fatalf("copied input changed: %v, %v", observation, err)
	}
}

func TestStartupInputFormattingAndJSONRedactValues(t *testing.T) {
	t.Parallel()

	input := mustInput(t, testHome, true, overridePath, true)
	formatted := fmt.Sprintf("%v|%+v|%#v|%q", input, input, input, input)
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"home":"present","claude_config_dir":"present"}` {
		t.Fatalf("JSON = %s", encoded)
	}
	for _, forbidden := range []string{testHome, overridePath, "alex-private-canary", "work-config-canary"} {
		if strings.Contains(formatted, forbidden) || strings.Contains(string(encoded), forbidden) {
			t.Fatalf("startup input disclosed %q", forbidden)
		}
	}
	if _, err := json.Marshal(config.StartupInput{}); err == nil {
		t.Fatal("zero input serialized")
	}
}
