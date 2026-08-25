package environment_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/fault"
)

func TestEnvironmentFaultEnumsAreClosed(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		value string
		want  environment.FaultKind
	}{
		{"unsupported", environment.UnsupportedFaultKind()},
		{"incomplete_environment", environment.IncompleteEnvironmentFaultKind()},
	} {
		got, err := environment.NewFaultKind(test.value)
		if err != nil || got != test.want || !got.Valid() || got.String() != test.value {
			t.Fatalf("NewFaultKind(%q) = %v, %v", test.value, got, err)
		}
	}
	for _, test := range []struct {
		value string
		want  environment.FaultReason
	}{
		{"unsupported_host", environment.UnsupportedHostReason()},
		{"unsupported_version", environment.UnsupportedVersionReason()},
		{"unsupported_executable", environment.UnsupportedExecutableReason()},
		{"unsupported_capability", environment.UnsupportedCapabilityReason()},
		{"missing_required_fact", environment.MissingRequiredFactReason()},
		{"config_override_empty", environment.EmptyConfigOverrideReason()},
		{"config_override_relative", environment.RelativeConfigOverrideReason()},
		{"config_override_wrong_owner", environment.WrongOwnerConfigOverrideReason()},
		{"config_override_symlinked", environment.SymlinkedConfigOverrideReason()},
		{"config_override_unsupported_version", environment.UnsupportedVersionConfigOverrideReason()},
		{"config_override_policy_prohibited", environment.PolicyProhibitedConfigOverrideReason()},
		{"untrusted_home", environment.UntrustedHomeReason()},
		{"directory_wrong_owner", environment.WrongOwnerDirectoryReason()},
		{"directory_symlinked", environment.SymlinkedDirectoryReason()},
		{"directory_unsafe_mode", environment.UnsafeModeDirectoryReason()},
		{"directory_wrong_type", environment.WrongTypeDirectoryReason()},
		{"directory_unsupported_filesystem", environment.UnsupportedFilesystemDirectoryReason()},
		{"directory_protected_root_overlap", environment.ProtectedRootOverlapDirectoryReason()},
	} {
		got, err := environment.NewFaultReason(test.value)
		if err != nil || got != test.want || !got.Valid() || got.String() != test.value {
			t.Fatalf("NewFaultReason(%q) = %v, %v", test.value, got, err)
		}
	}
	facts := allFaultFacts()
	for _, want := range facts {
		got, err := environment.NewEnvironmentFact(want.String())
		if err != nil || got != want || !got.Valid() {
			t.Fatalf("NewEnvironmentFact(%q) = %v, %v", want.String(), got, err)
		}
	}
	_, kindErr := environment.NewFaultKind("future")
	requireCode(t, kindErr, environment.CodeInvalidFaultKind)
	_, reasonErr := environment.NewFaultReason("future")
	requireCode(t, reasonErr, environment.CodeInvalidFaultReason)
	_, factErr := environment.NewEnvironmentFact("future")
	requireCode(t, factErr, environment.CodeInvalidEnvironmentFact)
	if (environment.FaultKind{}).Valid() || (environment.FaultReason{}).Valid() || (environment.EnvironmentFact{}).Valid() {
		t.Fatal("zero fault values must be invalid")
	}
}

func TestUnsupportedFaultsPreserveCategoryAndCanonicalFields(t *testing.T) {
	t.Parallel()

	validPairs := map[string]struct{}{
		faultPair(environment.UnsupportedHostReason(), environment.HostFact()):                                          {},
		faultPair(environment.UnsupportedVersionReason(), environment.DarwinVersionFact()):                              {},
		faultPair(environment.UnsupportedVersionReason(), environment.GitVersionFact()):                                 {},
		faultPair(environment.UnsupportedVersionReason(), environment.ClaudeVersionFact()):                              {},
		faultPair(environment.UnsupportedExecutableReason(), environment.GitExecutableFact()):                           {},
		faultPair(environment.UnsupportedExecutableReason(), environment.ClaudeExecutableFact()):                        {},
		faultPair(environment.UnsupportedCapabilityReason(), environment.CapabilityProfileFact()):                       {},
		faultPair(environment.UnsupportedCapabilityReason(), environment.TargetNativeValidationCapabilityFact()):        {},
		faultPair(environment.UnsupportedCapabilityReason(), environment.TargetInspectionCapabilityFact()):              {},
		faultPair(environment.UnsupportedCapabilityReason(), environment.TargetMarketplaceRegistrationCapabilityFact()): {},
		faultPair(environment.UnsupportedCapabilityReason(), environment.TargetPluginInstallationCapabilityFact()):      {},
		faultPair(environment.UnsupportedCapabilityReason(), environment.TargetEnablementCapabilityFact()):              {},
		faultPair(environment.UnsupportedCapabilityReason(), environment.TargetUpdateCapabilityFact()):                  {},
		faultPair(environment.UnsupportedCapabilityReason(), environment.TargetUninstallCapabilityFact()):               {},
		faultPair(environment.EmptyConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact()):               {},
		faultPair(environment.RelativeConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact()):            {},
		faultPair(environment.WrongOwnerConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact()):          {},
		faultPair(environment.SymlinkedConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact()):           {},
		faultPair(environment.UnsupportedVersionConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact()):  {},
		faultPair(environment.PolicyProhibitedConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact()):    {},
		faultPair(environment.UntrustedHomeReason(), environment.ClaudeConfigurationFact()):                             {},
	}
	for _, reason := range []environment.FaultReason{
		environment.WrongOwnerDirectoryReason(), environment.SymlinkedDirectoryReason(),
		environment.UnsafeModeDirectoryReason(), environment.WrongTypeDirectoryReason(),
		environment.UnsupportedFilesystemDirectoryReason(), environment.ProtectedRootOverlapDirectoryReason(),
	} {
		validPairs[faultPair(reason, environment.ClaudeConfigurationFact())] = struct{}{}
		validPairs[faultPair(reason, environment.ClaudeRulesFact())] = struct{}{}
	}
	for _, reason := range allFaultReasons() {
		for _, fact := range allFaultFacts() {
			got, err := environment.NewUnsupportedFault(reason, fact)
			_, wantValid := validPairs[faultPair(reason, fact)]
			if !wantValid {
				requireCode(t, err, environment.CodeInvalidEnvironmentFault)
				continue
			}
			if err != nil || !got.Valid() || got.Kind() != environment.UnsupportedFaultKind() || got.Reason() != reason || got.Fact() != fact {
				t.Fatalf("NewUnsupportedFault(%s, %s) = %v, %v", reason.String(), fact.String(), got, err)
			}
			if !errors.Is(got, environment.ErrUnsupported) || !errors.Is(got, fault.ErrUnsupportedCapability) || errors.Is(got, environment.ErrIncompleteEnvironment) {
				t.Fatalf("unsupported category mapping failed for %v", got)
			}
		}
	}
	_, reasonErr := environment.NewUnsupportedFault(environment.FaultReason{}, environment.HostFact())
	requireCode(t, reasonErr, environment.CodeInvalidEnvironmentFault)
	_, factErr := environment.NewUnsupportedFault(environment.UnsupportedHostReason(), environment.EnvironmentFact{})
	requireCode(t, factErr, environment.CodeInvalidEnvironmentFault)
}

func TestIncompleteEnvironmentFaultCoversEveryRequiredFact(t *testing.T) {
	t.Parallel()

	facts := allFaultFacts()
	for _, fact := range facts {
		got, err := environment.NewIncompleteEnvironmentFault(fact)
		if err != nil || !got.Valid() || got.Kind() != environment.IncompleteEnvironmentFaultKind() ||
			got.Reason() != environment.MissingRequiredFactReason() || got.Fact() != fact {
			t.Fatalf("NewIncompleteEnvironmentFault(%s) = %v, %v", fact.String(), got, err)
		}
		if !errors.Is(got, environment.ErrIncompleteEnvironment) || errors.Is(got, environment.ErrUnsupported) || errors.Is(got, fault.ErrUnsupportedCapability) {
			t.Fatalf("incomplete category mapping failed for %v", got)
		}
	}
	_, err := environment.NewIncompleteEnvironmentFault(environment.EnvironmentFact{})
	requireCode(t, err, environment.CodeInvalidEnvironmentFault)
	if (environment.EnvironmentFault{}).Valid() {
		t.Fatal("zero environment fault must be invalid")
	}
	_, marshalErr := json.Marshal(environment.EnvironmentFault{})
	requireCode(t, marshalErr, environment.CodeInvalidEnvironmentFault)
}

func allFaultReasons() []environment.FaultReason {
	return []environment.FaultReason{
		environment.UnsupportedHostReason(), environment.UnsupportedVersionReason(), environment.UnsupportedExecutableReason(),
		environment.UnsupportedCapabilityReason(), environment.MissingRequiredFactReason(), environment.EmptyConfigOverrideReason(),
		environment.RelativeConfigOverrideReason(), environment.WrongOwnerConfigOverrideReason(), environment.SymlinkedConfigOverrideReason(),
		environment.UnsupportedVersionConfigOverrideReason(), environment.PolicyProhibitedConfigOverrideReason(),
		environment.UntrustedHomeReason(), environment.WrongOwnerDirectoryReason(), environment.SymlinkedDirectoryReason(),
		environment.UnsafeModeDirectoryReason(), environment.WrongTypeDirectoryReason(),
		environment.UnsupportedFilesystemDirectoryReason(), environment.ProtectedRootOverlapDirectoryReason(),
	}
}

func allFaultFacts() []environment.EnvironmentFact {
	return []environment.EnvironmentFact{
		environment.HostFact(), environment.DarwinVersionFact(), environment.GitExecutableFact(), environment.GitVersionFact(),
		environment.ClaudeExecutableFact(), environment.ClaudeVersionFact(), environment.ClaudeConfigurationFact(),
		environment.ClaudeRulesFact(), environment.ClaudeConfigurationOverrideFact(), environment.AI4JStateFact(),
		environment.AI4JRecoveryFact(), environment.CapabilityProfileFact(), environment.TargetNativeValidationCapabilityFact(),
		environment.TargetInspectionCapabilityFact(), environment.TargetMarketplaceRegistrationCapabilityFact(),
		environment.TargetPluginInstallationCapabilityFact(), environment.TargetEnablementCapabilityFact(),
		environment.TargetUpdateCapabilityFact(), environment.TargetUninstallCapabilityFact(), environment.PolicyFact(),
	}
}

func faultPair(reason environment.FaultReason, fact environment.EnvironmentFact) string {
	return reason.String() + ":" + fact.String()
}

func TestEnvironmentFaultSchemaAndFormattingExposeOnlyCanonicalFields(t *testing.T) {
	t.Parallel()

	value, err := environment.NewUnsupportedFault(environment.UnsupportedVersionReason(), environment.ClaudeVersionFact())
	if err != nil {
		t.Fatal(err)
	}
	formatted := fmt.Sprintf("%v|%+v|%#v|%q", value, value, value, value)
	encoded, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	wantJSON := `{"kind":"unsupported","reason":"unsupported_version","fact":"claude_version"}`
	if string(encoded) != wantJSON {
		t.Fatalf("MarshalJSON() = %s, want %s", encoded, wantJSON)
	}
	for _, forbidden := range []string{pathCanary, testDigest, "/Users/", "native-output-canary"} {
		if strings.Contains(formatted, forbidden) || strings.Contains(string(encoded), forbidden) {
			t.Fatalf("fault disclosed %q", forbidden)
		}
	}
}
