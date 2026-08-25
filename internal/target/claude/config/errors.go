package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrorCode is a closed, disclosure-safe configuration-resolution code.
type ErrorCode string

const (
	CodeInvalidStartupInput       ErrorCode = "claude.config.invalid_startup_input"
	CodeInvalidTrustedHome        ErrorCode = "claude.config.invalid_trusted_home"
	CodeInvalidOverridePolicy     ErrorCode = "claude.config.invalid_override_policy"
	CodeInvalidDirectoryCandidate ErrorCode = "claude.config.invalid_directory_candidate"
	CodeInvalidContext            ErrorCode = "claude.config.invalid_context"
	CodeInvalidObservation        ErrorCode = "claude.config.invalid_observation"
	CodeCancelled                 ErrorCode = "claude.config.cancelled"
	CodeTimedOut                  ErrorCode = "claude.config.timed_out"
)

// Valid reports whether the code belongs to the fixed public schema.
func (c ErrorCode) Valid() bool {
	switch c {
	case CodeInvalidStartupInput, CodeInvalidTrustedHome, CodeInvalidOverridePolicy,
		CodeInvalidDirectoryCandidate, CodeInvalidContext, CodeInvalidObservation,
		CodeCancelled, CodeTimedOut:
		return true
	default:
		return false
	}
}

// Error retains only a fixed code. It cannot carry a rejected environment
// value, filesystem path, object identity, or adapter error.
type Error struct{ code ErrorCode }

func newError(code ErrorCode) error { return Error{code: code} }

// Code returns the stable public error code.
func (e Error) Code() ErrorCode { return e.code }

func (e Error) safeCode() ErrorCode {
	if e.code.Valid() {
		return e.code
	}
	return "claude.config.invalid_error"
}

func (e Error) Error() string { return string(e.safeCode()) }

// Is preserves only standard cancellation categories.
func (e Error) Is(target error) bool {
	switch e.code {
	case CodeCancelled:
		return target == context.Canceled
	case CodeTimedOut:
		return target == context.DeadlineExceeded
	default:
		return false
	}
}

// Format emits only the fixed safe code for every formatting verb and flag.
func (e Error) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, string(e.safeCode()))
}

// MarshalText emits only the fixed safe code.
func (e Error) MarshalText() ([]byte, error) { return []byte(e.safeCode()), nil }

// MarshalJSON emits the fixed error schema.
func (e Error) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Code ErrorCode `json:"code"`
	}{Code: e.safeCode()})
}

func contextFailure(ctx context.Context, err error) error {
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return newError(CodeCancelled)
	case errors.Is(err, context.DeadlineExceeded):
		return newError(CodeTimedOut)
	default:
		return nil
	}
}
