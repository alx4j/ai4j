package environment

import "encoding/json"

// PolicyObservation is a closed native-policy observation independent from
// capability presence and mutation qualification.
type PolicyObservation struct{ value uint8 }

var (
	policyAllowed       = PolicyObservation{value: 1}
	policyBlocked       = PolicyObservation{value: 2}
	policyUnknown       = PolicyObservation{value: 3}
	policyNotObservable = PolicyObservation{value: 4}
)

// PolicyAllowed returns an explicit native-policy allowed observation.
func PolicyAllowed() PolicyObservation { return policyAllowed }

// PolicyBlocked returns an explicit native-policy blocked observation.
func PolicyBlocked() PolicyObservation { return policyBlocked }

// PolicyUnknown returns an explicit native-policy unknown observation.
func PolicyUnknown() PolicyObservation { return policyUnknown }

// PolicyNotObservable returns a profile state with no supported policy channel.
func PolicyNotObservable() PolicyObservation { return policyNotObservable }

// NewPolicyObservation parses a canonical policy observation.
func NewPolicyObservation(value string) (PolicyObservation, error) {
	switch value {
	case "allowed":
		return policyAllowed, nil
	case "policy_blocked":
		return policyBlocked, nil
	case "unknown":
		return policyUnknown, nil
	case "not_observable":
		return policyNotObservable, nil
	default:
		return PolicyObservation{}, newValidationError(CodeInvalidPolicyObservation)
	}
}

// String returns the canonical policy observation.
func (p PolicyObservation) String() string {
	switch p {
	case policyAllowed:
		return "allowed"
	case policyBlocked:
		return "policy_blocked"
	case policyUnknown:
		return "unknown"
	case policyNotObservable:
		return "not_observable"
	default:
		return "invalid"
	}
}

// Valid reports whether the policy observation is registered.
func (p PolicyObservation) Valid() bool {
	return p == policyAllowed || p == policyBlocked || p == policyUnknown || p == policyNotObservable
}

// MarshalText emits the canonical policy observation.
func (p PolicyObservation) MarshalText() ([]byte, error) {
	if !p.Valid() {
		return nil, newValidationError(CodeInvalidPolicyObservation)
	}
	return []byte(p.String()), nil
}

// MarshalJSON emits the canonical policy observation without inferred facts.
func (p PolicyObservation) MarshalJSON() ([]byte, error) {
	if !p.Valid() {
		return nil, newValidationError(CodeInvalidPolicyObservation)
	}
	return json.Marshal(p.String())
}
