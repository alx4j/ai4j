//go:build windows

package hostprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsJobObjectTerminatesProcessTree(t *testing.T) {
	role := os.Getenv("AI4J_JOB_TEST_ROLE")
	if role == "grandchild" {
		time.Sleep(30 * time.Second)
		return
	}
	if role == "parent" {
		child := exec.Command(os.Args[0], childTestProcessArguments("TestWindowsJobObjectTerminatesProcessTree")...)
		child.Env = append(os.Environ(), "AI4J_JOB_TEST_ROLE=grandchild")
		if child.Start() != nil {
			os.Exit(2)
		}
		if publishProcessID(os.Getenv("AI4J_JOB_TEST_PID"), child.Process.Pid) != nil {
			os.Exit(3)
		}
		time.Sleep(30 * time.Second)
		return
	}

	pidFile := processIDFile(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	invocation := newTestProcessInvocation(t, "TestWindowsJobObjectTerminatesProcessTree", "AI4J_JOB_TEST_ROLE=parent", "AI4J_JOB_TEST_PID="+pidFile)
	outcomes := startProcess(func() (Result, error) {
		return (OSRunner{}).Run(ctx, "", os.Args[0], invocation.arguments, invocation.environment)
	})
	pid, early, readinessErr := waitForProcessID(pidFile, outcomes)
	cancel()
	outcome := processOutcome{}
	var joinErr error
	if early != nil {
		outcome = *early
	} else {
		outcome, joinErr = waitForProcessOutcome(outcomes)
	}
	if readinessErr != nil {
		t.Fatalf("process readiness: %v; Run() = %#v, %v", readinessErr, outcome.result, outcome.err)
	}
	if joinErr != nil {
		t.Fatal(joinErr)
	}
	if !errors.Is(outcome.err, context.Canceled) || !outcome.result.Started || outcome.result.TimedOut {
		t.Fatalf("Run() = %#v, %v", outcome.result, outcome.err)
	}
	if !waitForWindowsProcessExit(uint32(pid)) {
		t.Fatalf("grandchild process %d survived cancellation", pid)
	}
}

func TestWindowsIsolatedProcessDoesNotInheritAmbientEnvironment(t *testing.T) {
	if os.Getenv("AI4J_ISOLATED_TEST_ROLE") == "child" {
		if os.Getenv("AI4J_SECRET_CANARY") != "" || os.Getenv("AI4J_VISIBLE_CANARY") != "visible" || os.Getenv("GOCOVERDIR") == "" {
			os.Exit(4)
		}
		return
	}
	t.Setenv("AI4J_SECRET_CANARY", "must-not-inherit")
	invocation := newTestProcessInvocation(t, "TestWindowsIsolatedProcessDoesNotInheritAmbientEnvironment", "AI4J_ISOLATED_TEST_ROLE=child", "AI4J_VISIBLE_CANARY=visible")
	result, err := (OSRunner{}).RunIsolated(context.Background(), "", os.Args[0], invocation.arguments, invocation.environment)
	if err != nil || !result.Started || result.ExitCode != 0 {
		t.Fatalf("RunIsolated() = %#v, %v", result, err)
	}
}

func waitForWindowsProcessExit(pid uint32) bool {
	timer := time.NewTimer(processTestTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for processActive(pid) {
		select {
		case <-ticker.C:
		case <-timer.C:
			return false
		}
	}
	return true
}

func processActive(pid uint32) bool {
	const stillActive = 259
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == stillActive
}
