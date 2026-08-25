package qualification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrorCode is a closed, disclosure-safe qualification code.
type ErrorCode string

const (
	CodeInvalidService            ErrorCode = "claude.config.qualification.invalid_service"
	CodeInvalidContext            ErrorCode = "claude.config.qualification.invalid_context"
	CodeInvalidProof              ErrorCode = "claude.config.qualification.invalid_proof"
	CodeInvalidObservation        ErrorCode = "claude.config.qualification.invalid_observation"
	CodeDirectoryInspectionFailed ErrorCode = "claude.config.directory_inspection_failed"
	CodeCancelled                 ErrorCode = "claude.config.qualification.cancelled"
	CodeTimedOut                  ErrorCode = "claude.config.qualification.timed_out"
)

func (c ErrorCode) Valid() bool {
	switch c {
	case CodeInvalidService, CodeInvalidContext, CodeInvalidProof, CodeInvalidObservation,
		CodeDirectoryInspectionFailed, CodeCancelled, CodeTimedOut:
		return true
	default:
		return false
	}
}

// Error retains only a fixed public code and never wraps a host locator or
// adapter diagnostic.
type Error struct{ code ErrorCode }

func newError(code ErrorCode) error { return Error{code: code} }

func (e Error) Code() ErrorCode { return e.code }

func (e Error) safeCode() ErrorCode {
	if e.code.Valid() {
		return e.code
	}
	return "claude.config.qualification.invalid_error"
}

func (e Error) Error() string { return string(e.safeCode()) }

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

func (e Error) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, string(e.safeCode()))
}

func (e Error) MarshalText() ([]byte, error) { return []byte(e.safeCode()), nil }

func (e Error) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Code ErrorCode `json:"code"`
	}{Code: e.safeCode()})
}

func contextFailure(ctx context.Context, err error) error {
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
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
