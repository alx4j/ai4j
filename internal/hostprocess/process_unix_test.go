//go:build darwin || linux

package hostprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestUnixProcessGroupTerminatesDescendants(t *testing.T) {
	role := os.Getenv("AI4J_GROUP_TEST_ROLE")
	if role == "grandchild" {
		time.Sleep(30 * time.Second)
		return
	}
	if role == "parent" {
		child := exec.Command(os.Args[0], childTestProcessArguments("TestUnixProcessGroupTerminatesDescendants")...)
		child.Env = append(os.Environ(), "AI4J_GROUP_TEST_ROLE=grandchild")
		if child.Start() != nil {
			os.Exit(2)
		}
		if publishProcessID(os.Getenv("AI4J_GROUP_TEST_PID"), child.Process.Pid) != nil {
			os.Exit(3)
		}
		time.Sleep(30 * time.Second)
		return
	}

	pidFile := processIDFile(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	invocation := newTestProcessInvocation(t, "TestUnixProcessGroupTerminatesDescendants", "AI4J_GROUP_TEST_ROLE=parent", "AI4J_GROUP_TEST_PID="+pidFile)
	outcomes := startProcess(func() (Result, error) {
		return (OSRunner{}).RunIsolated(ctx, "", os.Args[0], invocation.arguments, invocation.environment)
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
		t.Fatalf("process readiness: %v; RunIsolated() = %#v, %v", readinessErr, outcome.result, outcome.err)
	}
	if joinErr != nil {
		t.Fatal(joinErr)
	}
	if !errors.Is(outcome.err, context.Canceled) || !outcome.result.Started || outcome.result.TimedOut {
		t.Fatalf("RunIsolated() = %#v, %v", outcome.result, outcome.err)
	}
	if !waitForUnixProcessExit(pid) {
		t.Fatalf("grandchild process %d survived cancellation", pid)
	}
}

func waitForUnixProcessExit(pid int) bool {
	timer := time.NewTimer(processTestTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for processActiveUnix(pid) {
		select {
		case <-ticker.C:
		case <-timer.C:
			return false
		}
	}
	return true
}

func processActiveUnix(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
