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
		{name: "claude", target: "claude", expected: []string{"plugins/ai4j-review/.claude-plugin/plugin.json", "plugins/ai4j-review/agents/repository-reviewer.md", "plugins/ai4j-review/skills/repository-review/SKILL.md", "plugins/ai4j-review/skills/repository-review/references/checklist.md", "plugins/ai4j-review/skills/repository-review/scripts/check-diff.ps1", "plugins/ai4j-review/skills/repository-review/scripts/check-diff.sh", "plugins/ai4j-tools/.claude-plugin/plugin.json", "plugins/ai4j-tools/.mcp.json", "configuration/rules/ai4j-rules.md", "ai4j-build.json"}},
		{name: "codex", target: "codex", expected: []string{"plugins/ai4j-review/.codex-plugin/plugin.json", "plugins/ai4j-review/skills/repository-review/SKILL.md", "plugins/ai4j-review/skills/repository-review/references/checklist.md", "plugins/ai4j-review/skills/repository-review/scripts/check-diff.ps1", "plugins/ai4j-review/skills/repository-review/scripts/check-diff.sh", "plugins/ai4j-tools/.codex-plugin/plugin.json", "plugins/ai4j-tools/.mcp.json", "configuration/AGENTS.md", "configuration/.codex/agents/repository-reviewer.toml", "ai4j-build.json"}},
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
				request, parseErr := cli.NewParser().Parse([]string{"ai4j", "build", "--target", test.target, "--host", "darwin-arm64", "--output", output, "--all"})
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
				if manifest.SourceCommit != testCommit || manifest.SourceDigest == "" || manifest.CLIBuild != testBuild || manifest.TargetProfile == "" || !manifest.Reproducible || len(manifest.Mappings) != 6 || len(manifest.Selection) != 6 {
					t.Fatalf("build manifest = %#v", manifest)
				}
				if test.target == "claude" {
					var plugin struct {
						Version *string `json:"version"`
						Author  struct {
							Name string `json:"name"`
						} `json:"author"`
					}
					if err := json.Unmarshal(snapshots[len(snapshots)-1]["plugins/ai4j-review/.claude-plugin/plugin.json"], &plugin); err != nil || plugin.Version != nil || plugin.Author.Name != "AI4J" {
						t.Fatalf("Claude plugin metadata = %#v, %v", plugin, err)
					}
				}
			}
			if !equalBuildTrees(snapshots[0], snapshots[1]) {
				t.Fatal("two builds from the same commit differ")
			}
			wantNativeValidations := 0
			if test.target == "claude" {
				wantNativeValidations = 4
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

func TestBuildUsesCodexAgentFilenameForOutputAndMapping(t *testing.T) {
	files := firstPartyFiles(t)
	const original = "plugins/ai4j-review/agents/repository-reviewer.md"
	const renamed = "plugins/ai4j-review/agents/review-assistant.md"
	files[renamed] = files[original]
	delete(files, original)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	assetByID(&manifest, "repository-reviewer").Path = renamed
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	output := filepath.Join(t.TempDir(), "codex-build")
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.NewParser().Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--bundle", "default"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone || len(report.Problems) != 0 {
		t.Fatalf("build failure=%s problems=%v", report.Failure, report.Problems)
	}

	tree := readBuildTree(t, output)
	const expected = "configuration/.codex/agents/review-assistant.toml"
	if _, ok := tree[expected]; !ok {
		t.Fatalf("build output is missing %s", expected)
	}
	if _, ok := tree["configuration/.codex/agents/repository-reviewer.toml"]; ok {
		t.Fatal("build emitted an agent path derived from the asset ID")
	}
	var built buildManifest
	if err := json.Unmarshal(tree["ai4j-build.json"], &built); err != nil {
		t.Fatal(err)
	}
	for _, mapping := range built.Mappings {
		if mapping.Canonical == "agent:repository-reviewer" {
			if mapping.Native != expected {
				t.Fatalf("agent mapping = %q, want %q", mapping.Native, expected)
			}
			return
		}
	}
	t.Fatal("agent mapping is missing")
}

func TestBuildRejectsCrossPackageCodexAgentOutputCollisionBeforeStaging(t *testing.T) {
	files := firstPartyFiles(t)
	const collidingSource = "plugins/codex-review/agents/repository-reviewer.md"
	files[collidingSource] = []byte("---\nname: tools-reviewer\n---\n\nReview tools.\n")
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Assets = append(manifest.Assets, asset{
		ID: "tools-reviewer", Type: "agent", Ownership: "package",
		Variants: []assetVariant{{ID: "codex", Path: collidingSource, Targets: []string{"codex"}, Hosts: []string{"darwin-arm64", "windows-amd64"}}},
	})
	codex := manifest.Targets["codex"]
	codex.Packages = append(codex.Packages, nativePackage{ID: "codex-review", Path: "plugins/codex-review", Assets: []string{"tools-reviewer"}})
	manifest.Targets["codex"] = codex
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	parent := t.TempDir()
	output := filepath.Join(parent, "codex-build")
	outputCapacityChecked := false
	service, err := NewService(Config{
		GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir(),
		Capacity: func(path string, _ uint64) error {
			if filepath.Clean(path) == filepath.Clean(parent) {
				outputCapacityChecked = true
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.NewParser().Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	_, statErr := os.Lstat(output)
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "codex_agent_output_collision" || !os.IsNotExist(statErr) || outputCapacityChecked {
		t.Fatalf("failure=%s problems=%v outputError=%v outputCapacityChecked=%t", report.Failure, report.Problems, statErr, outputCapacityChecked)
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
			request, err := cli.NewParser().Parse([]string{"ai4j.exe", "build", "--target", target, "--host", "windows-amd64", "--output", output, "--bundle", "default"})
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
				wantNativeValidations = 2
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
	request, _ := cli.NewParser().Parse([]string{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64", "--output", output, "--all"})
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
	request, _ := cli.NewParser().Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all"})
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
		{name: "bundle dependency closure", target: "codex", selection: []string{"--bundle", "default"}, wantReasons: map[string]string{"ai4j-rules": "bundle", "check-diff": "native_unit", "claude-tools": "native_unit", "repository-review": "native_unit", "repository-reviewer": "native_unit", "review-checklist": "dependency"}, wantPath: "plugins/ai4j-review/.codex-plugin/plugin.json"},
		{name: "mixed selection", target: "claude", selection: []string{"--asset", "ai4j-rules", "--bundle", "default"}, wantReasons: map[string]string{"ai4j-rules": "explicit", "check-diff": "native_unit", "claude-tools": "native_unit", "repository-review": "native_unit", "repository-reviewer": "native_unit", "review-checklist": "dependency"}, wantPath: "plugins/ai4j-review/.claude-plugin/plugin.json"},
		{name: "native unit expansion", target: "claude", selection: []string{"--asset", "repository-review"}, wantReasons: map[string]string{"check-diff": "dependency", "repository-review": "explicit", "repository-reviewer": "native_unit", "review-checklist": "dependency"}, wantPath: "plugin/.claude-plugin/plugin.json"},
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
			request, parseErr := cli.NewParser().Parse(arguments)
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
	request, _ := cli.NewParser().Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--asset", "missing-asset"})
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	_, statErr := os.Lstat(output)
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "unknown_asset" || !os.IsNotExist(statErr) {
		t.Fatalf("failure=%s problems=%v outputError=%v", report.Failure, report.Problems, statErr)
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
	request, err := cli.NewParser().Parse([]string{"ai4j", "build", "--source", checkout, "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all", "--allow-dirty"})
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
