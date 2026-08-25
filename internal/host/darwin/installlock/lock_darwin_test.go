//go:build darwin

package installlock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestLockBlocksConcurrentMutationAndReleases(t *testing.T) {
	locker, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := locker.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := locker.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent Acquire() error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := locker.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestLockIsReleasedWhenOwnerProcessExits(t *testing.T) {
	home := os.Getenv("AI4J_LOCK_TEST_HOME")
	if home != "" {
		locker, err := New(home)
		if err != nil {
			os.Exit(2)
		}
		if _, err := locker.Acquire(context.Background()); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	home = t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestLockIsReleasedWhenOwnerProcessExits$")
	command.Env = append(os.Environ(), "AI4J_LOCK_TEST_HOME="+home)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lock owner process: %v: %s", err, output)
	}
	locker, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := locker.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire() after owner exit: %v", err)
	}
	if err := handle.Release(); err != nil {
		t.Fatal(err)
	}
}
