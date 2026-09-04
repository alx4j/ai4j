package validate

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
)

func TestSelectLifecycleReturnsCanonicalMultiPackageBundle(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fixtureRunner{files: firstPartyFiles(t)}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := cli.Parse([]string{"ai4j", "validate", "--target", "claude"})
	if err != nil {
		t.Fatal(err)
	}

	report := service.SelectLifecycle(context.Background(), parsed.(cli.ValidateRequest).Source(), "default")

	if report.Failure != FailureNone || len(report.Problems) != 0 || report.RequestedBundle != "default" {
		t.Fatalf("lifecycle selection = failure:%s problems:%v bundle:%q", report.Failure, report.Problems, report.RequestedBundle)
	}
	if !slices.Equal(report.ResolvedBundles, []string{"default", "review", "tools"}) ||
		!slices.Equal(report.ResolvedPackages, []string{"ai4j-review", "ai4j-reviewer", "ai4j-tools"}) ||
		!slices.Equal(report.ResolvedAssets, []string{"ai4j-rules", "check-diff", "claude-tools", "repository-review", "repository-reviewer", "review-checklist"}) {
		t.Fatalf("resolved = bundles:%v packages:%v assets:%v", report.ResolvedBundles, report.ResolvedPackages, report.ResolvedAssets)
	}
	if len(report.Packages) != 3 || runner.claudeValidations != 3 || !bytes.Equal(report.Rules, runner.files["toolkit/rules/ai4j.md"]) {
		t.Fatalf("packages=%d validations=%d rules=%d", len(report.Packages), runner.claudeValidations, len(report.Rules))
	}
	sourceRoots := map[string]string{
		"ai4j-review":   "plugins/ai4j-review",
		"ai4j-reviewer": "plugins/ai4j-reviewer-claude",
		"ai4j-tools":    "plugins/ai4j-tools",
	}
	for index, selected := range report.Packages {
		sourceRoot := sourceRoots[selected.ID]
		if selected.ID != report.ResolvedPackages[index] || selected.Path != sourceRoot || len(selected.NativeArtifact) == 0 {
			t.Fatalf("retained package %d = %#v", index, selected)
		}
		archived := readArchiveTree(t, selected.NativeArtifact)
		want := sourceUnitTree(runner.files, sourceRoot)
		if selected.ID == "ai4j-review" {
			delete(want, "skills/repository-review/scripts/check-diff.ps1")
		}
		if len(archived) != len(want) {
			t.Fatalf("retained package %s has %d files, want complete %d-file source unit", selected.ID, len(archived), len(want))
		}
		for path, content := range want {
			archivedContent, ok := archived[path]
			if !ok || !bytes.Equal(archivedContent, content) {
				t.Fatalf("retained package %s changed or omitted %s", selected.ID, path)
			}
		}
	}
}

func readArchiveTree(t *testing.T, artifact []byte) map[string][]byte {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(artifact), int64(len(artifact)))
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string][]byte, len(archive.File))
	for _, file := range archive.File {
		opened, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(opened)
		closeErr := opened.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read archived file %s: read=%v close=%v", file.Name, readErr, closeErr)
		}
		result[file.Name] = content
	}
	return result
}

func sourceUnitTree(files map[string][]byte, root string) map[string][]byte {
	prefix := strings.TrimSuffix(root, "/") + "/"
	result := map[string][]byte{}
	for path, content := range files {
		if strings.HasPrefix(path, prefix) {
			result[strings.TrimPrefix(path, prefix)] = content
		}
	}
	return result
}
