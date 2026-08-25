package lifecycle

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maximumShebangFieldBytes = 512

// StaticExecutableProfileVersion identifies the byte-classification contract.
// Unknown versions fail closed.
type StaticExecutableProfileVersion uint8

const StaticExecutableProfileV1 StaticExecutableProfileVersion = 1

// StaticExecutableKind is format-neutral. Host adapters project native formats
// such as Mach-O or PE into the same closed native facts.
type StaticExecutableKind string

const (
	StaticExecutableNative      StaticExecutableKind = "native_binary"
	StaticExecutableScript      StaticExecutableKind = "script"
	StaticExecutableUnsupported StaticExecutableKind = "unsupported"
	StaticExecutableMalformed   StaticExecutableKind = "malformed"
	StaticExecutableTruncated   StaticExecutableKind = "truncated"
)

func (k StaticExecutableKind) Valid() bool {
	switch k {
	case StaticExecutableNative, StaticExecutableScript, StaticExecutableUnsupported,
		StaticExecutableMalformed, StaticExecutableTruncated:
		return true
	default:
		return false
	}
}

type NativeImageLayout string

const (
	NativeSingleImage NativeImageLayout = "single_image"
	NativeMultiImage  NativeImageLayout = "multi_image"
)

func (l NativeImageLayout) Valid() bool { return l == NativeSingleImage || l == NativeMultiImage }

type NativeFileRole string

const (
	NativeObject          NativeFileRole = "object"
	NativeExecutable      NativeFileRole = "executable"
	NativeFixedVMLibrary  NativeFileRole = "fixed_vm_library"
	NativeCore            NativeFileRole = "core"
	NativePreload         NativeFileRole = "preload"
	NativeSharedLibrary   NativeFileRole = "shared_library"
	NativeDynamicLinker   NativeFileRole = "dynamic_linker"
	NativeBundle          NativeFileRole = "bundle"
	NativeSharedStub      NativeFileRole = "shared_stub"
	NativeDebugSymbols    NativeFileRole = "debug_symbols"
	NativeKernelExtension NativeFileRole = "kernel_extension"
	NativeFileSet         NativeFileRole = "file_set"
)

func (r NativeFileRole) Valid() bool {
	switch r {
	case NativeObject, NativeExecutable, NativeFixedVMLibrary, NativeCore, NativePreload,
		NativeSharedLibrary, NativeDynamicLinker, NativeBundle, NativeSharedStub,
		NativeDebugSymbols, NativeKernelExtension, NativeFileSet:
		return true
	default:
		return false
	}
}

// ExecutableArchitectureSet is a closed bit set. A host parser projects any
// unregistered architecture into an unsupported observation profile.
type ExecutableArchitectureSet uint8

const (
	ExecutableARM64 ExecutableArchitectureSet = 1 << iota
	ExecutableX8664
	knownExecutableArchitectures = ExecutableARM64 | ExecutableX8664
)

func (s ExecutableArchitectureSet) Valid() bool {
	return s != 0 && s&^knownExecutableArchitectures == 0
}

func (s ExecutableArchitectureSet) Contains(value ExecutableArchitectureSet) bool {
	return value.Valid() && s.Valid() && s&value == value
}

// NativeExecutableProfile contains only generic projected facts. It never
// retains native headers, offsets, load commands, or executable bytes.
type NativeExecutableProfile struct {
	layout        NativeImageLayout
	role          NativeFileRole
	architectures ExecutableArchitectureSet
}

func NewNativeExecutableProfile(layout NativeImageLayout, role NativeFileRole, architectures ExecutableArchitectureSet) (NativeExecutableProfile, error) {
	if !layout.Valid() || !role.Valid() || !architectures.Valid() ||
		(layout == NativeSingleImage && architectures != ExecutableARM64 && architectures != ExecutableX8664) {
		return NativeExecutableProfile{}, fmt.Errorf("invalid native executable profile")
	}
	return NativeExecutableProfile{layout: layout, role: role, architectures: architectures}, nil
}

func (p NativeExecutableProfile) Valid() bool {
	candidate, err := NewNativeExecutableProfile(p.layout, p.role, p.architectures)
	return err == nil && candidate == p
}
func (p NativeExecutableProfile) Layout() NativeImageLayout                { return p.layout }
func (p NativeExecutableProfile) Role() NativeFileRole                     { return p.role }
func (p NativeExecutableProfile) Architectures() ExecutableArchitectureSet { return p.architectures }

type ShebangForm string

const (
	ShebangDirect       ShebangForm = "direct"
	ShebangEnv          ShebangForm = "env"
	ShebangEnvAmbiguous ShebangForm = "env_ambiguous"
)

func (f ShebangForm) Valid() bool {
	return f == ShebangDirect || f == ShebangEnv || f == ShebangEnvAmbiguous
}

type ShebangAmbiguity string

const (
	ShebangEnvMissingTarget  ShebangAmbiguity = "env_missing_target"
	ShebangEnvOption         ShebangAmbiguity = "env_option"
	ShebangEnvMultipleTokens ShebangAmbiguity = "env_multiple_tokens"
	ShebangEnvAssignment     ShebangAmbiguity = "env_assignment"
)

func (a ShebangAmbiguity) Valid() bool {
	return a == ShebangEnvMissingTarget || a == ShebangEnvOption || a == ShebangEnvMultipleTokens || a == ShebangEnvAssignment
}

// ShebangProfile contains kernel-meaningful structured facts. It never retains
// the source line or other script bytes.
type ShebangProfile struct {
	form          ShebangForm
	interpreter   string
	fixedArgument string
	envTarget     string
	ambiguity     ShebangAmbiguity
}

func NewDirectShebangProfile(interpreter, fixedArgument string) (ShebangProfile, error) {
	if !validHostLocator(interpreter) || fixedArgument != "" && !validShebangArgument(fixedArgument) {
		return ShebangProfile{}, fmt.Errorf("invalid direct shebang profile")
	}
	return ShebangProfile{form: ShebangDirect, interpreter: interpreter, fixedArgument: fixedArgument}, nil
}

func NewEnvShebangProfile(interpreter, target string) (ShebangProfile, error) {
	if !validHostLocator(interpreter) || !validShebangToken(target) || target[0] == '-' || strings.Contains(target, "=") {
		return ShebangProfile{}, fmt.Errorf("invalid env shebang profile")
	}
	return ShebangProfile{form: ShebangEnv, interpreter: interpreter, envTarget: target}, nil
}

func NewAmbiguousEnvShebangProfile(interpreter string, ambiguity ShebangAmbiguity) (ShebangProfile, error) {
	if !validHostLocator(interpreter) || !ambiguity.Valid() {
		return ShebangProfile{}, fmt.Errorf("invalid ambiguous env shebang profile")
	}
	return ShebangProfile{form: ShebangEnvAmbiguous, interpreter: interpreter, ambiguity: ambiguity}, nil
}

func (p ShebangProfile) Valid() bool {
	switch p.form {
	case ShebangDirect:
		candidate, err := NewDirectShebangProfile(p.interpreter, p.fixedArgument)
		return err == nil && candidate == p
	case ShebangEnv:
		candidate, err := NewEnvShebangProfile(p.interpreter, p.envTarget)
		return err == nil && candidate == p
	case ShebangEnvAmbiguous:
		candidate, err := NewAmbiguousEnvShebangProfile(p.interpreter, p.ambiguity)
		return err == nil && candidate == p
	default:
		return false
	}
}

func (p ShebangProfile) Form() ShebangForm           { return p.form }
func (p ShebangProfile) Interpreter() string         { return p.interpreter }
func (p ShebangProfile) FixedArgument() string       { return p.fixedArgument }
func (p ShebangProfile) EnvTarget() string           { return p.envTarget }
func (p ShebangProfile) Ambiguity() ShebangAmbiguity { return p.ambiguity }
func (p ShebangProfile) Runnable() bool              { return p.form == ShebangDirect || p.form == ShebangEnv }

func (p ShebangProfile) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<shebang-profile:redacted>")
}
func (p ShebangProfile) MarshalText() ([]byte, error) {
	return []byte("<shebang-profile:redacted>"), nil
}
func (p ShebangProfile) MarshalJSON() ([]byte, error) {
	return []byte(`{"form":"` + string(p.form) + `"}`), nil
}

func validShebangToken(value string) bool    { return validASCIIField(value, false) }
func validShebangArgument(value string) bool { return validASCIIField(value, false) }

func validASCIIField(value string, allowSpace bool) bool {
	if value == "" || len(value) > maximumShebangFieldBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			if !(allowSpace && character == ' ') {
				return false
			}
		}
	}
	return true
}

type StaticExecutableIssue string

const (
	StaticIssueUnsupportedFormat       StaticExecutableIssue = "unsupported_format"
	StaticIssueUnsupportedArchitecture StaticExecutableIssue = "unsupported_architecture"
	StaticIssueUnsupportedFileRole     StaticExecutableIssue = "unsupported_file_role"
	StaticIssueMalformedHeader         StaticExecutableIssue = "malformed_header"
	StaticIssueMalformedShebang        StaticExecutableIssue = "malformed_shebang"
	StaticIssueTruncatedHeader         StaticExecutableIssue = "truncated_header"
	StaticIssueTruncatedShebang        StaticExecutableIssue = "truncated_shebang"
	StaticIssueTooManyArchitectures    StaticExecutableIssue = "too_many_architectures"
	StaticIssueInconsistentFileRole    StaticExecutableIssue = "inconsistent_file_role"
)

func (i StaticExecutableIssue) Valid() bool {
	switch i {
	case StaticIssueUnsupportedFormat, StaticIssueUnsupportedArchitecture, StaticIssueUnsupportedFileRole,
		StaticIssueMalformedHeader, StaticIssueMalformedShebang, StaticIssueTruncatedHeader,
		StaticIssueTruncatedShebang, StaticIssueTooManyArchitectures, StaticIssueInconsistentFileRole:
		return true
	default:
		return false
	}
}

// StaticExecutableProfile is a closed, comparable classification of immutable
// executable bytes. Private fields prevent internally inconsistent values.
type StaticExecutableProfile struct {
	version StaticExecutableProfileVersion
	kind    StaticExecutableKind
	native  NativeExecutableProfile
	shebang ShebangProfile
	issue   StaticExecutableIssue
}

func NewNativeStaticExecutableProfile(profile NativeExecutableProfile) (StaticExecutableProfile, error) {
	if !profile.Valid() {
		return StaticExecutableProfile{}, fmt.Errorf("invalid native executable profile")
	}
	return StaticExecutableProfile{version: StaticExecutableProfileV1, kind: StaticExecutableNative, native: profile}, nil
}

func NewScriptStaticExecutableProfile(profile ShebangProfile) (StaticExecutableProfile, error) {
	if !profile.Valid() {
		return StaticExecutableProfile{}, fmt.Errorf("invalid shebang profile")
	}
	return StaticExecutableProfile{version: StaticExecutableProfileV1, kind: StaticExecutableScript, shebang: profile}, nil
}

func NewStaticExecutableIssueProfile(kind StaticExecutableKind, issue StaticExecutableIssue) (StaticExecutableProfile, error) {
	if !coherentStaticIssue(kind, issue) {
		return StaticExecutableProfile{}, fmt.Errorf("invalid executable issue profile")
	}
	return StaticExecutableProfile{version: StaticExecutableProfileV1, kind: kind, issue: issue}, nil
}

func coherentStaticIssue(kind StaticExecutableKind, issue StaticExecutableIssue) bool {
	switch kind {
	case StaticExecutableUnsupported:
		return issue == StaticIssueUnsupportedFormat || issue == StaticIssueUnsupportedArchitecture || issue == StaticIssueUnsupportedFileRole
	case StaticExecutableMalformed:
		return issue == StaticIssueMalformedHeader || issue == StaticIssueMalformedShebang ||
			issue == StaticIssueTooManyArchitectures || issue == StaticIssueInconsistentFileRole
	case StaticExecutableTruncated:
		return issue == StaticIssueTruncatedHeader || issue == StaticIssueTruncatedShebang
	default:
		return false
	}
}

func (p StaticExecutableProfile) Valid() bool {
	if p.version != StaticExecutableProfileV1 || !p.kind.Valid() {
		return false
	}
	switch p.kind {
	case StaticExecutableNative:
		return p.native.Valid() && p.shebang == (ShebangProfile{}) && p.issue == ""
	case StaticExecutableScript:
		return p.shebang.Valid() && p.native == (NativeExecutableProfile{}) && p.issue == ""
	case StaticExecutableUnsupported, StaticExecutableMalformed, StaticExecutableTruncated:
		return coherentStaticIssue(p.kind, p.issue) && p.native == (NativeExecutableProfile{}) && p.shebang == (ShebangProfile{})
	default:
		return false
	}
}

func (p StaticExecutableProfile) Version() StaticExecutableProfileVersion { return p.version }
func (p StaticExecutableProfile) Kind() StaticExecutableKind              { return p.kind }
func (p StaticExecutableProfile) Native() (NativeExecutableProfile, bool) {
	return p.native, p.kind == StaticExecutableNative && p.native.Valid()
}
func (p StaticExecutableProfile) Shebang() (ShebangProfile, bool) {
	return p.shebang, p.kind == StaticExecutableScript && p.shebang.Valid()
}
func (p StaticExecutableProfile) Issue() (StaticExecutableIssue, bool) {
	return p.issue, p.issue.Valid()
}

// ExecutionEligible rejects observation-only issue profiles. Host adapters
// separately apply architecture and native-format policy.
func (p StaticExecutableProfile) ExecutionEligible() bool {
	switch p.kind {
	case StaticExecutableNative:
		return p.native.Valid() && p.native.role == NativeExecutable
	case StaticExecutableScript:
		return p.shebang.Valid() && p.shebang.Runnable()
	default:
		return false
	}
}

// Format emits metadata only and excludes interpreter, argument, bytes,
// offsets, digests, and content-derived fragments.
func (p StaticExecutableProfile) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<static-executable-profile:")
	if p.Valid() {
		_, _ = io.WriteString(state, string(p.kind))
	} else {
		_, _ = io.WriteString(state, "invalid")
	}
	_, _ = io.WriteString(state, ">")
}
func (p StaticExecutableProfile) MarshalText() ([]byte, error) {
	return []byte(fmt.Sprintf("%v", p)), nil
}
func (p StaticExecutableProfile) MarshalJSON() ([]byte, error) {
	kind := "invalid"
	if p.Valid() {
		kind = string(p.kind)
	}
	return []byte(`{"version":1,"kind":"` + kind + `"}`), nil
}

// InterpreterBinding ties the exact classified script requirement and exact
// candidate given to CheckExecutable to its resolved executable expectation.
type InterpreterBinding struct {
	Requirement  ShebangProfile
	Candidate    string
	ResolvedPath string
	Executable   ExecutableExpectation
}

func (b InterpreterBinding) Empty() bool { return b == (InterpreterBinding{}) }

func (b InterpreterBinding) Valid() bool {
	return b.Requirement.Valid() && b.Requirement.Runnable() && validHostLocator(b.Candidate) &&
		validHostLocator(b.ResolvedPath) && b.Executable.Valid() &&
		b.Executable.Profile.kind == StaticExecutableNative && b.Executable.Profile.native.role == NativeExecutable
}

func (b InterpreterBinding) Matches(profile StaticExecutableProfile) bool {
	if !b.Valid() || profile.kind != StaticExecutableScript || profile.shebang != b.Requirement {
		return false
	}
	switch b.Requirement.form {
	case ShebangDirect:
		return b.Candidate == b.Requirement.interpreter
	case ShebangEnv:
		return b.Candidate == b.Requirement.envTarget
	default:
		return false
	}
}

func validHostLocator(value string) bool {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}
