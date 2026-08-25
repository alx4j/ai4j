package discovery

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alx4j/ai4j/internal/environment"
)

// PrerequisiteObservation is the immutable result of T2 discovery. It binds
// the supported host tuple to exact Git and Claude executable identities while
// retaining no mutation authority.
type PrerequisiteObservation struct {
	host   environment.HostTuple
	git    environment.ExecutableIdentity
	claude environment.ExecutableIdentity
}

// NewPrerequisiteObservation constructs a complete, distinct prerequisite set.
func NewPrerequisiteObservation(
	host environment.HostTuple,
	git environment.ExecutableIdentity,
	claude environment.ExecutableIdentity,
) (PrerequisiteObservation, error) {
	result := PrerequisiteObservation{host: host, git: git, claude: claude}
	if !result.Valid() {
		return PrerequisiteObservation{}, newError(CodeInvalidObservation)
	}
	return result, nil
}

// Host returns the trusted normalized host tuple.
func (o PrerequisiteObservation) Host() environment.HostTuple { return o.host }

// Executable returns the exact qualified identity for a prerequisite tool.
func (o PrerequisiteObservation) Executable(tool environment.Tool) (environment.ExecutableIdentity, bool) {
	switch tool {
	case environment.GitTool():
		return o.git, o.git.Valid()
	case environment.ClaudeTool():
		return o.claude, o.claude.Valid()
	default:
		return environment.ExecutableIdentity{}, false
	}
}

// Valid reports whether both tool identities are complete and name distinct
// canonical executable objects.
func (o PrerequisiteObservation) Valid() bool {
	if !o.host.Valid() || !o.git.Valid() || !o.claude.Valid() ||
		o.git.Tool() != environment.GitTool() || o.claude.Tool() != environment.ClaudeTool() {
		return false
	}
	gitObservation := o.git.Observation()
	claudeObservation := o.claude.Observation()
	return gitObservation.Resource.Identity != claudeObservation.Resource.Identity &&
		gitObservation.ResolvedPath != claudeObservation.ResolvedPath
}

// Format excludes executable locators, digests, object identities, and native bytes.
func (o PrerequisiteObservation) Format(state fmt.State, _ rune) {
	if !o.Valid() {
		_, _ = io.WriteString(state, "<prerequisite-observation:invalid>")
		return
	}
	_, _ = io.WriteString(state, "<prerequisite-observation:darwin-arm64:redacted>")
}

// MarshalText emits only the fixed redacted observation marker.
func (o PrerequisiteObservation) MarshalText() ([]byte, error) {
	if !o.Valid() {
		return nil, newError(CodeInvalidObservation)
	}
	return []byte(fmt.Sprintf("%v", o)), nil
}

// MarshalJSON delegates executable serialization to the redacting T1 value.
func (o PrerequisiteObservation) MarshalJSON() ([]byte, error) {
	if !o.Valid() {
		return nil, newError(CodeInvalidObservation)
	}
	return json.Marshal(struct {
		Host   environment.HostTuple          `json:"host"`
		Git    environment.ExecutableIdentity `json:"git"`
		Claude environment.ExecutableIdentity `json:"claude"`
	}{Host: o.host, Git: o.git, Claude: o.claude})
}
