package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
)

func TestBuildRendersDeterministicClaudeAndCodexOutputs(t *testing.T) {
	files := firstPartyFiles(t)
	tests := []struct {
		name     string
		target   string
		expected []string
	}{
		{name: "claude", target: "claude", expected: []string{"plugin/.claude-plugin/plugin.json", "plugin/.mcp.json", "plugin/agents/repository-reviewer.md", "plugin/skills/repository-review/SKILL.md", "plugin/skills/repository-review/references/checklist.md", "plugin/skills/repository-review/scripts/check-diff.ps1", "plugin/skills/repository-review/scripts/check-diff.sh", "configuration/rules/ai4j-rules.md", "ai4j-build.json"}},
		{name: "codex", target: "codex", expected: []string{"plugin/.codex-plugin/plugin.json", "plugin/.mcp.json", "plugin/skills/repository-review/SKILL.md", "plugin/skills/repository-review/references/checklist.md", "plugin/skills/repository-review/scripts/check-diff.ps1", "plugin/skills/repository-review/scripts/check-diff.sh", "configuration/AGENTS.md", "configuration/.codex/agents/repository-reviewer.toml", "ai4j-build.json"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			home := t.TempDir()
			runner := &fixtureRunner{files: files}
			service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			outputs := []string{filepath.Join(parent, "first"), filepath.Join(parent, "second")}
			var snapshots []map[string][]byte
			for _, output := range outputs {
				request, parseErr := cli.NewParser("darwin").Parse([]string{"ai4j", "build", "--target", test.target, "--host", "darwin-arm64", "--output", output, "--all"})
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				report := service.Build(context.Background(), request.(cli.BuildRequest))
				if report.Failure != FailureNone || len(report.Problems) != 0 || report.Source.Commit().OID().String() != testCommit || len(report.Artifacts) != len(test.expected) {
					t.Fatalf("build failure=%s problems=%v commit=%s artifacts=%d", report.Failure, report.Problems, report.Source.Commit().OID(), len(report.Artifacts))
				}
				snapshots = append(snapshots, readBuildTree(t, output))
				for _, expected := range test.expected {
					if _, ok := snapshots[len(snapshots)-1][expected]; !ok {
						t.Errorf("output is missing %s", expected)
					}
				}
				var manifest buildManifest
				if err := json.Unmarshal(snapshots[len(snapshots)-1]["ai4j-build.json"], &manifest); err != nil {
					t.Fatal(err)
				}
				if manifest.SourceCommit != testCommit || manifest.SourceDigest == "" || manifest.CLIBuild != testBuild || manifest.TargetProfile == "" || !manifest.Reproducible || len(manifest.Mappings) != 6 || len(manifest.Selection) != 6 || manifest.Migration != nil {
					t.Fatalf("build manifest = %#v", manifest)
				}
				if test.target == "claude" {
					var plugin struct {
						Version string `json:"version"`
						Author  struct {
							Name string `json:"name"`
						} `json:"author"`
					}
					if err := json.Unmarshal(snapshots[len(snapshots)-1]["plugin/.claude-plugin/plugin.json"], &plugin); err != nil || plugin.Version != "1.0.0" || plugin.Author.Name != "AI4J" {
						t.Fatalf("Claude plugin metadata = %#v, %v", plugin, err)
					}
				}
			}
			if !equalBuildTrees(snapshots[0], snapshots[1]) {
				t.Fatal("two builds from the same commit differ")
			}
			wantNativeValidations := 0
			if test.target == "claude" {
				wantNativeValidations = 2
			}
			if runner.claudeValidations != wantNativeValidations || runner.toolkitExecutions != 0 {
				t.Fatalf("native validations=%d toolkit executions=%d", runner.claudeValidations, runner.toolkitExecutions)
			}
			entries, readErr := os.ReadDir(home)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("target home was modified: entries=%v error=%v", entries, readErr)
			}
		})
	}
}

func TestWindowsBuildRendersWindowsHostProfile(t *testing.T) {
	for _, target := range []string{"claude", "codex"} {
		t.Run(target, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "windows-build")
			runner := &fixtureRunner{files: firstPartyFiles(t)}
			service, err := NewService(Config{GOOS: "windows", GOARCH: "amd64", Home: t.TempDir(), BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			request, err := cli.NewParser("windows").Parse([]string{"ai4j.exe", "build", "--target", target, "--host", "windows-amd64", "--output", output, "--bundle", "default"})
			if err != nil {
				t.Fatal(err)
			}
			report := service.Build(context.Background(), request.(cli.BuildRequest))
			if report.Failure != FailureNone || report.Host != cli.BuildHostWindowsAMD64 {
				t.Fatalf("Windows build = failure:%s host:%s problems:%v", report.Failure, report.Host, report.Problems)
			}
			var manifest buildManifest
			if err := json.Unmarshal(readBuildTree(t, output)["ai4j-build.json"], &manifest); err != nil || manifest.Host != cli.BuildHostWindowsAMD64 || manifest.Target != cli.BuildTarget(target) {
				t.Fatalf("Windows build manifest = %#v, %v", manifest, err)
			}
			wantNativeValidations := 0
			if target == "claude" {
				wantNativeValidations = 1
			}
			if runner.claudeValidations != wantNativeValidations || runner.toolkitExecutions != 0 {
				t.Fatalf("native validations=%d toolkit executions=%d", runner.claudeValidations, runner.toolkitExecutions)
			}
		})
	}
}

func TestBuildRejectsNativeInvalidOutputBeforePublishing(t *testing.T) {
	output := filepath.Join(t.TempDir(), "rejected")
	runner := &fixtureRunner{files: firstPartyFiles(t), nativeExitCode: 1}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64", "--output", output, "--all"})
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	_, statErr := os.Lstat(output)
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "native_validation_failed" || !os.IsNotExist(statErr) {
		t.Fatalf("failure=%s problems=%v outputError=%v", report.Failure, report.Problems, statErr)
	}
}

func TestBuildRefusesToOverwriteExistingOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(output, "owned-by-user")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fixtureRunner{files: firstPartyFiles(t)}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all"})
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	content, readErr := os.ReadFile(marker)
	if report.Failure != FailureConflict || len(report.Problems) != 1 || report.Problems[0].Code() != "output_occupied" || readErr != nil || string(content) != "unchanged" {
		t.Fatalf("failure=%s problems=%v marker=%q readErr=%v", report.Failure, report.Problems, content, readErr)
	}
}

func TestBuildResolvesAssetsBundlesDependenciesAndNativeUnits(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		selection   []string
		wantReasons map[string]string
		wantPath    string
	}{
		{name: "single configuration asset", target: "codex", selection: []string{"--asset", "ai4j-rules"}, wantReasons: map[string]string{"ai4j-rules": "explicit"}, wantPath: "configuration/AGENTS.md"},
		{name: "bundle dependency closure", target: "codex", selection: []string{"--bundle", "default"}, wantReasons: map[string]string{"ai4j-rules": "bundle", "check-diff": "dependency", "claude-tools": "bundle", "repository-review": "bundle", "repository-reviewer": "bundle", "review-checklist": "dependency"}, wantPath: "plugin/.codex-plugin/plugin.json"},
		{name: "mixed selection", target: "claude", selection: []string{"--asset", "ai4j-rules", "--bundle", "default"}, wantReasons: map[string]string{"ai4j-rules": "explicit", "check-diff": "dependency", "claude-tools": "bundle", "repository-review": "bundle", "repository-reviewer": "bundle", "review-checklist": "dependency"}, wantPath: "plugin/.claude-plugin/plugin.json"},
		{name: "native unit expansion", target: "claude", selection: []string{"--asset", "repository-review"}, wantReasons: map[string]string{"check-diff": "dependency", "claude-tools": "native_unit", "repository-review": "explicit", "repository-reviewer": "native_unit", "review-checklist": "dependency"}, wantPath: "plugin/.claude-plugin/plugin.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fixtureRunner{files: firstPartyFiles(t)}
			service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "build")
			arguments := []string{"ai4j", "build", "--target", test.target, "--host", "darwin-arm64", "--output", output}
			arguments = append(arguments, test.selection...)
			request, parseErr := cli.NewParser("darwin").Parse(arguments)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			report := service.Build(context.Background(), request.(cli.BuildRequest))
			if report.Failure != FailureNone || len(report.Selection) != len(test.wantReasons) {
				t.Fatalf("failure=%s problems=%v selection=%v", report.Failure, report.Problems, report.Selection)
			}
			for _, item := range report.Selection {
				if want := test.wantReasons[item.Asset()]; item.Reason() != want {
					t.Errorf("selection %s reason=%s want=%s", item.Asset(), item.Reason(), want)
				}
			}
			if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(test.wantPath))); err != nil {
				t.Fatalf("selected output %s: %v", test.wantPath, err)
			}
		})
	}
}

func TestBuildRejectsUnknownAssetBeforePublishing(t *testing.T) {
	output := filepath.Join(t.TempDir(), "build")
	service, _ := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: firstPartyFiles(t)}, TempRoot: t.TempDir()})
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--asset", "missing-asset"})
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	_, statErr := os.Lstat(output)
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "unknown_asset" || !os.IsNotExist(statErr) {
		t.Fatalf("failure=%s problems=%v outputError=%v", report.Failure, report.Problems, statErr)
	}
}

func TestBuildSchemaOneProducesMigrationPreview(t *testing.T) {
	files := firstPartyFiles(t)
	files["plugins/ai4j-default/.claude-plugin/plugin.json"] = []byte("{\n  \"name\": \"ai4j-default\",\n  \"description\": \"Legacy AI4J toolkit\"\n}\n")
	files["toolkit.json"] = []byte(`{
  "schemaVersion": 1,
  "toolkit": {"id": "ai4j"},
  "marketplace": {"id": "ai4j", "path": ".claude-plugin/marketplace.json"},
  "plugin": {"id": "ai4j-default", "path": "plugins/ai4j-default"},
  "sharedRules": [{"id": "ai4j-rules", "path": "toolkit/rules/ai4j.md"}],
  "executables": [{"id": "claude-mcp", "command": "claude", "ownership": "host", "dependency": "required"}]
}`)
	service, _ := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
	output := filepath.Join(t.TempDir(), "legacy")
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all"})
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone {
		t.Fatalf("legacy build failure=%s problems=%v", report.Failure, report.Problems)
	}
	var manifest buildManifest
	content, err := os.ReadFile(filepath.Join(output, "ai4j-build.json"))
	if err != nil || json.Unmarshal(content, &manifest) != nil || manifest.Migration == nil || manifest.Migration.FromSchema != 1 || manifest.Migration.ToSchema != 2 || len(manifest.Migration.Review) == 0 {
		t.Fatalf("migration manifest=%#v readError=%v", manifest.Migration, err)
	}
}

func TestBuildUsesReadOnlyDirtyLocalSnapshotAndMarksOutputNonReproducible(t *testing.T) {
	checkout, err := canonicalLocalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(checkout, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := firstPartyFiles(t)
	for path, content := range files {
		destination := filepath.Join(checkout, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "local-build")
	runner := &fixtureRunner{files: files, localRoot: checkout, localDirty: true}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.NewParser("darwin").Parse([]string{"ai4j", "build", "--source", checkout, "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all", "--allow-dirty"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone || report.Reproducible || report.Source.Mode() != cli.SourceDevelopment || !report.Source.Dirty() {
		t.Fatalf("local build report = %#v", report)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(output, "ai4j-build.json"))
	if err != nil || !bytes.Contains(manifestBytes, []byte(`"sourceMode": "development_source"`)) || !bytes.Contains(manifestBytes, []byte(`"reproducible": false`)) {
		t.Fatalf("local build manifest = %s, %v", manifestBytes, err)
	}
	if contents, err := os.ReadFile(filepath.Join(checkout, "toolkit.json")); err != nil || !bytes.Equal(contents, files["toolkit.json"]) {
		t.Fatalf("local source changed = %v", err)
	}
}

func readBuildTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)], err = os.ReadFile(path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func equalBuildTrees(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for path, content := range left {
		if !bytes.Equal(content, right[path]) {
			return false
		}
	}
	return true
}
