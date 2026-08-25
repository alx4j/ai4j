package environment

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/alx4j/ai4j/internal/domain"
)

const candidateCapabilityCount = 7

// ProfileID is a bounded exact compatibility-profile identity.
type ProfileID struct{ value string }

// NewProfileID constructs a canonical lowercase profile identity.
func NewProfileID(value string) (ProfileID, error) {
	if !validProfileID(value) {
		return ProfileID{}, newValidationError(CodeInvalidProfileID)
	}
	return ProfileID{value: value}, nil
}

// String returns the canonical profile identity.
func (i ProfileID) String() string {
	if !i.Valid() {
		return "invalid"
	}
	return i.value
}

// Valid reports whether the profile identity is canonical.
func (i ProfileID) Valid() bool { return validProfileID(i.value) }

func validProfileID(value string) bool {
	if len(value) == 0 || len(value) > maximumProfileIDBytes || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-' || character == '.') {
			return false
		}
	}
	return true
}

// CandidateCapabilityFact is one exact MVP capability observed at candidate scope.
type CandidateCapabilityFact struct{ capability domain.Capability }

// NewCandidateCapabilityFact constructs a fact for an exact MVP capability.
func NewCandidateCapabilityFact(capability domain.Capability) (CandidateCapabilityFact, error) {
	if !isMVPCapability(capability) {
		return CandidateCapabilityFact{}, newValidationError(CodeInvalidCapabilityFact)
	}
	return CandidateCapabilityFact{capability: capability}, nil
}

// Capability returns the normalized target capability.
func (f CandidateCapabilityFact) Capability() domain.Capability { return f.capability }

// Valid reports whether the fact identifies an exact MVP capability.
func (f CandidateCapabilityFact) Valid() bool { return isMVPCapability(f.capability) }

func isMVPCapability(capability domain.Capability) bool {
	switch capability {
	case domain.NativeValidationCapability(), domain.InspectionCapability(), domain.MarketplaceRegistrationCapability(),
		domain.PluginInstallationCapability(), domain.EnablementCapability(), domain.UpdateCapability(), domain.UninstallCapability():
		return true
	default:
		return false
	}
}

// CandidateCapabilitySet is the immutable candidate-only capability surface.
// Its distinct type intentionally cannot be assigned to domain.CapabilitySet,
// which is accepted by lifecycle registration as mutation authority. A
// separate ST014 qualification overlay must make that later transition.
type CandidateCapabilitySet struct {
	facts [candidateCapabilityCount]CandidateCapabilityFact
}

// Facts returns a sorted copy of every candidate capability fact.
func (s CandidateCapabilitySet) Facts() []CandidateCapabilityFact {
	return append([]CandidateCapabilityFact(nil), s.facts[:]...)
}

// Values returns a sorted copy of the candidate capability identities. It
// does not return the generic capability-set type used for mutation authority.
func (s CandidateCapabilitySet) Values() []domain.Capability {
	values := make([]domain.Capability, len(s.facts))
	for index, fact := range s.facts {
		values[index] = fact.capability
	}
	return values
}

// Contains reports whether the exact MVP capability is a candidate.
func (s CandidateCapabilitySet) Contains(capability domain.Capability) bool {
	if !s.Valid() || !isMVPCapability(capability) {
		return false
	}
	for _, fact := range s.facts {
		if fact.capability == capability {
			return true
		}
	}
	return false
}

// Valid reports whether the set contains every exact MVP capability once in
// canonical order.
func (s CandidateCapabilitySet) Valid() bool {
	seen := make(map[domain.Capability]struct{}, candidateCapabilityCount)
	previous := ""
	for index, fact := range s.facts {
		if !fact.Valid() {
			return false
		}
		current := fact.capability.String()
		if index > 0 && current <= previous {
			return false
		}
		if _, duplicate := seen[fact.capability]; duplicate {
			return false
		}
		seen[fact.capability] = struct{}{}
		previous = current
	}
	for _, capability := range domain.MVPCapabilities().Values() {
		if _, present := seen[capability]; !present {
			return false
		}
	}
	return len(seen) == candidateCapabilityCount
}

// Format exposes only the closed candidate count and never mutation authority.
func (s CandidateCapabilitySet) Format(state fmt.State, _ rune) {
	validity := "invalid"
	if s.Valid() {
		validity = "complete"
	}
	_, _ = io.WriteString(state, "<candidate-capabilities:"+validity+">")
}

// MarshalJSON emits the exact closed candidate identities as a sorted array.
func (s CandidateCapabilitySet) MarshalJSON() ([]byte, error) {
	if !s.Valid() {
		return nil, newValidationError(CodeInvalidCapabilityProfile)
	}
	values := s.Values()
	capabilities := make([]string, len(values))
	for index, capability := range values {
		capabilities[index] = capability.String()
	}
	return json.Marshal(capabilities)
}

// MutationQualification is a closed mutation-authority state.
type MutationQualification struct{ value uint8 }

var mutationUnqualified = MutationQualification{value: 1}

// MutationUnqualified returns the only mutation state available to ST005.
func MutationUnqualified() MutationQualification { return mutationUnqualified }

// String returns the canonical mutation qualification.
func (q MutationQualification) String() string {
	if q == mutationUnqualified {
		return "unqualified"
	}
	return "invalid"
}

// Valid reports whether the qualification is registered.
func (q MutationQualification) Valid() bool { return q == mutationUnqualified }

// CapabilityProfile is the immutable exact candidate capability profile. It
// intentionally cannot represent mutation-qualified authority.
type CapabilityProfile struct {
	id       ProfileID
	facts    [candidateCapabilityCount]CandidateCapabilityFact
	mutation MutationQualification
}

// NewCapabilityProfile constructs a complete exact MVP candidate profile.
func NewCapabilityProfile(id ProfileID, facts []CandidateCapabilityFact) (CapabilityProfile, error) {
	if !id.Valid() || len(facts) != candidateCapabilityCount {
		return CapabilityProfile{}, newValidationError(CodeInvalidCapabilityProfile)
	}
	owned := append([]CandidateCapabilityFact(nil), facts...)
	seen := make(map[domain.Capability]struct{}, len(owned))
	for _, fact := range owned {
		if !fact.Valid() {
			return CapabilityProfile{}, newValidationError(CodeInvalidCapabilityProfile)
		}
		if _, duplicate := seen[fact.capability]; duplicate {
			return CapabilityProfile{}, newValidationError(CodeInvalidCapabilityProfile)
		}
		seen[fact.capability] = struct{}{}
	}
	for _, capability := range domain.MVPCapabilities().Values() {
		if _, present := seen[capability]; !present {
			return CapabilityProfile{}, newValidationError(CodeInvalidCapabilityProfile)
		}
	}
	if len(seen) != candidateCapabilityCount {
		return CapabilityProfile{}, newValidationError(CodeInvalidCapabilityProfile)
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].capability.String() < owned[j].capability.String() })
	profile := CapabilityProfile{id: id, mutation: mutationUnqualified}
	copy(profile.facts[:], owned)
	return profile, nil
}

// ID returns the exact compatibility-profile identity.
func (p CapabilityProfile) ID() ProfileID { return p.id }

// CandidateFacts returns a sorted copy of candidate capability facts.
func (p CapabilityProfile) CandidateFacts() []CandidateCapabilityFact {
	return append([]CandidateCapabilityFact(nil), p.facts[:]...)
}

// CandidateCapabilities returns a candidate-specific immutable set. The
// result cannot be used as a registry's mutation-qualified capability set.
func (p CapabilityProfile) CandidateCapabilities() CandidateCapabilitySet {
	return CandidateCapabilitySet{facts: p.facts}
}

// MutationQualification returns unqualified for every ST005 profile.
func (p CapabilityProfile) MutationQualification() MutationQualification { return p.mutation }

// Valid reports whether the profile contains every exact MVP capability once.
func (p CapabilityProfile) Valid() bool {
	candidate, err := NewCapabilityProfile(p.id, p.CandidateFacts())
	return err == nil && candidate == p
}

// Format emits only the safe profile identity and unqualified authority state.
func (p CapabilityProfile) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<capability-profile:"+p.id.String()+":"+p.mutation.String()+">")
}

// MarshalJSON emits the deterministic candidate profile without mutation authority.
func (p CapabilityProfile) MarshalJSON() ([]byte, error) {
	if !p.Valid() {
		return nil, newValidationError(CodeInvalidCapabilityProfile)
	}
	capabilities := make([]string, len(p.facts))
	for index, fact := range p.facts {
		capabilities[index] = fact.capability.String()
	}
	return json.Marshal(struct {
		ID         string   `json:"id"`
		Candidates []string `json:"candidateCapabilities"`
		Mutation   string   `json:"mutationQualification"`
	}{ID: p.id.String(), Candidates: capabilities, Mutation: p.mutation.String()})
}
