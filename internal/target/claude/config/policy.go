package config

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alx4j/ai4j/internal/environment"
)

// OverrideDecision is the closed result of qualifying CLAUDE_CONFIG_DIR for
// one exact Claude version. It never implies a semantic-version range.
type OverrideDecision struct{ value uint8 }

var (
	allowedOverrideDecision            = OverrideDecision{value: 1}
	unsupportedVersionOverrideDecision = OverrideDecision{value: 2}
	policyProhibitedOverrideDecision   = OverrideDecision{value: 3}
)

// AllowedOverrideDecision permits a qualified override for the bound version.
func AllowedOverrideDecision() OverrideDecision { return allowedOverrideDecision }

// UnsupportedVersionOverrideDecision marks the bound version unqualified for overrides.
func UnsupportedVersionOverrideDecision() OverrideDecision {
	return unsupportedVersionOverrideDecision
}

// PolicyProhibitedOverrideDecision denies overrides under the bound policy.
func PolicyProhibitedOverrideDecision() OverrideDecision {
	return policyProhibitedOverrideDecision
}

// NewOverrideDecision parses a canonical decision.
func NewOverrideDecision(value string) (OverrideDecision, error) {
	switch value {
	case "allowed":
		return allowedOverrideDecision, nil
	case "unsupported_version":
		return unsupportedVersionOverrideDecision, nil
	case "policy_prohibited":
		return policyProhibitedOverrideDecision, nil
	default:
		return OverrideDecision{}, newError(CodeInvalidOverridePolicy)
	}
}

// String returns the canonical decision name.
func (d OverrideDecision) String() string {
	switch d {
	case allowedOverrideDecision:
		return "allowed"
	case unsupportedVersionOverrideDecision:
		return "unsupported_version"
	case policyProhibitedOverrideDecision:
		return "policy_prohibited"
	default:
		return "invalid"
	}
}

// Valid reports whether the decision is registered.
func (d OverrideDecision) Valid() bool {
	return d == allowedOverrideDecision || d == unsupportedVersionOverrideDecision || d == policyProhibitedOverrideDecision
}

// OverridePolicy binds one closed decision to one exact normalized Claude
// version. A caller must supply a separate policy for every tested version.
type OverridePolicy struct {
	version  environment.ToolVersion
	decision OverrideDecision
}

// NewOverridePolicy constructs an exact-version policy. Git and unnormalized
// or zero versions are rejected.
func NewOverridePolicy(version environment.ToolVersion, decision OverrideDecision) (OverridePolicy, error) {
	result := OverridePolicy{version: version, decision: decision}
	if !result.Valid() {
		return OverridePolicy{}, newError(CodeInvalidOverridePolicy)
	}
	return result, nil
}

// Version returns the exact Claude version bound to this policy.
func (p OverridePolicy) Version() environment.ToolVersion { return p.version }

// Decision returns the closed override decision.
func (p OverridePolicy) Decision() OverrideDecision { return p.decision }

// Valid reports whether the policy binds an exact semantic Claude version.
func (p OverridePolicy) Valid() bool {
	return p.version.Valid() && p.version.Tool() == environment.ClaudeTool() &&
		p.version.Form() == environment.SemanticToolVersionForm() && p.decision.Valid()
}

// Format emits only normalized non-sensitive policy facts.
func (p OverridePolicy) Format(state fmt.State, _ rune) {
	version := "invalid"
	decision := "invalid"
	if p.version.Valid() {
		version = p.version.String()
	}
	if p.decision.Valid() {
		decision = p.decision.String()
	}
	_, _ = io.WriteString(state, "<claude-config-override-policy:"+version+":"+decision+">")
}

// MarshalJSON emits only the exact normalized version and decision.
func (p OverridePolicy) MarshalJSON() ([]byte, error) {
	if !p.Valid() {
		return nil, newError(CodeInvalidOverridePolicy)
	}
	return json.Marshal(struct {
		Version  string `json:"claude_version"`
		Decision string `json:"decision"`
	}{Version: p.version.String(), Decision: p.decision.String()})
}
