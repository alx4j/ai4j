//go:build !windows

package hostprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func run(ctx context.Context, directory, executable string, arguments, environment []string, inheritEnvironment bool) (Result, error) {
	if ctx == nil || executable == "" {
		return Result{}, errors.New("invalid process request")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	command := exec.Command(executable, arguments...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	if inheritEnvironment {
		command.Env = append(os.Environ(), environment...)
	}
	var stdout, stderr limitedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return Result{}, err
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	var err error
	timedOut := false
	select {
	case err = <-wait:
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	case <-ctx.Done():
		timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-wait
		err = ctx.Err()
	}
	if stdout.overflow || stderr.overflow {
		return Result{}, errors.New("process output limit exceeded")
	}
	result := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Started: true, TimedOut: timedOut}
	if timedOut || ctx.Err() != nil {
		return result, err
	}
	if err == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	return Result{}, err
}
