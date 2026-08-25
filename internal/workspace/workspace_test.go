package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceCloseAndPublish(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	value, err := Create(root, ValidateSource)
	if err != nil {
		t.Fatal(err)
	}
	path := value.Path()
	if err := value.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("closed workspace remains: %v", err)
	}

	stage, err := Create(root, BuildStage)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage.Path(), "artifact"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "output")
	if err := stage.Publish(output); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(output, "artifact")); err != nil || string(content) != "ok" {
		t.Fatalf("published content = %q, %v", content, err)
	}
	for _, metadata := range []string{markerName, leaseName} {
		if _, err := os.Lstat(metadataPath(stage.path, metadata)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("published metadata %s remains: %v", metadata, err)
		}
	}
}

func TestScavengeRemovesOnlyUnlockedMarkedWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	live, err := Create(root, Lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	orphan, err := Create(root, BuildSource)
	if err != nil {
		t.Fatal(err)
	}
	orphanPath := orphan.Path()
	if err := orphan.lease.release(); err != nil {
		t.Fatal(err)
	}
	orphan.closed = true
	unrelated := filepath.Join(root, "ai4j-validate-unrelated")
	if err := os.Mkdir(unrelated, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Scavenge(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(orphanPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan remains: %v", err)
	}
	for _, retained := range []string{live.Path(), unrelated} {
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("retained path %s: %v", retained, err)
		}
	}
}
