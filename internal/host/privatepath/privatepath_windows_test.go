//go:build windows

package privatepath

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestEnsureDirectoryAppliesOwnerRestrictedACLToChildren(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := EnsureDirectory(root); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "state.json")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, file} {
		descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		dacl, _, err := descriptor.DACL()
		if err != nil || dacl == nil {
			t.Fatalf("ACL for %s is unavailable, err=%v", path, err)
		}
		if dacl.AceCount != 2 {
			t.Fatalf("ACL for %s has %d entries", path, dacl.AceCount)
		}
	}
	descriptor, _ := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("private root DACL is not protected: control=%x err=%v", control, err)
	}
}

func TestEnsureDirectoryRejectsReparsePointBeforeCreatingChild(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("Windows symbolic links are unavailable: %v", err)
	}
	child := filepath.Join(link, "must-not-exist")
	if err := EnsureDirectory(child); err == nil {
		t.Fatal("reparse-point path was accepted")
	}
	if _, err := os.Lstat(filepath.Join(outside, "must-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("child was created through reparse point: %v", err)
	}
}

func TestRemoveAllRejectsReparsePointWithoutTouchingTarget(t *testing.T) {
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "cleanup")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("Windows symbolic links are unavailable: %v", err)
	}
	if err := RemoveAll(root); err == nil {
		t.Fatal("cleanup containing a reparse point was accepted")
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "keep" {
		t.Fatalf("outside marker = %q, err=%v", contents, err)
	}
}
