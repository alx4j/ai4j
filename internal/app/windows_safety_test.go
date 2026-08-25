//go:build windows

package app

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsOwnedReplacementFailurePreservesDestination(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "owned.json")
	temporary := filepath.Join(root, "replacement.tmp")
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporary, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, _ := windows.UTF16PtrFromString(destination)
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitOwnedReplacement(temporary, destination); err == nil {
		_ = windows.CloseHandle(handle)
		t.Fatal("replacement unexpectedly succeeded while destination was locked")
	}
	_ = windows.CloseHandle(handle)
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != "old" {
		t.Fatalf("destination = %q, err=%v", contents, err)
	}
}

func TestWindowsOwnedWriteRejectsReparseParent(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(home, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("Windows symbolic links are unavailable: %v", err)
	}
	if err := writeOwnedNew(home, filepath.Join(link, "owned.json"), []byte("unsafe")); err == nil {
		t.Fatal("owned write accepted a reparse-point parent")
	}
	if _, err := os.Lstat(filepath.Join(outside, "owned.json")); !os.IsNotExist(err) {
		t.Fatalf("owned file was written through reparse point: %v", err)
	}
}
