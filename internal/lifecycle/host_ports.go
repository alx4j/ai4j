package lifecycle

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/domain"
)

type HostInspectionRequest struct{ Host domain.Host }

// HostObservation contains trusted host facts collected by a host adapter.
type HostObservation struct {
	Host      domain.Host
	OS        string
	Arch      string
	OSVersion string
}

// RootRole identifies an explicitly configured filesystem authority. It is not
// a path and must be resolved by the selected host adapter.
type RootRole string

const (
	StateRoot           RootRole = "state"
	RecoveryRoot        RootRole = "recovery"
	TemporarySourceRoot RootRole = "temporary_source"
	StagingRoot         RootRole = "staging"
	ManagedOutputRoot   RootRole = "managed_output"
)

func (r RootRole) Valid() bool {
	switch r {
	case StateRoot, RecoveryRoot, TemporarySourceRoot, StagingRoot, ManagedOutputRoot:
		return true
	default:
		return false
	}
}

type ResourceKind string

const (
	RegularResource    ResourceKind = "regular"
	DirectoryResource  ResourceKind = "directory"
	ExecutableResource ResourceKind = "executable"
)

func (k ResourceKind) Valid() bool {
	switch k {
	case RegularResource, DirectoryResource, ExecutableResource:
		return true
	default:
		return false
	}
}

// ObjectIdentity is a comparable semantic identity for an opened filesystem
// object. Callers may compare it but must not interpret its numeric values.
type ObjectIdentity struct {
	Filesystem uint64
	Object     uint64
}

type OwnerClass string

const (
	CurrentUserOwner OwnerClass = "current_user"
	SystemOwner      OwnerClass = "system"
	OtherOwner       OwnerClass = "other"
)

func (o OwnerClass) TrustedExecutableOwner() bool {
	return o == CurrentUserOwner || o == SystemOwner
}

// ExecutableAuthorityClass identifies the closed ownership authority that a
// host adapter must prove for an executable and retain through launch. It is
// intentionally distinct from OwnerClass: system_owned_chain_v1 also governs
// every controlling path ancestor, not only the executable leaf.
type ExecutableAuthorityClass string

const (
	TrustedUserOrSystemAuthority ExecutableAuthorityClass = "trusted_user_or_system_v1"
	CurrentUserAuthority         ExecutableAuthorityClass = "current_user_v1"
	SystemOwnedChainAuthority    ExecutableAuthorityClass = "system_owned_chain_v1"
)

func (a ExecutableAuthorityClass) Valid() bool {
	switch a {
	case TrustedUserOrSystemAuthority, CurrentUserAuthority, SystemOwnedChainAuthority:
		return true
	default:
		return false
	}
}

func (a ExecutableAuthorityClass) accepts(owner OwnerClass) bool {
	switch a {
	case TrustedUserOrSystemAuthority:
		return owner.TrustedExecutableOwner()
	case CurrentUserAuthority:
		return owner == CurrentUserOwner
	case SystemOwnedChainAuthority:
		return owner == SystemOwner
	default:
		return false
	}
}

func (i ObjectIdentity) Valid() bool { return i.Filesystem != 0 && i.Object != 0 }

type ResourceRequest struct {
	Root                RootRole
	Path                string
	Kind                ResourceKind
	RequireCurrentOwner bool
	RejectMultipleLinks bool
}

// ResourceObservation is derived from the opened object, never from path text
// alone. A missing object is represented by Exists=false and a zero identity.
type ResourceObservation struct {
	Exists              bool
	Kind                ResourceKind
	OwnedByCurrentUser  bool
	OwnerClass          OwnerClass
	PrivilegeBearing    bool
	WritableByUntrusted bool
	ExecutableDigest    domain.ExecutableDigest
	Mode                fs.FileMode
	Size                int64
	LinkCount           uint64
	RootIdentity        ObjectIdentity
	ParentIdentity      ObjectIdentity
	Identity            ObjectIdentity
}

type ResourceReadRequest struct {
	Resource ResourceRequest
	MaxBytes int64
}

type ResourceReadResult struct {
	Observation ResourceObservation
	Content     []byte
}

type FileExpectationState string

const (
	ExpectAbsent  FileExpectationState = "absent"
	ExpectPresent FileExpectationState = "present"
)

type FileExpectation struct {
	State              FileExpectationState
	Digest             domain.RenderedDigest
	RootIdentity       ObjectIdentity
	ParentIdentity     ObjectIdentity
	Identity           ObjectIdentity
	Mode               fs.FileMode
	Size               int64
	OwnedByCurrentUser bool
}

func (e FileExpectation) Empty() bool { return e == (FileExpectation{}) }

func (e FileExpectation) Valid() bool {
	switch e.State {
	case ExpectAbsent:
		return !e.Digest.Valid() && e.RootIdentity.Valid() && e.ParentIdentity.Valid() &&
			!e.Identity.Valid() && e.Mode == 0 && e.Size == 0 && !e.OwnedByCurrentUser
	case ExpectPresent:
		return e.Digest.Valid() && e.RootIdentity.Valid() && e.ParentIdentity.Valid() &&
			e.Identity.Valid() && e.Mode.Perm() == e.Mode && e.Size >= 0 && e.OwnedByCurrentUser
	default:
		return false
	}
}

// FileContentExpectation is the journalable desired predicate known before a
// temporary inode exists. Exact Prepared identity, when available, is carried
// separately by FileArtifactInspectionRequest.
type FileContentExpectation struct {
	Digest domain.RenderedDigest
	Mode   fs.FileMode
	Size   int64
}

func (e FileContentExpectation) Valid() bool {
	return e.Digest.Valid() && e.Mode != 0 && e.Mode.Perm() == e.Mode && e.Size >= 0
}

func (e FileContentExpectation) Matches(file FileExpectation) bool {
	return e.Valid() && file.Valid() && file.State == ExpectPresent && file.OwnedByCurrentUser &&
		e.Digest == file.Digest && file.Mode&^e.Mode == 0 && e.Size == file.Size
}

type FileMutation struct {
	OperationID   domain.OperationID
	ArtifactToken domain.ArtifactToken
	Artifacts     FileArtifactPlan
	Root          RootRole
	Destination   string
	Content       []byte
	Mode          fs.FileMode
	Expected      FileExpectation
}

func (m FileMutation) Valid() bool {
	planned, ok := PlanFileArtifacts(m.OperationID, m.ArtifactToken)
	return ok && m.Artifacts == planned && m.Root.Valid() && validRootedArtifactPath(m.Destination) &&
		m.Expected.Valid() && (m.Mode == 0 || m.Mode.Perm() == m.Mode) &&
		!destinationAliasesArtifact(m.Destination, planned)
}

type FileArtifactPlan struct {
	TemporaryName  string
	QuarantineName string
}

func PlanFileArtifacts(operation domain.OperationID, token domain.ArtifactToken) (FileArtifactPlan, bool) {
	if !operation.Valid() || !token.Valid() {
		return FileArtifactPlan{}, false
	}
	base := ".ai4j-" + operation.String() + "-" + token.String()
	plan := FileArtifactPlan{TemporaryName: base + ".tmp", QuarantineName: base + ".quarantine"}
	return plan, len(plan.TemporaryName) <= 255 && len(plan.QuarantineName) <= 255
}

type CleanupDisposition string

const (
	CleanupNotRequired CleanupDisposition = "not_required"
	CleanupComplete    CleanupDisposition = "complete"
	CleanupRequired    CleanupDisposition = "required"
)

func (d CleanupDisposition) Valid() bool {
	switch d {
	case CleanupNotRequired, CleanupComplete, CleanupRequired:
		return true
	default:
		return false
	}
}

type FileMutationResult struct {
	Digest             domain.RenderedDigest
	Cleanup            CleanupDisposition
	CleanupArtifact    CleanupArtifact
	RecoveryConflict   FileRecoveryConflict
	Visibility         FileVisibility
	Durability         NamespaceDurability
	VisibleExpectation FileExpectation
}

type FileVisibility string

const (
	FileNotApplied      FileVisibility = "not_applied"
	FileAppliedVerified FileVisibility = "applied_verified"
	FileIndeterminate   FileVisibility = "indeterminate"
)

type NamespaceDurability string

const (
	NamespaceNotStarted NamespaceDurability = "not_started"
	NamespacePending    NamespaceDurability = "pending"
	NamespaceDurable    NamespaceDurability = "durable"
)

func (r FileMutationResult) Coherent() bool {
	if !r.Cleanup.Valid() {
		return false
	}
	if r.Cleanup == CleanupRequired {
		if !(r.CleanupArtifact.Valid() && r.RecoveryConflict.Empty() || r.CleanupArtifact.Empty() && r.RecoveryConflict.Valid()) {
			return false
		}
	} else if !r.CleanupArtifact.Empty() || !r.RecoveryConflict.Empty() {
		return false
	}
	switch r.Visibility {
	case FileNotApplied:
		return r.Durability == NamespaceNotStarted && !r.VisibleExpectation.Valid()
	case FileAppliedVerified:
		return r.Durability == NamespaceDurable && r.VisibleExpectation.Valid()
	case FileIndeterminate:
		return (r.Durability == NamespacePending || r.Durability == NamespaceDurable) && r.VisibleExpectation.Valid()
	default:
		return false
	}
}

type RecoveryObjectKind string

const (
	RecoveryRegularObject   RecoveryObjectKind = "regular"
	RecoveryDirectoryObject RecoveryObjectKind = "directory"
	RecoverySymlinkObject   RecoveryObjectKind = "symlink"
	RecoverySpecialObject   RecoveryObjectKind = "special"
	RecoveryMissingObject   RecoveryObjectKind = "missing"
	RecoveryUnknownObject   RecoveryObjectKind = "unknown"
)

func (k RecoveryObjectKind) Valid() bool {
	switch k {
	case RecoveryRegularObject, RecoveryDirectoryObject, RecoverySymlinkObject, RecoverySpecialObject, RecoveryMissingObject, RecoveryUnknownObject:
		return true
	default:
		return false
	}
}

type FileRecoveryConflictReason string

const (
	RecoveryPredicateMismatch FileRecoveryConflictReason = "predicate_mismatch"
	RecoveryUnsafeObject      FileRecoveryConflictReason = "unsafe_object"
	RecoveryAuthorityDetached FileRecoveryConflictReason = "authority_detached"
	RecoveryObservationFailed FileRecoveryConflictReason = "observation_failed"
)

func (r FileRecoveryConflictReason) Valid() bool {
	switch r {
	case RecoveryPredicateMismatch, RecoveryUnsafeObject, RecoveryAuthorityDetached, RecoveryObservationFailed:
		return true
	default:
		return false
	}
}

// FileRecoveryConflict identifies a deterministic operation artifact that
// must never be adopted or deleted. It contains no bytes or content digest.
type FileRecoveryConflict struct {
	Root     RootRole
	Path     string
	Reason   FileRecoveryConflictReason
	Kind     RecoveryObjectKind
	Identity ObjectIdentity
}

func (c FileRecoveryConflict) Empty() bool { return c == (FileRecoveryConflict{}) }

func (c FileRecoveryConflict) Valid() bool {
	if !c.Root.Valid() || !validRootedArtifactPath(c.Path) || !c.Reason.Valid() || !c.Kind.Valid() {
		return false
	}
	if c.Reason == RecoveryAuthorityDetached || c.Reason == RecoveryObservationFailed {
		return c.Kind == RecoveryUnknownObject && !c.Identity.Valid()
	}
	if c.Kind == RecoveryMissingObject {
		return c.Reason == RecoveryPredicateMismatch && !c.Identity.Valid()
	}
	return c.Kind != RecoveryUnknownObject && c.Identity.Valid()
}

// CleanupArtifact is a bounded rooted full-state predicate for an AI4J-owned,
// pre-journaled operation artifact. Path must name exactly the operation's
// planned temporary or quarantine entry. It may identify a live object or an
// object whose unlink is awaiting a durable parent sync. It never carries
// content and grants no authority to delete an ordinary destination path.
type CleanupArtifact struct {
	OperationID   domain.OperationID
	ArtifactToken domain.ArtifactToken
	Artifacts     FileArtifactPlan
	Root          RootRole
	Path          string
	Expected      FileExpectation
}

func (a CleanupArtifact) Empty() bool { return a == (CleanupArtifact{}) }

func (a CleanupArtifact) Valid() bool {
	planned, ok := PlanFileArtifacts(a.OperationID, a.ArtifactToken)
	if !ok || a.Artifacts != planned || !a.Root.Valid() || !a.Expected.Valid() ||
		a.Expected.State != ExpectPresent || !validRootedArtifactPath(a.Path) {
		return false
	}
	leaf := rootedPathLeaf(a.Path)
	return leaf == planned.TemporaryName || leaf == planned.QuarantineName
}

func validRootedArtifactPath(value string) bool {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

type FileCleanupResult struct {
	Cleanup          CleanupDisposition
	Artifact         CleanupArtifact
	RecoveryConflict FileRecoveryConflict
}

func (r FileCleanupResult) Coherent() bool {
	switch r.Cleanup {
	case CleanupComplete:
		return r.Artifact.Empty() && r.RecoveryConflict.Empty()
	case CleanupRequired:
		return r.Artifact.Valid() && r.RecoveryConflict.Empty() || r.Artifact.Empty() && r.RecoveryConflict.Valid()
	default:
		return false
	}
}

type FileArtifactInspectionRequest struct {
	OperationID    domain.OperationID
	ArtifactToken  domain.ArtifactToken
	Artifacts      FileArtifactPlan
	Root           RootRole
	Destination    string
	RootIdentity   ObjectIdentity
	ParentIdentity ObjectIdentity
	Preimage       FileExpectation
	Desired        FileContentExpectation
	Prepared       FileExpectation
}

func (r FileArtifactInspectionRequest) Valid() bool {
	planned, ok := PlanFileArtifacts(r.OperationID, r.ArtifactToken)
	if !ok || planned != r.Artifacts || !r.Root.Valid() || !validRootedArtifactPath(r.Destination) ||
		destinationAliasesArtifact(r.Destination, planned) ||
		!r.RootIdentity.Valid() || !r.ParentIdentity.Valid() || !r.Preimage.Valid() || !r.Desired.Valid() ||
		r.Preimage.RootIdentity != r.RootIdentity || r.Preimage.ParentIdentity != r.ParentIdentity {
		return false
	}
	if r.Prepared.Empty() {
		return true
	}
	return r.Prepared.Valid() && r.Prepared.State == ExpectPresent && r.Prepared.RootIdentity == r.RootIdentity &&
		r.Prepared.ParentIdentity == r.ParentIdentity && r.Desired.Matches(r.Prepared)
}

func destinationAliasesArtifact(destination string, plan FileArtifactPlan) bool {
	leaf := strings.ToLower(rootedPathLeaf(destination))
	temporary := strings.ToLower(plan.TemporaryName)
	quarantine := strings.ToLower(plan.QuarantineName)
	base := strings.TrimSuffix(temporary, ".tmp")
	return strings.HasPrefix(leaf, ".ai4j-") || leaf == temporary || leaf == quarantine || base != "" && strings.HasPrefix(leaf, base)
}

func rootedPathLeaf(value string) string {
	if separator := strings.LastIndexByte(value, '/'); separator >= 0 {
		return value[separator+1:]
	}
	return value
}

type FileArtifactInspectionResult struct {
	Artifacts []CleanupArtifact
	Conflicts []FileRecoveryConflict
}

func (r FileArtifactInspectionResult) Coherent() bool {
	if len(r.Artifacts) > 2 || len(r.Conflicts) > 3 {
		return false
	}
	seen := make(map[string]struct{}, len(r.Artifacts)+len(r.Conflicts))
	key := func(root RootRole, path string) string { return string(root) + "\x00" + path }
	for _, artifact := range r.Artifacts {
		if !artifact.Valid() {
			return false
		}
		identity := key(artifact.Root, artifact.Path)
		if _, duplicate := seen[identity]; duplicate {
			return false
		}
		seen[identity] = struct{}{}
	}
	for _, conflict := range r.Conflicts {
		if !conflict.Valid() {
			return false
		}
		identity := key(conflict.Root, conflict.Path)
		if _, duplicate := seen[identity]; duplicate {
			return false
		}
		seen[identity] = struct{}{}
	}
	return true
}

type ProcessRequest struct {
	Executable            string
	Arguments             []string
	WorkingDirectory      DirectoryExpectation
	EnvironmentProfile    ProcessEnvironmentProfileID
	Environment           []EnvironmentBinding
	ExecutableEnvironment []ExecutableEnvironmentBinding
	Timeout               time.Duration
	OutputLimitBytes      int64
	StdoutMode            ProcessOutputMode
	StderrMode            ProcessOutputMode
	TerminationGrace      time.Duration
	ExpectedExecutable    ExecutableExpectation
	Interpreter           InterpreterBinding
}

const (
	maximumProcessArguments              = 256
	maximumProcessEnvironmentBindings    = 128
	maximumExecutableEnvironmentBindings = 8
	maximumProcessFieldBytes             = 16 << 10
	maximumProcessEnvironmentBytes       = 64 << 10
	maximumProcessInputBytes             = 256 << 10
	maximumProcessOutputBytes            = 16 << 20
	maximumProcessTimeout                = time.Hour
	maximumTerminationGrace              = 30 * time.Second
)

type ExecutableExpectation struct {
	Identity            ObjectIdentity
	Authority           ExecutableAuthorityClass
	OwnerClass          OwnerClass
	Mode                fs.FileMode
	PrivilegeBearing    bool
	WritableByUntrusted bool
	Digest              domain.ExecutableDigest
	Profile             StaticExecutableProfile
}

func (e ExecutableExpectation) Valid() bool {
	return e.Identity.Valid() && e.Authority.Valid() && e.Authority.accepts(e.OwnerClass) &&
		!e.PrivilegeBearing && !e.WritableByUntrusted && e.Digest.Valid() && e.Profile.ExecutionEligible()
}

func (r ProcessRequest) Valid() bool {
	if !validHostLocator(r.Executable) || !(r.WorkingDirectory.Empty() || r.WorkingDirectory.Valid()) ||
		!r.EnvironmentProfile.Valid() || !r.ExpectedExecutable.Valid() ||
		r.Timeout <= 0 || r.Timeout > maximumProcessTimeout || r.OutputLimitBytes <= 0 ||
		r.OutputLimitBytes > maximumProcessOutputBytes || r.TerminationGrace <= 0 ||
		r.TerminationGrace > maximumTerminationGrace || r.TerminationGrace > r.Timeout ||
		r.Environment == nil || len(r.Arguments) > maximumProcessArguments ||
		len(r.Environment) > maximumProcessEnvironmentBindings ||
		len(r.ExecutableEnvironment) > maximumExecutableEnvironmentBindings {
		return false
	}
	if _, ok := NormalizeProcessOutputMode(r.StdoutMode); !ok {
		return false
	}
	if _, ok := NormalizeProcessOutputMode(r.StderrMode); !ok {
		return false
	}
	total := len(r.Executable)
	for _, argument := range r.Arguments {
		if !validProcessField(argument, true, maximumProcessFieldBytes) || total > maximumProcessInputBytes-len(argument) {
			return false
		}
		total += len(argument)
	}
	seenEnvironment := make(map[string]struct{}, len(r.Environment))
	for _, binding := range r.Environment {
		if !validEnvironmentName(binding.Name) || unsafeProcessEnvironmentName(binding.Name) ||
			!validProcessField(binding.Value, true, maximumProcessEnvironmentBytes) {
			return false
		}
		if _, duplicate := seenEnvironment[binding.Name]; duplicate {
			return false
		}
		seenEnvironment[binding.Name] = struct{}{}
		addition := len(binding.Name) + len(binding.Value)
		if total > maximumProcessInputBytes-addition {
			return false
		}
		total += addition
	}
	for _, binding := range r.ExecutableEnvironment {
		if !binding.Valid() {
			return false
		}
		if _, duplicate := seenEnvironment[binding.Name]; duplicate {
			return false
		}
		seenEnvironment[binding.Name] = struct{}{}
		addition := len(binding.Name) + len(binding.ResolvedPath)
		if total > maximumProcessInputBytes-addition {
			return false
		}
		total += addition
	}
	switch r.ExpectedExecutable.Profile.Kind() {
	case StaticExecutableNative:
		return r.Interpreter.Empty()
	case StaticExecutableScript:
		return r.Interpreter.Matches(r.ExpectedExecutable.Profile)
	default:
		return false
	}
}

func unsafeProcessEnvironmentName(value string) bool {
	for _, prefix := range []string{"DYLD_", "LD_", "GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	switch value {
	case "BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "CDPATH", "GLOBIGNORE", "PROMPT_COMMAND", "PS4", "ZDOTDIR",
		"NODE_OPTIONS", "NODE_PATH", "PERL5OPT", "RUBYOPT", "PYTHONINSPECT", "PYTHONSTARTUP",
		"GIT_CONFIG_COUNT", "GIT_EXEC_PATH", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_PROXY_COMMAND", "GIT_ASKPASS", "SSH_ASKPASS":
		return true
	default:
		return false
	}
}

func validProcessField(value string, allowEmpty bool, maximum int) bool {
	if (!allowEmpty && value == "") || len(value) > maximum || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validEnvironmentName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if !(character == '_' || character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' || index > 0 && character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func (r ProcessRequest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<process-request:redacted>"))
}
func (r ProcessRequest) MarshalText() ([]byte, error) {
	return []byte("<process-request:redacted>"), nil
}
func (r ProcessRequest) MarshalJSON() ([]byte, error) { return []byte(`{"request":"redacted"}`), nil }

type ProcessOutputMode string

const (
	SanitizedTextOutput ProcessOutputMode = "sanitized_text"
	OpaqueBytesOutput   ProcessOutputMode = "opaque_bytes"
)

func NormalizeProcessOutputMode(mode ProcessOutputMode) (ProcessOutputMode, bool) {
	switch mode {
	case "", SanitizedTextOutput:
		return SanitizedTextOutput, true
	case OpaqueBytesOutput:
		return OpaqueBytesOutput, true
	default:
		return "", false
	}
}

// ProcessStream is a closed display-safety union. SanitizedText succeeds only
// for display-safe text; OpaqueBytes succeeds only for exact protocol bytes.
// Accessors return copies where mutation would otherwise be possible.
type ProcessStream struct {
	mode      ProcessOutputMode
	data      []byte
	truncated bool
}

const redactedProcessStream = "<process-stream:redacted>"

// Format prevents fmt, including formatting of an enclosing ProcessResult,
// from reflecting private protocol bytes. It intentionally omits content,
// length, hashes, and truncation facts for every verb and flag combination.
func (s ProcessStream) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte(redactedProcessStream))
}

// MarshalText keeps generic text encoders non-data-bearing. Callers that are
// explicitly authorized to consume a machine protocol must use OpaqueBytes.
func (s ProcessStream) MarshalText() ([]byte, error) {
	return []byte(redactedProcessStream), nil
}

func NewProcessStream(mode ProcessOutputMode, data []byte, truncated bool) (ProcessStream, bool) {
	normalized, ok := NormalizeProcessOutputMode(mode)
	if !ok {
		return ProcessStream{}, false
	}
	if normalized == SanitizedTextOutput {
		if !utf8.Valid(data) {
			return ProcessStream{}, false
		}
		for _, character := range string(data) {
			if unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t' {
				return ProcessStream{}, false
			}
		}
	}
	return ProcessStream{mode: normalized, data: append([]byte(nil), data...), truncated: truncated}, true
}

func (s ProcessStream) Mode() ProcessOutputMode { return s.mode }
func (s ProcessStream) Truncated() bool         { return s.truncated }
func (s ProcessStream) SanitizedText() (string, bool) {
	if s.mode != SanitizedTextOutput {
		return "", false
	}
	return string(s.data), true
}
func (s ProcessStream) OpaqueBytes() ([]byte, bool) {
	if s.mode != OpaqueBytesOutput {
		return nil, false
	}
	return append([]byte(nil), s.data...), true
}

// DirectoryExpectation identifies an already-observed rooted directory. Its
// zero value requests the runner's constructor-configured private safe cwd;
// ambient invocation cwd is never inherited.
type DirectoryExpectation struct {
	Root           RootRole
	Path           string
	RootIdentity   ObjectIdentity
	ParentIdentity ObjectIdentity
	Identity       ObjectIdentity
}

func (e DirectoryExpectation) Valid() bool {
	if !e.Root.Valid() || !validHostLocator(e.Path) || !e.RootIdentity.Valid() ||
		!e.ParentIdentity.Valid() || !e.Identity.Valid() {
		return false
	}
	if e.Path == "." {
		return e.Root != ManagedOutputRoot && e.RootIdentity == e.Identity
	}
	return true
}

func (e DirectoryExpectation) Empty() bool { return e == (DirectoryExpectation{}) }

type EnvironmentBinding struct {
	Name  string
	Value string
}

func (b EnvironmentBinding) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<environment-binding:redacted>"))
}
func (b EnvironmentBinding) MarshalText() ([]byte, error) {
	return []byte("<environment-binding:redacted>"), nil
}
func (b EnvironmentBinding) MarshalJSON() ([]byte, error) {
	return []byte(`{"environment":"redacted"}`), nil
}

// ExecutableEnvironmentBinding represents an environment variable whose value
// is a separately qualified executable locator, such as an AI4J-owned GIT_SSH
// helper. The runner revalidates the expectation and applies an exact
// constructor-owned name policy; ordinary EnvironmentBinding cannot carry it.
type ExecutableEnvironmentBinding struct {
	Name               string
	ResolvedPath       string
	ExpectedExecutable ExecutableExpectation
}

func (b ExecutableEnvironmentBinding) Valid() bool {
	return validEnvironmentName(b.Name) && validHostLocator(b.ResolvedPath) && b.ExpectedExecutable.Valid() &&
		b.ExpectedExecutable.Profile.Kind() == StaticExecutableNative
}

func (b ExecutableEnvironmentBinding) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<executable-environment-binding:redacted>"))
}
func (b ExecutableEnvironmentBinding) MarshalText() ([]byte, error) {
	return []byte("<executable-environment-binding:redacted>"), nil
}
func (b ExecutableEnvironmentBinding) MarshalJSON() ([]byte, error) {
	return []byte(`{"executable_environment":"redacted"}`), nil
}

// ProcessResult contains bounded streams whose mode controls display safety.
// Opaque streams are protocol bytes and must never be rendered or logged. When
// RunProcess returns an error it may also return stable exit and cleanup facts.
type ProcessResult struct {
	Started   bool
	ExitCode  int
	Exited    bool
	Signaled  bool
	Signal    string
	Stdout    ProcessStream
	Stderr    ProcessStream
	Cancelled bool
	TimedOut  bool
}

type ExecutableRequest struct {
	Candidate string
	Authority ExecutableAuthorityClass
}

func (r ExecutableRequest) Valid() bool {
	return validHostLocator(r.Candidate) && r.Authority.Valid()
}

type ExecutableObservation struct {
	ResolvedPath string
	Authority    ExecutableAuthorityClass
	Resource     ResourceObservation
	Profile      StaticExecutableProfile
}

func (o ExecutableObservation) Valid() bool {
	return validHostLocator(o.ResolvedPath) && o.Authority.Valid() && o.Authority.accepts(o.Resource.OwnerClass) &&
		(o.Resource.OwnerClass == CurrentUserOwner) == o.Resource.OwnedByCurrentUser &&
		o.Profile.Valid() && o.Resource.Exists &&
		o.Resource.Kind == ExecutableResource && o.Resource.RootIdentity.Valid() && o.Resource.ParentIdentity.Valid() &&
		o.Resource.Identity.Valid() && o.Resource.OwnerClass.TrustedExecutableOwner() &&
		o.Resource.ExecutableDigest.Valid() && !o.Resource.PrivilegeBearing &&
		!o.Resource.WritableByUntrusted
}

type DiskAllocation struct {
	Root  RootRole
	Bytes uint64
}

func (a DiskAllocation) Active() bool { return a.Root.Valid() && a.Bytes > 0 }

func (a DiskAllocation) Valid() bool {
	return a == (DiskAllocation{}) || a.Active()
}

// DiskPreflightRequest has a fixed shape so caller-controlled cardinality is
// bounded. The adapter groups allocations by opened filesystem identity.
type DiskPreflightRequest struct {
	TemporarySource DiskAllocation
	StagedOutput    DiskAllocation
	Journal         DiskAllocation
	Recovery        DiskAllocation
}

func (r DiskPreflightRequest) Valid() bool {
	allocations := []DiskAllocation{
		r.TemporarySource,
		r.StagedOutput,
		r.Journal,
		r.Recovery,
	}
	active := 0
	var total uint64
	for _, allocation := range allocations {
		if !allocation.Valid() {
			return false
		}
		if !allocation.Active() {
			continue
		}
		if total > ^uint64(0)-allocation.Bytes {
			return false
		}
		total += allocation.Bytes
		active++
	}
	return active > 0
}

func (r DiskPreflightRequest) TotalBytes() (uint64, bool) {
	if !r.Valid() {
		return 0, false
	}
	var total uint64
	for _, allocation := range [...]DiskAllocation{
		r.TemporarySource,
		r.StagedOutput,
		r.Journal,
		r.Recovery,
	} {
		if total > ^uint64(0)-allocation.Bytes {
			return 0, false
		}
		total += allocation.Bytes
	}
	return total, true
}

type FilesystemCapacity struct {
	Identity  uint64
	Required  uint64
	Available uint64
	Known     bool
}

func (c FilesystemCapacity) Valid() bool {
	return c.Identity != 0 && c.Required > 0 && (c.Known || c.Available == 0)
}

type DiskPreflightResult struct {
	Sufficient  bool
	Filesystems []FilesystemCapacity
}

func NewDiskPreflightResult(filesystems []FilesystemCapacity) (DiskPreflightResult, error) {
	if len(filesystems) == 0 || len(filesystems) > 4 {
		return DiskPreflightResult{}, fmt.Errorf("invalid disk preflight result")
	}
	copied := append([]FilesystemCapacity(nil), filesystems...)
	sort.Slice(copied, func(left, right int) bool { return copied[left].Identity < copied[right].Identity })
	sufficient := true
	for index, capacity := range copied {
		if !capacity.Valid() || index > 0 && copied[index-1].Identity == capacity.Identity {
			return DiskPreflightResult{}, fmt.Errorf("invalid disk preflight result")
		}
		sufficient = sufficient && capacity.Known && capacity.Available >= capacity.Required
	}
	result := DiskPreflightResult{Sufficient: sufficient, Filesystems: copied}
	if !result.Coherent() {
		return DiskPreflightResult{}, fmt.Errorf("invalid disk preflight result")
	}
	return result, nil
}

func (r DiskPreflightResult) Coherent() bool {
	if len(r.Filesystems) == 0 || len(r.Filesystems) > 4 {
		return false
	}
	sufficient := true
	for index, capacity := range r.Filesystems {
		if !capacity.Valid() || index > 0 && r.Filesystems[index-1].Identity >= capacity.Identity {
			return false
		}
		sufficient = sufficient && capacity.Known && capacity.Available >= capacity.Required
	}
	return r.Sufficient == sufficient
}

type HostInspector interface {
	InspectHost(context.Context, HostInspectionRequest) (HostObservation, error)
}

type AtomicFileWriter interface {
	ReplaceFile(context.Context, FileMutation) (FileMutationResult, error)
	CleanupFile(context.Context, CleanupArtifact) (FileCleanupResult, error)
	InspectFileArtifacts(context.Context, FileArtifactInspectionRequest) (FileArtifactInspectionResult, error)
}

type ProcessRunner interface {
	RunProcess(context.Context, ProcessRequest) (ProcessResult, error)
}

type DiskPreflighter interface {
	PreflightDisk(context.Context, DiskPreflightRequest) (DiskPreflightResult, error)
}

type ResourceChecker interface {
	CheckResource(context.Context, ResourceRequest) (ResourceObservation, error)
	ReadResource(context.Context, ResourceReadRequest) (ResourceReadResult, error)
	CheckExecutable(context.Context, ExecutableRequest) (ExecutableObservation, error)
	DiskPreflighter
}
