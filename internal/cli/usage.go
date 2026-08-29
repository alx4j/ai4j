package cli

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

// UsageError retains the stable reason needed by presentation code without
// retaining arbitrary argument values.
type UsageError struct {
	issue   UsageIssue
	command Command
	option  string
	json    bool
	cause   error
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

func (e *UsageError) Unwrap() error       { return e.cause }
func (e *UsageError) Issue() UsageIssue   { return e.issue }
func (e *UsageError) Command() Command    { return e.command }
func (e *UsageError) Option() string      { return e.option }
func (e *UsageError) JSONRequested() bool { return e.json }

func newUsageError(
	issue UsageIssue,
	command Command,
	option string,
	jsonRequested bool,
	cause error,
) *UsageError {
	return &UsageError{
		issue:   issue,
		command: command,
		option:  option,
		json:    jsonRequested,
		cause:   cause,
	}
}
