package lifecycle

import (
	"errors"
	"unicode/utf8"
)

const maximumProcessEnvironmentProfileIDBytes = 64

var errInvalidProcessEnvironmentProfileID = errors.New("invalid process environment profile identifier")

// ProcessEnvironmentProfileID is a bounded host-neutral selector for an
// immutable environment profile owned by a ProcessRunner implementation. The
// lifecycle layer deliberately does not attach Git, Claude, or host semantics
// to profile identifiers.
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
