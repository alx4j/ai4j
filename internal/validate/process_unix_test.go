//go:build darwin || linux

package validate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		child := exec.Command(os.Args[0], "-test.run=^TestUnixProcessGroupTerminatesDescendants$")
		child.Env = append(os.Environ(), "AI4J_GROUP_TEST_ROLE=grandchild")
		if child.Start() != nil {
			os.Exit(2)
		}
		if os.WriteFile(os.Getenv("AI4J_GROUP_TEST_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0o600) != nil {
			os.Exit(3)
		}
		time.Sleep(30 * time.Second)
		return
	}

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := (OSProcessRunner{}).RunIsolated(ctx, "", os.Args[0], []string{"-test.run=^TestUnixProcessGroupTerminatesDescendants$"}, []string{"AI4J_GROUP_TEST_ROLE=parent", "AI4J_GROUP_TEST_PID=" + pidFile})
	if !errors.Is(err, context.DeadlineExceeded) || !result.Started || !result.TimedOut {
		t.Fatalf("RunIsolated() = %#v, %v", result, err)
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
	for processActiveUnix(pid) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if processActiveUnix(pid) {
		t.Fatalf("grandchild process %d survived cancellation", pid)
	}
}

func processActiveUnix(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
