package environment

import (
	"encoding/json"
	"fmt"
	"io"
)

// ErrorCode is a closed, disclosure-safe environment validation code.
type ErrorCode string

const (
	CodeInvalidOperatingSystem   ErrorCode = "environment.invalid_operating_system"
	CodeInvalidArchitecture      ErrorCode = "environment.invalid_architecture"
	CodeInvalidDarwinVersion     ErrorCode = "environment.invalid_darwin_version"
	CodeInvalidHostTuple         ErrorCode = "environment.invalid_host_tuple"
	CodeInvalidTool              ErrorCode = "environment.invalid_tool"
	CodeInvalidSemanticVersion   ErrorCode = "environment.invalid_semantic_version"
	CodeInvalidAppleGitRevision  ErrorCode = "environment.invalid_apple_git_revision"
	CodeInvalidToolVersion       ErrorCode = "environment.invalid_tool_version"
	CodeInvalidExecutable        ErrorCode = "environment.invalid_executable_identity"
	CodeInvalidDirectoryRole     ErrorCode = "environment.invalid_directory_role"
	CodeInvalidDirectorySource   ErrorCode = "environment.invalid_directory_source"
	CodeInvalidDirectoryPresence ErrorCode = "environment.invalid_directory_presence"
	CodeInvalidDirectory         ErrorCode = "environment.invalid_directory"
	CodeInvalidProfileID         ErrorCode = "environment.invalid_profile_id"
	CodeInvalidCapabilityFact    ErrorCode = "environment.invalid_capability_fact"
	CodeInvalidCapabilityProfile ErrorCode = "environment.invalid_capability_profile"
	CodeInvalidPolicyObservation ErrorCode = "environment.invalid_policy_observation"
	CodeInvalidFaultKind         ErrorCode = "environment.invalid_fault_kind"
	CodeInvalidFaultReason       ErrorCode = "environment.invalid_fault_reason"
	CodeInvalidEnvironmentFact   ErrorCode = "environment.invalid_environment_fact"
	CodeInvalidEnvironmentFault  ErrorCode = "environment.invalid_environment_fault"
	CodeInvalidObservation       ErrorCode = "environment.invalid_observation"
)

// Valid reports whether the code is part of the fixed T1 validation contract.
func (c ErrorCode) Valid() bool {
	switch c {
	case CodeInvalidOperatingSystem, CodeInvalidArchitecture, CodeInvalidDarwinVersion,
		CodeInvalidHostTuple, CodeInvalidTool, CodeInvalidSemanticVersion,
		CodeInvalidAppleGitRevision, CodeInvalidToolVersion, CodeInvalidExecutable,
		CodeInvalidDirectoryRole, CodeInvalidDirectorySource, CodeInvalidDirectoryPresence,
		CodeInvalidDirectory, CodeInvalidProfileID, CodeInvalidCapabilityFact,
		CodeInvalidCapabilityProfile, CodeInvalidPolicyObservation, CodeInvalidFaultKind,
		CodeInvalidFaultReason, CodeInvalidEnvironmentFact, CodeInvalidEnvironmentFault,
		CodeInvalidObservation:
		return true
	default:
		return false
	}
}

// ValidationError reports only a fixed safe code. It deliberately retains no
// rejected value, filesystem locator, digest, or native output.
type ValidationError struct{ code ErrorCode }

func newValidationError(code ErrorCode) error { return ValidationError{code: code} }

// Code returns the stable validation code.
func (e ValidationError) Code() ErrorCode { return e.code }

func (e ValidationError) safeCode() ErrorCode {
	if e.code.Valid() {
		return e.code
	}
	return "environment.invalid_error"
}

func (e ValidationError) Error() string { return string(e.safeCode()) }

// Format prevents formatting flags and verbs from reflecting rejected input.
func (e ValidationError) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, string(e.safeCode()))
}

// MarshalText emits only the stable safe code.
func (e ValidationError) MarshalText() ([]byte, error) {
	return []byte(e.safeCode()), nil
}

// MarshalJSON emits only the stable safe code.
func (e ValidationError) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Code ErrorCode `json:"code"`
	}{Code: e.safeCode()})
}
