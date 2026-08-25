package discovery_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/environment/discovery"
	"github.com/alx4j/ai4j/internal/fault"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestDiscoverPrerequisitesUsesExactReadOnlyProbeSequence(t *testing.T) {
	t.Parallel()

	service, host, executables, runner, trace := newFixture(t)
	observation, err := service.DiscoverPrerequisites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Valid() || observation.Host().String() != "darwin/arm64/15.6.1" {
		t.Fatalf("observation = %v", observation)
	}
	if !reflect.DeepEqual(*trace, []string{
		"host", "executable:git", "executable:claude", "process:/usr/bin/git",
		"process:/Users/alex/.local/share/claude/versions/2.1.211",
	}) {
		t.Fatalf("trace = %#v", *trace)
	}
	if len(host.requests) != 1 || host.requests[0].Host != domain.DarwinHost() {
		t.Fatalf("host requests = %#v", host.requests)
	}
	if len(executables.requests) != 2 ||
		executables.requests[0] != (lifecycle.ExecutableRequest{Candidate: "git", Authority: lifecycle.TrustedUserOrSystemAuthority}) ||
		executables.requests[1] != (lifecycle.ExecutableRequest{Candidate: "claude", Authority: lifecycle.TrustedUserOrSystemAuthority}) {
		t.Fatalf("executable requests = %#v", executables.requests)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("process requests = %d", len(runner.requests))
	}
	assertGitRequest(t, runner.requests[0], executables.responses[0].value)
	assertClaudeRequest(t, runner.requests[1], executables.responses[1].value)

	git, ok := observation.Executable(environment.GitTool())
	if !ok || git.Version().String() != "2.39.5 (Apple Git-154.3)" ||
		git.Version().Form() != environment.AppleGitToolVersionForm() {
		t.Fatalf("Git identity = %v", git)
	}
	claude, ok := observation.Executable(environment.ClaudeTool())
	if !ok || claude.Version().String() != "2.1.211" {
		t.Fatalf("Claude identity = %v", claude)
	}
}

func assertGitRequest(t *testing.T, request lifecycle.ProcessRequest, observation lifecycle.ExecutableObservation) {
	t.Helper()
	if !request.Valid() || request.Executable != observation.ResolvedPath ||
		!reflect.DeepEqual(request.Arguments, []string{"--version"}) ||
		request.EnvironmentProfile.String() != "isolated" || request.Environment == nil || len(request.Environment) != 0 ||
		request.Timeout != 5*time.Minute || request.OutputLimitBytes != 256 ||
		request.StdoutMode != lifecycle.SanitizedTextOutput || request.StderrMode != lifecycle.SanitizedTextOutput ||
		request.TerminationGrace != time.Second || !request.WorkingDirectory.Empty() || !request.Interpreter.Empty() ||
		len(request.ExecutableEnvironment) != 0 {
		t.Fatalf("Git request = %#v", request)
	}
	assertExpectation(t, request.ExpectedExecutable, observation)
}

func assertClaudeRequest(t *testing.T, request lifecycle.ProcessRequest, observation lifecycle.ExecutableObservation) {
	t.Helper()
	wantEnvironment := []lifecycle.EnvironmentBinding{
		{Name: "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", Value: "1"},
		{Name: "CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL", Value: "1"},
		{Name: "DISABLE_UPDATES", Value: "1"},
	}
	if !request.Valid() || request.Executable != observation.ResolvedPath ||
		!reflect.DeepEqual(request.Arguments, []string{"--version"}) ||
		request.EnvironmentProfile.String() != "claude_probe_v1" ||
		!reflect.DeepEqual(request.Environment, wantEnvironment) || request.Timeout != 2*time.Minute ||
		request.OutputLimitBytes != 256 || request.StdoutMode != lifecycle.SanitizedTextOutput ||
		request.StderrMode != lifecycle.SanitizedTextOutput || request.TerminationGrace != time.Second ||
		!request.WorkingDirectory.Empty() || !request.Interpreter.Empty() || len(request.ExecutableEnvironment) != 0 {
		t.Fatalf("Claude request = %#v", request)
	}
	assertExpectation(t, request.ExpectedExecutable, observation)
}

func assertExpectation(t *testing.T, got lifecycle.ExecutableExpectation, observation lifecycle.ExecutableObservation) {
	t.Helper()
	want := lifecycle.ExecutableExpectation{
		Identity:            observation.Resource.Identity,
		Authority:           observation.Authority,
		OwnerClass:          observation.Resource.OwnerClass,
		Mode:                observation.Resource.Mode,
		PrivilegeBearing:    observation.Resource.PrivilegeBearing,
		WritableByUntrusted: observation.Resource.WritableByUntrusted,
		Digest:              observation.Resource.ExecutableDigest,
		Profile:             observation.Profile,
	}
	if got != want || !got.Valid() {
		t.Fatalf("expectation = %#v, want %#v", got, want)
	}
}

func TestDiscoverPrerequisitesRejectsUnsupportedOrIncompleteHostBeforeFilesystem(t *testing.T) {
	t.Parallel()

	otherHost, err := domain.NewHost("other_host")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		observation lifecycle.HostObservation
		kind        environment.FaultKind
		reason      environment.FaultReason
		fact        environment.EnvironmentFact
	}{
		{name: "wrong registered host", observation: lifecycle.HostObservation{Host: otherHost, OS: "darwin", Arch: "arm64", OSVersion: "15.6.1"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedHostReason(), fact: environment.HostFact()},
		{name: "wrong operating system", observation: lifecycle.HostObservation{Host: domain.DarwinHost(), OS: "linux", Arch: "arm64", OSVersion: "15.6.1"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedHostReason(), fact: environment.HostFact()},
		{name: "wrong architecture", observation: lifecycle.HostObservation{Host: domain.DarwinHost(), OS: "darwin", Arch: "x86_64", OSVersion: "15.6.1"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedHostReason(), fact: environment.HostFact()},
		{name: "missing version", observation: lifecycle.HostObservation{Host: domain.DarwinHost(), OS: "darwin", Arch: "arm64"}, kind: environment.IncompleteEnvironmentFaultKind(), reason: environment.MissingRequiredFactReason(), fact: environment.DarwinVersionFact()},
		{name: "malformed version", observation: lifecycle.HostObservation{Host: domain.DarwinHost(), OS: "darwin", Arch: "arm64", OSVersion: "15.6.1.2"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedVersionReason(), fact: environment.DarwinVersionFact()},
		{name: "zero major", observation: lifecycle.HostObservation{Host: domain.DarwinHost(), OS: "darwin", Arch: "arm64", OSVersion: "0.1"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedVersionReason(), fact: environment.DarwinVersionFact()},
		{name: "whitespace", observation: lifecycle.HostObservation{Host: domain.DarwinHost(), OS: "darwin", Arch: "arm64", OSVersion: " 15.6"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedVersionReason(), fact: environment.DarwinVersionFact()},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, host, executables, runner, trace := newFixture(t)
			host.response.value = test.observation
			_, err := service.DiscoverPrerequisites(context.Background())
			requireEnvironmentFault(t, err, test.kind, test.reason, test.fact)
			if !reflect.DeepEqual(*trace, []string{"host"}) || len(executables.requests) != 0 || len(runner.requests) != 0 {
				t.Fatalf("unsafe continuation: trace=%#v executable=%d process=%d", *trace, len(executables.requests), len(runner.requests))
			}
		})
	}
}

func TestDiscoverPrerequisitesQualifiesBothExecutablesBeforeAnyProbe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		configure func(*testing.T, *recordingExecutables)
		wantTrace []string
		kind      environment.FaultKind
		reason    environment.FaultReason
		fact      environment.EnvironmentFact
		code      discovery.ErrorCode
	}{
		{
			name: "missing Git",
			configure: func(_ *testing.T, values *recordingExecutables) {
				values.responses[0] = result[lifecycle.ExecutableObservation]{err: lifecycle.ErrExecutableNotFound}
			},
			wantTrace: []string{"host", "executable:git"}, kind: environment.IncompleteEnvironmentFaultKind(), reason: environment.MissingRequiredFactReason(), fact: environment.GitExecutableFact(),
		},
		{
			name: "operational Git inspection failure",
			configure: func(_ *testing.T, values *recordingExecutables) {
				values.responses[0] = result[lifecycle.ExecutableObservation]{err: errors.New(pathCanary)}
			},
			wantTrace: []string{"host", "executable:git"}, code: discovery.CodeExecutableInspectionFailed,
		},
		{
			name: "unsafe Git conflict",
			configure: func(_ *testing.T, values *recordingExecutables) {
				values.responses[0] = result[lifecycle.ExecutableObservation]{err: executableConflict("unsafe_trust")}
			},
			wantTrace: []string{"host", "executable:git"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedExecutableReason(), fact: environment.GitExecutableFact(),
		},
		{
			name: "non-executable Git conflict",
			configure: func(_ *testing.T, values *recordingExecutables) {
				values.responses[0] = result[lifecycle.ExecutableObservation]{err: executableConflict("not_executable")}
			},
			wantTrace: []string{"host", "executable:git"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedExecutableReason(), fact: environment.GitExecutableFact(),
		},
		{
			name: "unsafe Git alias conflict",
			configure: func(_ *testing.T, values *recordingExecutables) {
				values.responses[0] = result[lifecycle.ExecutableObservation]{err: executableConflict("unsafe_alias")}
			},
			wantTrace: []string{"host", "executable:git"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedExecutableReason(), fact: environment.GitExecutableFact(),
		},
		{
			name: "Git substitution race",
			configure: func(_ *testing.T, values *recordingExecutables) {
				values.responses[0] = result[lifecycle.ExecutableObservation]{err: executableConflict("target_changed")}
			},
			wantTrace: []string{"host", "executable:git"}, code: discovery.CodeExecutableInspectionFailed,
		},
		{
			name: "Git lookup conflict",
			configure: func(_ *testing.T, values *recordingExecutables) {
				values.responses[0] = result[lifecycle.ExecutableObservation]{err: executableConflict("lookup_failed")}
			},
			wantTrace: []string{"host", "executable:git"}, code: discovery.CodeExecutableInspectionFailed,
		},
		{
			name: "Git alias race",
			configure: func(_ *testing.T, values *recordingExecutables) {
				values.responses[0] = result[lifecycle.ExecutableObservation]{err: executableConflict("alias_changed")}
			},
			wantTrace: []string{"host", "executable:git"}, code: discovery.CodeExecutableInspectionFailed,
		},
		{
			name: "missing Claude",
			configure: func(_ *testing.T, values *recordingExecutables) {
				values.responses[1] = result[lifecycle.ExecutableObservation]{err: lifecycle.ErrExecutableNotFound}
			},
			wantTrace: []string{"host", "executable:git", "executable:claude"}, kind: environment.IncompleteEnvironmentFaultKind(), reason: environment.MissingRequiredFactReason(), fact: environment.ClaudeExecutableFact(),
		},
		{
			name: "x86 Git",
			configure: func(t *testing.T, values *recordingExecutables) {
				values.responses[0].value.Profile = nativeProfile(t, lifecycle.NativeSingleImage, lifecycle.ExecutableX8664)
			},
			wantTrace: []string{"host", "executable:git"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedExecutableReason(), fact: environment.GitExecutableFact(),
		},
		{
			name: "script Claude",
			configure: func(t *testing.T, values *recordingExecutables) {
				values.responses[1].value.Profile = scriptProfile(t)
			},
			wantTrace: []string{"host", "executable:git", "executable:claude"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedExecutableReason(), fact: environment.ClaudeExecutableFact(),
		},
		{
			name: "same object",
			configure: func(_ *testing.T, values *recordingExecutables) {
				values.responses[1].value.Resource.Identity = values.responses[0].value.Resource.Identity
			},
			wantTrace: []string{"host", "executable:git", "executable:claude"}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedExecutableReason(), fact: environment.ClaudeExecutableFact(),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, _, executables, runner, trace := newFixture(t)
			test.configure(t, executables)
			_, err := service.DiscoverPrerequisites(context.Background())
			if test.code.Valid() {
				requireDiscoveryCode(t, err, test.code)
			} else {
				requireEnvironmentFault(t, err, test.kind, test.reason, test.fact)
			}
			if !reflect.DeepEqual(*trace, test.wantTrace) || len(runner.requests) != 0 {
				t.Fatalf("unsafe continuation: trace=%#v process=%d", *trace, len(runner.requests))
			}
			if strings.Contains(fmt.Sprintf("%+v", err), pathCanary) {
				t.Fatal("checker error disclosed a path canary")
			}
		})
	}
}

func TestDiscoverPrerequisitesAcceptsUniversalARM64Executable(t *testing.T) {
	t.Parallel()

	service, _, executables, _, _ := newFixture(t)
	executables.responses[1].value.Profile = nativeProfile(
		t,
		lifecycle.NativeMultiImage,
		lifecycle.ExecutableARM64|lifecycle.ExecutableX8664,
	)
	if observation, err := service.DiscoverPrerequisites(context.Background()); err != nil || !observation.Valid() {
		t.Fatalf("DiscoverPrerequisites() = %v, %v", observation, err)
	}
}

func TestDiscoverPrerequisitesFailsClosedOnProbeOutcomes(t *testing.T) {
	t.Parallel()

	malformed := successfulResult(t, outputCanary+"\n")
	empty := successfulResult(t, "")
	stderr := successfulResult(t, "git version 2.51.0\n")
	stderr.Stderr = stream(t, lifecycle.SanitizedTextOutput, []byte(outputCanary), false)
	truncated := successfulResult(t, "git version 2.51.0")
	truncated.Stdout = stream(t, lifecycle.SanitizedTextOutput, []byte("git version 2.51.0"), true)
	opaque := successfulResult(t, "git version 2.51.0")
	opaque.Stdout = stream(t, lifecycle.OpaqueBytesOutput, []byte("git version 2.51.0"), false)
	nonzero := successfulResult(t, "git version 2.51.0\n")
	nonzero.ExitCode = 1
	signaled := successfulResult(t, "git version 2.51.0\n")
	signaled.Exited = false
	signaled.Signaled = true
	signaled.Signal = "TERM"
	beforeStart := lifecycle.ProcessResult{}
	afterStart := successfulResult(t, "git version 2.51.0\n")
	timedOut := lifecycle.ProcessResult{Started: true, TimedOut: true}
	cancelled := lifecycle.ProcessResult{Started: true, Cancelled: true}

	tests := []struct {
		name       string
		response   result[lifecycle.ProcessResult]
		code       discovery.ErrorCode
		kind       environment.FaultKind
		reason     environment.FaultReason
		fact       environment.EnvironmentFact
		isCategory error
	}{
		{name: "empty", response: result[lifecycle.ProcessResult]{value: empty}, kind: environment.IncompleteEnvironmentFaultKind(), reason: environment.MissingRequiredFactReason(), fact: environment.GitVersionFact()},
		{name: "malformed", response: result[lifecycle.ProcessResult]{value: malformed}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedVersionReason(), fact: environment.GitVersionFact()},
		{name: "stderr", response: result[lifecycle.ProcessResult]{value: stderr}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedVersionReason(), fact: environment.GitVersionFact()},
		{name: "truncated", response: result[lifecycle.ProcessResult]{value: truncated}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedVersionReason(), fact: environment.GitVersionFact()},
		{name: "opaque stream", response: result[lifecycle.ProcessResult]{value: opaque}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedVersionReason(), fact: environment.GitVersionFact()},
		{name: "nonzero", response: result[lifecycle.ProcessResult]{value: nonzero}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedVersionReason(), fact: environment.GitVersionFact()},
		{name: "signaled", response: result[lifecycle.ProcessResult]{value: signaled}, kind: environment.UnsupportedFaultKind(), reason: environment.UnsupportedVersionReason(), fact: environment.GitVersionFact()},
		{name: "error before start", response: result[lifecycle.ProcessResult]{value: beforeStart, err: errors.New(pathCanary)}, code: discovery.CodeProbeExecutionFailed},
		{name: "error after start", response: result[lifecycle.ProcessResult]{value: afterStart, err: errors.New(pathCanary)}, code: discovery.CodeProbeExecutionFailed},
		{name: "timeout", response: result[lifecycle.ProcessResult]{value: timedOut, err: context.DeadlineExceeded}, code: discovery.CodeTimedOut, isCategory: context.DeadlineExceeded},
		{name: "cancelled", response: result[lifecycle.ProcessResult]{value: cancelled, err: context.Canceled}, code: discovery.CodeCancelled, isCategory: context.Canceled},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, _, _, runner, trace := newFixture(t)
			runner.responses[0] = test.response
			_, err := service.DiscoverPrerequisites(context.Background())
			if test.code.Valid() {
				requireDiscoveryCode(t, err, test.code)
			} else {
				requireEnvironmentFault(t, err, test.kind, test.reason, test.fact)
			}
			if test.isCategory != nil && !errors.Is(err, test.isCategory) {
				t.Fatalf("errors.Is(%v) = false", test.isCategory)
			}
			if len(runner.requests) != 1 || len(*trace) != 4 {
				t.Fatalf("Claude probe continued after Git failure: trace=%#v", *trace)
			}
			formatted := fmt.Sprintf("%v|%+v|%#v|%q", err, err, err, err)
			encoded, marshalErr := json.Marshal(err)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if strings.Contains(formatted, pathCanary) || strings.Contains(formatted, outputCanary) ||
				strings.Contains(string(encoded), pathCanary) || strings.Contains(string(encoded), outputCanary) {
				t.Fatalf("probe failure disclosed canary: %s / %s", formatted, encoded)
			}
		})
	}
}

func TestDiscoverPrerequisitesPropagatesCancellationWithoutCallingPorts(t *testing.T) {
	t.Parallel()

	service, _, _, _, trace := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.DiscoverPrerequisites(ctx)
	requireDiscoveryCode(t, err, discovery.CodeCancelled)
	if !errors.Is(err, context.Canceled) || len(*trace) != 0 {
		t.Fatalf("cancelled discovery called ports: trace=%#v err=%v", *trace, err)
	}
}

func TestDiscoverPrerequisitesReturnsSafeHostInspectionFailure(t *testing.T) {
	t.Parallel()

	service, host, _, _, trace := newFixture(t)
	host.response.err = errors.New(pathCanary)
	_, err := service.DiscoverPrerequisites(context.Background())
	requireDiscoveryCode(t, err, discovery.CodeHostInspectionFailed)
	if len(*trace) != 1 || strings.Contains(fmt.Sprintf("%#v", err), pathCanary) {
		t.Fatalf("host failure = %v, trace=%#v", err, *trace)
	}
}

func TestDiscoveryServiceRejectsNilContextAndPorts(t *testing.T) {
	t.Parallel()

	service, host, executables, runner, _ := newFixture(t)
	if _, err := service.DiscoverPrerequisites(nil); err == nil {
		t.Fatal("nil context accepted")
	} else {
		requireDiscoveryCode(t, err, discovery.CodeInvalidContext)
	}
	var nilHost *recordingHost
	var nilExecutables *recordingExecutables
	var nilRunner *recordingRunner
	profile := validProbeProfile(t)
	for name, construct := range map[string]func() error{
		"host": func() error {
			_, err := discovery.New(nilHost, executables, runner, profile)
			return err
		},
		"executables": func() error {
			_, err := discovery.New(host, nilExecutables, runner, profile)
			return err
		},
		"runner": func() error {
			_, err := discovery.New(host, executables, nilRunner, profile)
			return err
		},
		"profile": func() error {
			_, err := discovery.New(host, executables, runner, discovery.ProbeProfile{})
			return err
		},
	} {
		name, construct := name, construct
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireDiscoveryCode(t, construct(), discovery.CodeInvalidService)
		})
	}
}

func requireEnvironmentFault(
	t *testing.T,
	err error,
	kind environment.FaultKind,
	reason environment.FaultReason,
	fact environment.EnvironmentFact,
) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil")
	}
	var fault environment.EnvironmentFault
	if !errors.As(err, &fault) || fault.Kind() != kind || fault.Reason() != reason || fault.Fact() != fact {
		t.Fatalf("fault = %T %v, want %s/%s/%s", err, err, kind.String(), reason.String(), fact.String())
	}
}

func requireDiscoveryCode(t *testing.T, err error, want discovery.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %q", want)
	}
	var discoveryError discovery.Error
	if !errors.As(err, &discoveryError) || discoveryError.Code() != want {
		t.Fatalf("error = %T %v, want code %q", err, err, want)
	}
}

func executableConflict(identity string) error {
	detail, err := fault.NewConflictDetail("executable", identity)
	if err != nil {
		panic(err)
	}
	return fault.MustNew(fault.Conflict, detail, nil)
}
