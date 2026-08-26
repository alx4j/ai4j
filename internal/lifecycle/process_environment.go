package lifecycle

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

const maximumProcessEnvironmentProfileIDBytes = 64

var errInvalidProcessEnvironmentProfileID = errors.New("invalid process environment profile identifier")

// ProcessEnvironmentProfileID is a bounded selector for a predefined process
// environment. The value deliberately carries no Git or host semantics.
type ProcessEnvironmentProfileID struct{ value string }

func NewProcessEnvironmentProfileID(value string) (ProcessEnvironmentProfileID, error) {
	if value == "" || len(value) > maximumProcessEnvironmentProfileIDBytes || !utf8.ValidString(value) {
		return ProcessEnvironmentProfileID{}, errInvalidProcessEnvironmentProfileID
	}
	for index, character := range value {
		if !(character == '_' || character >= 'a' && character <= 'z' ||
			index > 0 && character >= '0' && character <= '9') {
			return ProcessEnvironmentProfileID{}, errInvalidProcessEnvironmentProfileID
		}
	}
	return ProcessEnvironmentProfileID{value: value}, nil
}

func (p ProcessEnvironmentProfileID) Valid() bool {
	validated, err := NewProcessEnvironmentProfileID(p.value)
	return err == nil && validated == p
}

func (p ProcessEnvironmentProfileID) String() string { return p.value }

// EnvironmentBinding is a process environment name and value that always
// redacts its contents when formatted or marshaled.
type EnvironmentBinding struct {
	Name  string
	Value string
}

func (b EnvironmentBinding) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<environment-binding:redacted>"))
}

func (b EnvironmentBinding) MarshalText() ([]byte, error) {
	return []byte("<environment-binding:redacted>"), nil
}

func (b EnvironmentBinding) MarshalJSON() ([]byte, error) {
	return []byte(`{"environment":"redacted"}`), nil
}
