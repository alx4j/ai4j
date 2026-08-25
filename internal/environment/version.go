package environment

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Tool is a closed prerequisite executable identity.
type Tool struct{ value uint8 }

var (
	gitTool    = Tool{value: 1}
	claudeTool = Tool{value: 2}
)

// GitTool returns the Git prerequisite identity.
func GitTool() Tool { return gitTool }

// ClaudeTool returns the Claude Code prerequisite identity.
func ClaudeTool() Tool { return claudeTool }

// NewTool parses a canonical prerequisite tool name.
func NewTool(value string) (Tool, error) {
	switch value {
	case "git":
		return gitTool, nil
	case "claude":
		return claudeTool, nil
	default:
		return Tool{}, newValidationError(CodeInvalidTool)
	}
}

// String returns the canonical tool name.
func (t Tool) String() string {
	switch t {
	case gitTool:
		return "git"
	case claudeTool:
		return "claude"
	default:
		return "invalid"
	}
}

// Valid reports whether the tool is registered.
func (t Tool) Valid() bool { return t == gitTool || t == claudeTool }

// MarshalText emits the canonical tool name.
func (t Tool) MarshalText() ([]byte, error) {
	if !t.Valid() {
		return nil, newValidationError(CodeInvalidTool)
	}
	return []byte(t.String()), nil
}

// SemanticVersion is an exact three-component semantic core version. It does
// not silently accept prerelease or build suffixes.
type SemanticVersion struct {
	set    bool
	values [3]uint32
}

// NewSemanticVersion parses canonical major.minor.patch text.
func NewSemanticVersion(value string) (SemanticVersion, error) {
	parts, ok := parseDecimalComponents(value, 3, 3)
	if !ok {
		return SemanticVersion{}, newValidationError(CodeInvalidSemanticVersion)
	}
	return SemanticVersion{set: true, values: [3]uint32{parts[0], parts[1], parts[2]}}, nil
}

// Major returns the major version component.
func (v SemanticVersion) Major() uint32 { return v.values[0] }

// Minor returns the minor version component.
func (v SemanticVersion) Minor() uint32 { return v.values[1] }

// Patch returns the patch version component.
func (v SemanticVersion) Patch() uint32 { return v.values[2] }

// Valid reports whether the semantic version was constructed canonically.
func (v SemanticVersion) Valid() bool {
	if !v.set {
		return false
	}
	parsed, err := NewSemanticVersion(v.String())
	return err == nil && parsed == v
}

// String returns canonical major.minor.patch text.
func (v SemanticVersion) String() string {
	if !v.set {
		return "invalid"
	}
	return strconv.FormatUint(uint64(v.values[0]), 10) + "." +
		strconv.FormatUint(uint64(v.values[1]), 10) + "." +
		strconv.FormatUint(uint64(v.values[2]), 10)
}

// MarshalText emits the canonical semantic version.
func (v SemanticVersion) MarshalText() ([]byte, error) {
	if !v.Valid() {
		return nil, newValidationError(CodeInvalidSemanticVersion)
	}
	return []byte(v.String()), nil
}

// AppleGitRevision preserves Apple's distinct Git vendor revision without
// treating it as the upstream Git semantic version.
type AppleGitRevision struct {
	components uint8
	values     [4]uint32
}

// NewAppleGitRevision parses one to four canonical decimal components.
func NewAppleGitRevision(value string) (AppleGitRevision, error) {
	parts, ok := parseDecimalComponents(value, 1, 4)
	if !ok {
		return AppleGitRevision{}, newValidationError(CodeInvalidAppleGitRevision)
	}
	revision := AppleGitRevision{components: uint8(len(parts))}
	copy(revision.values[:], parts)
	return revision, nil
}

// Valid reports whether the Apple Git vendor revision is canonical.
func (v AppleGitRevision) Valid() bool {
	if v.components < 1 || v.components > 4 {
		return false
	}
	for index := int(v.components); index < len(v.values); index++ {
		if v.values[index] != 0 {
			return false
		}
	}
	parsed, err := NewAppleGitRevision(v.String())
	return err == nil && parsed == v
}

// String returns the canonical Apple Git vendor revision.
func (v AppleGitRevision) String() string {
	if v.components < 1 || v.components > 4 {
		return "invalid"
	}
	parts := make([]string, v.components)
	for index := range parts {
		parts[index] = strconv.FormatUint(uint64(v.values[index]), 10)
	}
	return strings.Join(parts, ".")
}

// MarshalText emits the canonical Apple Git vendor revision.
func (v AppleGitRevision) MarshalText() ([]byte, error) {
	if !v.Valid() {
		return nil, newValidationError(CodeInvalidAppleGitRevision)
	}
	return []byte(v.String()), nil
}

// ToolVersionForm distinguishes a plain semantic tool version from Apple's
// vendor-qualified Git form.
type ToolVersionForm struct{ value uint8 }

var (
	semanticToolVersionForm = ToolVersionForm{value: 1}
	appleGitVersionForm     = ToolVersionForm{value: 2}
)

// SemanticToolVersionForm returns the plain semantic form.
func SemanticToolVersionForm() ToolVersionForm { return semanticToolVersionForm }

// AppleGitToolVersionForm returns the Apple vendor-qualified Git form.
func AppleGitToolVersionForm() ToolVersionForm { return appleGitVersionForm }

// String returns the canonical form name.
func (f ToolVersionForm) String() string {
	switch f {
	case semanticToolVersionForm:
		return "semantic"
	case appleGitVersionForm:
		return "apple_git"
	default:
		return "invalid"
	}
}

// Valid reports whether the form is registered.
func (f ToolVersionForm) Valid() bool {
	return f == semanticToolVersionForm || f == appleGitVersionForm
}

// ToolVersion binds an exact version form to its prerequisite tool.
type ToolVersion struct {
	tool     Tool
	semantic SemanticVersion
	form     ToolVersionForm
	apple    AppleGitRevision
}

// NewSemanticToolVersion constructs an upstream semantic Git or Claude version.
func NewSemanticToolVersion(tool Tool, version SemanticVersion) (ToolVersion, error) {
	if !tool.Valid() || !version.Valid() {
		return ToolVersion{}, newValidationError(CodeInvalidToolVersion)
	}
	return ToolVersion{tool: tool, semantic: version, form: semanticToolVersionForm}, nil
}

// NewAppleGitToolVersion constructs an Apple vendor-qualified Git version.
func NewAppleGitToolVersion(version SemanticVersion, revision AppleGitRevision) (ToolVersion, error) {
	if !version.Valid() || !revision.Valid() {
		return ToolVersion{}, newValidationError(CodeInvalidToolVersion)
	}
	return ToolVersion{tool: gitTool, semantic: version, form: appleGitVersionForm, apple: revision}, nil
}

// Tool returns the executable tool associated with the version.
func (v ToolVersion) Tool() Tool { return v.tool }

// Semantic returns the exact upstream semantic version.
func (v ToolVersion) Semantic() SemanticVersion { return v.semantic }

// Form returns the exact output/version form.
func (v ToolVersion) Form() ToolVersionForm { return v.form }

// AppleRevision returns the Apple Git revision when the version uses that form.
func (v ToolVersion) AppleRevision() (AppleGitRevision, bool) {
	return v.apple, v.form == appleGitVersionForm && v.apple.Valid()
}

// Valid reports whether the tool and form are coherent.
func (v ToolVersion) Valid() bool {
	if !v.tool.Valid() || !v.semantic.Valid() || !v.form.Valid() {
		return false
	}
	switch v.form {
	case semanticToolVersionForm:
		return v.apple == (AppleGitRevision{})
	case appleGitVersionForm:
		return v.tool == gitTool && v.apple.Valid()
	default:
		return false
	}
}

// String preserves the exact normalized tool-version form.
func (v ToolVersion) String() string {
	if !v.Valid() {
		return "invalid"
	}
	if v.form == appleGitVersionForm {
		return v.semantic.String() + " (Apple Git-" + v.apple.String() + ")"
	}
	return v.semantic.String()
}

// Format emits only normalized non-sensitive version facts.
func (v ToolVersion) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, v.String()) }

// MarshalJSON emits only normalized non-sensitive version facts.
func (v ToolVersion) MarshalJSON() ([]byte, error) {
	if !v.Valid() {
		return nil, newValidationError(CodeInvalidToolVersion)
	}
	return json.Marshal(struct {
		Tool    string `json:"tool"`
		Version string `json:"version"`
		Form    string `json:"form"`
	}{Tool: v.tool.String(), Version: v.String(), Form: v.form.String()})
}
