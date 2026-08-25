// Package fault defines safe, target-neutral failure categories for the core.
package fault

import (
	"fmt"
	"regexp"
)

const maxDetailLength = 128

var safeDetailPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)

type Detail interface {
	detail()
	Summary() string
}

type InvalidReason string

const (
	ReasonEmpty         InvalidReason = "empty"
	ReasonInvalidFormat InvalidReason = "invalid_format"
	ReasonOutOfRange    InvalidReason = "out_of_range"
	ReasonUnknownValue  InvalidReason = "unknown_value"
)

type InvalidDetail struct {
	field  string
	reason InvalidReason
}

func NewInvalidDetail(field string, reason InvalidReason) (InvalidDetail, error) {
	if err := validateSafe("field", field); err != nil {
		return InvalidDetail{}, err
	}
	switch reason {
	case ReasonEmpty, ReasonInvalidFormat, ReasonOutOfRange, ReasonUnknownValue:
	default:
		return InvalidDetail{}, fmt.Errorf("unknown invalid-input reason %q", reason)
	}
	return InvalidDetail{field: field, reason: reason}, nil
}
func (InvalidDetail) detail()                 {}
func (d InvalidDetail) Field() string         { return d.field }
func (d InvalidDetail) Reason() InvalidReason { return d.reason }
func (d InvalidDetail) Summary() string       { return d.field + ":" + string(d.reason) }

type UnsupportedDetail struct {
	kind       string
	value      string
	capability string
}

func NewUnsupportedDetail(kind, value, capability string) (UnsupportedDetail, error) {
	for _, candidate := range []struct {
		name  string
		value string
	}{{name: "kind", value: kind}, {name: "value", value: value}} {
		if err := validateSafe(candidate.name, candidate.value); err != nil {
			return UnsupportedDetail{}, err
		}
	}
	if capability != "" {
		if err := validateSafe("capability", capability); err != nil {
			return UnsupportedDetail{}, err
		}
	}
	return UnsupportedDetail{kind: kind, value: value, capability: capability}, nil
}
func (UnsupportedDetail) detail()              {}
func (d UnsupportedDetail) Kind() string       { return d.kind }
func (d UnsupportedDetail) Value() string      { return d.value }
func (d UnsupportedDetail) Capability() string { return d.capability }
func (d UnsupportedDetail) Summary() string {
	if d.capability == "" {
		return d.kind + ":" + d.value
	}
	return d.kind + ":" + d.value + ":" + d.capability
}

type ConflictDetail struct {
	resource string
	identity string
}

func NewConflictDetail(resource, identity string) (ConflictDetail, error) {
	if err := validateSafe("resource", resource); err != nil {
		return ConflictDetail{}, err
	}
	if err := validateSafe("identity", identity); err != nil {
		return ConflictDetail{}, err
	}
	return ConflictDetail{resource: resource, identity: identity}, nil
}
func (ConflictDetail) detail()            {}
func (d ConflictDetail) Resource() string { return d.resource }
func (d ConflictDetail) Identity() string { return d.identity }
func (d ConflictDetail) Summary() string  { return d.resource + ":" + d.identity }

type OperationDetail struct{ operation string }

func NewOperationDetail(operation string) (OperationDetail, error) {
	if err := validateSafe("operation", operation); err != nil {
		return OperationDetail{}, err
	}
	return OperationDetail{operation: operation}, nil
}
func (OperationDetail) detail()             {}
func (d OperationDetail) Operation() string { return d.operation }
func (d OperationDetail) Summary() string   { return d.operation }

func validateSafe(name, value string) error {
	if len(value) == 0 || len(value) > maxDetailLength || !safeDetailPattern.MatchString(value) {
		return fmt.Errorf("%s is not a bounded safe identifier", name)
	}
	return nil
}
