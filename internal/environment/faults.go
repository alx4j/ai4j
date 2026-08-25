package environment

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alx4j/ai4j/internal/fault"
)

type environmentFaultSentinel string

func (s environmentFaultSentinel) Error() string { return string(s) }

var (
	// ErrUnsupported identifies a tested environment or capability mismatch.
	ErrUnsupported error = environmentFaultSentinel("unsupported")
	// ErrIncompleteEnvironment identifies a missing required environment fact.
	ErrIncompleteEnvironment error = environmentFaultSentinel("incomplete_environment")
)

// FaultKind is the closed discovery-facing environment fault category.
type FaultKind struct{ value uint8 }

var (
	unsupportedFaultKind = FaultKind{value: 1}
	incompleteFaultKind  = FaultKind{value: 2}
)

// UnsupportedFaultKind returns a supported-contract mismatch.
func UnsupportedFaultKind() FaultKind { return unsupportedFaultKind }

// IncompleteEnvironmentFaultKind returns a missing-prerequisite category.
func IncompleteEnvironmentFaultKind() FaultKind { return incompleteFaultKind }

// NewFaultKind parses a canonical discovery fault category.
func NewFaultKind(value string) (FaultKind, error) {
	switch value {
	case "unsupported":
		return unsupportedFaultKind, nil
	case "incomplete_environment":
		return incompleteFaultKind, nil
	default:
		return FaultKind{}, newValidationError(CodeInvalidFaultKind)
	}
}

// String returns the canonical discovery fault category.
func (k FaultKind) String() string {
	switch k {
	case unsupportedFaultKind:
		return "unsupported"
	case incompleteFaultKind:
		return "incomplete_environment"
	default:
		return "invalid"
	}
}

// Valid reports whether the category is registered.
func (k FaultKind) Valid() bool { return k == unsupportedFaultKind || k == incompleteFaultKind }

// FaultReason is the closed actionable discovery reason.
type FaultReason struct{ value uint8 }

var (
	unsupportedHostReason                  = FaultReason{value: 1}
	unsupportedVersionReason               = FaultReason{value: 2}
	unsupportedExecutableReason            = FaultReason{value: 3}
	unsupportedCapabilityReason            = FaultReason{value: 4}
	missingRequiredFactReason              = FaultReason{value: 5}
	emptyConfigOverrideReason              = FaultReason{value: 6}
	relativeConfigOverrideReason           = FaultReason{value: 7}
	wrongOwnerConfigOverrideReason         = FaultReason{value: 8}
	symlinkedConfigOverrideReason          = FaultReason{value: 9}
	unsupportedVersionConfigOverrideReason = FaultReason{value: 10}
	policyProhibitedConfigOverrideReason   = FaultReason{value: 11}
	untrustedHomeReason                    = FaultReason{value: 12}
	wrongOwnerDirectoryReason              = FaultReason{value: 13}
	symlinkedDirectoryReason               = FaultReason{value: 14}
	unsafeModeDirectoryReason              = FaultReason{value: 15}
	wrongTypeDirectoryReason               = FaultReason{value: 16}
	unsupportedFilesystemDirectoryReason   = FaultReason{value: 17}
	protectedRootOverlapDirectoryReason    = FaultReason{value: 18}
)

// UnsupportedHostReason returns a host-contract mismatch reason.
func UnsupportedHostReason() FaultReason { return unsupportedHostReason }

// UnsupportedVersionReason returns an untested-version reason.
func UnsupportedVersionReason() FaultReason { return unsupportedVersionReason }

// UnsupportedExecutableReason returns a statically incompatible executable reason.
func UnsupportedExecutableReason() FaultReason { return unsupportedExecutableReason }

// UnsupportedCapabilityReason returns a missing tested-capability reason.
func UnsupportedCapabilityReason() FaultReason { return unsupportedCapabilityReason }

// MissingRequiredFactReason returns a missing-prerequisite reason.
func MissingRequiredFactReason() FaultReason { return missingRequiredFactReason }

// EmptyConfigOverrideReason returns an explicitly empty override reason.
func EmptyConfigOverrideReason() FaultReason { return emptyConfigOverrideReason }

// RelativeConfigOverrideReason returns a non-absolute override reason.
func RelativeConfigOverrideReason() FaultReason { return relativeConfigOverrideReason }

// WrongOwnerConfigOverrideReason returns an untrusted-owner override reason.
func WrongOwnerConfigOverrideReason() FaultReason { return wrongOwnerConfigOverrideReason }

// SymlinkedConfigOverrideReason returns a symlinked override reason.
func SymlinkedConfigOverrideReason() FaultReason { return symlinkedConfigOverrideReason }

// UnsupportedVersionConfigOverrideReason returns an override unavailable for the detected Claude version.
func UnsupportedVersionConfigOverrideReason() FaultReason {
	return unsupportedVersionConfigOverrideReason
}

// PolicyProhibitedConfigOverrideReason returns an override prohibited by native policy.
func PolicyProhibitedConfigOverrideReason() FaultReason {
	return policyProhibitedConfigOverrideReason
}

// UntrustedHomeReason returns a HOME/trusted-account-home mismatch reason.
func UntrustedHomeReason() FaultReason { return untrustedHomeReason }

// WrongOwnerDirectoryReason returns an untrusted documented-directory owner reason.
func WrongOwnerDirectoryReason() FaultReason { return wrongOwnerDirectoryReason }

// SymlinkedDirectoryReason returns a symlinked documented-directory reason.
func SymlinkedDirectoryReason() FaultReason { return symlinkedDirectoryReason }

// UnsafeModeDirectoryReason returns an unsafe documented-directory mode reason.
func UnsafeModeDirectoryReason() FaultReason { return unsafeModeDirectoryReason }

// WrongTypeDirectoryReason returns a non-directory documented-resource reason.
func WrongTypeDirectoryReason() FaultReason { return wrongTypeDirectoryReason }

// UnsupportedFilesystemDirectoryReason returns an unsupported filesystem-profile reason.
func UnsupportedFilesystemDirectoryReason() FaultReason { return unsupportedFilesystemDirectoryReason }

// ProtectedRootOverlapDirectoryReason returns an overlap with an AI4J-protected root.
func ProtectedRootOverlapDirectoryReason() FaultReason { return protectedRootOverlapDirectoryReason }

// NewFaultReason parses a canonical discovery fault reason.
func NewFaultReason(value string) (FaultReason, error) {
	switch value {
	case "unsupported_host":
		return unsupportedHostReason, nil
	case "unsupported_version":
		return unsupportedVersionReason, nil
	case "unsupported_executable":
		return unsupportedExecutableReason, nil
	case "unsupported_capability":
		return unsupportedCapabilityReason, nil
	case "missing_required_fact":
		return missingRequiredFactReason, nil
	case "config_override_empty":
		return emptyConfigOverrideReason, nil
	case "config_override_relative":
		return relativeConfigOverrideReason, nil
	case "config_override_wrong_owner":
		return wrongOwnerConfigOverrideReason, nil
	case "config_override_symlinked":
		return symlinkedConfigOverrideReason, nil
	case "config_override_unsupported_version":
		return unsupportedVersionConfigOverrideReason, nil
	case "config_override_policy_prohibited":
		return policyProhibitedConfigOverrideReason, nil
	case "untrusted_home":
		return untrustedHomeReason, nil
	case "directory_wrong_owner":
		return wrongOwnerDirectoryReason, nil
	case "directory_symlinked":
		return symlinkedDirectoryReason, nil
	case "directory_unsafe_mode":
		return unsafeModeDirectoryReason, nil
	case "directory_wrong_type":
		return wrongTypeDirectoryReason, nil
	case "directory_unsupported_filesystem":
		return unsupportedFilesystemDirectoryReason, nil
	case "directory_protected_root_overlap":
		return protectedRootOverlapDirectoryReason, nil
	default:
		return FaultReason{}, newValidationError(CodeInvalidFaultReason)
	}
}

// String returns the canonical discovery fault reason.
func (r FaultReason) String() string {
	switch r {
	case unsupportedHostReason:
		return "unsupported_host"
	case unsupportedVersionReason:
		return "unsupported_version"
	case unsupportedExecutableReason:
		return "unsupported_executable"
	case unsupportedCapabilityReason:
		return "unsupported_capability"
	case missingRequiredFactReason:
		return "missing_required_fact"
	case emptyConfigOverrideReason:
		return "config_override_empty"
	case relativeConfigOverrideReason:
		return "config_override_relative"
	case wrongOwnerConfigOverrideReason:
		return "config_override_wrong_owner"
	case symlinkedConfigOverrideReason:
		return "config_override_symlinked"
	case unsupportedVersionConfigOverrideReason:
		return "config_override_unsupported_version"
	case policyProhibitedConfigOverrideReason:
		return "config_override_policy_prohibited"
	case untrustedHomeReason:
		return "untrusted_home"
	case wrongOwnerDirectoryReason:
		return "directory_wrong_owner"
	case symlinkedDirectoryReason:
		return "directory_symlinked"
	case unsafeModeDirectoryReason:
		return "directory_unsafe_mode"
	case wrongTypeDirectoryReason:
		return "directory_wrong_type"
	case unsupportedFilesystemDirectoryReason:
		return "directory_unsupported_filesystem"
	case protectedRootOverlapDirectoryReason:
		return "directory_protected_root_overlap"
	default:
		return "invalid"
	}
}

// Valid reports whether the reason is registered.
func (r FaultReason) Valid() bool {
	switch r {
	case unsupportedHostReason, unsupportedVersionReason, unsupportedExecutableReason, unsupportedCapabilityReason,
		missingRequiredFactReason, emptyConfigOverrideReason, relativeConfigOverrideReason,
		wrongOwnerConfigOverrideReason, symlinkedConfigOverrideReason,
		unsupportedVersionConfigOverrideReason, policyProhibitedConfigOverrideReason, untrustedHomeReason,
		wrongOwnerDirectoryReason, symlinkedDirectoryReason, unsafeModeDirectoryReason,
		wrongTypeDirectoryReason, unsupportedFilesystemDirectoryReason, protectedRootOverlapDirectoryReason:
		return true
	default:
		return false
	}
}

// EnvironmentFact is a closed actionable environment-fact identity.
type EnvironmentFact struct{ value uint8 }

var (
	hostFact                             = EnvironmentFact{value: 1}
	darwinVersionFact                    = EnvironmentFact{value: 2}
	gitExecutableFact                    = EnvironmentFact{value: 3}
	gitVersionFact                       = EnvironmentFact{value: 4}
	claudeExecutableFact                 = EnvironmentFact{value: 5}
	claudeVersionFact                    = EnvironmentFact{value: 6}
	claudeConfigurationFact              = EnvironmentFact{value: 7}
	claudeRulesFact                      = EnvironmentFact{value: 8}
	claudeConfigurationOverrideFact      = EnvironmentFact{value: 9}
	ai4jStateFact                        = EnvironmentFact{value: 10}
	ai4jRecoveryFact                     = EnvironmentFact{value: 11}
	capabilityProfileFact                = EnvironmentFact{value: 12}
	targetNativeValidationCapabilityFact = EnvironmentFact{value: 13}
	targetInspectionCapabilityFact       = EnvironmentFact{value: 14}
	targetMarketplaceCapabilityFact      = EnvironmentFact{value: 15}
	targetInstallationCapabilityFact     = EnvironmentFact{value: 16}
	targetEnablementCapabilityFact       = EnvironmentFact{value: 17}
	targetUpdateCapabilityFact           = EnvironmentFact{value: 18}
	targetUninstallCapabilityFact        = EnvironmentFact{value: 19}
	policyFact                           = EnvironmentFact{value: 20}
)

// HostFact returns the supported host-tuple fact.
func HostFact() EnvironmentFact { return hostFact }

// DarwinVersionFact returns the trusted macOS product-version fact.
func DarwinVersionFact() EnvironmentFact { return darwinVersionFact }

// GitExecutableFact returns the Git executable-identity fact.
func GitExecutableFact() EnvironmentFact { return gitExecutableFact }

// GitVersionFact returns the Git version fact.
func GitVersionFact() EnvironmentFact { return gitVersionFact }

// ClaudeExecutableFact returns the Claude executable-identity fact.
func ClaudeExecutableFact() EnvironmentFact { return claudeExecutableFact }

// ClaudeVersionFact returns the Claude version fact.
func ClaudeVersionFact() EnvironmentFact { return claudeVersionFact }

// ClaudeConfigurationFact returns the effective Claude configuration fact.
func ClaudeConfigurationFact() EnvironmentFact { return claudeConfigurationFact }

// ClaudeRulesFact returns the documented Claude rules-directory fact.
func ClaudeRulesFact() EnvironmentFact { return claudeRulesFact }

// ClaudeConfigurationOverrideFact returns the documented override fact.
func ClaudeConfigurationOverrideFact() EnvironmentFact { return claudeConfigurationOverrideFact }

// AI4JStateFact returns the private AI4J state-directory fact.
func AI4JStateFact() EnvironmentFact { return ai4jStateFact }

// AI4JRecoveryFact returns the private AI4J recovery-directory fact.
func AI4JRecoveryFact() EnvironmentFact { return ai4jRecoveryFact }

// CapabilityProfileFact returns the exact candidate profile fact.
func CapabilityProfileFact() EnvironmentFact { return capabilityProfileFact }

// TargetNativeValidationCapabilityFact returns the exact native-validation requirement.
func TargetNativeValidationCapabilityFact() EnvironmentFact {
	return targetNativeValidationCapabilityFact
}

// TargetInspectionCapabilityFact returns the exact inspection requirement.
func TargetInspectionCapabilityFact() EnvironmentFact { return targetInspectionCapabilityFact }

// TargetMarketplaceRegistrationCapabilityFact returns the exact marketplace-registration requirement.
func TargetMarketplaceRegistrationCapabilityFact() EnvironmentFact {
	return targetMarketplaceCapabilityFact
}

// TargetPluginInstallationCapabilityFact returns the exact plugin-installation requirement.
func TargetPluginInstallationCapabilityFact() EnvironmentFact {
	return targetInstallationCapabilityFact
}

// TargetEnablementCapabilityFact returns the exact enablement requirement.
func TargetEnablementCapabilityFact() EnvironmentFact { return targetEnablementCapabilityFact }

// TargetUpdateCapabilityFact returns the exact update requirement.
func TargetUpdateCapabilityFact() EnvironmentFact { return targetUpdateCapabilityFact }

// TargetUninstallCapabilityFact returns the exact uninstall requirement.
func TargetUninstallCapabilityFact() EnvironmentFact { return targetUninstallCapabilityFact }

// PolicyFact returns the native-policy observation fact.
func PolicyFact() EnvironmentFact { return policyFact }

// NewEnvironmentFact parses a canonical actionable fact identity.
func NewEnvironmentFact(value string) (EnvironmentFact, error) {
	for _, fact := range allEnvironmentFacts() {
		if value == fact.String() {
			return fact, nil
		}
	}
	return EnvironmentFact{}, newValidationError(CodeInvalidEnvironmentFact)
}

// String returns the canonical fact identity.
func (f EnvironmentFact) String() string {
	switch f {
	case hostFact:
		return "host"
	case darwinVersionFact:
		return "darwin_version"
	case gitExecutableFact:
		return "git_executable"
	case gitVersionFact:
		return "git_version"
	case claudeExecutableFact:
		return "claude_executable"
	case claudeVersionFact:
		return "claude_version"
	case claudeConfigurationFact:
		return "claude_configuration"
	case claudeRulesFact:
		return "claude_rules"
	case claudeConfigurationOverrideFact:
		return "claude_config_override"
	case ai4jStateFact:
		return "ai4j_state"
	case ai4jRecoveryFact:
		return "ai4j_recovery"
	case capabilityProfileFact:
		return "capability_profile"
	case targetNativeValidationCapabilityFact:
		return "target_native_validation_capability"
	case targetInspectionCapabilityFact:
		return "target_inspection_capability"
	case targetMarketplaceCapabilityFact:
		return "target_marketplace_registration_capability"
	case targetInstallationCapabilityFact:
		return "target_plugin_installation_capability"
	case targetEnablementCapabilityFact:
		return "target_enablement_capability"
	case targetUpdateCapabilityFact:
		return "target_update_capability"
	case targetUninstallCapabilityFact:
		return "target_uninstall_capability"
	case policyFact:
		return "policy"
	default:
		return "invalid"
	}
}

// Valid reports whether the fact is registered.
func (f EnvironmentFact) Valid() bool {
	for _, candidate := range allEnvironmentFacts() {
		if f == candidate {
			return true
		}
	}
	return false
}

func allEnvironmentFacts() []EnvironmentFact {
	return []EnvironmentFact{
		hostFact, darwinVersionFact, gitExecutableFact, gitVersionFact, claudeExecutableFact, claudeVersionFact,
		claudeConfigurationFact, claudeRulesFact, claudeConfigurationOverrideFact, ai4jStateFact, ai4jRecoveryFact,
		capabilityProfileFact, targetNativeValidationCapabilityFact, targetInspectionCapabilityFact,
		targetMarketplaceCapabilityFact, targetInstallationCapabilityFact, targetEnablementCapabilityFact,
		targetUpdateCapabilityFact, targetUninstallCapabilityFact, policyFact,
	}
}

func isTargetCapabilityFact(fact EnvironmentFact) bool {
	switch fact {
	case targetNativeValidationCapabilityFact, targetInspectionCapabilityFact, targetMarketplaceCapabilityFact,
		targetInstallationCapabilityFact, targetEnablementCapabilityFact, targetUpdateCapabilityFact,
		targetUninstallCapabilityFact:
		return true
	default:
		return false
	}
}

func isConfigOverrideReason(reason FaultReason) bool {
	switch reason {
	case emptyConfigOverrideReason, relativeConfigOverrideReason, wrongOwnerConfigOverrideReason,
		symlinkedConfigOverrideReason, unsupportedVersionConfigOverrideReason, policyProhibitedConfigOverrideReason:
		return true
	default:
		return false
	}
}

func isDirectoryQualificationReason(reason FaultReason) bool {
	switch reason {
	case wrongOwnerDirectoryReason, symlinkedDirectoryReason, unsafeModeDirectoryReason,
		wrongTypeDirectoryReason, unsupportedFilesystemDirectoryReason, protectedRootOverlapDirectoryReason:
		return true
	default:
		return false
	}
}

// EnvironmentFault is an immutable discovery-facing fault with only canonical
// fields. It cannot carry a rejected value, path, digest, or native output.
type EnvironmentFault struct {
	kind   FaultKind
	reason FaultReason
	fact   EnvironmentFact
}

// NewUnsupportedFault constructs a coherent unsupported environment fault.
func NewUnsupportedFault(reason FaultReason, fact EnvironmentFact) (EnvironmentFault, error) {
	candidate := EnvironmentFault{kind: unsupportedFaultKind, reason: reason, fact: fact}
	if !candidate.Valid() {
		return EnvironmentFault{}, newValidationError(CodeInvalidEnvironmentFault)
	}
	return candidate, nil
}

// NewIncompleteEnvironmentFault constructs a missing required-fact fault.
func NewIncompleteEnvironmentFault(fact EnvironmentFact) (EnvironmentFault, error) {
	candidate := EnvironmentFault{kind: incompleteFaultKind, reason: missingRequiredFactReason, fact: fact}
	if !candidate.Valid() {
		return EnvironmentFault{}, newValidationError(CodeInvalidEnvironmentFault)
	}
	return candidate, nil
}

// Kind returns the discovery fault category.
func (e EnvironmentFault) Kind() FaultKind { return e.kind }

// Reason returns the actionable canonical reason.
func (e EnvironmentFault) Reason() FaultReason { return e.reason }

// Fact returns the missing or unsupported canonical fact.
func (e EnvironmentFault) Fact() EnvironmentFact { return e.fact }

// Valid reports whether kind, reason, and fact form a coherent discovery fault.
func (e EnvironmentFault) Valid() bool {
	if !e.kind.Valid() || !e.reason.Valid() || !e.fact.Valid() {
		return false
	}
	if e.kind == incompleteFaultKind {
		return e.reason == missingRequiredFactReason
	}
	switch e.reason {
	case unsupportedHostReason:
		return e.fact == hostFact
	case unsupportedVersionReason:
		return e.fact == darwinVersionFact || e.fact == gitVersionFact || e.fact == claudeVersionFact
	case unsupportedExecutableReason:
		return e.fact == gitExecutableFact || e.fact == claudeExecutableFact
	case unsupportedCapabilityReason:
		return e.fact == capabilityProfileFact || isTargetCapabilityFact(e.fact)
	case untrustedHomeReason:
		return e.fact == claudeConfigurationFact
	case wrongOwnerDirectoryReason, symlinkedDirectoryReason, unsafeModeDirectoryReason,
		wrongTypeDirectoryReason, unsupportedFilesystemDirectoryReason, protectedRootOverlapDirectoryReason:
		return isDirectoryQualificationReason(e.reason) &&
			(e.fact == claudeConfigurationFact || e.fact == claudeRulesFact)
	default:
		return isConfigOverrideReason(e.reason) && e.fact == claudeConfigurationOverrideFact
	}
}

func (e EnvironmentFault) Error() string {
	if !e.Valid() {
		return "invalid_environment_fault"
	}
	return e.kind.String() + ":" + e.reason.String() + ":" + e.fact.String()
}

// Is supports stable environment sentinels and maps unsupported discoveries to
// the core unsupported-capability category.
func (e EnvironmentFault) Is(target error) bool {
	if !e.Valid() {
		return false
	}
	switch e.kind {
	case unsupportedFaultKind:
		return target == ErrUnsupported || target == fault.ErrUnsupportedCapability
	case incompleteFaultKind:
		return target == ErrIncompleteEnvironment
	default:
		return false
	}
}

// Format emits only canonical fault fields regardless of formatting flags.
func (e EnvironmentFault) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, e.Error()) }

// MarshalText emits only canonical fault fields.
func (e EnvironmentFault) MarshalText() ([]byte, error) {
	if !e.Valid() {
		return nil, newValidationError(CodeInvalidEnvironmentFault)
	}
	return []byte(e.Error()), nil
}

// MarshalJSON emits the stable discovery fault schema.
func (e EnvironmentFault) MarshalJSON() ([]byte, error) {
	if !e.Valid() {
		return nil, newValidationError(CodeInvalidEnvironmentFault)
	}
	return json.Marshal(struct {
		Kind   string `json:"kind"`
		Reason string `json:"reason"`
		Fact   string `json:"fact"`
	}{Kind: e.kind.String(), Reason: e.reason.String(), Fact: e.fact.String()})
}
