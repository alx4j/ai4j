package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alx4j/ai4j/internal/host/privatepath"
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

func TestWorkspaceCloseRetainsRecoveryMetadataUntilRemovalSucceeds(t *testing.T) {
	root := t.TempDir()
	value, err := Create(root, Recovery)
	if err != nil {
		t.Fatal(err)
	}
	path := value.Path()
	if err := os.WriteFile(filepath.Join(path, "payload"), []byte("keep until cleanup succeeds"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("remove failed")
	calls := 0
	value.removeAll = func(candidate string) error {
		calls++
		if calls == 1 {
			return wantErr
		}
		return privatepath.RemoveAll(candidate)
	}

	if err := value.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("first close error = %v, want %v", err, wantErr)
	}
	if value.Path() != path {
		t.Fatal("failed close marked the workspace closed")
	}
	if _, err := os.Stat(metadataPath(path, markerName)); err != nil {
		t.Fatalf("failed close removed recovery marker: %v", err)
	}
	if err := value.Close(); err != nil {
		t.Fatalf("retry close: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace remains after retry: %v", err)
	}
}

func TestScavengeRetainsMarkedCandidateWhenRemovalFails(t *testing.T) {
	root := t.TempDir()
	orphan, err := Create(root, BuildSource)
	if err != nil {
		t.Fatal(err)
	}
	path := orphan.Path()
	if err := os.WriteFile(filepath.Join(path, "payload"), []byte("keep until cleanup succeeds"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := orphan.lease.release(); err != nil {
		t.Fatal(err)
	}
	orphan.closed = true
	wantErr := errors.New("remove failed")

	if err := scavenge(root, func(string) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("scavenge error = %v, want %v", err, wantErr)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("failed scavenge removed candidate: %v", err)
	}
	if _, err := os.Stat(metadataPath(path, markerName)); err != nil {
		t.Fatalf("failed scavenge removed marker: %v", err)
	}
	if err := Scavenge(root); err != nil {
		t.Fatalf("retry scavenge: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate remains after retry: %v", err)
	}
}

func TestScavengeCompletesSidecarCleanupAfterWorkspaceRemoval(t *testing.T) {
	for _, test := range []struct {
		name        string
		removeLease bool
	}{
		{name: "complete pair"},
		{name: "marker only", removeLease: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			orphan, err := Create(root, Recovery)
			if err != nil {
				t.Fatal(err)
			}
			path := orphan.Path()
			if err := privatepath.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			if err := orphan.lease.release(); err != nil {
				t.Fatal(err)
			}
			orphan.closed = true
			if test.removeLease {
				if err := os.Remove(metadataPath(path, leaseName)); err != nil {
					t.Fatal(err)
				}
			}

			if err := Scavenge(root); err != nil {
				t.Fatal(err)
			}
			for _, metadata := range []string{markerName, leaseName} {
				if _, err := os.Lstat(metadataPath(path, metadata)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("orphan sidecar %s remains: %v", metadata, err)
				}
			}
		})
	}
}
