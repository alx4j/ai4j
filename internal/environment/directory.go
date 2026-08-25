package environment

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
)

// DirectoryRole is a closed documented environment-directory purpose.
type DirectoryRole struct{ value uint8 }

var (
	claudeConfigurationRole = DirectoryRole{value: 1}
	claudeRulesRole         = DirectoryRole{value: 2}
	ai4jStateRole           = DirectoryRole{value: 3}
	ai4jRecoveryRole        = DirectoryRole{value: 4}
)

// ClaudeConfigurationDirectory returns the effective Claude user configuration role.
func ClaudeConfigurationDirectory() DirectoryRole { return claudeConfigurationRole }

// ClaudeRulesDirectory returns the documented Claude user rules role.
func ClaudeRulesDirectory() DirectoryRole { return claudeRulesRole }

// AI4JStateDirectory returns the private AI4J state role.
func AI4JStateDirectory() DirectoryRole { return ai4jStateRole }

// AI4JRecoveryDirectory returns the private AI4J recovery role.
func AI4JRecoveryDirectory() DirectoryRole { return ai4jRecoveryRole }

// NewDirectoryRole parses a canonical documented directory role.
func NewDirectoryRole(value string) (DirectoryRole, error) {
	switch value {
	case "claude_configuration":
		return claudeConfigurationRole, nil
	case "claude_rules":
		return claudeRulesRole, nil
	case "ai4j_state":
		return ai4jStateRole, nil
	case "ai4j_recovery":
		return ai4jRecoveryRole, nil
	default:
		return DirectoryRole{}, newValidationError(CodeInvalidDirectoryRole)
	}
}

// String returns the canonical role name.
func (r DirectoryRole) String() string {
	switch r {
	case claudeConfigurationRole:
		return "claude_configuration"
	case claudeRulesRole:
		return "claude_rules"
	case ai4jStateRole:
		return "ai4j_state"
	case ai4jRecoveryRole:
		return "ai4j_recovery"
	default:
		return "invalid"
	}
}

// Valid reports whether the role is registered.
func (r DirectoryRole) Valid() bool {
	return r == claudeConfigurationRole || r == claudeRulesRole || r == ai4jStateRole || r == ai4jRecoveryRole
}

// DirectorySource is the closed authority that selected a directory.
type DirectorySource struct{ value uint8 }

var (
	defaultDirectorySource    = DirectorySource{value: 1}
	environmentOverrideSource = DirectorySource{value: 2}
	privateRuntimeSource      = DirectorySource{value: 3}
)

// DefaultDirectorySource returns the documented Claude default source.
func DefaultDirectorySource() DirectorySource { return defaultDirectorySource }

// EnvironmentOverrideDirectorySource returns the CLAUDE_CONFIG_DIR source.
func EnvironmentOverrideDirectorySource() DirectorySource { return environmentOverrideSource }

// PrivateRuntimeDirectorySource returns the configured AI4J private-runtime source.
func PrivateRuntimeDirectorySource() DirectorySource { return privateRuntimeSource }

// NewDirectorySource parses a canonical documented directory source.
func NewDirectorySource(value string) (DirectorySource, error) {
	switch value {
	case "default":
		return defaultDirectorySource, nil
	case "environment_override":
		return environmentOverrideSource, nil
	case "private_runtime":
		return privateRuntimeSource, nil
	default:
		return DirectorySource{}, newValidationError(CodeInvalidDirectorySource)
	}
}

// String returns the canonical source name.
func (s DirectorySource) String() string {
	switch s {
	case defaultDirectorySource:
		return "default"
	case environmentOverrideSource:
		return "environment_override"
	case privateRuntimeSource:
		return "private_runtime"
	default:
		return "invalid"
	}
}

// Valid reports whether the source is registered.
func (s DirectorySource) Valid() bool {
	return s == defaultDirectorySource || s == environmentOverrideSource || s == privateRuntimeSource
}

// DirectoryPresence distinguishes an observed directory from a safe absent leaf.
type DirectoryPresence struct{ value uint8 }

var (
	presentDirectory = DirectoryPresence{value: 1}
	absentDirectory  = DirectoryPresence{value: 2}
)

// PresentDirectory returns an observed-present directory state.
func PresentDirectory() DirectoryPresence { return presentDirectory }

// AbsentDirectory returns an observed-absent directory state.
func AbsentDirectory() DirectoryPresence { return absentDirectory }

// NewDirectoryPresence parses a canonical directory-presence observation.
func NewDirectoryPresence(value string) (DirectoryPresence, error) {
	switch value {
	case "present":
		return presentDirectory, nil
	case "absent":
		return absentDirectory, nil
	default:
		return DirectoryPresence{}, newValidationError(CodeInvalidDirectoryPresence)
	}
}

// String returns the canonical presence name.
func (p DirectoryPresence) String() string {
	switch p {
	case presentDirectory:
		return "present"
	case absentDirectory:
		return "absent"
	default:
		return "invalid"
	}
}

// Valid reports whether the presence is registered.
func (p DirectoryPresence) Valid() bool { return p == presentDirectory || p == absentDirectory }

// Directory is a canonical documented environment-directory observation.
type Directory struct {
	role     DirectoryRole
	source   DirectorySource
	presence DirectoryPresence
	path     string
}

// NewDirectory constructs a canonical absolute directory observation. Path
// validation is lexical; host ownership and no-follow proof remain host duties.
func NewDirectory(role DirectoryRole, source DirectorySource, presence DirectoryPresence, absolutePath string) (Directory, error) {
	if !role.Valid() || !source.Valid() || !presence.Valid() || !validCanonicalDirectoryPath(absolutePath) || !coherentDirectorySource(role, source) {
		return Directory{}, newValidationError(CodeInvalidDirectory)
	}
	return Directory{role: role, source: source, presence: presence, path: absolutePath}, nil
}

// Role returns the documented directory purpose.
func (d Directory) Role() DirectoryRole { return d.role }

// Source returns the authority that selected the directory.
func (d Directory) Source() DirectorySource { return d.source }

// Presence returns whether the directory existed when qualified.
func (d Directory) Presence() DirectoryPresence { return d.presence }

// AbsolutePath returns the canonical qualified locator.
func (d Directory) AbsolutePath() string { return d.path }

// Valid reports whether the directory observation is coherent.
func (d Directory) Valid() bool {
	candidate, err := NewDirectory(d.role, d.source, d.presence, d.path)
	return err == nil && candidate == d
}

func coherentDirectorySource(role DirectoryRole, source DirectorySource) bool {
	switch role {
	case claudeConfigurationRole, claudeRulesRole:
		return source == defaultDirectorySource || source == environmentOverrideSource
	case ai4jStateRole, ai4jRecoveryRole:
		return source == privateRuntimeSource
	default:
		return false
	}
}

func validCanonicalDirectoryPath(value string) bool {
	return validBoundedText(value, maximumPathBytes) && value != "/" && path.IsAbs(value) && path.Clean(value) == value
}

// Format emits directory semantics while redacting the locator.
func (d Directory) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<directory:"+d.role.String()+":"+d.source.String()+":"+d.presence.String()+":redacted>")
}

// MarshalText redacts the directory locator.
func (d Directory) MarshalText() ([]byte, error) { return []byte(fmt.Sprintf("%v", d)), nil }

// MarshalJSON redacts the directory locator while retaining safe semantic facts.
func (d Directory) MarshalJSON() ([]byte, error) {
	if !d.Valid() {
		return nil, newValidationError(CodeInvalidDirectory)
	}
	return json.Marshal(struct {
		Role     string `json:"role"`
		Source   string `json:"source"`
		Presence string `json:"presence"`
		Path     string `json:"path"`
	}{Role: d.role.String(), Source: d.source.String(), Presence: d.presence.String(), Path: "redacted"})
}
