package discovery_test

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/environment/discovery"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

const (
	gitDigest    = "1111111111111111111111111111111111111111111111111111111111111111"
	claudeDigest = "2222222222222222222222222222222222222222222222222222222222222222"
	pathCanary   = "PRIVATE_PATH_CANARY_71d9"
	outputCanary = "RAW_OUTPUT_CANARY_2af4"
)

type result[T any] struct {
	value T
	err   error
}

type recordingHost struct {
	trace    *[]string
	response result[lifecycle.HostObservation]
	requests []lifecycle.HostInspectionRequest
}

func (h *recordingHost) InspectHost(_ context.Context, request lifecycle.HostInspectionRequest) (lifecycle.HostObservation, error) {
	*h.trace = append(*h.trace, "host")
	h.requests = append(h.requests, request)
	return h.response.value, h.response.err
}

type recordingExecutables struct {
	trace     *[]string
	responses []result[lifecycle.ExecutableObservation]
	requests  []lifecycle.ExecutableRequest
}

func (e *recordingExecutables) CheckExecutable(_ context.Context, request lifecycle.ExecutableRequest) (lifecycle.ExecutableObservation, error) {
	*e.trace = append(*e.trace, "executable:"+request.Candidate)
	e.requests = append(e.requests, request)
	index := len(e.requests) - 1
	if index >= len(e.responses) {
		return lifecycle.ExecutableObservation{}, context.Canceled
	}
	return e.responses[index].value, e.responses[index].err
}

type recordingRunner struct {
	trace     *[]string
	responses []result[lifecycle.ProcessResult]
	requests  []lifecycle.ProcessRequest
}

func (r *recordingRunner) RunProcess(_ context.Context, request lifecycle.ProcessRequest) (lifecycle.ProcessResult, error) {
	*r.trace = append(*r.trace, "process:"+request.Executable)
	r.requests = append(r.requests, cloneRequest(request))
	index := len(r.requests) - 1
	if index >= len(r.responses) {
		return lifecycle.ProcessResult{}, context.Canceled
	}
	return r.responses[index].value, r.responses[index].err
}

func cloneRequest(request lifecycle.ProcessRequest) lifecycle.ProcessRequest {
	environmentWasNonNil := request.Environment != nil
	request.Arguments = append([]string(nil), request.Arguments...)
	request.Environment = append([]lifecycle.EnvironmentBinding(nil), request.Environment...)
	if environmentWasNonNil && len(request.Environment) == 0 {
		request.Environment = make([]lifecycle.EnvironmentBinding, 0)
	}
	request.ExecutableEnvironment = append([]lifecycle.ExecutableEnvironmentBinding(nil), request.ExecutableEnvironment...)
	return request
}

func newFixture(t *testing.T) (*discovery.Service, *recordingHost, *recordingExecutables, *recordingRunner, *[]string) {
	t.Helper()
	trace := make([]string, 0, 5)
	host := &recordingHost{trace: &trace, response: result[lifecycle.HostObservation]{value: validHostObservation()}}
	executables := &recordingExecutables{trace: &trace, responses: []result[lifecycle.ExecutableObservation]{
		{value: executableObservation(t, environment.GitTool(), nativeProfile(t, lifecycle.NativeSingleImage, lifecycle.ExecutableARM64), 100)},
		{value: executableObservation(t, environment.ClaudeTool(), nativeProfile(t, lifecycle.NativeSingleImage, lifecycle.ExecutableARM64), 200)},
	}}
	runner := &recordingRunner{trace: &trace, responses: []result[lifecycle.ProcessResult]{
		{value: successfulResult(t, "git version 2.39.5 (Apple Git-154.3)\n")},
		{value: successfulResult(t, "2.1.211 (Claude Code)\n")},
	}}
	service, err := discovery.New(host, executables, runner, validProbeProfile(t))
	if err != nil {
		t.Fatal(err)
	}
	return service, host, executables, runner, &trace
}

func validProbeProfile(t *testing.T) discovery.ProbeProfile {
	t.Helper()
	version, err := lifecycle.NewResourcePolicyVersion("mvp_resource_v1")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := lifecycle.NewHostResourcePolicy(version, 5*time.Minute, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := discovery.NewMVPProbeProfile(policy)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func validHostObservation() lifecycle.HostObservation {
	return lifecycle.HostObservation{Host: domain.DarwinHost(), OS: "darwin", Arch: "arm64", OSVersion: "15.6.1"}
}

func executableObservation(
	t *testing.T,
	tool environment.Tool,
	profile lifecycle.StaticExecutableProfile,
	object uint64,
) lifecycle.ExecutableObservation {
	t.Helper()
	digestText := claudeDigest
	path := "/Users/alex/.local/share/claude/versions/2.1.211"
	owner := lifecycle.CurrentUserOwner
	if tool == environment.GitTool() {
		digestText = gitDigest
		path = "/usr/bin/git"
		owner = lifecycle.SystemOwner
	}
	digest, err := domain.NewExecutableDigest(digestText)
	if err != nil {
		t.Fatal(err)
	}
	return lifecycle.ExecutableObservation{
		ResolvedPath: path, Authority: lifecycle.TrustedUserOrSystemAuthority,
		Resource: lifecycle.ResourceObservation{
			Exists:             true,
			OwnedByCurrentUser: owner == lifecycle.CurrentUserOwner,
			Kind:               lifecycle.ExecutableResource,
			OwnerClass:         owner,
			ExecutableDigest:   digest,
			Mode:               fs.FileMode(0o755),
			Size:               4096,
			LinkCount:          1,
			RootIdentity:       lifecycle.ObjectIdentity{Filesystem: 1, Object: object + 1},
			ParentIdentity:     lifecycle.ObjectIdentity{Filesystem: 1, Object: object + 2},
			Identity:           lifecycle.ObjectIdentity{Filesystem: 1, Object: object},
		},
		Profile: profile,
	}
}

func nativeProfile(
	t *testing.T,
	layout lifecycle.NativeImageLayout,
	architectures lifecycle.ExecutableArchitectureSet,
) lifecycle.StaticExecutableProfile {
	t.Helper()
	native, err := lifecycle.NewNativeExecutableProfile(layout, lifecycle.NativeExecutable, architectures)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := lifecycle.NewNativeStaticExecutableProfile(native)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func scriptProfile(t *testing.T) lifecycle.StaticExecutableProfile {
	t.Helper()
	shebang, err := lifecycle.NewDirectShebangProfile("/bin/sh", "")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := lifecycle.NewScriptStaticExecutableProfile(shebang)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func successfulResult(t *testing.T, stdout string) lifecycle.ProcessResult {
	t.Helper()
	return lifecycle.ProcessResult{
		Started:  true,
		Exited:   true,
		ExitCode: 0,
		Stdout:   stream(t, lifecycle.SanitizedTextOutput, []byte(stdout), false),
		Stderr:   stream(t, lifecycle.SanitizedTextOutput, nil, false),
	}
}

func stream(t *testing.T, mode lifecycle.ProcessOutputMode, value []byte, truncated bool) lifecycle.ProcessStream {
	t.Helper()
	result, ok := lifecycle.NewProcessStream(mode, value, truncated)
	if !ok {
		t.Fatal("invalid stream fixture")
	}
	return result
}

func mustToolVersion(t *testing.T, tool environment.Tool, value string) environment.ToolVersion {
	t.Helper()
	semantic, err := environment.NewSemanticVersion(value)
	if err != nil {
		t.Fatal(err)
	}
	version, err := environment.NewSemanticToolVersion(tool, semantic)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func mustExecutableIdentity(
	t *testing.T,
	tool environment.Tool,
	observation lifecycle.ExecutableObservation,
) environment.ExecutableIdentity {
	t.Helper()
	identity, err := environment.NewExecutableIdentity(tool, mustToolVersion(t, tool, "2.1.211"), observation)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}
