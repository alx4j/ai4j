package environment

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
)

// Observation is the immutable complete T1 environment aggregate.
type Observation struct {
	host     HostTuple
	git      ExecutableIdentity
	claude   ExecutableIdentity
	config   Directory
	rules    Directory
	state    Directory
	recovery Directory
	profile  CapabilityProfile
	policy   PolicyObservation
}

// NewObservation constructs a complete normalized environment observation.
// Executable and directory input ordering is not semantic.
func NewObservation(host HostTuple, executables []ExecutableIdentity, directories []Directory, profile CapabilityProfile, policy PolicyObservation) (Observation, error) {
	if !host.Valid() || !profile.Valid() || !policy.Valid() || len(executables) != 2 || len(directories) != 4 {
		return Observation{}, newValidationError(CodeInvalidObservation)
	}
	var observation Observation
	observation.host = host
	observation.profile = profile
	observation.policy = policy
	seenTools := make(map[Tool]struct{}, len(executables))
	for _, executable := range append([]ExecutableIdentity(nil), executables...) {
		if !executable.Valid() {
			return Observation{}, newValidationError(CodeInvalidObservation)
		}
		if _, duplicate := seenTools[executable.tool]; duplicate {
			return Observation{}, newValidationError(CodeInvalidObservation)
		}
		seenTools[executable.tool] = struct{}{}
		switch executable.tool {
		case gitTool:
			observation.git = executable
		case claudeTool:
			observation.claude = executable
		default:
			return Observation{}, newValidationError(CodeInvalidObservation)
		}
	}
	seenRoles := make(map[DirectoryRole]struct{}, len(directories))
	seenPaths := make(map[string]struct{}, len(directories))
	for _, directory := range append([]Directory(nil), directories...) {
		if !directory.Valid() {
			return Observation{}, newValidationError(CodeInvalidObservation)
		}
		if _, duplicate := seenRoles[directory.role]; duplicate {
			return Observation{}, newValidationError(CodeInvalidObservation)
		}
		if _, duplicate := seenPaths[directory.path]; duplicate {
			return Observation{}, newValidationError(CodeInvalidObservation)
		}
		seenRoles[directory.role] = struct{}{}
		seenPaths[directory.path] = struct{}{}
		switch directory.role {
		case claudeConfigurationRole:
			observation.config = directory
		case claudeRulesRole:
			observation.rules = directory
		case ai4jStateRole:
			observation.state = directory
		case ai4jRecoveryRole:
			observation.recovery = directory
		default:
			return Observation{}, newValidationError(CodeInvalidObservation)
		}
	}
	if !observation.git.Valid() || !observation.claude.Valid() ||
		!observation.config.Valid() || !observation.rules.Valid() || !observation.state.Valid() || !observation.recovery.Valid() ||
		observation.config.source != observation.rules.source || path.Join(observation.config.path, "rules") != observation.rules.path ||
		observation.config.presence == absentDirectory && observation.rules.presence == presentDirectory {
		return Observation{}, newValidationError(CodeInvalidObservation)
	}
	return observation, nil
}

// Host returns the trusted normalized host tuple.
func (o Observation) Host() HostTuple { return o.host }

// Executable returns the exact tool identity when present.
func (o Observation) Executable(tool Tool) (ExecutableIdentity, bool) {
	switch tool {
	case gitTool:
		return o.git, o.git.Valid()
	case claudeTool:
		return o.claude, o.claude.Valid()
	default:
		return ExecutableIdentity{}, false
	}
}

// Executables returns Git then Claude as a fresh slice.
func (o Observation) Executables() []ExecutableIdentity {
	return []ExecutableIdentity{o.git, o.claude}
}

// Directory returns the exact documented directory observation when present.
func (o Observation) Directory(role DirectoryRole) (Directory, bool) {
	switch role {
	case claudeConfigurationRole:
		return o.config, o.config.Valid()
	case claudeRulesRole:
		return o.rules, o.rules.Valid()
	case ai4jStateRole:
		return o.state, o.state.Valid()
	case ai4jRecoveryRole:
		return o.recovery, o.recovery.Valid()
	default:
		return Directory{}, false
	}
}

// Directories returns canonical role order as a fresh slice.
func (o Observation) Directories() []Directory {
	return []Directory{o.config, o.rules, o.state, o.recovery}
}

// Profile returns the exact candidate capability profile.
func (o Observation) Profile() CapabilityProfile { return o.profile }

// Policy returns the explicit policy observation without inference from other facts.
func (o Observation) Policy() PolicyObservation { return o.policy }

// Valid reports whether the aggregate remains complete and coherent.
func (o Observation) Valid() bool {
	candidate, err := NewObservation(o.host, o.Executables(), o.Directories(), o.profile, o.policy)
	return err == nil && candidate == o
}

// Format redacts all filesystem, digest, and native-profile identity facts.
func (o Observation) Format(state fmt.State, _ rune) {
	profile := "invalid"
	if o.profile.Valid() {
		profile = o.profile.id.String()
	}
	_, _ = io.WriteString(state, "<environment-observation:"+profile+":redacted>")
}

// MarshalText redacts the aggregate's sensitive host-bound identity facts.
func (o Observation) MarshalText() ([]byte, error) { return []byte(fmt.Sprintf("%v", o)), nil }

// MarshalJSON exposes only the safe profile identity and a redaction marker.
func (o Observation) MarshalJSON() ([]byte, error) {
	if !o.Valid() {
		return nil, newValidationError(CodeInvalidObservation)
	}
	return json.Marshal(struct {
		Profile     string `json:"profile"`
		Observation string `json:"observation"`
	}{Profile: o.profile.id.String(), Observation: "redacted"})
}
