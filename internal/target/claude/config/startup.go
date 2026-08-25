package config

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// StartupValueState preserves absence separately from an explicitly empty
// environment value without disclosing the captured value.
type StartupValueState struct{ value uint8 }

var (
	absentStartupValue        = StartupValueState{value: 1}
	explicitEmptyStartupValue = StartupValueState{value: 2}
	presentStartupValue       = StartupValueState{value: 3}
)

// AbsentStartupValue returns the environment-variable-absent state.
func AbsentStartupValue() StartupValueState { return absentStartupValue }

// ExplicitEmptyStartupValue returns the explicitly present empty state.
func ExplicitEmptyStartupValue() StartupValueState { return explicitEmptyStartupValue }

// PresentStartupValue returns the non-empty present state.
func PresentStartupValue() StartupValueState { return presentStartupValue }

// NewStartupValueState parses a canonical startup-value state.
func NewStartupValueState(value string) (StartupValueState, error) {
	switch value {
	case "absent":
		return absentStartupValue, nil
	case "explicit_empty":
		return explicitEmptyStartupValue, nil
	case "present":
		return presentStartupValue, nil
	default:
		return StartupValueState{}, newError(CodeInvalidStartupInput)
	}
}

// String returns the canonical state name.
func (s StartupValueState) String() string {
	switch s {
	case absentStartupValue:
		return "absent"
	case explicitEmptyStartupValue:
		return "explicit_empty"
	case presentStartupValue:
		return "present"
	default:
		return "invalid"
	}
}

// Valid reports whether the state is registered.
func (s StartupValueState) Valid() bool {
	return s == absentStartupValue || s == explicitEmptyStartupValue || s == presentStartupValue
}

// StartupInput is one immutable startup snapshot of only HOME and
// CLAUDE_CONFIG_DIR. It intentionally provides no generic environment lookup.
type StartupInput struct {
	homeState     StartupValueState
	home          string
	overrideState StartupValueState
	override      string
}

// NewStartupInput constructs the dedicated startup snapshot. Empty present
// values remain valid observations so resolution can return their typed fault.
func NewStartupInput(home string, homePresent bool, override string, overridePresent bool) (StartupInput, error) {
	homeState, ok := startupState(home, homePresent)
	if !ok {
		return StartupInput{}, newError(CodeInvalidStartupInput)
	}
	overrideState, ok := startupState(override, overridePresent)
	if !ok {
		return StartupInput{}, newError(CodeInvalidStartupInput)
	}
	result := StartupInput{
		homeState:     homeState,
		home:          strings.Clone(home),
		overrideState: overrideState,
		override:      strings.Clone(override),
	}
	if !result.Valid() {
		return StartupInput{}, newError(CodeInvalidStartupInput)
	}
	return result, nil
}

func startupState(value string, present bool) (StartupValueState, bool) {
	if !present {
		return absentStartupValue, value == ""
	}
	if !validCapturedValue(value) {
		return StartupValueState{}, false
	}
	if value == "" {
		return explicitEmptyStartupValue, true
	}
	return presentStartupValue, true
}

// HomeState returns only the safe HOME presence state.
func (i StartupInput) HomeState() StartupValueState { return i.homeState }

// OverrideState returns only the safe CLAUDE_CONFIG_DIR presence state.
func (i StartupInput) OverrideState() StartupValueState { return i.overrideState }

// Valid reports whether the captured state and retained values are coherent.
func (i StartupInput) Valid() bool {
	homeState, homeOK := startupState(i.home, i.homeState != absentStartupValue)
	overrideState, overrideOK := startupState(i.override, i.overrideState != absentStartupValue)
	return homeOK && overrideOK && homeState == i.homeState && overrideState == i.overrideState
}

func (i StartupInput) homeValue() string     { return i.home }
func (i StartupInput) overrideValue() string { return i.override }

// Format redacts both retained values.
func (i StartupInput) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<claude-startup-input:home="+i.homeState.String()+":override="+i.overrideState.String()+":redacted>")
}

// MarshalText emits only presence states.
func (i StartupInput) MarshalText() ([]byte, error) {
	if !i.Valid() {
		return nil, newError(CodeInvalidStartupInput)
	}
	return []byte(fmt.Sprintf("%v", i)), nil
}

// MarshalJSON emits only presence states.
func (i StartupInput) MarshalJSON() ([]byte, error) {
	if !i.Valid() {
		return nil, newError(CodeInvalidStartupInput)
	}
	return json.Marshal(struct {
		Home     string `json:"home"`
		Override string `json:"claude_config_dir"`
	}{Home: i.homeState.String(), Override: i.overrideState.String()})
}
