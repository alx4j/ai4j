// Package domain contains target-neutral values used by the AI4J lifecycle core.
package domain

import (
	"fmt"
	"regexp"
	"sort"
)

var symbolPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func validateSymbol(kind, value string) error {
	if !symbolPattern.MatchString(value) {
		return fmt.Errorf("%s %q is not a canonical identifier", kind, value)
	}
	return nil
}

type Target struct{ value string }

func NewTarget(value string) (Target, error) {
	if err := validateSymbol("target", value); err != nil {
		return Target{}, err
	}
	return Target{value: value}, nil
}
func (v Target) String() string { return v.value }
func (v Target) Valid() bool    { return symbolPattern.MatchString(v.value) }

type Host struct{ value string }

func NewHost(value string) (Host, error) {
	if err := validateSymbol("host", value); err != nil {
		return Host{}, err
	}
	return Host{value: value}, nil
}
func (v Host) String() string { return v.value }
func (v Host) Valid() bool    { return symbolPattern.MatchString(v.value) }

type Scope struct{ value string }

func NewScope(value string) (Scope, error) {
	if err := validateSymbol("scope", value); err != nil {
		return Scope{}, err
	}
	return Scope{value: value}, nil
}
func (v Scope) String() string { return v.value }
func (v Scope) Valid() bool    { return symbolPattern.MatchString(v.value) }

type SourceMode struct{ value string }

func NewSourceMode(value string) (SourceMode, error) {
	if err := validateSymbol("source mode", value); err != nil {
		return SourceMode{}, err
	}
	return SourceMode{value: value}, nil
}
func (v SourceMode) String() string { return v.value }
func (v SourceMode) Valid() bool    { return symbolPattern.MatchString(v.value) }

type SourceSelection struct{ value string }

func NewSourceSelection(value string) (SourceSelection, error) {
	if err := validateSymbol("source selection", value); err != nil {
		return SourceSelection{}, err
	}
	return SourceSelection{value: value}, nil
}
func (v SourceSelection) String() string { return v.value }
func (v SourceSelection) Valid() bool    { return symbolPattern.MatchString(v.value) }

// GitTransport is the credential-free access preference retained for a
// GitHub source. It is deliberately separate from RepositoryIdentity: two
// transports that reach the same repository must compare as one source.
type GitTransport struct{ value string }

func NewGitTransport(value string) (GitTransport, error) {
	if value != "https" && value != "ssh" {
		return GitTransport{}, fmt.Errorf("git transport is not supported")
	}
	return GitTransport{value: value}, nil
}
func (v GitTransport) String() string { return v.value }
func (v GitTransport) Valid() bool    { return v.value == "https" || v.value == "ssh" }
func (v GitTransport) MarshalText() ([]byte, error) {
	if !v.Valid() {
		return nil, fmt.Errorf("invalid git transport")
	}
	return []byte(v.value), nil
}

type SelectionMode struct{ value string }

func NewSelectionMode(value string) (SelectionMode, error) {
	if err := validateSymbol("selection mode", value); err != nil {
		return SelectionMode{}, err
	}
	return SelectionMode{value: value}, nil
}
func (v SelectionMode) String() string { return v.value }
func (v SelectionMode) Valid() bool    { return symbolPattern.MatchString(v.value) }

type RecoveryPolicy struct{ value string }

func NewRecoveryPolicy(value string) (RecoveryPolicy, error) {
	if err := validateSymbol("recovery policy", value); err != nil {
		return RecoveryPolicy{}, err
	}
	return RecoveryPolicy{value: value}, nil
}
func (v RecoveryPolicy) String() string { return v.value }
func (v RecoveryPolicy) Valid() bool    { return symbolPattern.MatchString(v.value) }

type ObjectFormat struct{ value string }

func NewObjectFormat(value string) (ObjectFormat, error) {
	if err := validateSymbol("object format", value); err != nil {
		return ObjectFormat{}, err
	}
	return ObjectFormat{value: value}, nil
}
func (v ObjectFormat) String() string { return v.value }
func (v ObjectFormat) Valid() bool    { return symbolPattern.MatchString(v.value) }

type Capability struct{ value string }

func NewCapability(value string) (Capability, error) {
	if err := validateSymbol("capability", value); err != nil {
		return Capability{}, err
	}
	return Capability{value: value}, nil
}
func (v Capability) String() string { return v.value }
func (v Capability) Valid() bool    { return symbolPattern.MatchString(v.value) }

// StateSchemaVersion is deliberately numeric so an unknown newer wire value
// can be retained and rejected instead of being mapped to a known schema.
type StateSchemaVersion struct{ value uint16 }

func NewStateSchemaVersion(value uint16) (StateSchemaVersion, error) {
	if value == 0 {
		return StateSchemaVersion{}, fmt.Errorf("state schema version must be positive")
	}
	return StateSchemaVersion{value: value}, nil
}
func (v StateSchemaVersion) Uint16() uint16 { return v.value }
func (v StateSchemaVersion) Valid() bool    { return v.value != 0 }

var (
	sourceSelectionBuiltInDefault = mustValue(NewSourceSelection("built_in_default"))
	sourceSelectionExplicit       = mustValue(NewSourceSelection("explicit"))
	gitTransportHTTPS             = mustValue(NewGitTransport("https"))
	gitTransportSSH               = mustValue(NewGitTransport("ssh"))
	objectFormatSHA1              = mustValue(NewObjectFormat("sha1"))
)

func BuiltInDefaultSource() SourceSelection { return sourceSelectionBuiltInDefault }
func ExplicitSource() SourceSelection       { return sourceSelectionExplicit }
func HTTPSGitTransport() GitTransport       { return gitTransportHTTPS }
func SSHGitTransport() GitTransport         { return gitTransportSSH }
func SHA1ObjectFormat() ObjectFormat        { return objectFormatSHA1 }

func mustValue[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// CapabilitySet is an immutable, deterministic set of target capabilities.
type CapabilitySet struct{ values map[Capability]struct{} }

func NewCapabilitySet(values ...Capability) (CapabilitySet, error) {
	set := make(map[Capability]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() {
			return CapabilitySet{}, fmt.Errorf("invalid capability %q", value.String())
		}
		set[value] = struct{}{}
	}
	return CapabilitySet{values: set}, nil
}

func (s CapabilitySet) Contains(value Capability) bool {
	_, ok := s.values[value]
	return ok
}

func (s CapabilitySet) ContainsAll(required CapabilitySet) bool {
	for value := range required.values {
		if !s.Contains(value) {
			return false
		}
	}
	return true
}

func (s CapabilitySet) Values() []Capability {
	values := make([]Capability, 0, len(s.values))
	for value := range s.values {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].String() < values[j].String() })
	return values
}

func (s CapabilitySet) Empty() bool { return len(s.values) == 0 }
