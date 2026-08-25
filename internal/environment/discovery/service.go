package discovery

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/fault"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

// ExecutableInspector is the only filesystem capability required by T2.
type ExecutableInspector interface {
	CheckExecutable(context.Context, lifecycle.ExecutableRequest) (lifecycle.ExecutableObservation, error)
}

// Service performs synchronous read-only prerequisite discovery.
type Service struct {
	host        lifecycle.HostInspector
	executables ExecutableInspector
	processes   lifecycle.ProcessRunner
	profile     ProbeProfile
}

// New constructs a discovery service from narrow read-only ports.
func New(
	host lifecycle.HostInspector,
	executables ExecutableInspector,
	processes lifecycle.ProcessRunner,
	profile ProbeProfile,
) (*Service, error) {
	if nilInterface(host) || nilInterface(executables) || nilInterface(processes) || !profile.Valid() {
		return nil, newError(CodeInvalidService)
	}
	return &Service{host: host, executables: executables, processes: processes, profile: profile}, nil
}

// DiscoverPrerequisites qualifies the host and both executable proofs before
// invoking either fixed version probe.
func (s *Service) DiscoverPrerequisites(ctx context.Context) (PrerequisiteObservation, error) {
	if s == nil || nilInterface(s.host) || nilInterface(s.executables) || nilInterface(s.processes) || !s.profile.Valid() {
		return PrerequisiteObservation{}, newError(CodeInvalidService)
	}
	if ctx == nil {
		return PrerequisiteObservation{}, newError(CodeInvalidContext)
	}
	if err := contextFailure(ctx, ctx.Err()); err != nil {
		return PrerequisiteObservation{}, err
	}

	host, err := s.discoverHost(ctx)
	if err != nil {
		return PrerequisiteObservation{}, err
	}
	gitObservation, err := s.discoverExecutable(ctx, environment.GitTool())
	if err != nil {
		return PrerequisiteObservation{}, err
	}
	claudeObservation, err := s.discoverExecutable(ctx, environment.ClaudeTool())
	if err != nil {
		return PrerequisiteObservation{}, err
	}
	if sameExecutable(gitObservation, claudeObservation) {
		return PrerequisiteObservation{}, unsupported(environment.UnsupportedExecutableReason(), environment.ClaudeExecutableFact())
	}

	gitVersion, err := s.probeVersion(ctx, environment.GitTool(), gitObservation)
	if err != nil {
		return PrerequisiteObservation{}, err
	}
	claudeVersion, err := s.probeVersion(ctx, environment.ClaudeTool(), claudeObservation)
	if err != nil {
		return PrerequisiteObservation{}, err
	}
	gitIdentity, err := environment.NewExecutableIdentity(environment.GitTool(), gitVersion, gitObservation)
	if err != nil {
		return PrerequisiteObservation{}, unsupported(environment.UnsupportedExecutableReason(), environment.GitExecutableFact())
	}
	claudeIdentity, err := environment.NewExecutableIdentity(environment.ClaudeTool(), claudeVersion, claudeObservation)
	if err != nil {
		return PrerequisiteObservation{}, unsupported(environment.UnsupportedExecutableReason(), environment.ClaudeExecutableFact())
	}
	return NewPrerequisiteObservation(host, gitIdentity, claudeIdentity)
}

func (s *Service) discoverHost(ctx context.Context) (environment.HostTuple, error) {
	observation, err := s.host.InspectHost(ctx, lifecycle.HostInspectionRequest{Host: domain.DarwinHost()})
	if err != nil {
		if contextErr := contextFailure(ctx, err); contextErr != nil {
			return environment.HostTuple{}, contextErr
		}
		return environment.HostTuple{}, newError(CodeHostInspectionFailed)
	}
	if observation.Host != domain.DarwinHost() || observation.OS != "darwin" || observation.Arch != "arm64" {
		return environment.HostTuple{}, unsupported(environment.UnsupportedHostReason(), environment.HostFact())
	}
	if observation.OSVersion == "" {
		return environment.HostTuple{}, incomplete(environment.DarwinVersionFact())
	}
	version, err := environment.NewDarwinVersion(observation.OSVersion)
	if err != nil {
		return environment.HostTuple{}, unsupported(environment.UnsupportedVersionReason(), environment.DarwinVersionFact())
	}
	host, err := environment.NewHostTuple(
		observation.Host,
		environment.DarwinOperatingSystem(),
		environment.ARM64Architecture(),
		version,
	)
	if err != nil {
		return environment.HostTuple{}, unsupported(environment.UnsupportedHostReason(), environment.HostFact())
	}
	return host, nil
}

func (s *Service) discoverExecutable(ctx context.Context, tool environment.Tool) (lifecycle.ExecutableObservation, error) {
	if err := contextFailure(ctx, ctx.Err()); err != nil {
		return lifecycle.ExecutableObservation{}, err
	}
	observation, err := s.executables.CheckExecutable(ctx, lifecycle.ExecutableRequest{
		Candidate: tool.String(),
		Authority: lifecycle.TrustedUserOrSystemAuthority,
	})
	if err != nil {
		if contextErr := contextFailure(ctx, err); contextErr != nil {
			return lifecycle.ExecutableObservation{}, contextErr
		}
		if errors.Is(err, lifecycle.ErrExecutableNotFound) {
			return lifecycle.ExecutableObservation{}, incomplete(executableFact(tool))
		}
		if stableUnsupportedExecutable(err) {
			return lifecycle.ExecutableObservation{}, unsupported(environment.UnsupportedExecutableReason(), executableFact(tool))
		}
		return lifecycle.ExecutableObservation{}, newError(CodeExecutableInspectionFailed)
	}
	if !supportedExecutable(observation) {
		return lifecycle.ExecutableObservation{}, unsupported(environment.UnsupportedExecutableReason(), executableFact(tool))
	}
	return observation, nil
}

func (s *Service) probeVersion(
	ctx context.Context,
	tool environment.Tool,
	observation lifecycle.ExecutableObservation,
) (environment.ToolVersion, error) {
	if err := contextFailure(ctx, ctx.Err()); err != nil {
		return environment.ToolVersion{}, err
	}
	expectation := executableExpectation(observation)
	request := lifecycle.ProcessRequest{
		Executable:         observation.ResolvedPath,
		Arguments:          []string{"--version"},
		EnvironmentProfile: s.environmentProfile(tool),
		Environment:        s.environment(tool),
		Timeout:            s.timeout(tool),
		OutputLimitBytes:   probeOutputLimitBytes,
		StdoutMode:         lifecycle.SanitizedTextOutput,
		StderrMode:         lifecycle.SanitizedTextOutput,
		TerminationGrace:   probeTerminationGrace,
		ExpectedExecutable: expectation,
	}
	if !request.Valid() {
		return environment.ToolVersion{}, newError(CodeInvalidProfile)
	}
	result, err := s.processes.RunProcess(ctx, request)
	if contextErr := processContextFailure(ctx, result, err); contextErr != nil {
		return environment.ToolVersion{}, contextErr
	}
	if err != nil {
		return environment.ToolVersion{}, newError(CodeProbeExecutionFailed)
	}
	if !result.Started {
		return environment.ToolVersion{}, newError(CodeProbeExecutionFailed)
	}
	if !result.Exited || result.ExitCode != 0 || result.Signaled || result.Signal != "" {
		return environment.ToolVersion{}, unsupported(environment.UnsupportedVersionReason(), versionFact(tool))
	}
	stdout, stdoutOK := result.Stdout.SanitizedText()
	stderr, stderrOK := result.Stderr.SanitizedText()
	if !stdoutOK || !stderrOK || result.Stdout.Truncated() || result.Stderr.Truncated() || stderr != "" {
		return environment.ToolVersion{}, unsupported(environment.UnsupportedVersionReason(), versionFact(tool))
	}
	if stdout == "" {
		return environment.ToolVersion{}, incomplete(versionFact(tool))
	}
	version, ok := parseVersionOutput(tool, stdout)
	if !ok {
		return environment.ToolVersion{}, unsupported(environment.UnsupportedVersionReason(), versionFact(tool))
	}
	return version, nil
}

func (s *Service) environmentProfile(tool environment.Tool) lifecycle.ProcessEnvironmentProfileID {
	if tool == environment.GitTool() {
		return s.profile.GitEnvironmentProfile()
	}
	return s.profile.ClaudeEnvironmentProfile()
}

func (s *Service) environment(tool environment.Tool) []lifecycle.EnvironmentBinding {
	if tool == environment.GitTool() {
		return s.profile.GitEnvironment()
	}
	return s.profile.ClaudeEnvironment()
}

func (s *Service) timeout(tool environment.Tool) time.Duration {
	if tool == environment.GitTool() {
		return s.profile.GitTimeoutMaximum().Duration()
	}
	return s.profile.ClaudeTimeoutMaximum().Duration()
}

func processContextFailure(ctx context.Context, result lifecycle.ProcessResult, err error) error {
	if contextErr := contextFailure(ctx, err); contextErr != nil {
		return contextErr
	}
	if result.Cancelled && result.TimedOut {
		return newError(CodeProbeExecutionFailed)
	}
	if result.Cancelled {
		return newError(CodeCancelled)
	}
	if result.TimedOut {
		return newError(CodeTimedOut)
	}
	return nil
}

func supportedExecutable(observation lifecycle.ExecutableObservation) bool {
	native, ok := observation.Profile.Native()
	return observation.Valid() && ok && native.Role() == lifecycle.NativeExecutable &&
		native.Architectures().Contains(lifecycle.ExecutableARM64)
}

func executableExpectation(observation lifecycle.ExecutableObservation) lifecycle.ExecutableExpectation {
	return lifecycle.ExecutableExpectation{
		Identity:            observation.Resource.Identity,
		Authority:           observation.Authority,
		OwnerClass:          observation.Resource.OwnerClass,
		Mode:                observation.Resource.Mode,
		PrivilegeBearing:    observation.Resource.PrivilegeBearing,
		WritableByUntrusted: observation.Resource.WritableByUntrusted,
		Digest:              observation.Resource.ExecutableDigest,
		Profile:             observation.Profile,
	}
}

func sameExecutable(left, right lifecycle.ExecutableObservation) bool {
	return left.Resource.Identity == right.Resource.Identity || left.ResolvedPath == right.ResolvedPath
}

func stableUnsupportedExecutable(err error) bool {
	var typed *fault.Error
	if !errors.As(err, &typed) || typed.Category() != fault.Conflict {
		return false
	}
	detail, ok := typed.Detail().(fault.ConflictDetail)
	if !ok || detail.Resource() != "executable" {
		return false
	}
	switch detail.Identity() {
	case "not_executable", "unsafe_trust", "unsafe_alias":
		return true
	default:
		return false
	}
}

func executableFact(tool environment.Tool) environment.EnvironmentFact {
	if tool == environment.GitTool() {
		return environment.GitExecutableFact()
	}
	return environment.ClaudeExecutableFact()
}

func versionFact(tool environment.Tool) environment.EnvironmentFact {
	if tool == environment.GitTool() {
		return environment.GitVersionFact()
	}
	return environment.ClaudeVersionFact()
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
