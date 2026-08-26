package validate

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
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
	parsed, err := cli.NewParser("darwin").Parse([]string{"ai4j", "validate", "--target", "claude"})
	if err != nil {
		t.Fatal(err)
	}

	report := service.SelectLifecycle(context.Background(), parsed.(cli.ValidateRequest).Source(), "default")

	if report.Failure != FailureNone || len(report.Problems) != 0 || report.RequestedBundle != "default" {
		t.Fatalf("lifecycle selection = failure:%s problems:%v bundle:%q", report.Failure, report.Problems, report.RequestedBundle)
	}
	if !slices.Equal(report.ResolvedBundles, []string{"default", "review", "tools"}) ||
		!slices.Equal(report.ResolvedPackages, []string{"ai4j-review", "ai4j-tools"}) ||
		!slices.Equal(report.ResolvedAssets, []string{"ai4j-rules", "check-diff", "claude-tools", "repository-review", "repository-reviewer", "review-checklist"}) {
		t.Fatalf("resolved = bundles:%v packages:%v assets:%v", report.ResolvedBundles, report.ResolvedPackages, report.ResolvedAssets)
	}
	if len(report.Packages) != 2 || runner.claudeValidations != 2 || !bytes.Equal(report.Rules, runner.files["toolkit/rules/ai4j.md"]) {
		t.Fatalf("packages=%d validations=%d rules=%d", len(report.Packages), runner.claudeValidations, len(report.Rules))
	}
	for index, selected := range report.Packages {
		if selected.ID != report.ResolvedPackages[index] || len(selected.NativeArtifact) == 0 {
			t.Fatalf("retained package %d = %#v", index, selected)
		}
		archive, openErr := zip.NewReader(bytes.NewReader(selected.NativeArtifact), int64(len(selected.NativeArtifact)))
		if openErr != nil || len(archive.File) == 0 {
			t.Fatalf("retained package %s is not a non-empty zip: %v", selected.ID, openErr)
		}
	}
}
