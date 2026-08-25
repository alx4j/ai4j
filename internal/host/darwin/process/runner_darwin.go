//go:build darwin && arm64

package process

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/host/darwin/executableprofile"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"golang.org/x/sys/unix"
)

const (
	minimumWorkingDirectoryDescriptor       = 64
	maximumExecutableBytes            int64 = 512 << 20
	processGroupPoll                        = 10 * time.Millisecond
	collectorCloseJoin                      = 250 * time.Millisecond
)

// Runner owns an immutable launch policy. The descriptor authority remains
// responsible for canonical no-follow path traversal.
type Runner struct {
	authority DescriptorAuthority
	safeCWD   lifecycle.DirectoryExpectation
	policy    policy
	prover    executableprofile.Prover
	hooks     runnerHooks
}

type runnerHooks struct {
	beforeCWDRevalidation func()
	beforeStart           func()
	afterStart            func([]*os.File, *os.File)
}

var _ lifecycle.ProcessRunner = (*Runner)(nil)

func New(config Config) (*Runner, error) {
	return newRunnerWithProfiles(config, mvpEnvironmentProfiles())
}

// NewMVP constructs the closed production runner policy. The caller supplies
// only descriptor authority, the private fallback cwd, and already-qualified
// denied executable digests; environment profiles are not caller-configurable.
func NewMVP(
	authority DescriptorAuthority,
	safeWorkingDirectory lifecycle.DirectoryExpectation,
	deniedExecutableDigests []domain.ExecutableDigest,
) (*Runner, error) {
	return New(Config{
		Authority:               authority,
		SafeWorkingDirectory:    safeWorkingDirectory,
		DeniedExecutableDigests: deniedExecutableDigests,
	})
}

func newRunnerWithProfiles(config Config, profiles []environmentProfileDefinition) (*Runner, error) {
	launchPolicy, err := newPolicyWithProfiles(config, profiles)
	if err != nil {
		return nil, err
	}
	return &Runner{
		authority: config.Authority,
		safeCWD:   config.SafeWorkingDirectory,
		policy:    launchPolicy,
	}, nil
}

type executableFacts struct {
	identity lifecycle.ObjectIdentity
	owner    lifecycle.OwnerClass
	mode     fs.FileMode
	size     int64
	links    uint64
}

type boundExecutable struct {
	file        *os.File
	path        string
	locator     string
	expectation lifecycle.ExecutableExpectation
	native      bool
}

type preparedLaunch struct {
	path           string
	args           []string
	env            []string
	extraFiles     []*os.File
	cwd            *os.File
	cwdPath        string
	cwdExpectation lifecycle.DirectoryExpectation
	bound          []boundExecutable
}

func (p *preparedLaunch) close() {
	if p == nil {
		return
	}
	for _, file := range p.extraFiles {
		if file != nil {
			_ = file.Close()
		}
	}
	if p.cwd != nil {
		_ = p.cwd.Close()
	}
	p.extraFiles = nil
	p.bound = nil
	p.cwd = nil
}

func (p *preparedLaunch) command(stdout, stderr *os.File) *exec.Cmd {
	command := &exec.Cmd{
		Path:   p.path,
		Args:   append([]string(nil), p.args...),
		Env:    append([]string{}, p.env...),
		Dir:    p.cwdPath,
		Stdout: stdout,
		Stderr: stderr,
		SysProcAttr: &syscall.SysProcAttr{
			Setpgid: true,
		},
	}
	return command
}

func (r *Runner) prepareLaunch(ctx context.Context, request lifecycle.ProcessRequest) (_ *preparedLaunch, resultErr error) {
	if r == nil || ctx == nil || !request.Valid() {
		return nil, errInvalidProcessRequest
	}
	if err := r.preflightPolicy(request); err != nil {
		return nil, err
	}
	ordinary, err := r.policy.ordinaryEnvironment(request.EnvironmentProfile, request.Environment)
	if err != nil {
		return nil, err
	}

	workingDirectory := request.WorkingDirectory
	if workingDirectory.Empty() {
		workingDirectory = r.safeCWD
	}
	openedCWD, err := r.authority.OpenDirectory(ctx, workingDirectory)
	if err != nil {
		return nil, err
	}
	cwdPath, err := qualifiedDescriptorPath(openedCWD)
	if err != nil {
		_ = openedCWD.Close()
		return nil, err
	}
	cwd, err := reserveWorkingDirectory(openedCWD, workingDirectory)
	_ = openedCWD.Close()
	if err != nil {
		return nil, err
	}
	prepared := &preparedLaunch{cwd: cwd, cwdPath: cwdPath, cwdExpectation: workingDirectory}
	defer func() {
		if resultErr != nil {
			prepared.close()
		}
	}()

	main, err := r.openBoundExecutable(ctx, request.Executable, request.ExpectedExecutable, request.ExpectedExecutable.Profile.Kind() == lifecycle.StaticExecutableNative)
	if err != nil {
		return nil, err
	}
	if request.ExpectedExecutable.Profile.Kind() == lifecycle.StaticExecutableNative {
		prepared.extraFiles = append(prepared.extraFiles, main.file)
		prepared.bound = append(prepared.bound, main)
		prepared.path = main.path
		prepared.args = append([]string{request.Executable}, request.Arguments...)
	} else {
		if !request.Interpreter.Matches(request.ExpectedExecutable.Profile) {
			_ = main.file.Close()
			return nil, errInvalidProcessRequest
		}
		if r.deniedInterpreterBinding(request.Interpreter) {
			_ = main.file.Close()
			return nil, errProcessPolicyViolation
		}
		interpreter, openErr := r.openBoundExecutable(ctx, request.Interpreter.ResolvedPath, request.Interpreter.Executable, true)
		if openErr != nil {
			_ = main.file.Close()
			return nil, openErr
		}
		prepared.extraFiles = append(prepared.extraFiles, interpreter.file, main.file)
		prepared.bound = append(prepared.bound, interpreter, main)
		prepared.path = interpreter.path
		prepared.args = []string{request.Interpreter.Candidate}
		shebang, _ := request.ExpectedExecutable.Profile.Shebang()
		if shebang.Form() == lifecycle.ShebangDirect && shebang.FixedArgument() != "" {
			prepared.args = append(prepared.args, shebang.FixedArgument())
		}
		prepared.args = append(prepared.args, main.path)
		prepared.args = append(prepared.args, request.Arguments...)
	}

	helpers := append([]lifecycle.ExecutableEnvironmentBinding(nil), request.ExecutableEnvironment...)
	sort.Slice(helpers, func(left, right int) bool { return helpers[left].Name < helpers[right].Name })
	for _, helper := range helpers {
		bound, openErr := r.openBoundExecutable(ctx, helper.ResolvedPath, helper.ExpectedExecutable, true)
		if openErr != nil {
			return nil, openErr
		}
		prepared.extraFiles = append(prepared.extraFiles, bound.file)
		prepared.bound = append(prepared.bound, bound)
		if bindErr := r.policy.bindExecutableEnvironment(
			request.EnvironmentProfile,
			ordinary,
			helper.Name,
			bound.path,
		); bindErr != nil {
			return nil, bindErr
		}
	}
	prepared.env = environmentList(ordinary)
	if prepared.env == nil {
		prepared.env = []string{}
	}
	return prepared, nil
}

func (r *Runner) preflightPolicy(request lifecycle.ProcessRequest) error {
	if r.policy.deniedExecutable(request.Executable, request.ExpectedExecutable.Digest) {
		return errProcessPolicyViolation
	}
	if request.ExpectedExecutable.Profile.Kind() == lifecycle.StaticExecutableScript && r.deniedInterpreterBinding(request.Interpreter) {
		return errProcessPolicyViolation
	}
	for _, helper := range request.ExecutableEnvironment {
		if !r.policy.executableEnvironmentAllowed(request.EnvironmentProfile, helper.Name) ||
			r.policy.deniedExecutable(helper.ResolvedPath, helper.ExpectedExecutable.Digest) {
			return errProcessPolicyViolation
		}
	}
	return nil
}

func (r *Runner) revalidateWorkingDirectory(ctx context.Context, prepared *preparedLaunch) error {
	if r.hooks.beforeCWDRevalidation != nil {
		r.hooks.beforeCWDRevalidation()
	}
	opened, err := r.authority.OpenDirectory(ctx, prepared.cwdExpectation)
	if err != nil {
		return err
	}
	cwdPath, pathErr := qualifiedDescriptorPath(opened)
	refreshed, reserveErr := reserveWorkingDirectory(opened, prepared.cwdExpectation)
	closeErr := opened.Close()
	if pathErr != nil || reserveErr != nil || closeErr != nil {
		if refreshed != nil {
			_ = refreshed.Close()
		}
		return errors.Join(pathErr, reserveErr, closeErr)
	}
	old := prepared.cwd
	prepared.cwd = refreshed
	prepared.cwdPath = cwdPath
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (r *Runner) deniedInterpreterBinding(binding lifecycle.InterpreterBinding) bool {
	if r.policy.deniedExecutable(binding.Candidate, binding.Executable.Digest) ||
		r.policy.deniedExecutable(binding.ResolvedPath, binding.Executable.Digest) {
		return true
	}
	// Exact /usr/bin/env evidence is dispatched by AI4J and is never opened or
	// executed. A direct interpreter named env remains forbidden.
	if binding.Requirement.Form() == lifecycle.ShebangDirect {
		return r.policy.deniedExecutable(binding.Requirement.Interpreter(), binding.Executable.Digest)
	}
	return false
}

func (r *Runner) openBoundExecutable(ctx context.Context, locator string, expectation lifecycle.ExecutableExpectation, native bool) (boundExecutable, error) {
	if r.policy.deniedExecutable(locator, expectation.Digest) {
		return boundExecutable{}, errProcessPolicyViolation
	}
	file, err := r.authority.OpenExecutable(ctx, locator, expectation)
	if err != nil {
		return boundExecutable{}, err
	}
	path, err := qualifiedDescriptorPath(file)
	if err != nil {
		_ = file.Close()
		return boundExecutable{}, err
	}
	bound := boundExecutable{file: file, path: path, locator: locator, expectation: expectation, native: native}
	if err := r.verifyBoundExecutable(ctx, bound); err != nil {
		_ = file.Close()
		return boundExecutable{}, err
	}
	return bound, nil
}

func qualifiedDescriptorPath(file *os.File) (string, error) {
	if file == nil || file.Name() == "" || !filepath.IsAbs(file.Name()) || filepath.Clean(file.Name()) != file.Name() {
		return "", errExecutableChanged
	}
	return file.Name(), nil
}

func (r *Runner) verifyBoundExecutable(ctx context.Context, bound boundExecutable) error {
	before, err := inspectExecutableDescriptor(bound.file)
	if err != nil || !matchesExecutableFacts(before, bound.expectation) || before.size < 0 || before.size > maximumExecutableBytes {
		return errExecutableChanged
	}
	proofBefore, err := r.prover.Prove(ctx, bound.file, before.size, maximumExecutableBytes)
	if err != nil {
		return errors.Join(errExecutableChanged, err)
	}
	middle, err := inspectExecutableDescriptor(bound.file)
	if err != nil || middle != before {
		return errExecutableChanged
	}
	proofAfter, err := r.prover.Prove(ctx, bound.file, middle.size, maximumExecutableBytes)
	if err != nil {
		return errors.Join(errExecutableChanged, err)
	}
	after, err := inspectExecutableDescriptor(bound.file)
	if err != nil || after != middle || proofBefore != proofAfter ||
		proofAfter.Digest != bound.expectation.Digest || proofAfter.Profile != bound.expectation.Profile ||
		r.policy.deniedExecutable(bound.locator, proofAfter.Digest) {
		return errExecutableChanged
	}
	if bound.native && !runnableNativeDarwinARM64(proofAfter.Profile) {
		return errUnsupportedExecutable
	}
	return nil
}

func runnableNativeDarwinARM64(profile lifecycle.StaticExecutableProfile) bool {
	if profile.Kind() != lifecycle.StaticExecutableNative {
		return false
	}
	native, ok := profile.Native()
	return ok && native.Role() == lifecycle.NativeExecutable && native.Architectures().Contains(lifecycle.ExecutableARM64)
}

func inspectExecutableDescriptor(file *os.File) (executableFacts, error) {
	if file == nil {
		return executableFacts{}, errExecutableChanged
	}
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		return executableFacts{}, errExecutableChanged
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return executableFacts{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return executableFacts{}, errExecutableChanged
	}
	mode := fs.FileMode(stat.Mode & 0o777)
	if stat.Mode&unix.S_ISUID != 0 {
		mode |= fs.ModeSetuid
	}
	if stat.Mode&unix.S_ISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if stat.Mode&unix.S_ISVTX != 0 {
		mode |= fs.ModeSticky
	}
	owner := lifecycle.OtherOwner
	if stat.Uid == 0 {
		owner = lifecycle.SystemOwner
	} else if stat.Uid == uint32(os.Geteuid()) {
		owner = lifecycle.CurrentUserOwner
	}
	return executableFacts{
		identity: lifecycle.ObjectIdentity{Filesystem: uint64(stat.Dev), Object: stat.Ino},
		owner:    owner, mode: mode, size: stat.Size, links: uint64(stat.Nlink),
	}, nil
}

func matchesExecutableFacts(facts executableFacts, expectation lifecycle.ExecutableExpectation) bool {
	return expectation.Valid() && facts.identity == expectation.Identity && facts.owner == expectation.OwnerClass &&
		facts.mode == expectation.Mode && executableByRunner(facts) && facts.links > 0 &&
		facts.mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) == 0 && facts.mode.Perm()&0o022 == 0 &&
		!expectation.PrivilegeBearing && !expectation.WritableByUntrusted
}

func executableByRunner(facts executableFacts) bool {
	if facts.owner == lifecycle.CurrentUserOwner || facts.owner == lifecycle.SystemOwner && os.Geteuid() == 0 {
		return facts.mode.Perm()&0o100 != 0
	}
	return facts.mode.Perm()&0o001 != 0
}

func reserveWorkingDirectory(opened *os.File, expectation lifecycle.DirectoryExpectation) (*os.File, error) {
	if opened == nil || !expectation.Valid() {
		return nil, errExecutableChanged
	}
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil || limit.Cur <= minimumWorkingDirectoryDescriptor {
		return nil, errProcessPolicyViolation
	}
	fd, err := unix.FcntlInt(opened.Fd(), unix.F_DUPFD_CLOEXEC, minimumWorkingDirectoryDescriptor)
	if err != nil {
		return nil, err
	}
	reserved := os.NewFile(uintptr(fd), "ai4j-safe-working-directory")
	if reserved == nil {
		_ = unix.Close(fd)
		return nil, errProcessPolicyViolation
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		(lifecycle.ObjectIdentity{Filesystem: uint64(stat.Dev), Object: stat.Ino}) != expectation.Identity ||
		stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o700 != 0o700 || stat.Mode&0o022 != 0 ||
		stat.Mode&(unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) != 0 {
		_ = reserved.Close()
		return nil, errExecutableChanged
	}
	flags, err := unix.FcntlInt(reserved.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		_ = reserved.Close()
		return nil, errExecutableChanged
	}
	return reserved, nil
}

func (r *Runner) RunProcess(ctx context.Context, request lifecycle.ProcessRequest) (lifecycle.ProcessResult, error) {
	result := lifecycle.ProcessResult{ExitCode: -1}
	if r == nil || ctx == nil || !request.Valid() {
		return resultWithEmptyStreams(result, request), errInvalidProcessRequest
	}
	requestContext, cancel := context.WithTimeoutCause(ctx, request.Timeout, errRequestTimeout)
	defer cancel()

	prepared, err := r.prepareLaunch(requestContext, request)
	if err != nil {
		if contextErr := processContextError(ctx, requestContext, &result); contextErr != nil {
			return resultWithEmptyStreams(result, request), contextErr
		}
		return resultWithEmptyStreams(result, request), err
	}
	defer prepared.close()

	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return resultWithEmptyStreams(result, request), errProcessCapture
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		return resultWithEmptyStreams(result, request), errProcessCapture
	}
	stdoutResult := make(chan captureResult, 1)
	stderrResult := make(chan captureResult, 1)
	go func() {
		captured := capture(stdoutRead, request.OutputLimitBytes)
		_ = stdoutRead.Close()
		stdoutResult <- captured
	}()
	go func() {
		captured := capture(stderrRead, request.OutputLimitBytes)
		_ = stderrRead.Close()
		stderrResult <- captured
	}()

	for _, bound := range prepared.bound {
		if err := r.verifyBoundExecutable(requestContext, bound); err != nil {
			_ = stdoutWrite.Close()
			_ = stderrWrite.Close()
			stdout, stderr, collectErr := collectCaptured(stdoutRead, stderrRead, stdoutResult, stderrResult, request.TerminationGrace)
			if contextErr := processContextError(ctx, requestContext, &result); contextErr != nil {
				return finishStreams(result, request, stdout, stderr, errors.Join(contextErr, collectErr))
			}
			return finishStreams(result, request, stdout, stderr, errors.Join(err, collectErr))
		}
	}
	if err := r.revalidateWorkingDirectory(requestContext, prepared); err != nil {
		_ = stdoutWrite.Close()
		_ = stderrWrite.Close()
		stdout, stderr, collectErr := collectCaptured(stdoutRead, stderrRead, stdoutResult, stderrResult, request.TerminationGrace)
		if contextErr := processContextError(ctx, requestContext, &result); contextErr != nil {
			return finishStreams(result, request, stdout, stderr, errors.Join(contextErr, collectErr))
		}
		return finishStreams(result, request, stdout, stderr, errors.Join(err, collectErr))
	}
	if r.hooks.beforeStart != nil {
		r.hooks.beforeStart()
	}
	if err := requestContext.Err(); err != nil {
		_ = stdoutWrite.Close()
		_ = stderrWrite.Close()
		stdout, stderr, collectErr := collectCaptured(stdoutRead, stderrRead, stdoutResult, stderrResult, request.TerminationGrace)
		return finishStreams(result, request, stdout, stderr, errors.Join(processContextError(ctx, requestContext, &result), collectErr))
	}
	command := prepared.command(stdoutWrite, stderrWrite)
	if err := command.Start(); err != nil {
		_ = stdoutWrite.Close()
		_ = stderrWrite.Close()
		stdout, stderr, collectErr := collectCaptured(stdoutRead, stderrRead, stdoutResult, stderrResult, request.TerminationGrace)
		contextErr := processContextError(ctx, requestContext, &result)
		return finishStreams(result, request, stdout, stderr, errors.Join(contextErr, errProcessStart, collectErr))
	}
	result.Started = true
	// Start has consumed the final qualified paths. Parent authority
	// descriptors are no longer needed while untrusted code runs.
	closedExecutables := append([]*os.File(nil), prepared.extraFiles...)
	closedCWD := prepared.cwd
	prepared.close()
	if r.hooks.afterStart != nil {
		r.hooks.afterStart(closedExecutables, closedCWD)
	}
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()

	waitResult := make(chan error, 1)
	go func() { waitResult <- command.Wait() }()
	waitErr, contextEnded, teardownErr, cleanupDeadline := waitForProcess(requestContext, request.TerminationGrace, command.Process.Pid, waitResult)
	applyProcessState(&result, command.ProcessState)
	stdout, stderr, collectErr := collectCaptured(stdoutRead, stderrRead, stdoutResult, stderrResult, time.Until(cleanupDeadline))
	contextErr := error(nil)
	if contextEnded {
		contextErr = processContextError(ctx, requestContext, &result)
	}
	result, streamErr := finishStreams(result, request, stdout, stderr, errors.Join(contextErr, teardownErr, collectErr))
	if streamErr != nil {
		return result, streamErr
	}
	if contextErr != nil {
		return result, contextErr
	}
	// A nonzero exit or external signal is a typed command outcome, not a host
	// execution fault. Consumers interpret Exited/ExitCode/Signaled.
	if waitErr != nil && !result.Exited && !result.Signaled {
		return result, errProcessTeardown
	}
	return result, nil
}

func waitForProcess(ctx context.Context, grace time.Duration, processGroup int, wait <-chan error) (error, bool, error, time.Time) {
	// A terminal leader already waiting in the channel wins over a simultaneous
	// deadline, giving the boundary deterministic completion precedence.
	select {
	case err := <-wait:
		cleanupStart := time.Now()
		waitErr, teardownErr := teardownProcessGroup(processGroup, cleanupStart, grace, wait, true, err)
		return waitErr, false, teardownErr, cleanupStart.Add(grace)
	default:
	}
	select {
	case err := <-wait:
		cleanupStart := time.Now()
		waitErr, teardownErr := teardownProcessGroup(processGroup, cleanupStart, grace, wait, true, err)
		return waitErr, false, teardownErr, cleanupStart.Add(grace)
	case <-ctx.Done():
		select {
		case err := <-wait:
			cleanupStart := time.Now()
			waitErr, teardownErr := teardownProcessGroup(processGroup, cleanupStart, grace, wait, true, err)
			return waitErr, false, teardownErr, cleanupStart.Add(grace)
		default:
		}
		cleanupStart := time.Now()
		waitErr, teardownErr := teardownProcessGroup(processGroup, cleanupStart, grace, wait, false, nil)
		return waitErr, true, teardownErr, cleanupStart.Add(grace)
	}
}

func teardownProcessGroup(processGroup int, started time.Time, grace time.Duration, wait <-chan error, leaderDone bool, waitErr error) (error, error) {
	deadline := started.Add(grace)
	killAt := started.Add(grace / 2)
	alive, probeErr := processGroupAlive(processGroup)
	if probeErr != nil {
		return waitErr, errors.Join(errProcessTeardown, probeErr)
	}
	if alive {
		if err := signalProcessGroup(processGroup, unix.SIGTERM); err != nil {
			return waitErr, errors.Join(errProcessTeardown, err)
		}
	}
	killed := false
	for {
		if !leaderDone {
			select {
			case waitErr = <-wait:
				leaderDone = true
			default:
			}
		}
		alive, probeErr = processGroupAlive(processGroup)
		if probeErr != nil {
			return waitErr, errors.Join(errProcessTeardown, probeErr)
		}
		if leaderDone && !alive {
			return waitErr, nil
		}
		now := time.Now()
		if !killed && !now.Before(killAt) {
			if err := signalProcessGroup(processGroup, unix.SIGKILL); err != nil {
				return waitErr, errors.Join(errProcessTeardown, err)
			}
			killed = true
		}
		if !now.Before(deadline) {
			return waitErr, errProcessTeardown
		}
		pause := processGroupPoll
		if remaining := time.Until(deadline); remaining < pause {
			pause = remaining
		}
		timer := time.NewTimer(pause)
		if !leaderDone {
			select {
			case waitErr = <-wait:
				leaderDone = true
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
			}
		} else {
			<-timer.C
		}
	}
}

func processGroupAlive(processGroup int) (bool, error) {
	err := unix.Kill(-processGroup, 0)
	switch {
	case err == nil, errors.Is(err, unix.EPERM):
		return true, nil
	case errors.Is(err, unix.ESRCH):
		return false, nil
	default:
		return false, err
	}
}

func signalProcessGroup(processGroup int, signal unix.Signal) error {
	err := unix.Kill(-processGroup, signal)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

func collectCaptured(
	stdoutRead, stderrRead *os.File,
	stdoutResult, stderrResult <-chan captureResult,
	budget time.Duration,
) (captureResult, captureResult, error) {
	var stdout, stderr captureResult
	stdoutDone := false
	stderrDone := false
	timer := time.NewTimer(budget)
	defer timer.Stop()
	for !stdoutDone || !stderrDone {
		select {
		case stdout = <-stdoutResult:
			stdoutDone = true
			stdoutResult = nil
		case stderr = <-stderrResult:
			stderrDone = true
			stderrResult = nil
		case <-timer.C:
			_ = stdoutRead.Close()
			_ = stderrRead.Close()
			return joinClosedCollectors(stdout, stderr, stdoutDone, stderrDone, stdoutResult, stderrResult, budget)
		}
	}
	return stdout, stderr, nil
}

func joinClosedCollectors(
	stdout, stderr captureResult,
	stdoutDone, stderrDone bool,
	stdoutResult, stderrResult <-chan captureResult,
	budget time.Duration,
) (captureResult, captureResult, error) {
	joinBudget := budget
	if joinBudget <= 0 || joinBudget > collectorCloseJoin {
		joinBudget = collectorCloseJoin
	}
	timer := time.NewTimer(joinBudget)
	defer timer.Stop()
	for !stdoutDone || !stderrDone {
		select {
		case stdout = <-stdoutResult:
			stdoutDone = true
			stdoutResult = nil
		case stderr = <-stderrResult:
			stderrDone = true
			stderrResult = nil
		case <-timer.C:
			return stdout, stderr, errProcessTeardown
		}
	}
	return stdout, stderr, errProcessTeardown
}

func processContextError(parent, requestContext context.Context, result *lifecycle.ProcessResult) error {
	if parentErr := parent.Err(); parentErr != nil {
		if errors.Is(parentErr, context.DeadlineExceeded) {
			result.TimedOut = true
			return context.DeadlineExceeded
		} else {
			result.Cancelled = true
			return context.Canceled
		}
	}
	if requestContext.Err() != nil {
		result.TimedOut = true
		return errors.Join(context.DeadlineExceeded, errRequestTimeout)
	}
	return nil
}

func applyProcessState(result *lifecycle.ProcessResult, state *os.ProcessState) {
	if state == nil {
		return
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return
	}
	if status.Exited() {
		result.Exited = true
		result.ExitCode = status.ExitStatus()
	}
	if status.Signaled() {
		result.Signaled = true
		result.Signal = status.Signal().String()
	}
}

func resultWithEmptyStreams(result lifecycle.ProcessResult, request lifecycle.ProcessRequest) lifecycle.ProcessResult {
	stdoutMode, _ := lifecycle.NormalizeProcessOutputMode(request.StdoutMode)
	stderrMode, _ := lifecycle.NormalizeProcessOutputMode(request.StderrMode)
	result.Stdout, _ = lifecycle.NewProcessStream(stdoutMode, nil, false)
	result.Stderr, _ = lifecycle.NewProcessStream(stderrMode, nil, false)
	return result
}

func finishStreams(result lifecycle.ProcessResult, request lifecycle.ProcessRequest, stdout, stderr captureResult, prior error) (lifecycle.ProcessResult, error) {
	stdoutStream, stdoutErr := processStream(request.StdoutMode, stdout)
	stderrStream, stderrErr := processStream(request.StderrMode, stderr)
	if stdoutErr != nil || stderrErr != nil {
		return result, errors.Join(prior, errProcessCapture)
	}
	result.Stdout = stdoutStream
	result.Stderr = stderrStream
	return result, prior
}

func (r *Runner) String() string { return "<darwin-process-runner>" }

func (r *Runner) GoString() string { return r.String() }

func (r *Runner) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte(r.String()))
}
