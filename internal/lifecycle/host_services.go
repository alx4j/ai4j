package lifecycle

import (
	"context"
	"errors"
	"sort"
	"time"
)

const (
	maximumEnvironmentPresenceNames = 32
	maximumResourcePolicyVersion    = 64
	maximumHostPolicyTimeout        = time.Hour
)

var (
	errInvalidEnvironmentPresence = errors.New("invalid environment presence request")
	errInvalidHostResourcePolicy  = errors.New("invalid host resource policy")
	// ErrExecutableNotFound is the stable, disclosure-safe classification for
	// a missing executable candidate or dangling initial alias.
	ErrExecutableNotFound = errors.New("executable not found")
)

// EnvironmentPresenceRequest is an immutable bounded set of environment
// names. The host boundary may disclose only whether each name exists; it must
// never return or retain the corresponding value.
type EnvironmentPresenceRequest struct{ names []string }

func NewEnvironmentPresenceRequest(names []string) (EnvironmentPresenceRequest, error) {
	if len(names) == 0 || len(names) > maximumEnvironmentPresenceNames {
		return EnvironmentPresenceRequest{}, errInvalidEnvironmentPresence
	}
	copied := append([]string(nil), names...)
	sort.Strings(copied)
	for index, name := range copied {
		if !validEnvironmentName(name) || index > 0 && copied[index-1] == name {
			return EnvironmentPresenceRequest{}, errInvalidEnvironmentPresence
		}
	}
	return EnvironmentPresenceRequest{names: copied}, nil
}

func (r EnvironmentPresenceRequest) Names() []string {
	return append([]string(nil), r.names...)
}

func (r EnvironmentPresenceRequest) Valid() bool {
	if len(r.names) == 0 || len(r.names) > maximumEnvironmentPresenceNames || !sort.StringsAreSorted(r.names) {
		return false
	}
	for index, name := range r.names {
		if !validEnvironmentName(name) || index > 0 && r.names[index-1] == name {
			return false
		}
	}
	return true
}

type EnvironmentPresence struct {
	Name    string
	Present bool
}

func (p EnvironmentPresence) Valid() bool { return validEnvironmentName(p.Name) }

// EnvironmentPresenceResult is sorted, unique, and contains exactly one fact
// for every requested name. It carries no environment values.
type EnvironmentPresenceResult struct{ values []EnvironmentPresence }

func NewEnvironmentPresenceResult(values []EnvironmentPresence) (EnvironmentPresenceResult, error) {
	if len(values) == 0 || len(values) > maximumEnvironmentPresenceNames {
		return EnvironmentPresenceResult{}, errInvalidEnvironmentPresence
	}
	copied := append([]EnvironmentPresence(nil), values...)
	sort.Slice(copied, func(left, right int) bool { return copied[left].Name < copied[right].Name })
	for index, value := range copied {
		if !value.Valid() || index > 0 && copied[index-1].Name == value.Name {
			return EnvironmentPresenceResult{}, errInvalidEnvironmentPresence
		}
	}
	return EnvironmentPresenceResult{values: copied}, nil
}

func (r EnvironmentPresenceResult) Values() []EnvironmentPresence {
	return append([]EnvironmentPresence(nil), r.values...)
}

func (r EnvironmentPresenceResult) Coherent() bool {
	if len(r.values) == 0 || len(r.values) > maximumEnvironmentPresenceNames {
		return false
	}
	for index, value := range r.values {
		if !value.Valid() || index > 0 && r.values[index-1].Name >= value.Name {
			return false
		}
	}
	return true
}

type EnvironmentInspector interface {
	InspectEnvironment(context.Context, EnvironmentPresenceRequest) (EnvironmentPresenceResult, error)
}

// ResourcePolicyVersion is a host-neutral immutable policy identity.
type ResourcePolicyVersion struct{ value string }

func NewResourcePolicyVersion(value string) (ResourcePolicyVersion, error) {
	if !validResourcePolicyVersion(value) {
		return ResourcePolicyVersion{}, errInvalidHostResourcePolicy
	}
	return ResourcePolicyVersion{value: value}, nil
}

func (v ResourcePolicyVersion) String() string {
	if !v.Valid() {
		return "invalid"
	}
	return v.value
}

func (v ResourcePolicyVersion) Valid() bool { return validResourcePolicyVersion(v.value) }

type GitTimeoutMaximum struct{ value time.Duration }

func (m GitTimeoutMaximum) Duration() time.Duration { return m.value }
func (m GitTimeoutMaximum) Valid() bool             { return validHostPolicyTimeout(m.value) }

type ClaudeTimeoutMaximum struct{ value time.Duration }

func (m ClaudeTimeoutMaximum) Duration() time.Duration { return m.value }
func (m ClaudeTimeoutMaximum) Valid() bool             { return validHostPolicyTimeout(m.value) }

// HostResourcePolicy binds probe budgets to one versioned host profile. The
// distinct timeout types prevent Git and Claude maxima from being interchanged
// accidentally at a consumer boundary.
type HostResourcePolicy struct {
	version ResourcePolicyVersion
	git     GitTimeoutMaximum
	claude  ClaudeTimeoutMaximum
}

func NewHostResourcePolicy(version ResourcePolicyVersion, git, claude time.Duration) (HostResourcePolicy, error) {
	result := HostResourcePolicy{
		version: version,
		git:     GitTimeoutMaximum{value: git},
		claude:  ClaudeTimeoutMaximum{value: claude},
	}
	if !result.Valid() {
		return HostResourcePolicy{}, errInvalidHostResourcePolicy
	}
	return result, nil
}

func (p HostResourcePolicy) Version() ResourcePolicyVersion       { return p.version }
func (p HostResourcePolicy) GitTimeoutMaximum() GitTimeoutMaximum { return p.git }
func (p HostResourcePolicy) ClaudeTimeoutMaximum() ClaudeTimeoutMaximum {
	return p.claude
}

func (p HostResourcePolicy) Valid() bool {
	return p.version.Valid() && p.git.Valid() && p.claude.Valid()
}

type HostResourcePolicyProvider interface {
	ResourcePolicy() HostResourcePolicy
}

// HostServices is one lifetime-owned host implementation projected into
// read/mutation facets by the registry. Close ownership intentionally remains
// outside this neutral interface.
type HostServices interface {
	HostInspector
	EnvironmentInspector
	HostResourcePolicyProvider
	ResourceChecker
	AtomicFileWriter
	ProcessRunner
}

func validResourcePolicyVersion(value string) bool {
	if value == "" || len(value) > maximumResourcePolicyVersion {
		return false
	}
	for index, character := range []byte(value) {
		letter := character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		if !letter && !digit && character != '_' || index == 0 && !letter {
			return false
		}
	}
	return true
}

func validHostPolicyTimeout(value time.Duration) bool {
	return value > 0 && value <= maximumHostPolicyTimeout
}
