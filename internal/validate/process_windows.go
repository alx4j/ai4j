//go:build windows

package validate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func runOSProcess(ctx context.Context, directory, executable string, arguments, environment []string, inheritEnvironment bool) (ProcessResult, error) {
	if ctx == nil || executable == "" {
		return ProcessResult{}, errors.New("invalid process request")
	}
	if err := ctx.Err(); err != nil {
		return ProcessResult{}, err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return ProcessResult{}, err
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return ProcessResult{}, err
	}

	command := exec.Command(executable, arguments...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	if inheritEnvironment {
		command.Env = append(os.Environ(), environment...)
	}
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	var stdout, stderr limitedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return ProcessResult{}, err
	}
	process, openErr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if openErr != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return ProcessResult{}, openErr
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if assignErr != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return ProcessResult{}, assignErr
	}
	if err := resumeProcess(uint32(command.Process.Pid)); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		_ = command.Wait()
		return ProcessResult{}, err
	}

	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case err = <-wait:
	case <-ctx.Done():
		_ = windows.TerminateJobObject(job, 1)
		<-wait
		return ProcessResult{Started: true, TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded)}, ctx.Err()
	}
	if stdout.overflow || stderr.overflow {
		return ProcessResult{}, errors.New("process output limit exceeded")
	}
	result := ProcessResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Started: true}
	if err == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	return ProcessResult{}, err
}

func resumeProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == pid {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, resumeErr := windows.ResumeThread(thread)
			_ = windows.CloseHandle(thread)
			return resumeErr
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			return err
		}
	}
}
