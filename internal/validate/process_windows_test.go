//go:build windows

package validate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		time.Sleep(200 * time.Millisecond)
		child := exec.Command(os.Args[0], "-test.run=^TestWindowsJobObjectTerminatesProcessTree$")
		child.Env = append(os.Environ(), "AI4J_JOB_TEST_ROLE=grandchild")
		if child.Start() != nil {
			os.Exit(2)
		}
		if os.WriteFile(os.Getenv("AI4J_JOB_TEST_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0o600) != nil {
			os.Exit(3)
		}
		time.Sleep(30 * time.Second)
		return
	}

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := (OSProcessRunner{}).Run(ctx, "", os.Args[0], []string{"-test.run=^TestWindowsJobObjectTerminatesProcessTree$"}, []string{"AI4J_JOB_TEST_ROLE=parent", "AI4J_JOB_TEST_PID=" + pidFile})
	if !errors.Is(err, context.DeadlineExceeded) || !result.Started || !result.TimedOut {
		t.Fatalf("Run() = %#v, %v", result, err)
	}
	contents, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(contents))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for processActive(uint32(pid)) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if processActive(uint32(pid)) {
		t.Fatalf("grandchild process %d survived cancellation", pid)
	}
}

func TestWindowsIsolatedProcessDoesNotInheritAmbientEnvironment(t *testing.T) {
	if os.Getenv("AI4J_ISOLATED_TEST_ROLE") == "child" {
		if os.Getenv("AI4J_SECRET_CANARY") != "" || os.Getenv("AI4J_VISIBLE_CANARY") != "visible" {
			os.Exit(4)
		}
		return
	}
	t.Setenv("AI4J_SECRET_CANARY", "must-not-inherit")
	result, err := (OSProcessRunner{}).RunIsolated(context.Background(), "", os.Args[0], []string{"-test.run=^TestWindowsIsolatedProcessDoesNotInheritAmbientEnvironment$"}, []string{"AI4J_ISOLATED_TEST_ROLE=child", "AI4J_VISIBLE_CANARY=visible"})
	if err != nil || !result.Started || result.ExitCode != 0 {
		t.Fatalf("RunIsolated() = %#v, %v", result, err)
	}
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
