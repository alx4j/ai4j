package result

import (
	"fmt"
	"regexp"
	"sort"
	"unicode"
	"unicode/utf8"
)

const (
	maxDiagnosticCodeBytes    = 64
	maxDiagnosticMessageRunes = 512
	maxContextFieldBytes      = 64
	maxContextValueRunes      = 256
	maxDiagnosticContext      = 16
)

var diagnosticSymbolPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// Context is one bounded, named diagnostic fact. Values must already be
// sanitized for disclosure before construction.
type Context struct {
	field string
	value string
}

func NewContext(field, value string) (Context, error) {
	if len(field) > maxContextFieldBytes || !diagnosticSymbolPattern.MatchString(field) {
		return Context{}, fmt.Errorf("context field is not a bounded canonical identifier")
	}
	if err := validateDisplayText("context value", value, maxContextValueRunes); err != nil {
		return Context{}, err
	}
	return Context{field: field, value: value}, nil
}

func (c Context) Field() string { return c.field }
func (c Context) Value() string { return c.value }

func (c Context) valid() bool {
	if len(c.field) > maxContextFieldBytes || !diagnosticSymbolPattern.MatchString(c.field) {
		return false
	}
	return validateDisplayText("context value", c.value, maxContextValueRunes) == nil
}

// Warning is a stable, bounded non-fatal diagnostic.
type Warning struct {
	code    string
	message string
	context []Context
}

func NewWarning(code, message string, context []Context) (Warning, error) {
	diagnostic, err := newDiagnostic(code, message, context)
	if err != nil {
		return Warning{}, err
	}
	return Warning(diagnostic), nil
}

func (w Warning) Code() string       { return w.code }
func (w Warning) Message() string    { return w.message }
func (w Warning) Context() []Context { return cloneContexts(w.context) }

func (w Warning) valid() bool {
	return diagnostic{code: w.code, message: w.message, context: w.context}.valid()
}

// Problem is a stable, bounded command error. It deliberately carries no raw
// Go error or opaque child-process output.
type Problem struct {
	code    string
	message string
	context []Context
}

func NewProblem(code, message string, context []Context) (Problem, error) {
	diagnostic, err := newDiagnostic(code, message, context)
	if err != nil {
		return Problem{}, err
	}
	return Problem(diagnostic), nil
}

func (p Problem) Code() string       { return p.code }
func (p Problem) Message() string    { return p.message }
func (p Problem) Context() []Context { return cloneContexts(p.context) }

func (p Problem) valid() bool {
	return diagnostic{code: p.code, message: p.message, context: p.context}.valid()
}

type diagnostic struct {
	code    string
	message string
	context []Context
}

func newDiagnostic(code, message string, context []Context) (diagnostic, error) {
	if len(code) > maxDiagnosticCodeBytes || !diagnosticSymbolPattern.MatchString(code) {
		return diagnostic{}, fmt.Errorf("diagnostic code is not a bounded canonical identifier")
	}
	if err := validateDisplayText("diagnostic message", message, maxDiagnosticMessageRunes); err != nil {
		return diagnostic{}, err
	}
	if len(context) > maxDiagnosticContext {
		return diagnostic{}, fmt.Errorf("diagnostic context exceeds %d entries", maxDiagnosticContext)
	}

	owned := cloneContexts(context)
	for _, item := range owned {
		if !item.valid() {
			return diagnostic{}, fmt.Errorf("diagnostic context contains an invalid entry")
		}
	}
	sort.Slice(owned, func(i, j int) bool {
		if owned[i].field == owned[j].field {
			return owned[i].value < owned[j].value
		}
		return owned[i].field < owned[j].field
	})

	return diagnostic{code: code, message: message, context: owned}, nil
}

func (d diagnostic) valid() bool {
	if len(d.code) > maxDiagnosticCodeBytes || !diagnosticSymbolPattern.MatchString(d.code) {
		return false
	}
	if validateDisplayText("diagnostic message", d.message, maxDiagnosticMessageRunes) != nil {
		return false
	}
	if len(d.context) > maxDiagnosticContext {
		return false
	}
	for _, item := range d.context {
		if !item.valid() {
			return false
		}
	}
	return true
}

func validateDisplayText(name, value string, maxRunes int) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8", name)
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s exceeds %d characters", name, maxRunes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func cloneContexts(values []Context) []Context {
	return append([]Context(nil), values...)
}
