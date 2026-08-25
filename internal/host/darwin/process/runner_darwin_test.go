//go:build darwin && arm64

package process

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/host/darwin/executableprofile"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if os.Getenv("AI4J_SCRIPT_INTERPRETER") == "1" {
		if len(os.Args) < 2 {
			os.Exit(81)
		}
		content, err := os.ReadFile(os.Args[1])
		if err != nil || !bytes.Contains(content, []byte("AI4J_SCRIPT_FD_CANARY")) {
			os.Exit(82)
		}
		fmt.Fprintln(os.Stdout, "script-fd-executed")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fixtureAuthority struct {
	executables map[string]string
	directory   string
}

type countingRejectAuthority struct{ calls int }

func (a *countingRejectAuthority) OpenExecutable(context.Context, string, lifecycle.ExecutableExpectation) (*os.File, error) {
	a.calls++
	return nil, errors.New("authority must not be opened")
}

func (a *countingRejectAuthority) OpenDirectory(context.Context, lifecycle.DirectoryExpectation) (*os.File, error) {
	a.calls++
	return nil, errors.New("authority must not be opened")
}

func (a fixtureAuthority) OpenExecutable(_ context.Context, locator string, _ lifecycle.ExecutableExpectation) (*os.File, error) {
	path, ok := a.executables[locator]
	if !ok {
		return nil, errors.New("unknown fixture executable")
	}
	return os.Open(path)
}

func (a fixtureAuthority) OpenDirectory(context.Context, lifecycle.DirectoryExpectation) (*os.File, error) {
	return os.Open(a.directory)
}

func TestPrepareNativeLaunchBindsDescriptorsEnvironmentAndParentCWD(t *testing.T) {
	directory := t.TempDir()
	mainPath := writeNativeFixture(t, directory, "git", 'a')
	helperPath := writeNativeFixture(t, directory, "ssh-helper", 'b')
	mainExpectation := expectationForFixture(t, mainPath)
	helperExpectation := expectationForFixture(t, helperPath)
	cwd := directoryExpectationForFixture(t, directory)
	authority := fixtureAuthority{executables: map[string]string{
		"/qualified/git": mainPath, "/qualified/ssh-helper": helperPath,
	}, directory: directory}
	runner := newFixtureRunner(t, authority, cwd, testDigest(t, '1'))
	request := validFixtureRequest("/qualified/git", mainExpectation)
	request.Arguments = []string{"status", "--porcelain=v1"}
	request.EnvironmentProfile = gitHardenedProfileID
	request.Environment = gitHardenedEnvironment()
	request.ExecutableEnvironment = []lifecycle.ExecutableEnvironmentBinding{{
		Name: executableSSHEnvironment, ResolvedPath: "/qualified/ssh-helper", ExpectedExecutable: helperExpectation,
	}}

	prepared, err := runner.prepareLaunch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	if prepared.path != mainPath || !reflect.DeepEqual(prepared.args, []string{"/qualified/git", "status", "--porcelain=v1"}) {
		t.Fatalf("path/args = %q / %v", prepared.path, prepared.args)
	}
	wantEnvironment := []string{
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_LFS_SKIP_SMUDGE=1",
		"GIT_OPTIONAL_LOCKS=0", "GIT_PROTOCOL_FROM_USER=0", "GIT_SSH=" + helperPath,
		"GIT_SSH_VARIANT=ssh", "GIT_TERMINAL_PROMPT=0", "LANG=C", "LC_ALL=C",
	}
	if !reflect.DeepEqual(prepared.env, wantEnvironment) {
		t.Fatalf("environment = %v", prepared.env)
	}
	if len(prepared.extraFiles) != 2 || len(prepared.bound) != 2 {
		t.Fatalf("descriptor counts = %d / %d", len(prepared.extraFiles), len(prepared.bound))
	}
	if prepared.cwd.Fd() < minimumWorkingDirectoryDescriptor {
		t.Fatalf("cwd descriptor = %d", prepared.cwd.Fd())
	}
	flags, err := unix.FcntlInt(prepared.cwd.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("cwd descriptor flags = %d, %v", flags, err)
	}
	command := prepared.command(nil, nil)
	if command.Dir != directory || len(command.ExtraFiles) != 0 || command.SysProcAttr == nil || !command.SysProcAttr.Setpgid {
		t.Fatalf("command cwd/process group = %q / %#v", command.Dir, command.SysProcAttr)
	}
	if containsEnvironmentName(command.Env, "PATH") {
		t.Fatalf("ambient environment leaked: %v", command.Env)
	}
}

func TestPrepareScriptLaunchDispatchesQualifiedInterpreterWithoutEnv(t *testing.T) {
	directory := t.TempDir()
	scriptPath := filepath.Join(directory, "claude")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env node\nprocess.exit(0)\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(scriptPath, 0o700); err != nil {
		t.Fatal(err)
	}
	interpreterPath := writeNativeFixture(t, directory, "node", 'n')
	scriptExpectation := expectationForFixture(t, scriptPath)
	interpreterExpectation := expectationForFixture(t, interpreterPath)
	shebang, ok := scriptExpectation.Profile.Shebang()
	if !ok || shebang.Form() != lifecycle.ShebangEnv {
		t.Fatalf("script profile = %v", scriptExpectation.Profile)
	}
	cwd := directoryExpectationForFixture(t, directory)
	authority := fixtureAuthority{executables: map[string]string{
		"/qualified/claude": scriptPath, "/qualified/node": interpreterPath,
	}, directory: directory}
	runner := newFixtureRunner(t, authority, cwd, testDigest(t, '1'))
	request := validFixtureRequest("/qualified/claude", scriptExpectation)
	request.Arguments = []string{"--version"}
	request.EnvironmentProfile = claudeProbeProfileID
	request.Environment = claudeProbeEnvironment()
	request.Interpreter = lifecycle.InterpreterBinding{
		Requirement: shebang, Candidate: "node", ResolvedPath: "/qualified/node", Executable: interpreterExpectation,
	}

	prepared, err := runner.prepareLaunch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	if prepared.path != interpreterPath ||
		!reflect.DeepEqual(prepared.args, []string{"node", scriptPath, "--version"}) ||
		len(prepared.extraFiles) != 2 {
		t.Fatalf("script launch = %q %v files=%d", prepared.path, prepared.args, len(prepared.extraFiles))
	}
	for _, argument := range prepared.args {
		if argument == "/usr/bin/env" {
			t.Fatal("env dispatcher entered direct argv")
		}
	}
	wantEnvironment := []string{
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL=1",
		"DISABLE_UPDATES=1",
	}
	if !reflect.DeepEqual(prepared.env, wantEnvironment) || containsEnvironmentName(prepared.env, "LANG") ||
		containsEnvironmentName(prepared.env, "LC_ALL") {
		t.Fatalf("Claude probe environment = %v", prepared.env)
	}
}

func TestEnvironmentProfileMismatchFailsBeforeEveryAuthorityOpen(t *testing.T) {
	directory := t.TempDir()
	mainPath := writeNativeFixture(t, directory, "native", 'a')
	expectation := expectationForFixture(t, mainPath)
	cwd := directoryExpectationForFixture(t, directory)
	unknown := mustProcessEnvironmentProfileID("unknown_v1")
	tests := []struct {
		name   string
		mutate func(*lifecycle.ProcessRequest)
	}{
		{name: "unknown profile", mutate: func(request *lifecycle.ProcessRequest) {
			request.EnvironmentProfile = unknown
		}},
		{name: "missing Git binding", mutate: func(request *lifecycle.ProcessRequest) {
			request.EnvironmentProfile = gitHardenedProfileID
			request.Environment = gitHardenedEnvironment()[:7]
		}},
		{name: "altered Git binding", mutate: func(request *lifecycle.ProcessRequest) {
			request.EnvironmentProfile = gitHardenedProfileID
			request.Environment = replaceEnvironmentValue(gitHardenedEnvironment(), "GIT_OPTIONAL_LOCKS", "1")
		}},
		{name: "cross-profile bindings", mutate: func(request *lifecycle.ProcessRequest) {
			request.EnvironmentProfile = gitHardenedProfileID
			request.Environment = claudeProbeEnvironment()
		}},
		{name: "extra isolated binding", mutate: func(request *lifecycle.ProcessRequest) {
			request.Environment = []lifecycle.EnvironmentBinding{{Name: "LANG", Value: "C"}}
		}},
		{name: "Git helper outside Git profile", mutate: func(request *lifecycle.ProcessRequest) {
			request.ExecutableEnvironment = []lifecycle.ExecutableEnvironmentBinding{{
				Name: executableSSHEnvironment, ResolvedPath: "/qualified/helper", ExpectedExecutable: expectation,
			}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authority := &countingRejectAuthority{}
			runner, err := NewMVP(authority, cwd, []domain.ExecutableDigest{testDigest(t, '1')})
			if err != nil {
				t.Fatal(err)
			}
			request := validFixtureRequest("/qualified/native", expectation)
			test.mutate(&request)
			if !request.Valid() {
				t.Fatal("fixture must remain structurally valid so host policy is exercised")
			}
			if _, err := runner.prepareLaunch(context.Background(), request); !errors.Is(err, errProcessPolicyViolation) {
				t.Fatalf("prepare error = %v", err)
			}
			if authority.calls != 0 {
				t.Fatalf("authority calls = %d", authority.calls)
			}
		})
	}
}

func TestPreparedLaunchFinalProofRejectsSameInodeRewrite(t *testing.T) {
	directory := t.TempDir()
	path := writeNativeFixture(t, directory, "native", 'a')
	expectation := expectationForFixture(t, path)
	cwd := directoryExpectationForFixture(t, directory)
	authority := fixtureAuthority{executables: map[string]string{"/qualified/native": path}, directory: directory}
	runner := newFixtureRunner(t, authority, cwd, testDigest(t, '1'))
	request := validFixtureRequest("/qualified/native", expectation)
	prepared, err := runner.prepareLaunch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()

	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bytes[len(bytes)-1] ^= 0xff
	if err := os.WriteFile(path, bytes, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runner.verifyBoundExecutable(context.Background(), prepared.bound[0]); !errors.Is(err, errExecutableChanged) {
		t.Fatalf("rewrite error = %v", err)
	}
}

func TestPrepareRejectsDeniedInterpreterAndRenamedDeniedDigest(t *testing.T) {
	directory := t.TempDir()
	path := writeNativeFixture(t, directory, "renamed-safe", 'a')
	expectation := expectationForFixture(t, path)
	cwd := directoryExpectationForFixture(t, directory)
	authority := fixtureAuthority{executables: map[string]string{"/qualified/safe": path}, directory: directory}
	runner := newFixtureRunner(t, authority, cwd, expectation.Digest)
	request := validFixtureRequest("/qualified/safe", expectation)
	if _, err := runner.prepareLaunch(context.Background(), request); !errors.Is(err, errProcessPolicyViolation) {
		t.Fatalf("denied digest error = %v", err)
	}
}

type countingAuthority struct{ opens int }

func (a *countingAuthority) OpenExecutable(context.Context, string, lifecycle.ExecutableExpectation) (*os.File, error) {
	a.opens++
	return nil, errors.New("unexpected executable open")
}

func (a *countingAuthority) OpenDirectory(context.Context, lifecycle.DirectoryExpectation) (*os.File, error) {
	a.opens++
	return nil, errors.New("unexpected directory open")
}

func TestDirectShellAndElevationRequestsAreRejectedBeforeAuthorityOpen(t *testing.T) {
	directory := t.TempDir()
	path := writeNativeFixture(t, directory, "safe", 'a')
	expectation := expectationForFixture(t, path)
	for _, locator := range []string{"/bin/sh", "/usr/bin/sudo"} {
		t.Run(filepath.Base(locator), func(t *testing.T) {
			authority := &countingAuthority{}
			runner := newFixtureRunner(t, authority, directoryExpectationForFixture(t, directory), testDigest(t, '1'))
			request := validFixtureRequest(locator, expectation)
			if _, err := runner.prepareLaunch(context.Background(), request); !errors.Is(err, errProcessPolicyViolation) {
				t.Fatalf("policy error = %v", err)
			}
			if authority.opens != 0 {
				t.Fatalf("authority opens = %d", authority.opens)
			}
		})
	}
}

type missingExecutableAuthority struct{ fixtureAuthority }

func (a missingExecutableAuthority) OpenExecutable(context.Context, string, lifecycle.ExecutableExpectation) (*os.File, error) {
	return nil, os.ErrNotExist
}

func TestMissingExecutableReturnsNotStartedWithBoundedEmptyStreams(t *testing.T) {
	directory := t.TempDir()
	path := writeNativeFixture(t, directory, "native", 'a')
	expectation := expectationForFixture(t, path)
	authority := missingExecutableAuthority{fixtureAuthority{directory: directory}}
	runner := newFixtureRunner(t, authority, directoryExpectationForFixture(t, directory), testDigest(t, '1'))
	request := validFixtureRequest("/qualified/missing", expectation)
	request.StdoutMode = lifecycle.OpaqueBytesOutput
	result, err := runner.RunProcess(context.Background(), request)
	if !errors.Is(err, os.ErrNotExist) || result.Started || result.Exited || result.Signaled || result.ExitCode != -1 {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
	stdout, stdoutOK := result.Stdout.OpaqueBytes()
	stderr, stderrOK := result.Stderr.SanitizedText()
	if !stdoutOK || len(stdout) != 0 || !stderrOK || stderr != "" || result.Stdout.Truncated() || result.Stderr.Truncated() {
		t.Fatalf("empty streams = %v/%t and %q/%t", stdout, stdoutOK, stderr, stderrOK)
	}
}

type blockingAuthority struct{}

func (blockingAuthority) OpenExecutable(context.Context, string, lifecycle.ExecutableExpectation) (*os.File, error) {
	return nil, errors.New("unexpected executable open")
}

func (blockingAuthority) OpenDirectory(ctx context.Context, _ lifecycle.DirectoryExpectation) (*os.File, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRequestTimeoutCoversDescriptorPreparation(t *testing.T) {
	directory := t.TempDir()
	path := writeNativeFixture(t, directory, "native", 'a')
	expectation := expectationForFixture(t, path)
	runner := newFixtureRunner(t, blockingAuthority{}, directoryExpectationForFixture(t, directory), testDigest(t, '1'))
	request := validFixtureRequest("/qualified/native", expectation)
	request.Timeout = 25 * time.Millisecond
	request.TerminationGrace = 10 * time.Millisecond
	started := time.Now()
	result, err := runner.RunProcess(context.Background(), request)
	if !errors.Is(err, context.DeadlineExceeded) || !result.TimedOut || result.Cancelled || result.Started {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("preparation timeout was not bounded")
	}
}

func TestCallerDeadlineCoversDescriptorPreparation(t *testing.T) {
	directory := t.TempDir()
	path := writeNativeFixture(t, directory, "native", 'a')
	expectation := expectationForFixture(t, path)
	runner := newFixtureRunner(t, blockingAuthority{}, directoryExpectationForFixture(t, directory), testDigest(t, '1'))
	request := validFixtureRequest("/qualified/native", expectation)
	request.Timeout = time.Second
	request.TerminationGrace = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	result, err := runner.RunProcess(ctx, request)
	if !errors.Is(err, context.DeadlineExceeded) || !result.TimedOut || result.Cancelled || result.Started || errors.Is(err, errRequestTimeout) {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
}

func TestFinalCWDRevalidationRejectsRootAndNestedParentMoves(t *testing.T) {
	for _, test := range []struct {
		name      string
		makePaths func(*testing.T) (string, string, string)
	}{
		{
			name: "root move",
			makePaths: func(t *testing.T) (string, string, string) {
				outer := t.TempDir()
				root := filepath.Join(outer, "root")
				cwd := filepath.Join(root, "cwd")
				if err := os.MkdirAll(cwd, 0o700); err != nil {
					t.Fatal(err)
				}
				return cwd, root, filepath.Join(outer, "root-moved")
			},
		},
		{
			name: "nested parent move",
			makePaths: func(t *testing.T) (string, string, string) {
				root := t.TempDir()
				parent := filepath.Join(root, "parent")
				cwd := filepath.Join(parent, "cwd")
				if err := os.MkdirAll(cwd, 0o700); err != nil {
					t.Fatal(err)
				}
				return cwd, parent, filepath.Join(root, "parent-moved")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cwdPath, moveFrom, moveTo := test.makePaths(t)
			executablePath := writeNativeFixture(t, filepath.Dir(cwdPath), "native", 'a')
			expectation := expectationForFixture(t, executablePath)
			authority := fixtureAuthority{executables: map[string]string{"/qualified/native": executablePath}, directory: cwdPath}
			runner := newFixtureRunner(t, authority, directoryExpectationForFixture(t, cwdPath), testDigest(t, '1'))
			runner.hooks.beforeCWDRevalidation = func() {
				if err := os.Rename(moveFrom, moveTo); err != nil {
					t.Fatal(err)
				}
			}
			result, err := runner.RunProcess(context.Background(), validFixtureRequest("/qualified/native", expectation))
			if err == nil || result.Started {
				t.Fatalf("detached cwd result/error = %#v / %v", result, err)
			}
		})
	}
}

func TestCallerCancelCauseIsDiscoverableButNeverDisclosed(t *testing.T) {
	directory := t.TempDir()
	path := writeNativeFixture(t, directory, "native", 'a')
	expectation := expectationForFixture(t, path)
	authority := fixtureAuthority{executables: map[string]string{"/qualified/native": path}, directory: directory}
	runner := newFixtureRunner(t, authority, directoryExpectationForFixture(t, directory), testDigest(t, '1'))
	ctx, cancel := context.WithCancelCause(context.Background())
	canary := "AI4J_CANCEL_CAUSE_SECRET"
	runner.hooks.beforeStart = func() { cancel(errors.New(canary)) }
	result, err := runner.RunProcess(ctx, validFixtureRequest("/qualified/native", expectation))
	if !errors.Is(err, context.Canceled) || !result.Cancelled || result.TimedOut || result.Started {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("cancel cause leaked: %v", err)
	}
}

func TestCollectorFailureKeepsCancellationDiscoverable(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("AI4J_PRIVATE_CAUSE"))
	requestContext, stop := context.WithTimeoutCause(ctx, time.Second, errRequestTimeout)
	defer stop()
	result := lifecycle.ProcessResult{ExitCode: -1}
	contextErr := processContextError(ctx, requestContext, &result)
	request := lifecycle.ProcessRequest{StdoutMode: lifecycle.OpaqueBytesOutput, StderrMode: lifecycle.SanitizedTextOutput}
	_, err := finishStreams(result, request, captureResult{err: errProcessCapture}, captureResult{}, errors.Join(contextErr, errProcessTeardown))
	if !errors.Is(err, context.Canceled) || !errors.Is(err, errProcessTeardown) || strings.Contains(err.Error(), "AI4J_PRIVATE_CAUSE") {
		t.Fatalf("joined cancellation error = %v", err)
	}
}

func TestForcedCloseJoinsCollectorsWithinFixedBound(t *testing.T) {
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdoutWrite.Close()
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stderrWrite.Close()
	stdoutResult := make(chan captureResult, 1)
	stderrResult := make(chan captureResult, 1)
	go func() { stdoutResult <- capture(stdoutRead, 16) }()
	go func() { stderrResult <- capture(stderrRead, 16) }()
	started := time.Now()
	_, _, collectErr := collectCaptured(stdoutRead, stderrRead, stdoutResult, stderrResult, 10*time.Millisecond)
	if !errors.Is(collectErr, errProcessTeardown) || time.Since(started) > time.Second {
		t.Fatalf("forced collector join = %v after %s", collectErr, time.Since(started))
	}
}

func TestRunProcessReturnsTypedNonzeroAndSignalOutcomes(t *testing.T) {
	for _, test := range []struct {
		mode       string
		wantExit   int
		wantSignal bool
	}{
		{mode: "nonzero", wantExit: 7},
		{mode: "signal", wantExit: -1, wantSignal: true},
	} {
		t.Run(test.mode, func(t *testing.T) {
			runner, request := selfProcessFixture(t, test.mode)
			result, err := runner.RunProcess(context.Background(), request)
			if err != nil {
				t.Fatalf("host error = %v", err)
			}
			if !result.Started || result.ExitCode != test.wantExit || result.Signaled != test.wantSignal || result.Exited == test.wantSignal {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestRunProcessCapsSimultaneousStdoutAndStderrIndependently(t *testing.T) {
	runner, request := selfProcessFixture(t, "dual-output")
	request.OutputLimitBytes = 64
	request.StdoutMode = lifecycle.OpaqueBytesOutput
	request.StderrMode = lifecycle.SanitizedTextOutput
	result, err := runner.RunProcess(context.Background(), request)
	if err != nil || !result.Exited || result.ExitCode != 0 {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
	stdout, ok := result.Stdout.OpaqueBytes()
	wantStdout := helperStdoutPayload()[:request.OutputLimitBytes]
	if !ok || !bytes.Equal(stdout, wantStdout) || !result.Stdout.Truncated() {
		t.Fatalf("stdout = %v, %t, truncated=%t", stdout, ok, result.Stdout.Truncated())
	}
	stderr, ok := result.Stderr.SanitizedText()
	wantStderr := string(sanitizeText(helperStderrPayload()[:request.OutputLimitBytes]))
	if !ok || stderr != wantStderr || !result.Stderr.Truncated() {
		t.Fatalf("stderr = %q, %t, truncated=%t", stderr, ok, result.Stderr.Truncated())
	}
}

func TestQualifiedInterpreterExecutesTheBoundScriptDescriptor(t *testing.T) {
	directory := t.TempDir()
	scriptPath := filepath.Join(directory, "script")
	script := []byte("#!/usr/bin/env process-test\nAI4J_SCRIPT_FD_CANARY\n")
	if err := os.WriteFile(scriptPath, script, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(scriptPath, 0o700); err != nil {
		t.Fatal(err)
	}
	interpreterPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	interpreterPath, err = filepath.EvalSymlinks(interpreterPath)
	if err != nil {
		t.Fatal(err)
	}
	scriptExpectation := expectationForFixture(t, scriptPath)
	interpreterExpectation := expectationForFixture(t, interpreterPath)
	shebang, ok := scriptExpectation.Profile.Shebang()
	if !ok {
		t.Fatal("script profile missing shebang")
	}
	cwd := directoryExpectationForFixture(t, directory)
	authority := fixtureAuthority{executables: map[string]string{
		"/qualified/script": scriptPath, "/qualified/process-test": interpreterPath,
	}, directory: directory}
	testProfile := mustProcessEnvironmentProfileID("script_test_v1")
	runner, err := newRunnerWithProfiles(Config{
		Authority: authority, SafeWorkingDirectory: cwd,
		DeniedExecutableDigests: []domain.ExecutableDigest{testDigest(t, '1')},
	}, append(mvpEnvironmentProfiles(), environmentProfileDefinition{
		id:     testProfile,
		values: []lifecycle.EnvironmentBinding{{Name: "AI4J_SCRIPT_INTERPRETER", Value: "1"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	request := validFixtureRequest("/qualified/script", scriptExpectation)
	request.Timeout = 30 * time.Second
	request.EnvironmentProfile = testProfile
	request.Environment = []lifecycle.EnvironmentBinding{{Name: "AI4J_SCRIPT_INTERPRETER", Value: "1"}}
	request.Interpreter = lifecycle.InterpreterBinding{
		Requirement: shebang, Candidate: "process-test", ResolvedPath: "/qualified/process-test", Executable: interpreterExpectation,
	}
	result, err := runner.RunProcess(context.Background(), request)
	if err != nil || !result.Exited || result.ExitCode != 0 {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
	text, ok := result.Stdout.SanitizedText()
	if !ok || text != "script-fd-executed\n" {
		t.Fatalf("script output = %q, %t", text, ok)
	}
}

func TestMainChildExecutesTypedExecutableEnvironmentDescriptor(t *testing.T) {
	runner, request := selfProcessFixture(t, "exec-helper")
	request.ExecutableEnvironment = []lifecycle.ExecutableEnvironmentBinding{{
		Name: executableSSHEnvironment, ResolvedPath: "/qualified/ssh-helper", ExpectedExecutable: request.ExpectedExecutable,
	}}
	result, err := runner.RunProcess(context.Background(), request)
	if err != nil || !result.Exited || result.ExitCode != 0 {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
	text, ok := result.Stdout.SanitizedText()
	if !ok || text != "ssh-fd-executed\n" {
		t.Fatalf("helper output = %q, %t", text, ok)
	}
}

func TestRunProcessTimeoutKillsTermIgnoringLeaderAndPreservesDeadline(t *testing.T) {
	runner, request := selfProcessFixture(t, "ignore-term")
	request.Timeout = 5 * time.Second
	request.TerminationGrace = 300 * time.Millisecond
	result, err := runner.RunProcess(context.Background(), request)
	if !errors.Is(err, context.DeadlineExceeded) || !result.Started || !result.TimedOut || result.Cancelled {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
}

func TestRunProcessCancellationKillsStartedLeader(t *testing.T) {
	runner, request := selfProcessFixture(t, "ignore-term")
	request.Timeout = 30 * time.Second
	request.TerminationGrace = 500 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	checkedClosedDescriptors := false
	runner.hooks.afterStart = func(executables []*os.File, cwd *os.File) {
		for _, file := range append(executables, cwd) {
			if _, err := file.Stat(); err == nil {
				t.Error("parent authority descriptor remained open after Start")
			}
		}
		checkedClosedDescriptors = true
		cancel()
	}
	result, err := runner.RunProcess(ctx, request)
	if !errors.Is(err, context.Canceled) || !result.Started || !result.Cancelled || result.TimedOut {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
	if !checkedClosedDescriptors {
		t.Fatal("after-Start descriptor assertion did not run")
	}
}

func TestNormalLeaderExitKillsTermIgnoringInheritedPipeDescendant(t *testing.T) {
	runner, request := selfProcessFixture(t, "background-descendant")
	request.Timeout = 30 * time.Second
	request.TerminationGrace = time.Second
	started := time.Now()
	result, err := runner.RunProcess(context.Background(), request)
	if err != nil || !result.Exited || result.ExitCode != 0 || result.TimedOut || result.Cancelled {
		t.Fatalf("result/error = %#v / %v", result, err)
	}
	if time.Since(started) > 4*time.Second {
		t.Fatal("inherited output pipe prevented bounded completion")
	}
	text, ok := result.Stdout.SanitizedText()
	if !ok {
		t.Fatal("stdout was not sanitized text")
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(text))
	if parseErr != nil {
		t.Fatalf("descendant pid output = %q", text)
	}
	if killErr := unix.Kill(pid, 0); !errors.Is(killErr, unix.ESRCH) {
		t.Fatalf("descendant %d remains: %v", pid, killErr)
	}
}

func selfProcessFixture(t *testing.T, mode string) (*Runner, lifecycle.ProcessRequest) {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	expectation := expectationForFixture(t, path)
	directory := t.TempDir()
	cwd := directoryExpectationForFixture(t, directory)
	authority := fixtureAuthority{executables: map[string]string{
		"/qualified/process-test": path, "/qualified/ssh-helper": path,
	}, directory: directory}
	testProfile := mustProcessEnvironmentProfileID("process_test_v1")
	runner, err := newRunnerWithProfiles(Config{
		Authority: authority, SafeWorkingDirectory: cwd,
		DeniedExecutableDigests: []domain.ExecutableDigest{testDigest(t, '1')},
	}, append(mvpEnvironmentProfiles(), environmentProfileDefinition{
		id: testProfile,
		values: []lifecycle.EnvironmentBinding{
			{Name: "AI4J_PROCESS_HELPER", Value: mode},
			{Name: "AI4J_TEST_EXECUTABLE", Value: path},
		},
		allowExecutableSSH: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	request := validFixtureRequest("/qualified/process-test", expectation)
	request.EnvironmentProfile = testProfile
	request.Arguments = []string{"-test.run=^TestProcessRunnerChildHelper$"}
	request.Environment = []lifecycle.EnvironmentBinding{
		{Name: "AI4J_PROCESS_HELPER", Value: mode},
		{Name: "AI4J_TEST_EXECUTABLE", Value: path},
	}
	request.Timeout = 30 * time.Second
	request.TerminationGrace = time.Second
	return runner, request
}

func TestProcessRunnerChildHelper(t *testing.T) {
	mode := os.Getenv("AI4J_PROCESS_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "nonzero":
		os.Exit(7)
	case "signal":
		_ = unix.Kill(os.Getpid(), unix.SIGTERM)
		select {}
	case "ignore-term":
		signal.Ignore(unix.SIGTERM)
		fmt.Fprintln(os.Stdout, os.Getpid())
		time.Sleep(30 * time.Second)
	case "dual-output":
		var writes sync.WaitGroup
		writes.Add(2)
		go func() {
			defer writes.Done()
			writeAllHelper(os.Stdout, helperStdoutPayload())
		}()
		go func() {
			defer writes.Done()
			writeAllHelper(os.Stderr, helperStderrPayload())
		}()
		writes.Wait()
		os.Exit(0)
	case "exec-helper":
		helper := os.Getenv(executableSSHEnvironment)
		if helper == "" || os.Getenv(executableSSHVariant) != executableSSHVariantValue {
			os.Exit(95)
		}
		pid, err := syscall.ForkExec(helper, []string{helper, "-test.run=^TestProcessRunnerChildHelper$"}, &syscall.ProcAttr{
			Env:   []string{"AI4J_PROCESS_HELPER=ssh-helper"},
			Files: []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()},
		})
		if err != nil {
			os.Exit(96)
		}
		child, err := os.FindProcess(pid)
		if err != nil {
			os.Exit(97)
		}
		state, err := child.Wait()
		if err != nil || !state.Success() {
			os.Exit(98)
		}
		os.Exit(0)
	case "ssh-helper":
		fmt.Fprintln(os.Stdout, "ssh-fd-executed")
		os.Exit(0)
	case "background-descendant":
		executable := os.Getenv("AI4J_TEST_EXECUTABLE")
		if executable == "" {
			os.Exit(90)
		}
		readyRead, readyWrite, err := os.Pipe()
		if err != nil {
			os.Exit(91)
		}
		command := exec.Command(executable, "-test.run=^TestProcessRunnerChildHelper$")
		command.Env = []string{"AI4J_PROCESS_HELPER=descendant"}
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		command.ExtraFiles = []*os.File{readyWrite}
		if err := command.Start(); err != nil {
			os.Exit(92)
		}
		_ = readyWrite.Close()
		var ready [1]byte
		if _, err := readyRead.Read(ready[:]); err != nil {
			os.Exit(93)
		}
		_ = readyRead.Close()
		fmt.Fprintln(os.Stdout, command.Process.Pid)
		os.Exit(0)
	case "descendant":
		signal.Ignore(unix.SIGTERM)
		ready := os.NewFile(3, "ready")
		_, _ = ready.Write([]byte{1})
		_ = ready.Close()
		time.Sleep(30 * time.Second)
	default:
		os.Exit(94)
	}
}

func helperStdoutPayload() []byte {
	return bytes.Repeat([]byte{'A', 0, 'B', 0xff}, 1024)
}

func helperStderrPayload() []byte {
	return bytes.Repeat([]byte{'x', 0, 0xc2, 0x80, 0xff, '\n'}, 1024)
}

func writeAllHelper(file *os.File, content []byte) {
	for len(content) > 0 {
		written, err := file.Write(content)
		if err != nil || written <= 0 {
			os.Exit(99)
		}
		content = content[written:]
	}
}

func validFixtureRequest(locator string, expectation lifecycle.ExecutableExpectation) lifecycle.ProcessRequest {
	return lifecycle.ProcessRequest{
		Executable: locator, EnvironmentProfile: isolatedProfileID, Environment: []lifecycle.EnvironmentBinding{},
		Timeout: time.Second, OutputLimitBytes: 1024, TerminationGrace: 100 * time.Millisecond,
		ExpectedExecutable: expectation,
	}
}

func newFixtureRunner(t *testing.T, authority DescriptorAuthority, cwd lifecycle.DirectoryExpectation, denied domain.ExecutableDigest) *Runner {
	t.Helper()
	value, err := NewMVP(authority, cwd, []domain.ExecutableDigest{denied})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func writeNativeFixture(t *testing.T, directory, name string, payload byte) string {
	t.Helper()
	content := make([]byte, 64)
	binary.LittleEndian.PutUint32(content[0:4], 0xfeedfacf)
	binary.LittleEndian.PutUint32(content[4:8], 0x0100000c)
	binary.LittleEndian.PutUint32(content[8:12], 0)
	binary.LittleEndian.PutUint32(content[12:16], 2)
	for index := 32; index < len(content); index++ {
		content[index] = payload
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, content, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func expectationForFixture(t *testing.T, path string) lifecycle.ExecutableExpectation {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	facts, err := inspectExecutableDescriptor(file)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := (executableprofile.Prover{}).Prove(context.Background(), file, facts.size, maximumExecutableBytes)
	if err != nil {
		t.Fatal(err)
	}
	result := lifecycle.ExecutableExpectation{
		Identity: facts.identity, Authority: lifecycle.CurrentUserAuthority, OwnerClass: facts.owner, Mode: facts.mode,
		Digest: proof.Digest, Profile: proof.Profile,
	}
	if !result.Valid() {
		t.Fatalf("invalid fixture expectation: %v", result.Profile)
	}
	return result
}

func TestExecutableDescriptorFactsRequireCoherentAuthorityAndEffectiveExecutePermission(t *testing.T) {
	path := writeNativeFixture(t, t.TempDir(), "native", 'a')
	expectation := expectationForFixture(t, path)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := inspectExecutableDescriptor(file)
	_ = file.Close()
	if err != nil || !matchesExecutableFacts(facts, expectation) {
		t.Fatalf("valid executable facts = %#v, %v", facts, err)
	}

	zeroAuthority := expectation
	zeroAuthority.Authority = ""
	if matchesExecutableFacts(facts, zeroAuthority) {
		t.Fatal("zero executable authority matched descriptor facts")
	}
	wrongAuthority := expectation
	wrongAuthority.Authority = lifecycle.SystemOwnedChainAuthority
	if matchesExecutableFacts(facts, wrongAuthority) {
		t.Fatal("system authority matched a current-user executable")
	}
	ownerBitAbsent := expectation
	ownerBitAbsent.Mode = 0o001
	ownerFacts := facts
	ownerFacts.mode = 0o001
	if matchesExecutableFacts(ownerFacts, ownerBitAbsent) {
		t.Fatal("current-user executable matched without owner execute permission")
	}
	if os.Geteuid() != 0 {
		systemExpectation := expectation
		systemExpectation.Authority = lifecycle.SystemOwnedChainAuthority
		systemExpectation.OwnerClass = lifecycle.SystemOwner
		systemExpectation.Mode = 0o100
		systemFacts := facts
		systemFacts.owner = lifecycle.SystemOwner
		systemFacts.mode = 0o100
		if matchesExecutableFacts(systemFacts, systemExpectation) {
			t.Fatal("system executable matched without other execute permission")
		}
	}
}

func directoryExpectationForFixture(t *testing.T, path string) lifecycle.DirectoryExpectation {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		t.Fatal(err)
	}
	return lifecycle.DirectoryExpectation{
		Root: lifecycle.StateRoot, Path: "cwd",
		RootIdentity:   lifecycle.ObjectIdentity{Filesystem: uint64(stat.Dev), Object: stat.Ino + 1},
		ParentIdentity: lifecycle.ObjectIdentity{Filesystem: uint64(stat.Dev), Object: stat.Ino + 2},
		Identity:       lifecycle.ObjectIdentity{Filesystem: uint64(stat.Dev), Object: stat.Ino},
	}
}

func containsEnvironmentName(environment []string, name string) bool {
	prefix := name + "="
	for _, binding := range environment {
		if strings.HasPrefix(binding, prefix) {
			return true
		}
	}
	return false
}
