package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		{name: "claude", target: "claude", expected: []string{"plugins/ai4j-review/.claude-plugin/plugin.json", "plugins/ai4j-review/skills/repository-review/SKILL.md", "plugins/ai4j-review/skills/repository-review/references/checklist.md", "plugins/ai4j-review/skills/repository-review/scripts/check-diff.sh", "plugins/ai4j-reviewer/.claude-plugin/plugin.json", "plugins/ai4j-reviewer/agents/repository-reviewer.md", "plugins/ai4j-tools/.claude-plugin/plugin.json", "plugins/ai4j-tools/.mcp.json", "configuration/rules/ai4j-rules.md", "ai4j-build.json"}},
		{name: "codex", target: "codex", expected: []string{"plugins/ai4j-review/.codex-plugin/plugin.json", "plugins/ai4j-review/skills/repository-review/SKILL.md", "plugins/ai4j-review/skills/repository-review/references/checklist.md", "plugins/ai4j-review/skills/repository-review/scripts/check-diff.sh", "plugins/ai4j-tools/.codex-plugin/plugin.json", "plugins/ai4j-tools/.mcp.json", "configuration/AGENTS.md", "configuration/.codex/agents/repository-reviewer.toml", "ai4j-build.json"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			home := t.TempDir()
			temporary := t.TempDir()
			runner := &fixtureRunner{files: files}
			service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: temporary})
			if err != nil {
				t.Fatal(err)
			}
			outputs := []string{filepath.Join(parent, "first"), filepath.Join(parent, "second")}
			var snapshots []map[string][]byte
			for _, output := range outputs {
				request, parseErr := cli.Parse([]string{"ai4j", "build", "--target", test.target, "--host", "darwin-arm64", "--output", output, "--all"})
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
					if err := json.Unmarshal(snapshots[len(snapshots)-1]["plugins/ai4j-review/.claude-plugin/plugin.json"], &plugin); err != nil || plugin.Version == nil || *plugin.Version != "1.0.0" || plugin.Author.Name != "AI4J" {
						t.Fatalf("Claude plugin metadata = %#v, %v", plugin, err)
					}
					if !bytes.Equal(snapshots[len(snapshots)-1]["plugins/ai4j-reviewer/agents/repository-reviewer.md"], files["plugins/ai4j-reviewer-claude/agents/repository-reviewer.md"]) {
						t.Fatal("Claude agent was not preserved exactly")
					}
					assertBuildSubtreeEqual(t, files, "plugins/ai4j-review", snapshots[len(snapshots)-1], "plugins/ai4j-review", "plugins/ai4j-review/skills/repository-review/scripts/check-diff.ps1")
					assertBuildSubtreeEqual(t, files, "plugins/ai4j-reviewer-claude", snapshots[len(snapshots)-1], "plugins/ai4j-reviewer")
					assertBuildSubtreeEqual(t, files, "plugins/ai4j-tools", snapshots[len(snapshots)-1], "plugins/ai4j-tools")
				} else if !bytes.Equal(snapshots[len(snapshots)-1]["configuration/.codex/agents/repository-reviewer.toml"], files["plugins/ai4j-reviewer-codex/agents/repository-reviewer.toml"]) {
					t.Fatal("Codex agent was not preserved exactly")
				}
			}
			if !equalBuildTrees(snapshots[0], snapshots[1]) {
				t.Fatal("two builds from the same commit differ")
			}
			wantNativeValidations := 0
			wantCodexValidations := 0
			if test.target == "claude" {
				wantNativeValidations = 6
			} else {
				wantCodexValidations = 2
			}
			if runner.claudeValidations != wantNativeValidations || runner.codexValidations != wantCodexValidations || runner.toolkitExecutions != 0 {
				t.Fatalf("Claude validations=%d Codex validations=%d toolkit executions=%d", runner.claudeValidations, runner.codexValidations, runner.toolkitExecutions)
			}
			for _, validationHome := range runner.codexValidationHomes {
				if !inside(temporary, validationHome) {
					t.Errorf("Codex validation home %q is outside temporary root %q", validationHome, temporary)
				}
				if _, err := os.Lstat(validationHome); !os.IsNotExist(err) {
					t.Errorf("Codex validation home was not removed: %v", err)
				}
			}
			entries, readErr := os.ReadDir(home)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("target home was modified: entries=%v error=%v", entries, readErr)
			}
		})
	}
}

func TestBuildPreservesCodexAgentNameAndSourceFilename(t *testing.T) {
	files := firstPartyFiles(t)
	const source = "plugins/ai4j-reviewer-codex/agents/repository-reviewer.toml"
	files[source] = bytes.Replace(files[source], []byte(`name = "repository-reviewer"`), []byte(`name = "Repository Reviewer"`), 1)

	output := filepath.Join(t.TempDir(), "codex-build")
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--bundle", "default"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone || len(report.Problems) != 0 {
		t.Fatalf("build failure=%s problems=%v", report.Failure, report.Problems)
	}

	tree := readBuildTree(t, output)
	const expected = "configuration/.codex/agents/repository-reviewer.toml"
	if !bytes.Equal(tree[expected], files[source]) {
		t.Fatalf("build output is missing %s", expected)
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

func TestBuildRejectsClaudeAgentAsCodexConfiguration(t *testing.T) {
	files := firstPartyFiles(t)
	delete(files, "plugins/ai4j-reviewer-codex/agents/repository-reviewer.toml")
	const claudeAgent = "plugins/ai4j-reviewer-codex/agents/repository-reviewer.md"
	files[claudeAgent] = files["plugins/ai4j-reviewer-claude/agents/repository-reviewer.md"]
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	agent := assetByID(&manifest, "repository-reviewer")
	for index := range agent.Variants {
		if agent.Variants[index].ID == "codex" {
			agent.Variants[index].Path = claudeAgent
		}
	}
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	output := filepath.Join(t.TempDir(), "codex-build")
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--bundle", "default"})
	if err != nil {
		t.Fatal(err)
	}

	report := service.Build(context.Background(), request.(cli.BuildRequest))
	_, statErr := os.Lstat(output)
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "invalid_codex_agent" || !os.IsNotExist(statErr) {
		t.Fatalf("failure=%s problems=%v outputError=%v", report.Failure, report.Problems, statErr)
	}
}

func TestBuildRejectsCodexAgentWithNonNativeExtension(t *testing.T) {
	files := firstPartyFiles(t)
	const source = "plugins/ai4j-reviewer-codex/agents/repository-reviewer.toml"
	const renamed = "plugins/ai4j-reviewer-codex/agents/repository-reviewer.TOML"
	files[renamed] = files[source]
	delete(files, source)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	agent := assetByID(&manifest, "repository-reviewer")
	for index := range agent.Variants {
		if agent.Variants[index].ID == "codex" {
			agent.Variants[index].Path = renamed
		}
	}
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	output := filepath.Join(t.TempDir(), "codex-build")
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--bundle", "review"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "invalid_codex_agent" {
		t.Fatalf("failure=%s problems=%v", report.Failure, report.Problems)
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("failed build retained output: %v", err)
	}
}

func TestBuildRejectsInvalidCodexAgentConfiguration(t *testing.T) {
	for _, test := range []struct {
		name    string
		content []byte
	}{
		{name: "malformed TOML", content: []byte("name = [\n")},
		{name: "missing description", content: []byte("name = \"repository-reviewer\"\ndeveloper_instructions = \"Review.\"\n")},
		{name: "missing instructions", content: []byte("name = \"repository-reviewer\"\ndescription = \"Review.\"\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := firstPartyFiles(t)
			files["plugins/ai4j-reviewer-codex/agents/repository-reviewer.toml"] = test.content
			output := filepath.Join(t.TempDir(), "codex-build")
			service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--bundle", "review"})
			if err != nil {
				t.Fatal(err)
			}
			report := service.Build(context.Background(), request.(cli.BuildRequest))
			if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "invalid_codex_agent" {
				t.Fatalf("failure=%s problems=%v", report.Failure, report.Problems)
			}
		})
	}
}

func TestBuildPreservesCodexAgentRuntimeConfigurationExactly(t *testing.T) {
	files := firstPartyFiles(t)
	const source = "plugins/ai4j-reviewer-codex/agents/repository-reviewer.toml"
	files[source] = []byte("name = \"repository-reviewer\"\ndescription = \"Reviews repository changes.\"\nmodel = \"gpt-example\"\nmodel_reasoning_effort = \"high\"\nmodel_reasoning_summary = \"concise\"\nmodel_verbosity = \"low\"\npersonality = \"pragmatic\"\nservice_tier = \"priority\"\nnickname_candidates = [\"reviewer\", \"auditor\"]\ndeveloper_instructions = \"Review the requested scope.\"\n[features]\nshell_tool = false\nrequest_permissions_tool = false\n")
	output := filepath.Join(t.TempDir(), "codex-build")
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--bundle", "review"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone {
		t.Fatalf("failure=%s problems=%v", report.Failure, report.Problems)
	}
	built, err := os.ReadFile(filepath.Join(output, "configuration", ".codex", "agents", "repository-reviewer.toml"))
	if err != nil || !bytes.Equal(built, files[source]) {
		t.Fatalf("Codex agent runtime configuration changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(output, "plugin", ".codex-plugin", "plugin.json")); err != nil {
		t.Fatalf("review bundle plugin output: %v", err)
	}
	if _, err := os.Stat(filepath.Join(output, "plugins", "ai4j-review", ".codex-plugin", "plugin.json")); !os.IsNotExist(err) {
		t.Fatalf("single review plugin was not rendered at the canonical plugin root: %v", err)
	}
}

func TestBuildDoesNotRenderForeignHostCodexAgentVariantAsPluginContent(t *testing.T) {
	files := firstPartyFiles(t)
	const darwinSource = "plugins/ai4j-reviewer-codex/agents/repository-reviewer.toml"
	const windowsSource = "plugins/ai4j-reviewer-codex/agents/repository-reviewer-windows.toml"
	files[windowsSource] = append([]byte(nil), files[darwinSource]...)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	agent := assetByID(&manifest, "repository-reviewer")
	for index := range agent.Variants {
		if agent.Variants[index].ID == "codex" {
			agent.Variants[index].ID = "codex-darwin"
			agent.Variants[index].Hosts = []string{"darwin-arm64"}
		}
	}
	agent.Variants = append(agent.Variants, assetVariant{
		ID: "codex-windows", Path: windowsSource, Targets: []string{"codex"}, Hosts: []string{"windows-amd64"},
	})
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	output := filepath.Join(t.TempDir(), "codex-build")
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--asset", "repository-reviewer"})
	if err != nil {
		t.Fatal(err)
	}

	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone || len(report.Problems) != 0 {
		t.Fatalf("build failure=%s problems=%v", report.Failure, report.Problems)
	}
	tree := readBuildTree(t, output)
	if !bytes.Equal(tree["configuration/.codex/agents/repository-reviewer.toml"], files[darwinSource]) {
		t.Fatal("selected Codex agent configuration was not preserved")
	}
	if _, ok := tree["plugin/agents/repository-reviewer-windows.toml"]; ok {
		t.Fatal("foreign-host Codex agent variant was emitted")
	}
	if _, err := os.Lstat(filepath.Join(output, "plugin")); !os.IsNotExist(err) {
		t.Fatalf("agent-only selection produced a plugin: %v", err)
	}
}

func TestBuildKeepsASelectedFileSharedWithAForeignVariant(t *testing.T) {
	files := firstPartyFiles(t)
	const shared = "plugins/ai4j-reviewer-codex/references/shared.md"
	const windows = "plugins/ai4j-reviewer-codex/references/windows.md"
	files[shared] = []byte("Shared reference.\n")
	files[windows] = []byte("Windows reference.\n")
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Assets = append(manifest.Assets,
		asset{ID: "shared-reference", Type: "reference", Ownership: "package", Variants: []assetVariant{
			{ID: "codex", Path: shared, Targets: []string{"codex"}, Hosts: []string{"darwin-arm64", "windows-amd64"}},
		}},
		asset{ID: "host-reference", Type: "reference", Ownership: "package", Variants: []assetVariant{
			{ID: "darwin", Path: shared, Targets: []string{"codex"}, Hosts: []string{"darwin-arm64"}},
			{ID: "windows", Path: windows, Targets: []string{"codex"}, Hosts: []string{"windows-amd64"}},
		}},
	)
	unit := packageByID(&manifest, "codex", "ai4j-reviewer")
	unit.Assets = append(unit.Assets, "shared-reference", "host-reference")
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	output := filepath.Join(t.TempDir(), "codex-build")
	service, err := NewService(Config{GOOS: "windows", GOARCH: "amd64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "windows-amd64", "--output", output, "--all"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone || len(report.Problems) != 0 {
		t.Fatalf("build failure=%s problems=%v", report.Failure, report.Problems)
	}
	for _, relative := range []string{"plugins/ai4j-reviewer/references/shared.md", "plugins/ai4j-reviewer/references/windows.md"} {
		if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(relative))); err != nil {
			t.Errorf("selected output %s: %v", relative, err)
		}
	}
}

func TestBuildRejectsCrossPackageCodexAgentOutputCollisionBeforeStaging(t *testing.T) {
	files := firstPartyFiles(t)
	const collidingSource = "plugins/codex-review/agents/repository-reviewer.toml"
	files[collidingSource] = []byte("name = \"repository-reviewer\"\ndescription = \"Reviews tools.\"\ndeveloper_instructions = \"Review tools.\"\n")
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
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	_, statErr := os.Lstat(output)
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "codex_agent_output_collision" || !os.IsNotExist(statErr) || outputCapacityChecked {
		t.Fatalf("failure=%s problems=%v outputError=%v outputCapacityChecked=%t", report.Failure, report.Problems, statErr, outputCapacityChecked)
	}
}

func TestBuildPreservesSingleFileConfigurationDirectory(t *testing.T) {
	files := firstPartyFiles(t)
	const source = "toolkit/single-directory"
	const content = "only file\n"
	files[source+"/only.txt"] = []byte(content)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Assets = append(manifest.Assets, asset{
		ID: "single-directory", Type: "reference", Path: source, Ownership: "configuration",
	})
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files[toolkitManifestPath] = encoded

	output := filepath.Join(t.TempDir(), "build")
	service, err := NewService(Config{
		GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild,
		Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--asset", "single-directory"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone || len(report.Problems) != 0 {
		t.Fatalf("build failure=%s problems=%v", report.Failure, report.Problems)
	}
	path := filepath.Join(output, "configuration", "assets", "single-directory", "single-directory", "only.txt")
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != content {
		t.Fatalf("single-file directory content=%q error=%v", actual, err)
	}
	var built buildManifest
	if err := json.Unmarshal(readBuildTree(t, output)["ai4j-build.json"], &built); err != nil {
		t.Fatal(err)
	}
	wantNative := "configuration/assets/single-directory/single-directory"
	if len(built.Mappings) != 1 || built.Mappings[0].Canonical != "reference:single-directory" || built.Mappings[0].Native != wantNative {
		t.Fatalf("single-directory mapping=%#v want native %q", built.Mappings, wantNative)
	}
}

func TestGeneratedCodexBuildEmitsEverySelectedPackageAsset(t *testing.T) {
	files := firstPartyFiles(t)
	assets := []struct {
		id       string
		typeName string
		path     string
		content  string
	}{
		{id: "extra-prompt", typeName: "prompt", path: "plugins/codex-extra/commands/example.md", content: "Example prompt\n"},
		{id: "extra-reference", typeName: "reference", path: "plugins/codex-extra/references/guide.md", content: "Reference\n"},
		{id: "extra-support", typeName: "support", path: "plugins/codex-extra/support/data.txt", content: "Support data\n"},
		{id: "extra-extension", typeName: "extension", path: "plugins/codex-extra/extensions/extension.json", content: "{}\n"},
	}
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	unit := nativePackage{ID: "codex-extra", Path: "plugins/codex-extra"}
	for _, item := range assets {
		files[item.path] = []byte(item.content)
		manifest.Assets = append(manifest.Assets, asset{
			ID: item.id, Type: item.typeName, Ownership: "package",
			Variants: []assetVariant{{
				ID: "codex", Path: item.path, Targets: []string{"codex"},
				Hosts: []string{"darwin-arm64", "windows-amd64"},
			}},
		})
		unit.Assets = append(unit.Assets, item.id)
	}
	codex := manifest.Targets["codex"]
	codex.Packages = append(codex.Packages, unit)
	manifest.Targets["codex"] = codex
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files[toolkitManifestPath] = encoded

	output := filepath.Join(t.TempDir(), "build")
	service, err := NewService(Config{
		GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild,
		Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--asset", "extra-prompt"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone || len(report.Problems) != 0 {
		t.Fatalf("build failure=%s problems=%v", report.Failure, report.Problems)
	}
	if len(report.Selection) != len(assets) {
		t.Fatalf("selected assets=%d want=%d", len(report.Selection), len(assets))
	}
	for _, item := range assets {
		path := filepath.Join(output, "plugin", filepath.FromSlash(strings.TrimPrefix(item.path, unit.Path+"/")))
		content, err := os.ReadFile(path)
		if err != nil || string(content) != item.content {
			t.Errorf("asset %s content=%q error=%v", item.id, content, err)
		}
	}
	pluginBytes, err := os.ReadFile(filepath.Join(output, "plugin", ".codex-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("generated Codex plugin manifest: %v", err)
	}
	var plugin codexPluginManifest
	if err := json.Unmarshal(pluginBytes, &plugin); err != nil || plugin.Version != manifest.Toolkit.Version {
		t.Fatalf("generated Codex plugin version=%q want=%q error=%v", plugin.Version, manifest.Toolkit.Version, err)
	}
	if string(plugin.Commands) != `"./commands/example.md"` {
		t.Fatalf("generated Codex commands=%s", plugin.Commands)
	}
	var built buildManifest
	if err := json.Unmarshal(readBuildTree(t, output)["ai4j-build.json"], &built); err != nil {
		t.Fatal(err)
	}
	if len(built.Mappings) != len(assets) {
		t.Fatalf("build mappings=%d want=%d", len(built.Mappings), len(assets))
	}
	wantMappings := make(map[string]string, len(assets))
	for _, item := range assets {
		wantMappings[item.typeName+":"+item.id] = "plugin/" + strings.TrimPrefix(item.path, unit.Path+"/")
	}
	for _, mapping := range built.Mappings {
		want, ok := wantMappings[mapping.Canonical]
		if !ok {
			t.Errorf("unexpected mapping %s -> %s", mapping.Canonical, mapping.Native)
			continue
		}
		if mapping.Native != want {
			t.Errorf("mapping %s = %s, want %s", mapping.Canonical, mapping.Native, want)
		}
		delete(wantMappings, mapping.Canonical)
	}
	if len(wantMappings) != 0 {
		t.Fatalf("missing exact mappings: %v", wantMappings)
	}
}

func TestCodexManifestPathsUsesDeterministicNativeShape(t *testing.T) {
	for _, test := range []struct {
		name  string
		paths []string
		want  string
	}{
		{name: "none", want: ""},
		{name: "one", paths: []string{"./commands/review.md"}, want: `"./commands/review.md"`},
		{name: "many", paths: []string{"./commands/test.md", "./commands/review.md", "./commands/test.md"}, want: `["./commands/review.md","./commands/test.md"]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, err := codexManifestPaths(test.paths)
			if err != nil || string(actual) != test.want {
				t.Fatalf("paths=%v encoded=%s want=%s error=%v", test.paths, actual, test.want, err)
			}
		})
	}
}

func TestBuildPreservesNativeCodexPluginManifestExactly(t *testing.T) {
	files := firstPartyFiles(t)
	const path = "plugins/ai4j-review/.codex-plugin/plugin.json"
	files[path] = []byte("{\n  \"name\": \"ai4j-review\",\n  \"displayName\": \"AI4J Review\",\n  \"skills\": \"./skills/\"\n}\n")
	output := filepath.Join(t.TempDir(), "codex-build")
	service, err := NewService(Config{
		GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild,
		Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--asset", "repository-review"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone || len(report.Problems) != 0 {
		t.Fatalf("build failure=%s problems=%v", report.Failure, report.Problems)
	}
	built, err := os.ReadFile(filepath.Join(output, "plugin", ".codex-plugin", "plugin.json"))
	if err != nil || !bytes.Equal(built, files[path]) {
		t.Fatalf("native Codex plugin manifest changed: %v", err)
	}
}

func TestBuildMapsNativeCodexMCPAssetToItsCopiedPath(t *testing.T) {
	files := firstPartyFiles(t)
	const source = "plugins/ai4j-tools/.mcp.json"
	const moved = "plugins/ai4j-tools/config/servers.json"
	const pluginManifest = "plugins/ai4j-tools/.codex-plugin/plugin.json"
	files[moved] = files[source]
	delete(files, source)
	files[pluginManifest] = []byte("{\n  \"name\": \"ai4j-tools\",\n  \"version\": \"1.0.0\",\n  \"description\": \"AI4J tools\",\n  \"mcpServers\": \"./config/servers.json\"\n}\n")
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	assetByID(&manifest, "claude-tools").Path = moved
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	output := filepath.Join(t.TempDir(), "codex-build")
	service, err := NewService(Config{
		GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild,
		Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--asset", "claude-tools"})
	if err != nil {
		t.Fatal(err)
	}

	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone || len(report.Problems) != 0 {
		t.Fatalf("build failure=%s problems=%v", report.Failure, report.Problems)
	}
	tree := readBuildTree(t, output)
	if !bytes.Equal(tree["plugin/config/servers.json"], files[moved]) || !bytes.Equal(tree["plugin/.codex-plugin/plugin.json"], files[pluginManifest]) {
		t.Fatal("native Codex plugin was not copied exactly")
	}
	var built buildManifest
	if err := json.Unmarshal(tree["ai4j-build.json"], &built); err != nil {
		t.Fatal(err)
	}
	for _, mapping := range built.Mappings {
		if mapping.Canonical == "mcp:claude-tools" {
			if mapping.Native != "plugin/config/servers.json" {
				t.Fatalf("native MCP mapping=%q", mapping.Native)
			}
			return
		}
	}
	t.Fatal("native MCP mapping is missing")
}

func TestBuildRejectsCodexTransformedAssetOverlapBeforePublishing(t *testing.T) {
	files := firstPartyFiles(t)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Assets = append(manifest.Assets, asset{
		ID: "reviewer-support", Type: "support", Ownership: "package",
		Variants: []assetVariant{{
			ID: "codex", Path: "plugins/ai4j-reviewer-codex/agents", Targets: []string{"codex"},
			Hosts: []string{"darwin-arm64", "windows-amd64"},
		}},
	})
	codex := manifest.Targets["codex"]
	for index := range codex.Packages {
		if codex.Packages[index].ID == "ai4j-reviewer" {
			codex.Packages[index].Assets = append(codex.Packages[index].Assets, "reviewer-support")
		}
	}
	manifest.Targets["codex"] = codex
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	output := filepath.Join(t.TempDir(), "codex-build")
	service, err := NewService(Config{
		GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild,
		Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--bundle", "review"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	_, statErr := os.Lstat(output)
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "unsupported_codex_asset_overlap" || !os.IsNotExist(statErr) {
		t.Fatalf("failure=%s problems=%v outputError=%v", report.Failure, report.Problems, statErr)
	}
}

func TestBuildRejectsGeneratedCodexMCPServerCollisionBeforeStaging(t *testing.T) {
	files := firstPartyFiles(t)
	const firstPath = "plugins/codex-mcp/first.json"
	const secondPath = "plugins/codex-mcp/second.json"
	const declaration = `{"mcpServers":{"shared":{"type":"stdio","command":"claude","args":["mcp","serve"]}}}`
	files[firstPath] = []byte(declaration)
	files[secondPath] = []byte(declaration)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	executable := &executable{Command: "claude", Args: []string{"mcp", "serve"}, Dependency: "required"}
	manifest.Assets = append(manifest.Assets,
		asset{ID: "first-mcp", Type: "mcp", Ownership: "package", Variants: []assetVariant{{ID: "codex", Path: firstPath, Targets: []string{"codex"}, Hosts: []string{"darwin-arm64", "windows-amd64"}, Executable: executable}}},
		asset{ID: "second-mcp", Type: "mcp", Ownership: "package", Variants: []assetVariant{{ID: "codex", Path: secondPath, Targets: []string{"codex"}, Hosts: []string{"darwin-arm64", "windows-amd64"}, Executable: executable}}},
	)
	codex := manifest.Targets["codex"]
	codex.Packages = append(codex.Packages, nativePackage{ID: "codex-mcp", Path: "plugins/codex-mcp", Assets: []string{"first-mcp", "second-mcp"}})
	manifest.Targets["codex"] = codex
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	parent := t.TempDir()
	output := filepath.Join(parent, "codex-build")
	outputCapacityChecked := false
	service, err := NewService(Config{
		GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild,
		Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir(),
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
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--asset", "first-mcp"})
	if err != nil {
		t.Fatal(err)
	}

	report := service.Build(context.Background(), request.(cli.BuildRequest))
	_, statErr := os.Lstat(output)
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "codex_mcp_output_collision" || !os.IsNotExist(statErr) || outputCapacityChecked {
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
			request, err := cli.Parse([]string{"ai4j.exe", "build", "--target", target, "--host", "windows-amd64", "--output", output, "--bundle", "default"})
			if err != nil {
				t.Fatal(err)
			}
			report := service.Build(context.Background(), request.(cli.BuildRequest))
			if report.Failure != FailureNone || report.Host != cli.BuildHostWindowsAMD64 {
				t.Fatalf("Windows build = failure:%s host:%s problems:%v", report.Failure, report.Host, report.Problems)
			}
			tree := readBuildTree(t, output)
			var manifest buildManifest
			if err := json.Unmarshal(tree["ai4j-build.json"], &manifest); err != nil || manifest.Host != cli.BuildHostWindowsAMD64 || manifest.Target != cli.BuildTarget(target) {
				t.Fatalf("Windows build manifest = %#v, %v", manifest, err)
			}
			if _, ok := tree["plugins/ai4j-review/skills/repository-review/scripts/check-diff.ps1"]; !ok {
				t.Fatal("Windows build is missing the selected PowerShell script variant")
			}
			if _, ok := tree["plugins/ai4j-review/skills/repository-review/scripts/check-diff.sh"]; ok {
				t.Fatal("Windows build contains the foreign Darwin script variant")
			}
			wantNativeValidations := 0
			if target == "claude" {
				wantNativeValidations = 3
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
	request, _ := cli.Parse([]string{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64", "--output", output, "--all"})
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	_, statErr := os.Lstat(output)
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "native_validation_failed" || !os.IsNotExist(statErr) {
		t.Fatalf("failure=%s problems=%v outputError=%v", report.Failure, report.Problems, statErr)
	}
}

func TestBuildClaudeConfigurationOnlySkipsNativePluginValidation(t *testing.T) {
	output := filepath.Join(t.TempDir(), "rules")
	runner := &fixtureRunner{files: firstPartyFiles(t), nativeExitCode: 1}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64", "--output", output, "--asset", "ai4j-rules"})
	if err != nil {
		t.Fatal(err)
	}

	report := service.Build(context.Background(), request.(cli.BuildRequest))

	if report.Failure != FailureNone || runner.claudeValidations != 0 || runner.toolkitExecutions != 0 {
		t.Fatalf("failure=%s problems=%v native validations=%d toolkit executions=%d", report.Failure, report.Problems, runner.claudeValidations, runner.toolkitExecutions)
	}
	if _, err := os.Stat(filepath.Join(output, "configuration", "rules", "ai4j-rules.md")); err != nil {
		t.Fatalf("rendered instruction is missing: %v", err)
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
	request, _ := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all"})
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
		{name: "review bundle with agent-only companion", target: "codex", selection: []string{"--bundle", "review"}, wantReasons: map[string]string{"ai4j-rules": "bundle", "check-diff": "native_unit", "repository-review": "native_unit", "repository-reviewer": "native_unit", "review-checklist": "dependency"}, wantPath: "plugin/.codex-plugin/plugin.json"},
		{name: "bundle dependency closure", target: "codex", selection: []string{"--bundle", "default"}, wantReasons: map[string]string{"ai4j-rules": "bundle", "check-diff": "native_unit", "claude-tools": "native_unit", "repository-review": "native_unit", "repository-reviewer": "native_unit", "review-checklist": "dependency"}, wantPath: "plugins/ai4j-review/.codex-plugin/plugin.json"},
		{name: "mixed selection", target: "claude", selection: []string{"--asset", "ai4j-rules", "--bundle", "default"}, wantReasons: map[string]string{"ai4j-rules": "explicit", "check-diff": "native_unit", "claude-tools": "native_unit", "repository-review": "native_unit", "repository-reviewer": "native_unit", "review-checklist": "dependency"}, wantPath: "plugins/ai4j-review/.claude-plugin/plugin.json"},
		{name: "native unit expansion", target: "claude", selection: []string{"--asset", "repository-review"}, wantReasons: map[string]string{"check-diff": "dependency", "repository-review": "explicit", "review-checklist": "dependency"}, wantPath: "plugin/.claude-plugin/plugin.json"},
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
			request, parseErr := cli.Parse(arguments)
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
	request, _ := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--asset", "missing-asset"})
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
	request, err := cli.Parse([]string{"ai4j", "build", "--source", checkout, "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all", "--allow-dirty"})
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

func assertBuildSubtreeEqual(t *testing.T, source map[string][]byte, sourceRoot string, built map[string][]byte, destinationRoot string, ignored ...string) {
	t.Helper()
	sourcePrefix := strings.TrimSuffix(sourceRoot, "/") + "/"
	destinationPrefix := strings.TrimSuffix(destinationRoot, "/") + "/"
	ignoredSources := make(map[string]struct{}, len(ignored))
	for _, path := range ignored {
		ignoredSources[path] = struct{}{}
	}
	wantCount := 0
	for path, content := range source {
		if !strings.HasPrefix(path, sourcePrefix) {
			continue
		}
		if _, skip := ignoredSources[path]; skip {
			continue
		}
		wantCount++
		destination := destinationPrefix + strings.TrimPrefix(path, sourcePrefix)
		builtContent, ok := built[destination]
		if !ok || !bytes.Equal(builtContent, content) {
			t.Fatalf("build output %s does not match source %s", destination, path)
		}
	}
	gotCount := 0
	for path := range built {
		if strings.HasPrefix(path, destinationPrefix) {
			gotCount++
		}
	}
	if gotCount != wantCount {
		t.Fatalf("build output %s has %d files, want complete %d-file source unit %s", destinationRoot, gotCount, wantCount, sourceRoot)
	}
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
