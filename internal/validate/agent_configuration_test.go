package validate

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
)

const (
	claudeActivationPath   = "plugins/ai4j-reviewer-claude/settings.json"
	claudeDarwinAgentPath  = "plugins/ai4j-reviewer-claude/agents/repository-reviewer.md"
	claudeWindowsAgentPath = "plugins/ai4j-reviewer-claude/agents/repository-reviewer-windows.md"
)

func TestClaudeAgentNameUsesParsedFrontmatter(t *testing.T) {
	for _, test := range []struct {
		name    string
		source  []byte
		want    string
		wantErr bool
	}{
		{name: "LF", source: []byte("---\nname: root-orchestrator\ndescription: Review\n---\nAct.\n"), want: "root-orchestrator"},
		{name: "CRLF and quoted name", source: []byte("---\r\nname: 'root-orchestrator'\r\ndescription: Review\r\n---\r\nAct.\r\n"), want: "root-orchestrator"},
		{name: "supported native metadata", source: []byte("---\nname: root-orchestrator\ndescription: Review\nmodel: sonnet\neffort: high\ntools: Read, Grep\ndisallowedTools: Write\nmaxTurns: 5\nskills: [repository-review]\nmemory: project\nbackground: true\nisolation: worktree\n---\nAct.\n"), want: "root-orchestrator"},
		{name: "missing delimiter", source: []byte("name: root-orchestrator\n"), wantErr: true},
		{name: "duplicate name", source: []byte("---\nname: root-orchestrator\nname: other-agent\ndescription: Review\n---\nAct.\n"), wantErr: true},
		{name: "unknown field", source: []byte("---\nname: root-orchestrator\ndescription: Review\nfutureNativeField: retained\n---\nAct.\n"), wantErr: true},
		{name: "invalid tools type", source: []byte("---\nname: root-orchestrator\ndescription: Review\ntools: {Read: true}\n---\nAct.\n"), wantErr: true},
		{name: "invalid background type", source: []byte("---\nname: root-orchestrator\ndescription: Review\nbackground: yes please\n---\nAct.\n"), wantErr: true},
		{name: "missing description", source: []byte("---\nname: root-orchestrator\n---\nAct.\n"), wantErr: true},
		{name: "blank description", source: []byte("---\nname: root-orchestrator\ndescription: '  '\n---\nAct.\n"), wantErr: true},
		{name: "blank instructions", source: []byte("---\nname: root-orchestrator\ndescription: Review\n---\n \n"), wantErr: true},
		{name: "alias", source: []byte("---\nname: root-orchestrator\ndescription: &shared Review\ntools: *shared\n---\nAct.\n"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			name, err := claudeAgentName(test.source)
			if (err != nil) != test.wantErr || name != test.want {
				t.Fatalf("name=%q error=%v", name, err)
			}
		})
	}
}

func TestClaudeAgentRejectsUnsupportedPluginMetadata(t *testing.T) {
	for _, field := range []string{"permissionMode: default", "hooks: {}", "mcpServers: {}", "initialPrompt: /review"} {
		source := []byte("---\nname: root-orchestrator\ndescription: Review\n" + field + "\n---\nAct.\n")
		if _, err := claudeAgentName(source); !errors.Is(err, errUnsupportedClaudeAgentMetadata) {
			t.Fatalf("field=%q error=%v", field, err)
		}
	}
}

func TestCodexAgentNameAcceptsNativeConfigurationLayer(t *testing.T) {
	valid := []byte("name = \"Repository Reviewer\"\ndescription = \"Review\"\nmodel = \"gpt-example\"\nmodel_reasoning_effort = \"high\"\nmodel_reasoning_summary = \"concise\"\nmodel_verbosity = \"low\"\npersonality = \"pragmatic\"\nservice_tier = \"priority\"\nnickname_candidates = [\"reviewer\"]\ndeveloper_instructions = \"Act.\"\n[features]\nshell_tool = false\napps = false\npersonality = false\nplugins = false\nmemories = false\nrequest_permissions_tool = false\n")
	if name, err := codexAgentName(valid); err != nil || name != "Repository Reviewer" {
		t.Fatalf("name=%q error=%v", name, err)
	}

	for _, test := range []struct {
		name   string
		config string
	}{
		{name: "non-string name", config: "name = 42\ndescription = \"Review\"\ndeveloper_instructions = \"Act.\"\n"},
		{name: "blank description", config: "name = \"repository_reviewer\"\ndescription = \"  \"\ndeveloper_instructions = \"Act.\"\n"},
		{name: "missing instructions", config: "name = \"repository_reviewer\"\ndescription = \"Review\"\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := codexAgentName([]byte(test.config)); err == nil || errors.Is(err, errUnsupportedCodexAgentMetadata) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCodexAgentRejectsMetadataThatRolesDoNotApply(t *testing.T) {
	for _, metadata := range []string{
		"[mcp_servers.repository]\ncommand = \"repository-server\"\n",
		"[[skills.config]]\npath = \"/skills/reviewer/SKILL.md\"\nenabled = true\n",
		"sandbox_mode = \"read-only\"\n",
		"approval_policy = \"never\"\n",
		"web_search = \"disabled\"\n",
		"notify = [\"notify-review\"]\n",
		"model_instructions_file = \"instructions.md\"\n",
		"[hooks]\nafter_agent = [\"audit-review\"]\n",
		"[tools]\nweb_search = false\n",
	} {
		source := []byte("name = \"repository_reviewer\"\ndescription = \"Review\"\ndeveloper_instructions = \"Act.\"\n" + metadata)
		if _, err := codexAgentName(source); !errors.Is(err, errUnsupportedCodexAgentMetadata) {
			t.Fatalf("metadata=%q error=%v", metadata, err)
		}
	}
}

func TestCodexAgentRejectsFeatureConfigurationThatRolesDoNotReduce(t *testing.T) {
	for _, metadata := range []string{
		"[features]\nshell_tool = true\n",
		"[features]\nunknown_feature = false\n",
		"[features]\nmemory_tool = false\n",
		"[features]\n",
	} {
		source := []byte("name = \"repository_reviewer\"\ndescription = \"Review\"\ndeveloper_instructions = \"Act.\"\n" + metadata)
		if _, err := codexAgentName(source); !errors.Is(err, errUnsupportedCodexAgentMetadata) {
			t.Fatalf("metadata=%q error=%v", metadata, err)
		}
	}
}

func TestManifestValidatesEveryNativeAgentConfiguration(t *testing.T) {
	for _, test := range []struct {
		name     string
		target   string
		source   []byte
		wantCode string
	}{
		{
			name: "Claude missing required description", target: "claude",
			source: []byte("---\nname: repository-reviewer\n---\nAct.\n"), wantCode: "invalid_claude_agent",
		},
		{
			name: "Claude ignored permission metadata", target: "claude",
			source: []byte("---\nname: repository-reviewer\ndescription: Review\npermissionMode: default\n---\nAct.\n"), wantCode: "unsupported_claude_agent_metadata",
		},
		{
			name: "Claude duplicate frontmatter field", target: "claude",
			source: []byte("---\nname: repository-reviewer\nname: another-reviewer\ndescription: Review\n---\nAct.\n"), wantCode: "invalid_claude_agent",
		},
		{
			name: "Codex non-string description", target: "codex",
			source: []byte("name = \"repository-reviewer\"\ndescription = 42\ndeveloper_instructions = \"Act.\"\n"), wantCode: "invalid_codex_agent",
		},
		{
			name: "Codex embedded MCP server", target: "codex",
			source: []byte("name = \"repository-reviewer\"\ndescription = \"Review\"\ndeveloper_instructions = \"Act.\"\n[mcp_servers.repository]\ncommand = \"repository-server\"\n"), wantCode: "unsupported_codex_agent_metadata",
		},
		{
			name: "Codex embedded skill", target: "codex",
			source: []byte("name = \"repository-reviewer\"\ndescription = \"Review\"\ndeveloper_instructions = \"Act.\"\n[[skills.config]]\npath = \"/skills/reviewer/SKILL.md\"\n"), wantCode: "unsupported_codex_agent_metadata",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := firstPartyFiles(t)
			files[firstPartyAgentPath(t, files, test.target)] = test.source
			assertManifestProblem(t, files, test.wantCode)
		})
	}
}

func TestClaudeAgentActivationIsDeclaredSelectedAndTargetSpecific(t *testing.T) {
	files := activatedFirstPartyFiles(t)
	addClaudeHostAgentVariants(t, files)
	validationInspections := 0
	runner := &fixtureRunner{files: files, inspectClaudeValidation: func(directory string) error {
		if filepath.Base(directory) != "ai4j-reviewer" {
			return nil
		}
		validationInspections++
		return inspectDarwinClaudeReviewer(directory)
	}}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	claudeOutput := filepath.Join(t.TempDir(), "claude")
	claudeRequest, err := cli.Parse([]string{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64", "--output", claudeOutput, "--bundle", "review"})
	if err != nil {
		t.Fatal(err)
	}
	claudeReport := service.Build(context.Background(), claudeRequest.(cli.BuildRequest))
	if claudeReport.Failure != FailureNone || !selectionContains(claudeReport.Selection, "review-orchestrator") {
		t.Fatalf("Claude build = failure:%s problems:%v selection:%v", claudeReport.Failure, claudeReport.Problems, claudeReport.Selection)
	}
	activationDisclosed := slices.ContainsFunc(claudeReport.Content, func(item cli.ContentItem) bool {
		return item.Identifier() == "review-orchestrator" && item.ComponentType() == cli.ComponentExtension
	})
	if !activationDisclosed {
		t.Fatalf("Claude activation disclosure = %#v", claudeReport.Content)
	}
	if content, err := os.ReadFile(filepath.Join(claudeOutput, "plugins", "ai4j-reviewer", "settings.json")); err != nil || !bytes.Equal(content, files[claudeActivationPath]) {
		t.Fatalf("Claude activation = %q, %v", content, err)
	}
	if _, err := os.Lstat(filepath.Join(claudeOutput, "plugins", "ai4j-reviewer", "agents", "repository-reviewer-windows.md")); !os.IsNotExist(err) {
		t.Fatalf("Claude output contains the foreign Windows agent variant: %v", err)
	}
	if validationInspections == 0 {
		t.Fatal("Claude did not validate the filtered reviewer package")
	}

	codexOutput := filepath.Join(t.TempDir(), "codex")
	codexRequest, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", codexOutput, "--bundle", "review"})
	if err != nil {
		t.Fatal(err)
	}
	codexReport := service.Build(context.Background(), codexRequest.(cli.BuildRequest))
	if codexReport.Failure != FailureNone || selectionContains(codexReport.Selection, "review-orchestrator") {
		t.Fatalf("Codex build = failure:%s problems:%v selection:%v", codexReport.Failure, codexReport.Problems, codexReport.Selection)
	}
	if _, err := os.Lstat(filepath.Join(codexOutput, "plugins", "ai4j-reviewer", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("Codex output contains Claude settings: %v", err)
	}
}

func TestClaudeAgentActivationIsRetainedExactly(t *testing.T) {
	files := activatedFirstPartyFiles(t)
	addClaudeHostAgentVariants(t, files)
	validationInspections := 0
	runner := &fixtureRunner{files: files, inspectClaudeValidation: func(directory string) error {
		if filepath.Base(directory) != "ai4j-reviewer" {
			return nil
		}
		validationInspections++
		return inspectDarwinClaudeReviewer(directory)
	}}
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := cli.Parse([]string{"ai4j", "validate", "--target", "claude"})
	if err != nil {
		t.Fatal(err)
	}

	report := service.SelectLifecycle(context.Background(), parsed.(cli.ValidateRequest).Source(), "review")
	if report.Failure != FailureNone || len(report.Packages) != 2 || !report.AgentActivation || !slices.Contains(report.ResolvedAssets, "review-orchestrator") {
		t.Fatalf("lifecycle selection = failure:%s problems:%v assets:%v", report.Failure, report.Problems, report.ResolvedAssets)
	}
	packageIndex := slices.IndexFunc(report.Packages, func(selected LifecyclePackage) bool { return selected.ID == "ai4j-reviewer" })
	if packageIndex < 0 {
		t.Fatal("lifecycle selection is missing ai4j-reviewer")
	}
	artifact := report.Packages[packageIndex].NativeArtifact
	reader, err := zip.NewReader(bytes.NewReader(artifact), int64(len(artifact)))
	if err != nil {
		t.Fatal(err)
	}
	foundSettings := false
	foundManifest := false
	for _, file := range reader.File {
		if file.Name == "agents/repository-reviewer-windows.md" {
			t.Fatal("Claude retained package contains the foreign Windows agent variant")
		}
		if file.Name == ".claude-plugin/plugin.json" {
			foundManifest = true
		}
		if file.Name != "settings.json" {
			continue
		}
		opened, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		content, readErr := io.ReadAll(opened)
		closeErr := opened.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(content, files[claudeActivationPath]) {
			t.Fatalf("retained activation = %q, read=%v close=%v", content, readErr, closeErr)
		}
		foundSettings = true
	}
	if !foundSettings || !foundManifest || validationInspections == 0 {
		t.Fatalf("retained settings=%t manifest=%t validation inspections=%d", foundSettings, foundManifest, validationInspections)
	}
}

func TestManifestRejectsInvalidClaudeAgentActivations(t *testing.T) {
	tests := []struct {
		name     string
		settings []byte
		mutate   func(*toolkitManifest, map[string][]byte)
	}{
		{name: "unknown setting", settings: []byte("{\"agent\":\"repository-reviewer\",\"unknown\":true}\n")},
		{name: "duplicate agent setting", settings: []byte("{\"agent\":\"repository-reviewer\",\"agent\":\"other-agent\"}\n")},
		{name: "executable status line", settings: []byte("{\"agent\":\"repository-reviewer\",\"subagentStatusLine\":{\"type\":\"command\",\"command\":\"hidden\"}}\n")},
		{name: "missing agent", settings: []byte("{}\n")},
		{name: "frontmatter mismatch", settings: []byte("{\"agent\":\"other-agent\"}\n")},
		{name: "missing dependency", settings: []byte("{\"agent\":\"repository-reviewer\"}\n"), mutate: func(manifest *toolkitManifest, _ map[string][]byte) {
			assetByID(manifest, "review-orchestrator").Dependencies = nil
		}},
		{name: "multiple dependencies", settings: []byte("{\"agent\":\"repository-reviewer\"}\n"), mutate: func(manifest *toolkitManifest, _ map[string][]byte) {
			assetByID(manifest, "review-orchestrator").Dependencies = []string{"repository-reviewer", "repository-review"}
		}},
		{name: "non-agent dependency", settings: []byte("{\"agent\":\"repository-reviewer\"}\n"), mutate: func(manifest *toolkitManifest, _ map[string][]byte) {
			assetByID(manifest, "review-orchestrator").Dependencies = []string{"repository-review"}
		}},
		{name: "nested settings", settings: []byte("{\"agent\":\"repository-reviewer\"}\n"), mutate: func(manifest *toolkitManifest, files map[string][]byte) {
			const nested = "plugins/ai4j-reviewer-claude/config/settings.json"
			files[nested] = files[claudeActivationPath]
			delete(files, claudeActivationPath)
			assetByID(manifest, "review-orchestrator").Variants[0].Path = nested
		}},
		{name: "Codex target", settings: []byte("{\"agent\":\"repository-reviewer\"}\n"), mutate: func(manifest *toolkitManifest, files map[string][]byte) {
			const codexSettings = "plugins/ai4j-reviewer-codex/settings.json"
			files[codexSettings] = files[claudeActivationPath]
			activation := assetByID(manifest, "review-orchestrator")
			activation.Variants = append(activation.Variants, assetVariant{ID: "codex", Path: codexSettings, Targets: []string{"codex"}, Hosts: []string{"darwin-arm64", "windows-amd64"}})
			unit := packageByID(manifest, "codex", "ai4j-reviewer")
			unit.Assets = append(unit.Assets, "review-orchestrator")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := activatedFirstPartyFiles(t)
			files[claudeActivationPath] = test.settings
			var manifest toolkitManifest
			if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
				t.Fatal(err)
			}
			if test.mutate != nil {
				test.mutate(&manifest, files)
			}
			files[toolkitManifestPath], _ = json.Marshal(manifest)
			assertManifestProblem(t, files, "invalid_agent_activation")
		})
	}
}

func TestSelectionRejectsMultipleClaudeAgentActivations(t *testing.T) {
	files := activatedFirstPartyFiles(t)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	second := *assetByID(&manifest, "review-orchestrator")
	second.ID = "alternate-orchestrator"
	second.Dependencies = slices.Clone(second.Dependencies)
	second.Variants = append([]assetVariant(nil), second.Variants...)
	manifest.Assets = append(manifest.Assets, second)
	unit := packageByID(&manifest, "claude", "ai4j-reviewer")
	unit.Assets = append(unit.Assets, second.ID)
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	output := filepath.Join(t.TempDir(), "build")
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64", "--output", output, "--bundle", "review"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "conflicting_agent_activation" {
		t.Fatalf("failure=%s problems=%v", report.Failure, report.Problems)
	}
}

func activatedFirstPartyFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files := firstPartyFiles(t)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Assets = append(manifest.Assets, asset{
		ID: "review-orchestrator", Type: "agent_activation", Ownership: "package", Dependencies: []string{"repository-reviewer"},
		Variants: []assetVariant{{ID: "claude", Path: claudeActivationPath, Targets: []string{"claude"}, Hosts: []string{"darwin-arm64", "windows-amd64"}}},
	})
	unit := packageByID(&manifest, "claude", "ai4j-reviewer")
	unit.Assets = append(unit.Assets, "review-orchestrator")
	files[claudeActivationPath] = []byte("{\n  \"agent\": \"repository-reviewer\"\n}\n")
	files[toolkitManifestPath], _ = json.Marshal(manifest)
	return files
}

func addClaudeHostAgentVariants(t *testing.T, files map[string][]byte) {
	t.Helper()
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	agent := assetByID(&manifest, "repository-reviewer")
	for index := range agent.Variants {
		if agent.Variants[index].ID == "claude" {
			agent.Variants[index].ID = "claude-darwin"
			agent.Variants[index].Hosts = []string{"darwin-arm64"}
		}
	}
	agent.Variants = append(agent.Variants, assetVariant{
		ID: "claude-windows", Path: claudeWindowsAgentPath, Targets: []string{"claude"}, Hosts: []string{"windows-amd64"},
	})
	files[claudeWindowsAgentPath] = append([]byte(nil), files[claudeDarwinAgentPath]...)
	files[toolkitManifestPath], _ = json.Marshal(manifest)
}

func inspectDarwinClaudeReviewer(directory string) error {
	for _, relative := range []string{".claude-plugin/plugin.json", "agents/repository-reviewer.md"} {
		if _, err := os.Stat(filepath.Join(directory, filepath.FromSlash(relative))); err != nil {
			return err
		}
	}
	_, err := os.Lstat(filepath.Join(directory, "agents", "repository-reviewer-windows.md"))
	if err == nil {
		return errors.New("foreign Windows agent variant entered Claude validation")
	}
	if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func firstPartyAgentPath(t *testing.T, files map[string][]byte, target string) string {
	t.Helper()
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	paths := assetPathsForTarget(*assetByID(&manifest, "repository-reviewer"), target)
	if len(paths) != 1 {
		t.Fatalf("%s repository-reviewer paths = %v", target, paths)
	}
	return paths[0]
}

func selectionContains(selection []cli.BuildSelection, identifier string) bool {
	return slices.ContainsFunc(selection, func(item cli.BuildSelection) bool { return item.Asset() == identifier })
}
