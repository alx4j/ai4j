package domain

import (
	"fmt"
	"regexp"
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type OperationID struct{ value string }

func NewOperationID(value string) (OperationID, error) {
	if !identifierPattern.MatchString(value) {
		return OperationID{}, fmt.Errorf("operation ID %q is not canonical", value)
	}
	return OperationID{value: value}, nil
}
func (v OperationID) String() string { return v.value }
func (v OperationID) Valid() bool    { return identifierPattern.MatchString(v.value) }

type InstallationID struct{ value string }

func NewInstallationID(value string) (InstallationID, error) {
	if !identifierPattern.MatchString(value) {
		return InstallationID{}, fmt.Errorf("installation ID %q is not canonical", value)
	}
	return InstallationID{value: value}, nil
}
func (v InstallationID) String() string { return v.value }
func (v InstallationID) Valid() bool    { return identifierPattern.MatchString(v.value) }
