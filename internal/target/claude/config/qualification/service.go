package qualification

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/pathsafe"
	claudeconfig "github.com/alx4j/ai4j/internal/target/claude/config"
)

// ProofSource is the target-owned view of a read-only neutral Bootstrap. It
// accepts no absolute directory supplied by the target and exposes no generic
// filesystem operation.
type ProofSource interface {
	InspectUserHome(context.Context) (lifecycle.UserHomeProof, error)
	QualifyUserDirectory(context.Context, lifecycle.UserHomeProof, pathsafe.RelativePath) (lifecycle.DirectoryLeafProof, error)
}

// Service binds pure mapping and host qualification to one retained home
// proof. It stores no request context.
type Service struct{ source ProofSource }

func NewService(source ProofSource) (*Service, error) {
	if nilInterface(source) {
		return nil, newError(CodeInvalidService)
	}
	return &Service{source: source}, nil
}

// ResolveAndQualify obtains exactly one home proof, performs pure mapping
// against that proof's locator, and qualifies every reachable documented leaf
// against the same retained proof.
func (s *Service) ResolveAndQualify(
	ctx context.Context,
	startup claudeconfig.StartupInput,
	claudeVersion environment.ToolVersion,
	overridePolicy claudeconfig.OverridePolicy,
) (Observation, error) {
	if s == nil || nilInterface(s.source) {
		return Observation{}, newError(CodeInvalidService)
	}
	if ctx == nil {
		return Observation{}, newError(CodeInvalidContext)
	}
	if err := contextFailure(ctx, ctx.Err()); err != nil {
		return Observation{}, err
	}

	home, err := s.source.InspectUserHome(ctx)
	if err != nil {
		return Observation{}, classifyProofSourceError(ctx, err, environment.ClaudeConfigurationDirectory(), environment.DirectorySource{})
	}
	if !home.Valid() {
		return Observation{}, newError(CodeInvalidProof)
	}
	trustedHome, err := claudeconfig.NewTrustedHome(home.Locator().Value())
	if err != nil {
		return Observation{}, newError(CodeInvalidProof)
	}
	candidate, err := claudeconfig.ResolveCandidate(ctx, startup, trustedHome, claudeVersion, overridePolicy)
	if err != nil {
		return Observation{}, err
	}

	configurationCandidate := candidate.Configuration()
	configurationProof, err := s.source.QualifyUserDirectory(ctx, home, configurationCandidate.RelativePath())
	if err != nil {
		return Observation{}, classifyProofSourceError(ctx, err, configurationCandidate.Role(), configurationCandidate.Source())
	}
	if !proofMatches(home, configurationCandidate.RelativePath(), configurationProof) {
		return Observation{}, newError(CodeInvalidProof)
	}
	configuration, err := directoryFromProof(configurationCandidate, configurationProof)
	if err != nil {
		return Observation{}, err
	}

	rulesCandidate := candidate.Rules()
	if configurationProof.Presence() == lifecycle.AbsentDirectoryLeaf() {
		rules, deriveErr := derivedAbsentDirectory(home, rulesCandidate)
		if deriveErr != nil {
			return Observation{}, deriveErr
		}
		return newObservation(candidate, home, configurationProof, lifecycle.DirectoryLeafProof{}, configuration, rules, true)
	}

	rulesProof, err := s.source.QualifyUserDirectory(ctx, home, rulesCandidate.RelativePath())
	if err != nil {
		return Observation{}, classifyProofSourceError(ctx, err, rulesCandidate.Role(), rulesCandidate.Source())
	}
	configurationLeaf, present := configurationProof.Leaf()
	if !present || !proofMatches(home, rulesCandidate.RelativePath(), rulesProof) || rulesProof.Parent() != configurationLeaf {
		return Observation{}, newError(CodeInvalidProof)
	}
	rules, err := directoryFromProof(rulesCandidate, rulesProof)
	if err != nil {
		return Observation{}, err
	}
	return newObservation(candidate, home, configurationProof, rulesProof, configuration, rules, false)
}

func proofMatches(
	home lifecycle.UserHomeProof,
	relative pathsafe.RelativePath,
	proof lifecycle.DirectoryLeafProof,
) bool {
	if !home.Valid() || !relative.Valid() || !proof.Valid() || proof.HomeProof() != home ||
		proof.RelativePath() != relative || proof.Root() != home.Home() {
		return false
	}
	want, ok := absoluteLocator(home, relative)
	return ok && proof.Locator().Value() == want
}

func directoryFromProof(
	candidate claudeconfig.DirectoryCandidate,
	proof lifecycle.DirectoryLeafProof,
) (environment.Directory, error) {
	presence, ok := environmentPresence(proof.Presence())
	if !candidate.Valid() || !proof.Valid() || !ok {
		return environment.Directory{}, newError(CodeInvalidProof)
	}
	directory, err := environment.NewDirectory(candidate.Role(), candidate.Source(), presence, proof.Locator().Value())
	if err != nil {
		return environment.Directory{}, newError(CodeInvalidProof)
	}
	return directory, nil
}

func derivedAbsentDirectory(
	home lifecycle.UserHomeProof,
	candidate claudeconfig.DirectoryCandidate,
) (environment.Directory, error) {
	absolute, ok := absoluteLocator(home, candidate.RelativePath())
	if !candidate.Valid() || !ok {
		return environment.Directory{}, newError(CodeInvalidProof)
	}
	directory, err := environment.NewDirectory(
		candidate.Role(), candidate.Source(), environment.AbsentDirectory(), absolute,
	)
	if err != nil {
		return environment.Directory{}, newError(CodeInvalidProof)
	}
	return directory, nil
}

func absoluteLocator(home lifecycle.UserHomeProof, relative pathsafe.RelativePath) (string, bool) {
	if !home.Valid() || !relative.Valid() {
		return "", false
	}
	root := home.Locator().Value()
	if root == "" || strings.HasSuffix(root, "/") {
		return "", false
	}
	return root + "/" + relative.String(), true
}

func environmentPresence(presence lifecycle.DirectoryLeafPresence) (environment.DirectoryPresence, bool) {
	switch presence {
	case lifecycle.PresentDirectoryLeaf():
		return environment.PresentDirectory(), true
	case lifecycle.AbsentDirectoryLeaf():
		return environment.AbsentDirectory(), true
	default:
		return environment.DirectoryPresence{}, false
	}
}

func classifyProofSourceError(
	ctx context.Context,
	err error,
	role environment.DirectoryRole,
	source environment.DirectorySource,
) error {
	if contextErr := contextFailure(ctx, err); contextErr != nil {
		return contextErr
	}
	var issue lifecycle.DirectoryQualificationIssue
	if !errors.As(err, &issue) || !issue.Valid() {
		return newError(CodeDirectoryInspectionFailed)
	}
	fact := directoryFact(role)
	if !fact.Valid() {
		return newError(CodeInvalidProof)
	}
	switch issue {
	case lifecycle.TrustedAccountUnavailableIssue(), lifecycle.MissingIntermediateIssue():
		return incomplete(fact)
	case lifecycle.InvalidDirectoryLocatorIssue():
		return newError(CodeInvalidProof)
	case lifecycle.DirectoryIdentityChangedIssue(), lifecycle.DirectoryObservationFailedIssue():
		return newError(CodeDirectoryInspectionFailed)
	case lifecycle.WrongDirectoryOwnerIssue():
		if role == environment.ClaudeConfigurationDirectory() && source == environment.EnvironmentOverrideDirectorySource() {
			return unsupported(environment.WrongOwnerConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact())
		}
		return unsupported(environment.WrongOwnerDirectoryReason(), fact)
	case lifecycle.SymlinkedDirectoryIssue():
		if role == environment.ClaudeConfigurationDirectory() && source == environment.EnvironmentOverrideDirectorySource() {
			return unsupported(environment.SymlinkedConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact())
		}
		return unsupported(environment.SymlinkedDirectoryReason(), fact)
	case lifecycle.UnsafeDirectoryModeIssue():
		return unsupported(environment.UnsafeModeDirectoryReason(), fact)
	case lifecycle.WrongDirectoryTypeIssue():
		return unsupported(environment.WrongTypeDirectoryReason(), fact)
	case lifecycle.UnsupportedFilesystemIssue():
		return unsupported(environment.UnsupportedFilesystemDirectoryReason(), fact)
	case lifecycle.ProtectedRootOverlapIssue():
		if role == environment.ClaudeConfigurationDirectory() && source == environment.EnvironmentOverrideDirectorySource() {
			return unsupported(environment.PolicyProhibitedConfigOverrideReason(), environment.ClaudeConfigurationOverrideFact())
		}
		return unsupported(environment.ProtectedRootOverlapDirectoryReason(), fact)
	default:
		return newError(CodeInvalidProof)
	}
}

func directoryFact(role environment.DirectoryRole) environment.EnvironmentFact {
	switch role {
	case environment.ClaudeConfigurationDirectory():
		return environment.ClaudeConfigurationFact()
	case environment.ClaudeRulesDirectory():
		return environment.ClaudeRulesFact()
	default:
		return environment.EnvironmentFact{}
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

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
