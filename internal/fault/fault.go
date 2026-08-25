package fault

import (
	"errors"
	"fmt"
)

type Category string

const (
	InvalidInput          Category = "invalid_input"
	UnsupportedCapability Category = "unsupported_capability"
	Conflict              Category = "conflict"
	Cancelled             Category = "cancelled"
	Timeout               Category = "timeout"
	Internal              Category = "internal"
)

var (
	ErrInvalidInput          = categoryError(InvalidInput)
	ErrUnsupportedCapability = categoryError(UnsupportedCapability)
	ErrConflict              = categoryError(Conflict)
	ErrCancelled             = categoryError(Cancelled)
	ErrTimeout               = categoryError(Timeout)
	ErrInternal              = categoryError(Internal)
)

type categoryError Category

func (e categoryError) Error() string { return string(e) }

type Error struct {
	category Category
	detail   Detail
	cause    error
}

func New(category Category, detail Detail, cause error) (*Error, error) {
	if !validCategory(category) {
		return nil, fmt.Errorf("unknown fault category %q", category)
	}
	if detail == nil {
		return nil, errors.New("fault detail is required")
	}
	if !detailMatches(category, detail) {
		return nil, fmt.Errorf("fault detail %T does not match category %q", detail, category)
	}
	return &Error{category: category, detail: detail, cause: cause}, nil
}

func detailMatches(category Category, detail Detail) bool {
	switch category {
	case InvalidInput:
		_, ok := detail.(InvalidDetail)
		return ok
	case UnsupportedCapability:
		_, ok := detail.(UnsupportedDetail)
		return ok
	case Conflict:
		_, ok := detail.(ConflictDetail)
		return ok
	case Cancelled, Timeout, Internal:
		_, ok := detail.(OperationDetail)
		return ok
	default:
		return false
	}
}

func MustNew(category Category, detail Detail, cause error) *Error {
	fault, err := New(category, detail, cause)
	if err != nil {
		panic(err)
	}
	return fault
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return string(e.category) + ": " + e.detail.Summary()
}

func (e *Error) Category() Category { return e.category }
func (e *Error) Detail() Detail     { return e.detail }
func (e *Error) Unwrap() error      { return e.cause }

func (e *Error) Is(target error) bool {
	sentinel, ok := target.(categoryError)
	return ok && Category(sentinel) == e.category
}

func validCategory(category Category) bool {
	switch category {
	case InvalidInput, UnsupportedCapability, Conflict, Cancelled, Timeout, Internal:
		return true
	default:
		return false
	}
}
