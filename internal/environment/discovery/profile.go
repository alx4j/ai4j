package discovery

import (
	"fmt"
	"io"
	"time"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

const (
	mvpResourcePolicyVersion           = "mvp_resource_v1"
	gitEnvironmentProfileName          = "isolated"
	claudeEnvironmentProfileName       = "claude_probe_v1"
	probeOutputLimitBytes        int64 = 256
	probeTerminationGrace              = time.Second
	mvpGitTimeoutMaximum               = 5 * time.Minute
	mvpClaudeTimeoutMaximum            = 2 * time.Minute
)

// ProbeProfile is the immutable T2 binding between a versioned host resource
// policy and the two closed process-environment profiles used for version
// discovery. Callers cannot provide independent duration overrides.
type ProbeProfile struct {
	policyVersion lifecycle.ResourcePolicyVersion
	gitTimeout    lifecycle.GitTimeoutMaximum
	claudeTimeout lifecycle.ClaudeTimeoutMaximum
	gitEnv        lifecycle.ProcessEnvironmentProfileID
	claudeEnv     lifecycle.ProcessEnvironmentProfileID
}

// NewMVPProbeProfile derives the exact probe maxima from the selected host
// policy and binds the fixed environment-profile identifiers.
func NewMVPProbeProfile(policy lifecycle.HostResourcePolicy) (ProbeProfile, error) {
	gitEnvironment, gitErr := lifecycle.NewProcessEnvironmentProfileID(gitEnvironmentProfileName)
	claudeEnvironment, claudeErr := lifecycle.NewProcessEnvironmentProfileID(claudeEnvironmentProfileName)
	profile := ProbeProfile{
		policyVersion: policy.Version(),
		gitTimeout:    policy.GitTimeoutMaximum(),
		claudeTimeout: policy.ClaudeTimeoutMaximum(),
		gitEnv:        gitEnvironment,
		claudeEnv:     claudeEnvironment,
	}
	if gitErr != nil || claudeErr != nil || !policy.Valid() || !profile.Valid() {
		return ProbeProfile{}, newError(CodeInvalidProfile)
	}
	return profile, nil
}

// PolicyVersion returns the exact host resource-policy identity.
func (p ProbeProfile) PolicyVersion() lifecycle.ResourcePolicyVersion { return p.policyVersion }

// GitTimeoutMaximum returns the separately typed Git probe maximum.
func (p ProbeProfile) GitTimeoutMaximum() lifecycle.GitTimeoutMaximum { return p.gitTimeout }

// ClaudeTimeoutMaximum returns the separately typed Claude probe maximum.
func (p ProbeProfile) ClaudeTimeoutMaximum() lifecycle.ClaudeTimeoutMaximum {
	return p.claudeTimeout
}

// GitEnvironmentProfile returns the closed isolated profile identifier.
func (p ProbeProfile) GitEnvironmentProfile() lifecycle.ProcessEnvironmentProfileID { return p.gitEnv }

// ClaudeEnvironmentProfile returns the closed Claude probe profile identifier.
func (p ProbeProfile) ClaudeEnvironmentProfile() lifecycle.ProcessEnvironmentProfileID {
	return p.claudeEnv
}

// GitEnvironment returns a non-nil copy of the exact isolated binding set.
func (p ProbeProfile) GitEnvironment() []lifecycle.EnvironmentBinding {
	return make([]lifecycle.EnvironmentBinding, 0)
}

// ClaudeEnvironment returns a copy of the exact Claude probe binding set.
func (p ProbeProfile) ClaudeEnvironment() []lifecycle.EnvironmentBinding {
	return []lifecycle.EnvironmentBinding{
		{Name: "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", Value: "1"},
		{Name: "CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL", Value: "1"},
		{Name: "DISABLE_UPDATES", Value: "1"},
	}
}

// Valid reports whether the profile is the closed MVP discovery shape.
func (p ProbeProfile) Valid() bool {
	return p.policyVersion.Valid() && p.policyVersion.String() == mvpResourcePolicyVersion &&
		p.gitTimeout.Valid() && p.gitTimeout.Duration() == mvpGitTimeoutMaximum &&
		p.claudeTimeout.Valid() && p.claudeTimeout.Duration() == mvpClaudeTimeoutMaximum &&
		p.gitEnv.Valid() && p.gitEnv.String() == gitEnvironmentProfileName &&
		p.claudeEnv.Valid() && p.claudeEnv.String() == claudeEnvironmentProfileName
}

// Format emits only safe profile identities and no duration or environment value.
func (p ProbeProfile) Format(state fmt.State, _ rune) {
	if !p.Valid() {
		_, _ = io.WriteString(state, "<environment-probe-profile:invalid>")
		return
	}
	_, _ = io.WriteString(state, "<environment-probe-profile:mvp_resource_v1>")
}
