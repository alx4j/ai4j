package repocheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFormat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "drift.go")
	if err := os.WriteFile(path, []byte("package sample\nfunc drift( ){ }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := CheckFormat(root, []string{"drift.go"})
	if err == nil || !strings.Contains(err.Error(), "drift.go") {
		t.Fatalf("CheckFormat() error = %v, want formatting drift", err)
	}
}

func TestCheckFormatAcceptsCanonicalNestedGitPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "nested", "source.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package nested\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CheckFormat(root, []string{"nested/source.go"}); err != nil {
		t.Fatalf("CheckFormat() error = %v", err)
	}
}
