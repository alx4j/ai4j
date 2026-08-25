package installlock

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNewUsesOneUserMutationLock(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	locker, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Library", "Application Support", "ai4j", "state", "mutation.lock")
	if locker.Path() != want {
		t.Fatalf("Path() = %q, want %q", locker.Path(), want)
	}
	if _, err := New("relative"); err == nil {
		t.Fatal("New(relative) succeeded")
	}
	if _, err := locker.Acquire(nil); err == nil {
		t.Fatal("Acquire(nil) succeeded")
	}
	_ = context.Background()
}
