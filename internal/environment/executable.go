package environment

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

// ExecutableIdentity binds a normalized tool version to the exact canonical
// executable observation supplied by the host boundary. This package never
// reinterprets or reparses the host locator.
type ExecutableIdentity struct {
	tool        Tool
	version     ToolVersion
	observation lifecycle.ExecutableObservation
}

// NewExecutableIdentity constructs an identity from a qualified host observation.
// ST005 accepts only native executable images that contain ARM64 code. Script
// profiles are deliberately excluded: retaining a script alone would not prove
// the exact interpreter identity and architecture that the kernel will execute.
func NewExecutableIdentity(tool Tool, version ToolVersion, observation lifecycle.ExecutableObservation) (ExecutableIdentity, error) {
	native, nativeOK := observation.Profile.Native()
	if !tool.Valid() || !version.Valid() || version.Tool() != tool || !observation.Valid() || !nativeOK ||
		native.Role() != lifecycle.NativeExecutable || !native.Architectures().Contains(lifecycle.ExecutableARM64) {
		return ExecutableIdentity{}, newValidationError(CodeInvalidExecutable)
	}
	return ExecutableIdentity{tool: tool, version: version, observation: observation}, nil
}

// Tool returns the prerequisite tool identity.
func (e ExecutableIdentity) Tool() Tool { return e.tool }

// Version returns the exact normalized tool version.
func (e ExecutableIdentity) Version() ToolVersion { return e.version }

// ResolvedPath returns the canonical host locator proved by the host observation.
func (e ExecutableIdentity) ResolvedPath() string { return e.observation.ResolvedPath }

// Profile returns the host-proved static executable profile.
func (e ExecutableIdentity) Profile() lifecycle.StaticExecutableProfile { return e.observation.Profile }

// Digest returns the digest of the exact opened executable bytes.
func (e ExecutableIdentity) Digest() domain.ExecutableDigest {
	return e.observation.Resource.ExecutableDigest
}

// Observation returns a value copy of the exact host proof for later binding.
func (e ExecutableIdentity) Observation() lifecycle.ExecutableObservation { return e.observation }

// Valid reports whether all identity facts remain coherent.
func (e ExecutableIdentity) Valid() bool {
	candidate, err := NewExecutableIdentity(e.tool, e.version, e.observation)
	return err == nil && candidate == e
}

// Format redacts the locator, digest, object identities, and native profile details.
func (e ExecutableIdentity) Format(state fmt.State, _ rune) {
	tool := "invalid"
	if e.tool.Valid() {
		tool = e.tool.String()
	}
	_, _ = io.WriteString(state, "<executable-identity:"+tool+":redacted>")
}

// MarshalText redacts host-private identity facts.
func (e ExecutableIdentity) MarshalText() ([]byte, error) {
	return []byte(fmt.Sprintf("%v", e)), nil
}

// MarshalJSON redacts host-private identity facts while retaining safe selection facts.
func (e ExecutableIdentity) MarshalJSON() ([]byte, error) {
	if !e.Valid() {
		return nil, newValidationError(CodeInvalidExecutable)
	}
	return json.Marshal(struct {
		Tool     string `json:"tool"`
		Version  string `json:"version"`
		Identity string `json:"identity"`
	}{Tool: e.tool.String(), Version: e.version.String(), Identity: "redacted"})
}
