//go:build windows

package installlock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsLockBlocksConcurrentMutationAndReleases(t *testing.T) {
	locker, err := NewAt(filepath.Join(t.TempDir(), "state"))
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
