package qualification_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/lifecycle"
	claudeconfig "github.com/alx4j/ai4j/internal/target/claude/config"
	"github.com/alx4j/ai4j/internal/target/claude/config/qualification"
)

func TestResolveAndQualifyBindsOneHomeProofEndToEnd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		configuration      lifecycle.DirectoryLeafPresence
		rules              lifecycle.DirectoryLeafPresence
		wantCalls          []string
		wantConfigPresence environment.DirectoryPresence
		wantRulesPresence  environment.DirectoryPresence
		wantDerived        bool
		wantRulesProof     bool
	}{
		{
			name: "present configuration and rules", configuration: lifecycle.PresentDirectoryLeaf(), rules: lifecycle.PresentDirectoryLeaf(),
			wantCalls: []string{"home", ".claude", ".claude/rules"}, wantConfigPresence: environment.PresentDirectory(),
			wantRulesPresence: environment.PresentDirectory(), wantRulesProof: true,
		},
		{
			name: "present configuration and absent rules", configuration: lifecycle.PresentDirectoryLeaf(), rules: lifecycle.AbsentDirectoryLeaf(),
			wantCalls: []string{"home", ".claude", ".claude/rules"}, wantConfigPresence: environment.PresentDirectory(),
			wantRulesPresence: environment.AbsentDirectory(), wantRulesProof: true,
		},
		{
			name: "absent configuration derives rules absence", configuration: lifecycle.AbsentDirectoryLeaf(), rules: lifecycle.DirectoryLeafPresence{},
			wantCalls: []string{"home", ".claude"}, wantConfigPresence: environment.AbsentDirectory(),
			wantRulesPresence: environment.AbsentDirectory(), wantDerived: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source, _ := newProofFixture(t, test.configuration, test.rules)
			service, err := qualification.NewService(source)
			if err != nil {
				t.Fatal(err)
			}
			observation, err := service.ResolveAndQualify(
				t.Context(), mustStartup(t, "", false), mustVersion(t), mustPolicy(t, claudeconfig.AllowedOverrideDecision()),
			)
			if err != nil || !observation.Valid() {
				t.Fatalf("ResolveAndQualify() = %v, %v", observation, err)
			}
			if !reflect.DeepEqual(source.calls, test.wantCalls) {
				t.Fatalf("calls = %v, want %v", source.calls, test.wantCalls)
			}
			for _, passed := range source.passedHome {
				if passed != observation.HomeProof() {
					t.Fatal("qualification rebound the mapped home")
				}
			}
			if observation.Configuration().Presence() != test.wantConfigPresence ||
				observation.Rules().Presence() != test.wantRulesPresence ||
				observation.RulesAbsenceDerived() != test.wantDerived ||
				observation.Qualification() != qualification.ReadOnlyQualified() {
				t.Fatal("qualified observation facts changed")
			}
			_, gotRulesProof := observation.RulesProof()
			if gotRulesProof != test.wantRulesProof {
				t.Fatalf("RulesProof() present = %v, want %v", gotRulesProof, test.wantRulesProof)
			}
		})
	}
}

func TestResolveAndQualifyPreservesOverrideSourceAndExactPath(t *testing.T) {
	t.Parallel()

	seal := mustSeal(t, 0x72)
	home := mustHome(t, seal, qualificationHomeCanary, 13)
	configLeaf := mustObject(t, 14, lifecycle.CurrentUserOwner, 0o700)
	source := &proofSource{
		home: home,
		proofs: map[string]lifecycle.DirectoryLeafProof{
			"custom": mustLeaf(t, seal, home, "custom", qualificationHomeCanary+"/custom", lifecycle.PresentDirectoryLeaf(), home.Home(), configLeaf),
			"custom/rules": mustLeaf(
				t, seal, home, "custom/rules", qualificationHomeCanary+"/custom/rules",
				lifecycle.AbsentDirectoryLeaf(), configLeaf, lifecycle.DirectoryObjectProof{},
			),
		},
		proofErrs: make(map[string]error),
	}
	service, err := qualification.NewService(source)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := service.ResolveAndQualify(
		t.Context(),
		mustStartup(t, qualificationHomeCanary+"/custom", true),
		mustVersion(t),
		mustPolicy(t, claudeconfig.AllowedOverrideDecision()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Configuration().Source() != environment.EnvironmentOverrideDirectorySource() ||
		observation.Configuration().AbsolutePath() != qualificationHomeCanary+"/custom" ||
		observation.Rules().AbsolutePath() != qualificationHomeCanary+"/custom/rules" {
		t.Fatal("override mapping or exact path changed")
	}
}

func TestResolveAndQualifyRejectsProofRebindingAndRelationshipDrift(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*testing.T, *proofSource, lifecycle.ProofIssuerSeal)
	}{
		{
			name: "configuration proof from another home",
			mutate: func(t *testing.T, source *proofSource, _ lifecycle.ProofIssuerSeal) {
				otherSeal := mustSeal(t, 0x73)
				otherHome := mustHome(t, otherSeal, "/Users/other-secret-home", 23)
				source.proofs[".claude"] = mustLeaf(
					t, otherSeal, otherHome, ".claude", "/Users/other-secret-home/.claude",
					lifecycle.AbsentDirectoryLeaf(), otherHome.Home(), lifecycle.DirectoryObjectProof{},
				)
			},
		},
		{
			name: "configuration locator mismatch",
			mutate: func(t *testing.T, source *proofSource, seal lifecycle.ProofIssuerSeal) {
				source.proofs[".claude"] = mustLeaf(
					t, seal, source.home, ".claude", qualificationHomeCanary+"/different",
					lifecycle.AbsentDirectoryLeaf(), source.home.Home(), lifecycle.DirectoryObjectProof{},
				)
			},
		},
		{
			name: "rules parent is not configuration leaf",
			mutate: func(t *testing.T, source *proofSource, seal lifecycle.ProofIssuerSeal) {
				otherParent := mustObject(t, 6, lifecycle.CurrentUserOwner, 0o700)
				source.proofs[".claude/rules"] = mustLeaf(
					t, seal, source.home, ".claude/rules", qualificationHomeCanary+"/.claude/rules",
					lifecycle.AbsentDirectoryLeaf(), otherParent, lifecycle.DirectoryObjectProof{},
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source, seal := newProofFixture(t, lifecycle.PresentDirectoryLeaf(), lifecycle.AbsentDirectoryLeaf())
			test.mutate(t, source, seal)
			service, err := qualification.NewService(source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.ResolveAndQualify(
				t.Context(), mustStartup(t, "", false), mustVersion(t), mustPolicy(t, claudeconfig.AllowedOverrideDecision()),
			)
			requireQualificationCode(t, err, qualification.CodeInvalidProof)
		})
	}
}

func TestResolveAndQualifyMapsClosedIssuesWithoutOperationalMisclassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		issue      lifecycle.DirectoryQualificationIssue
		override   bool
		wantKind   environment.FaultKind
		wantReason environment.FaultReason
		wantFact   environment.EnvironmentFact
		wantCode   qualification.ErrorCode
	}{
		{name: "missing home account", issue: lifecycle.TrustedAccountUnavailableIssue(), wantKind: environment.IncompleteEnvironmentFaultKind(), wantReason: environment.MissingRequiredFactReason(), wantFact: environment.ClaudeConfigurationFact()},
		{name: "missing intermediate", issue: lifecycle.MissingIntermediateIssue(), wantKind: environment.IncompleteEnvironmentFaultKind(), wantReason: environment.MissingRequiredFactReason(), wantFact: environment.ClaudeConfigurationFact()},
		{name: "default wrong owner", issue: lifecycle.WrongDirectoryOwnerIssue(), wantKind: environment.UnsupportedFaultKind(), wantReason: environment.WrongOwnerDirectoryReason(), wantFact: environment.ClaudeConfigurationFact()},
		{name: "override wrong owner", issue: lifecycle.WrongDirectoryOwnerIssue(), override: true, wantKind: environment.UnsupportedFaultKind(), wantReason: environment.WrongOwnerConfigOverrideReason(), wantFact: environment.ClaudeConfigurationOverrideFact()},
		{name: "override symlink", issue: lifecycle.SymlinkedDirectoryIssue(), override: true, wantKind: environment.UnsupportedFaultKind(), wantReason: environment.SymlinkedConfigOverrideReason(), wantFact: environment.ClaudeConfigurationOverrideFact()},
		{name: "unsafe mode", issue: lifecycle.UnsafeDirectoryModeIssue(), wantKind: environment.UnsupportedFaultKind(), wantReason: environment.UnsafeModeDirectoryReason(), wantFact: environment.ClaudeConfigurationFact()},
		{name: "wrong type", issue: lifecycle.WrongDirectoryTypeIssue(), wantKind: environment.UnsupportedFaultKind(), wantReason: environment.WrongTypeDirectoryReason(), wantFact: environment.ClaudeConfigurationFact()},
		{name: "unsupported filesystem", issue: lifecycle.UnsupportedFilesystemIssue(), wantKind: environment.UnsupportedFaultKind(), wantReason: environment.UnsupportedFilesystemDirectoryReason(), wantFact: environment.ClaudeConfigurationFact()},
		{name: "protected overlap", issue: lifecycle.ProtectedRootOverlapIssue(), wantKind: environment.UnsupportedFaultKind(), wantReason: environment.ProtectedRootOverlapDirectoryReason(), wantFact: environment.ClaudeConfigurationFact()},
		{name: "override protected overlap", issue: lifecycle.ProtectedRootOverlapIssue(), override: true, wantKind: environment.UnsupportedFaultKind(), wantReason: environment.PolicyProhibitedConfigOverrideReason(), wantFact: environment.ClaudeConfigurationOverrideFact()},
		{name: "invalid locator", issue: lifecycle.InvalidDirectoryLocatorIssue(), wantCode: qualification.CodeInvalidProof},
		{name: "identity race", issue: lifecycle.DirectoryIdentityChangedIssue(), wantCode: qualification.CodeDirectoryInspectionFailed},
		{name: "observation failure", issue: lifecycle.DirectoryObservationFailedIssue(), wantCode: qualification.CodeDirectoryInspectionFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source, _ := newProofFixture(t, lifecycle.AbsentDirectoryLeaf(), lifecycle.DirectoryLeafPresence{})
			override := ""
			overridePresent := false
			path := ".claude"
			if test.override {
				override = qualificationHomeCanary + "/custom"
				overridePresent = true
				path = "custom"
				source.proofs[path] = source.proofs[".claude"]
			}
			source.proofErrs[path] = test.issue
			service, err := qualification.NewService(source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.ResolveAndQualify(
				t.Context(), mustStartup(t, override, overridePresent), mustVersion(t), mustPolicy(t, claudeconfig.AllowedOverrideDecision()),
			)
			if test.wantCode.Valid() {
				requireQualificationCode(t, err, test.wantCode)
				return
			}
			var fault environment.EnvironmentFault
			if !errors.As(err, &fault) || fault.Kind() != test.wantKind || fault.Reason() != test.wantReason || fault.Fact() != test.wantFact {
				t.Fatalf("fault = %v, want %s:%s:%s", err, test.wantKind.String(), test.wantReason.String(), test.wantFact.String())
			}
		})
	}
}

func TestResolveAndQualifyPreservesContextCategories(t *testing.T) {
	t.Parallel()

	source, _ := newProofFixture(t, lifecycle.AbsentDirectoryLeaf(), lifecycle.DirectoryLeafPresence{})
	service, err := qualification.NewService(source)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.ResolveAndQualify(cancelled, mustStartup(t, "", false), mustVersion(t), mustPolicy(t, claudeconfig.AllowedOverrideDecision()))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
	_, err = service.ResolveAndQualify(nil, mustStartup(t, "", false), mustVersion(t), mustPolicy(t, claudeconfig.AllowedOverrideDecision()))
	requireQualificationCode(t, err, qualification.CodeInvalidContext)
}

func TestNewServiceRejectsNilAndTypedNilSource(t *testing.T) {
	t.Parallel()

	var typedNil *proofSource
	for _, source := range []qualification.ProofSource{nil, typedNil} {
		service, err := qualification.NewService(source)
		if service != nil {
			t.Fatal("nil proof source produced a service")
		}
		requireQualificationCode(t, err, qualification.CodeInvalidService)
	}
}

func requireQualificationCode(t *testing.T, err error, want qualification.ErrorCode) {
	t.Helper()
	var typed qualification.Error
	if !errors.As(err, &typed) || typed.Code() != want {
		t.Fatalf("error = %v, want code %s", err, want)
	}
}
