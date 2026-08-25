package validate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
)

func TestManifestV2RejectsDependencyAndVariantFailures(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*toolkitManifestV2)
		build    bool
		wantCode string
	}{
		{name: "missing dependency", mutate: func(manifest *toolkitManifestV2) {
			manifest.Assets[0].Dependencies = append(manifest.Assets[0].Dependencies, "missing-asset")
		}, wantCode: "missing_dependency"},
		{name: "dependency cycle", mutate: func(manifest *toolkitManifestV2) { manifest.Assets[3].Dependencies = []string{"repository-review"} }, wantCode: "dependency_cycle"},
		{name: "ambiguous variant", mutate: func(manifest *toolkitManifestV2) {
			manifest.Assets[2].Path = ""
			manifest.Assets[2].Variants = []assetVariantV2{{ID: "first", Path: "toolkit/rules/ai4j.md", Targets: []string{"codex"}, Hosts: []string{"darwin-arm64"}}, {ID: "second", Path: "toolkit/rules/ai4j.md", Targets: []string{"codex"}, Hosts: []string{"darwin-arm64"}}}
		}, build: true, wantCode: "ambiguous_variant"},
		{name: "unsupported variant", mutate: func(manifest *toolkitManifestV2) {
			manifest.Assets[2].Path = ""
			manifest.Assets[2].Variants = []assetVariantV2{{ID: "windows", Path: "toolkit/rules/ai4j.md", Targets: []string{"codex"}, Hosts: []string{"windows-amd64"}}}
		}, build: true, wantCode: "unsupported_variant"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := firstPartyFiles(t)
			var manifest toolkitManifestV2
			if err := json.Unmarshal(files["toolkit.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			test.mutate(&manifest)
			files["toolkit.json"], _ = json.Marshal(manifest)
			runner := &fixtureRunner{files: files}
			home := t.TempDir()
			if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			service, _ := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
			var failure Failure
			var code string
			if test.build {
				output := filepath.Join(t.TempDir(), "build")
				request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all"})
				report := service.Build(context.Background(), request.(cli.BuildRequest))
				failure = report.Failure
				if len(report.Problems) != 0 {
					code = report.Problems[0].Code()
				}
			} else {
				request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "validate"})
				report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())
				failure = report.Failure
				if len(report.Problems) != 0 {
					code = report.Problems[0].Code()
				}
			}
			if failure != FailureValidation || code != test.wantCode || runner.claudeValidations != 0 {
				t.Fatalf("failure=%s code=%s nativeValidations=%d", failure, code, runner.claudeValidations)
			}
		})
	}
}

func TestManifestV2RejectsUnknownFutureSchema(t *testing.T) {
	files := firstPartyFiles(t)
	var manifest map[string]any
	if err := json.Unmarshal(files["toolkit.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["schemaVersion"] = 99
	files["toolkit.json"], _ = json.Marshal(manifest)
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "validate"})
	report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "unsupported_schema" {
		t.Fatalf("failure=%s problems=%v", report.Failure, report.Problems)
	}
}
