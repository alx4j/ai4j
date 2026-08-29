package validate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadLocalFileUsesTheValidatedOpenFile(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "toolkit.json"), []byte("toolkit"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	content, info, err := readLocalFile(root, "toolkit.json")

	if err != nil || string(content) != "toolkit" || info.Name() != "toolkit.json" {
		t.Fatalf("readLocalFile() = %q, %v, %v", content, info, err)
	}
}

func TestReadLocalFileRejectsSymlinks(t *testing.T) {
	rootPath := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(rootPath, "toolkit.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	_, _, err = readLocalFile(root, "toolkit.json")

	if !errors.Is(err, errLocalSourceInvalid) {
		t.Fatalf("readLocalFile() error = %v, want %v", err, errLocalSourceInvalid)
	}
}
