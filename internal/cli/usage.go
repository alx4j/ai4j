package cli

import (
	"fmt"

	"github.com/alx4j/ai4j/internal/fault"
)

// UsageIssue is a stable, typed reason that argv did not match the CLI grammar.
type UsageIssue string

const (
	UsageMissingExecutable     UsageIssue = "missing_executable"
	UsageAlternateExecutable   UsageIssue = "alternate_executable"
	UsageMissingCommand        UsageIssue = "missing_command"
	UsageUnknownCommand        UsageIssue = "unknown_command"
	UsageUnexpectedArgument    UsageIssue = "unexpected_argument"
	UsageUnknownOption         UsageIssue = "unknown_option"
	UsageMisplacedOption       UsageIssue = "misplaced_option"
	UsageInapplicableOption    UsageIssue = "inapplicable_option"
	UsageDuplicateOption       UsageIssue = "duplicate_option"
	UsageMissingOptionValue    UsageIssue = "missing_option_value"
	UsageEmptyOptionValue      UsageIssue = "empty_option_value"
	UsageUnexpectedOptionValue UsageIssue = "unexpected_option_value"
	UsageInvalidOptionValue    UsageIssue = "invalid_option_value"
)

// UsageError wraps the target-neutral invalid-input fault while retaining the
// CLI-specific reason needed by later presentation code. It never retains an
// arbitrary argument value.
type UsageError struct {
	issue   UsageIssue
	command Command
	option  string
	json    bool
	fault   *fault.Error
}

func (e *UsageError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := "invalid CLI usage: " + string(e.issue)
	if e.command.Valid() {
		message += " for " + e.command.String()
	}
	if e.option != "" {
		message += " (--" + e.option + ")"
	}
	return message
}

func (e *UsageError) Unwrap() error       { return e.fault }
func (e *UsageError) Issue() UsageIssue   { return e.issue }
func (e *UsageError) Command() Command    { return e.command }
func (e *UsageError) Option() string      { return e.option }
func (e *UsageError) JSONRequested() bool { return e.json }

func newUsageError(
	issue UsageIssue,
	command Command,
	option string,
	jsonRequested bool,
	field string,
	reason fault.InvalidReason,
	cause error,
) *UsageError {
	detail, err := fault.NewInvalidDetail(field, reason)
	if err != nil {
		panic(fmt.Sprintf("invalid CLI fault detail: %v", err))
	}
	return &UsageError{
		issue:   issue,
		command: command,
		option:  option,
		json:    jsonRequested,
		fault:   fault.MustNew(fault.InvalidInput, detail, cause),
	}
}
