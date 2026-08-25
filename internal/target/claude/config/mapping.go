package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/pathsafe"
)

// TrustedHome is the canonical current-user home reference obtained from the
// future neutral bootstrap proof. It is not filesystem authority and the
// neutral proof must remain retained separately for later host revalidation.
type TrustedHome struct{ absolute string }

// NewTrustedHome validates the canonical home spelling. Provenance is supplied
// by the neutral trusted-account and descriptor-proof boundary, not by this
// lexical constructor.
func NewTrustedHome(absolute string) (TrustedHome, error) {
	if !validAbsoluteDirectory(absolute) {
		return TrustedHome{}, newError(CodeInvalidTrustedHome)
	}
	return TrustedHome{absolute: strings.Clone(absolute)}, nil
}

// Valid reports whether the retained spelling is canonical.
func (h TrustedHome) Valid() bool { return validAbsoluteDirectory(h.absolute) }

// Format redacts the home locator.
func (h TrustedHome) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<trusted-home:redacted>")
}

// MarshalText redacts the home locator.
func (h TrustedHome) MarshalText() ([]byte, error) {
	if !h.Valid() {
		return nil, newError(CodeInvalidTrustedHome)
	}
	return []byte(fmt.Sprintf("%v", h)), nil
}

// MarshalJSON redacts the home locator.
func (h TrustedHome) MarshalJSON() ([]byte, error) {
	if !h.Valid() {
		return nil, newError(CodeInvalidTrustedHome)
	}
	return []byte(`{"home":"redacted"}`), nil
}

// DirectoryCandidate is a documented target mapping expressed only as a
// portable canonical path relative to the trusted home. It is unqualified and
// cannot be used as host authority.
type DirectoryCandidate struct {
	role   environment.DirectoryRole
	source environment.DirectorySource
	path   pathsafe.RelativePath
}

// NewDirectoryCandidate constructs a typed home-relative target mapping.
func NewDirectoryCandidate(
	role environment.DirectoryRole,
	source environment.DirectorySource,
	relative pathsafe.RelativePath,
) (DirectoryCandidate, error) {
	result := DirectoryCandidate{role: role, source: source, path: relative}
	if !result.Valid() {
		return DirectoryCandidate{}, newError(CodeInvalidDirectoryCandidate)
	}
	return result, nil
}

// Role returns the documented target-directory role.
func (c DirectoryCandidate) Role() environment.DirectoryRole { return c.role }

// Source returns whether the default or environment override selected it.
func (c DirectoryCandidate) Source() environment.DirectorySource { return c.source }

// RelativePath returns the typed path relative to the trusted home.
func (c DirectoryCandidate) RelativePath() pathsafe.RelativePath { return c.path }

// Valid reports whether role, source, and path are coherent target facts.
func (c DirectoryCandidate) Valid() bool {
	if !c.path.Valid() {
		return false
	}
	switch c.role {
	case environment.ClaudeConfigurationDirectory(), environment.ClaudeRulesDirectory():
		return c.source == environment.DefaultDirectorySource() ||
			c.source == environment.EnvironmentOverrideDirectorySource()
	default:
		return false
	}
}

// Format redacts the relative path.
func (c DirectoryCandidate) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<directory-candidate:"+c.role.String()+":"+c.source.String()+":unqualified:redacted>")
}

// MarshalJSON emits documented semantics but not the path.
func (c DirectoryCandidate) MarshalJSON() ([]byte, error) {
	if !c.Valid() {
		return nil, newError(CodeInvalidDirectoryCandidate)
	}
	return json.Marshal(struct {
		Role          string `json:"role"`
		Source        string `json:"source"`
		Path          string `json:"path"`
		Qualification string `json:"qualification"`
	}{
		Role: c.role.String(), Source: c.source.String(), Path: "redacted",
		Qualification: UnqualifiedMapping().String(),
	})
}

// ResolveCandidate performs only pure target mapping. It requires the same
// exact Claude version in the observation and policy, exact equality between
// startup HOME and the trusted home, and a canonical pathsafe descendant for
// every accepted override. It observes no filesystem state.
func ResolveCandidate(
	ctx context.Context,
	input StartupInput,
	home TrustedHome,
	claudeVersion environment.ToolVersion,
	policy OverridePolicy,
) (CandidateObservation, error) {
	if ctx == nil {
		return CandidateObservation{}, newError(CodeInvalidContext)
	}
	if err := contextFailure(ctx, ctx.Err()); err != nil {
		return CandidateObservation{}, err
	}
	if !input.Valid() {
		return CandidateObservation{}, newError(CodeInvalidStartupInput)
	}
	if !home.Valid() {
		return CandidateObservation{}, newError(CodeInvalidTrustedHome)
	}
	if !policy.Valid() || !claudeVersion.Valid() || claudeVersion.Tool() != environment.ClaudeTool() ||
		policy.Version() != claudeVersion {
		return CandidateObservation{}, newError(CodeInvalidOverridePolicy)
	}
	if input.HomeState() != PresentStartupValue() {
		return CandidateObservation{}, incomplete(environment.ClaudeConfigurationFact())
	}
	if !validAbsoluteDirectory(input.homeValue()) || input.homeValue() != home.absolute {
		return CandidateObservation{}, unsupported(environment.UntrustedHomeReason(), environment.ClaudeConfigurationFact())
	}

	configurationPath, source, err := resolveConfigurationPath(input, home, policy)
	if err != nil {
		return CandidateObservation{}, err
	}
	configuration, err := NewDirectoryCandidate(environment.ClaudeConfigurationDirectory(), source, configurationPath)
	if err != nil {
		return CandidateObservation{}, newError(CodeInvalidObservation)
	}
	rulesPath, err := pathsafe.NewRelativePath(configurationPath.String() + "/rules")
	if err != nil {
		if source == environment.EnvironmentOverrideDirectorySource() {
			return CandidateObservation{}, unsupported(
				environment.PolicyProhibitedConfigOverrideReason(),
				environment.ClaudeConfigurationOverrideFact(),
			)
		}
		return CandidateObservation{}, newError(CodeInvalidObservation)
	}
	rules, err := NewDirectoryCandidate(environment.ClaudeRulesDirectory(), source, rulesPath)
	if err != nil {
		return CandidateObservation{}, newError(CodeInvalidObservation)
	}
	if err := contextFailure(ctx, ctx.Err()); err != nil {
		return CandidateObservation{}, err
	}
	return NewCandidateObservation(home, configuration, rules, policy)
}

func resolveConfigurationPath(
	input StartupInput,
	home TrustedHome,
	policy OverridePolicy,
) (pathsafe.RelativePath, environment.DirectorySource, error) {
	switch input.OverrideState() {
	case AbsentStartupValue():
		relative, err := pathsafe.NewRelativePath(".claude")
		if err != nil {
			return pathsafe.RelativePath{}, environment.DirectorySource{}, newError(CodeInvalidObservation)
		}
		return relative, environment.DefaultDirectorySource(), nil
	case ExplicitEmptyStartupValue():
		return pathsafe.RelativePath{}, environment.DirectorySource{}, unsupported(
			environment.EmptyConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact(),
		)
	case PresentStartupValue():
		value := input.overrideValue()
		if !path.IsAbs(value) {
			return pathsafe.RelativePath{}, environment.DirectorySource{}, unsupported(
				environment.RelativeConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact(),
			)
		}
		switch policy.Decision() {
		case UnsupportedVersionOverrideDecision():
			return pathsafe.RelativePath{}, environment.DirectorySource{}, unsupported(
				environment.UnsupportedVersionConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact(),
			)
		case PolicyProhibitedOverrideDecision():
			return pathsafe.RelativePath{}, environment.DirectorySource{}, unsupported(
				environment.PolicyProhibitedConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact(),
			)
		case AllowedOverrideDecision():
		default:
			return pathsafe.RelativePath{}, environment.DirectorySource{}, newError(CodeInvalidOverridePolicy)
		}
		relative, ok := beneathHome(home.absolute, value)
		if !ok {
			return pathsafe.RelativePath{}, environment.DirectorySource{}, unsupported(
				environment.PolicyProhibitedConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact(),
			)
		}
		return relative, environment.EnvironmentOverrideDirectorySource(), nil
	default:
		return pathsafe.RelativePath{}, environment.DirectorySource{}, newError(CodeInvalidStartupInput)
	}
}

func unsupported(reason environment.FaultReason, fact environment.EnvironmentFact) error {
	result, err := environment.NewUnsupportedFault(reason, fact)
	if err != nil {
		return newError(CodeInvalidObservation)
	}
	return result
}

func incomplete(fact environment.EnvironmentFact) error {
	result, err := environment.NewIncompleteEnvironmentFault(fact)
	if err != nil {
		return newError(CodeInvalidObservation)
	}
	return result
}
