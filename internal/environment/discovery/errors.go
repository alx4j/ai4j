package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrorCode is a closed, disclosure-safe discovery failure code.
type ErrorCode string

const (
	CodeInvalidContext             ErrorCode = "environment.discovery.invalid_context"
	CodeInvalidProfile             ErrorCode = "environment.discovery.invalid_profile"
	CodeInvalidService             ErrorCode = "environment.discovery.invalid_service"
	CodeInvalidObservation         ErrorCode = "environment.discovery.invalid_observation"
	CodeHostInspectionFailed       ErrorCode = "environment.discovery.host_inspection_failed"
	CodeExecutableInspectionFailed ErrorCode = "environment.discovery.executable_inspection_failed"
	CodeProbeExecutionFailed       ErrorCode = "environment.discovery.probe_execution_failed"
	CodeCancelled                  ErrorCode = "environment.discovery.cancelled"
	CodeTimedOut                   ErrorCode = "environment.discovery.timed_out"
)

// Valid reports whether the code belongs to the fixed discovery error schema.
func (c ErrorCode) Valid() bool {
	switch c {
	case CodeInvalidContext, CodeInvalidProfile, CodeInvalidService, CodeInvalidObservation,
		CodeHostInspectionFailed, CodeExecutableInspectionFailed, CodeProbeExecutionFailed,
		CodeCancelled, CodeTimedOut:
		return true
	default:
		return false
	}
}

// Error reports only a fixed code and deliberately retains no rejected value,
// native output, executable locator, digest, or underlying host error.
type Error struct{ code ErrorCode }

func newError(code ErrorCode) error { return Error{code: code} }

// Code returns the stable discovery error code.
func (e Error) Code() ErrorCode { return e.code }

func (e Error) safeCode() ErrorCode {
	if e.code.Valid() {
		return e.code
	}
	return "environment.discovery.invalid_error"
}

func (e Error) Error() string { return string(e.safeCode()) }

// Is preserves cancellation and timeout categories without wrapping a
// potentially disclosive adapter error.
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

// MarshalJSON emits the fixed discovery error schema.
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
