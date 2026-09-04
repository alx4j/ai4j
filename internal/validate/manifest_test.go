package validate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
)

func TestFirstPartyClaudeMarketplaceMatchesDeclaredPackages(t *testing.T) {
	files := firstPartyFiles(t)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	type entry struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	var marketplace struct {
		Plugins []entry `json:"plugins"`
	}
	if err := json.Unmarshal(files[".claude-plugin/marketplace.json"], &marketplace); err != nil {
		t.Fatal(err)
	}
	declared := manifest.Targets[string(cli.BuildTargetClaude)].Packages
	want := make([]entry, 0, len(declared))
	for _, pkg := range declared {
		want = append(want, entry{Name: pkg.ID, Source: "./" + pkg.Path})
	}
	if !slices.Equal(marketplace.Plugins, want) {
		t.Fatalf("marketplace packages = %#v, want %#v", marketplace.Plugins, want)
	}
}

func TestManifestRejectsDependencyAndVariantFailures(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*toolkitManifest)
		build    bool
		wantCode string
	}{
		{name: "missing dependency", mutate: func(manifest *toolkitManifest) {
			manifest.Assets[0].Dependencies = append(manifest.Assets[0].Dependencies, "missing-asset")
		}, wantCode: "missing_dependency"},
		{name: "dependency cycle", mutate: func(manifest *toolkitManifest) { manifest.Assets[3].Dependencies = []string{"repository-review"} }, wantCode: "dependency_cycle"},
		{name: "ambiguous variant", mutate: func(manifest *toolkitManifest) {
			manifest.Assets[2].Path = ""
			manifest.Assets[2].Variants = []assetVariant{{ID: "first", Path: "toolkit/rules/ai4j.md", Targets: []string{"codex"}, Hosts: []string{"darwin-arm64"}}, {ID: "second", Path: "toolkit/rules/ai4j.md", Targets: []string{"codex"}, Hosts: []string{"darwin-arm64"}}}
		}, build: true, wantCode: "ambiguous_variant"},
		{name: "unsupported variant", mutate: func(manifest *toolkitManifest) {
			manifest.Assets[2].Path = ""
			manifest.Assets[2].Variants = []assetVariant{{ID: "windows", Path: "toolkit/rules/ai4j.md", Targets: []string{"codex"}, Hosts: []string{"windows-amd64"}}}
		}, build: true, wantCode: "unsupported_variant"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := firstPartyFiles(t)
			var manifest toolkitManifest
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
				request, _ := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all"})
				report := service.Build(context.Background(), request.(cli.BuildRequest))
				failure = report.Failure
				if len(report.Problems) != 0 {
					code = report.Problems[0].Code()
				}
			} else {
				request, _ := cli.Parse([]string{"ai4j", "validate", "--target", "claude"})
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

func TestManifestRejectsUnknownFutureSchema(t *testing.T) {
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
	request, _ := cli.Parse([]string{"ai4j", "validate", "--target", "claude"})
	report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "unsupported_schema" {
		t.Fatalf("failure=%s problems=%v", report.Failure, report.Problems)
	}
}

func TestManifestValidatesDirectAndVariantMCPDeclarations(t *testing.T) {
	tests := []struct {
		name     string
		variant  bool
		wantArgs []string
	}{
		{name: "direct", wantArgs: []string{"mcp", "serve"}},
		{name: "variant", variant: true, wantArgs: []string{"mcp", "serve", "darwin"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := firstPartyFiles(t)
			if test.variant {
				var manifest toolkitManifest
				if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
					t.Fatal(err)
				}
				declaration := assetByID(&manifest, "claude-tools")
				declaration.Path = ""
				declaration.Executable = nil
				declaration.Variants = []assetVariant{
					{
						ID: "darwin", Path: "plugins/ai4j-tools/mcp/darwin.json",
						Targets: []string{"claude", "codex"}, Hosts: []string{"darwin-arm64"},
						Executable: &executable{Command: "claude", Args: test.wantArgs, Dependency: "required"},
					},
					{
						ID: "windows", Path: "plugins/ai4j-tools/mcp/windows.json",
						Targets: []string{"claude", "codex"}, Hosts: []string{"windows-amd64"},
						Executable: &executable{Command: "claude", Args: []string{"mcp", "serve", "windows"}, Dependency: "required"},
					},
				}
				delete(files, "plugins/ai4j-tools/.mcp.json")
				files["plugins/ai4j-tools/mcp/darwin.json"] = []byte(`{"mcpServers":{"claude-tools":{"type":"stdio","command":"claude","args":["mcp","serve","darwin"]}}}`)
				files["plugins/ai4j-tools/mcp/windows.json"] = []byte(`{"mcpServers":{"claude-tools":{"type":"stdio","command":"claude","args":["mcp","serve","windows"]}}}`)
				encoded, err := json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				files[toolkitManifestPath] = encoded
			}

			home := t.TempDir()
			if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			service, err := NewService(Config{
				GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild,
				Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatal(err)
			}
			validationRequest, err := cli.Parse([]string{"ai4j", "validate", "--target", "claude"})
			if err != nil {
				t.Fatal(err)
			}
			validation := service.Validate(context.Background(), validationRequest.(cli.ValidateRequest).Source())
			if validation.Failure != FailureNone || len(validation.Problems) != 0 {
				t.Fatalf("validation failure=%s problems=%v", validation.Failure, validation.Problems)
			}

			output := filepath.Join(t.TempDir(), "build")
			buildRequest, err := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--asset", "claude-tools"})
			if err != nil {
				t.Fatal(err)
			}
			build := service.Build(context.Background(), buildRequest.(cli.BuildRequest))
			if build.Failure != FailureNone || len(build.Problems) != 0 {
				t.Fatalf("build failure=%s problems=%v", build.Failure, build.Problems)
			}
			var servers map[string]codexMCPServer
			if err := json.Unmarshal(readBuildTree(t, output)["plugin/.mcp.json"], &servers); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(servers["claude-tools"].Args, test.wantArgs) {
				t.Fatalf("rendered MCP args=%v want=%v", servers["claude-tools"].Args, test.wantArgs)
			}
		})
	}
}

func TestManifestRejectsInvalidNonSelectedMCPVariant(t *testing.T) {
	files := firstPartyFiles(t)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	declaration := assetByID(&manifest, "claude-tools")
	declaration.Path = ""
	declaration.Executable = nil
	declaration.Variants = []assetVariant{
		{
			ID: "darwin", Path: "plugins/ai4j-tools/mcp/darwin.json",
			Targets: []string{"claude", "codex"}, Hosts: []string{"darwin-arm64"},
			Executable: &executable{Command: "claude", Args: []string{"mcp", "serve", "darwin"}, Dependency: "required"},
		},
		{
			ID: "windows", Path: "plugins/ai4j-tools/mcp/windows.json",
			Targets: []string{"claude", "codex"}, Hosts: []string{"windows-amd64"},
			Executable: &executable{Command: "claude", Args: []string{"mcp", "serve", "windows"}, Dependency: "required"},
		},
	}
	delete(files, "plugins/ai4j-tools/.mcp.json")
	files["plugins/ai4j-tools/mcp/darwin.json"] = []byte(`{"mcpServers":{"claude-tools":{"type":"stdio","command":"claude","args":["mcp","serve","darwin"]}}}`)
	files["plugins/ai4j-tools/mcp/windows.json"] = []byte(`{"mcpServers":{"claude-tools":{"type":"stdio","command":"claude","args":["mcp","serve","wrong"]}}}`)
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fixtureRunner{files: files}
	service, err := NewService(Config{
		GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild,
		Runner: runner, TempRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{"ai4j", "validate", "--target", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "invalid_mcp" || runner.claudeValidations != 0 {
		t.Fatalf("failure=%s problems=%v nativeValidations=%d", report.Failure, report.Problems, runner.claudeValidations)
	}
}

func TestExecutableValidationRequiresHostResolvedMCPCommandsOnly(t *testing.T) {
	invalid := []struct {
		name    string
		command string
	}{
		{name: "empty", command: ""},
		{name: "current directory", command: "."},
		{name: "parent directory", command: ".."},
		{name: "relative path", command: "../claude"},
		{name: "absolute path", command: "/usr/local/bin/claude"},
		{name: "Windows path", command: `tools\claude.exe`},
		{name: "drive separator", command: "C:claude.exe"},
		{name: "option-like name", command: "-claude"},
		{name: "space", command: "claude code"},
		{name: "tab", command: "claude\tcode"},
		{name: "control", command: "claude\x00"},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			declaration := &executable{Command: test.command, Dependency: "required"}
			if code, _ := packageProblem(validateExecutableDeclaration("mcp", declaration)); code != "invalid_executable" {
				t.Fatalf("MCP command %q returned %q", test.command, code)
			}
		})
	}
	for _, command := range []string{"claude", "c++", "node.exe"} {
		t.Run("valid "+command, func(t *testing.T) {
			declaration := &executable{Command: command, Dependency: "required"}
			if err := validateExecutableDeclaration("mcp", declaration); err != nil {
				t.Fatalf("MCP command %q was rejected: %v", command, err)
			}
		})
	}
	declaration := &executable{Command: "plugins/tools/run.sh", Dependency: "required"}
	if err := validateExecutableDeclaration("script", declaration); err != nil {
		t.Fatalf("toolkit-owned script path was rejected: %v", err)
	}
}

func TestManifestRejectsInvalidPackageAndBundleOwnership(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*toolkitManifest)
		wantCode string
	}{
		{name: "bundle directly selects package asset", mutate: func(manifest *toolkitManifest) {
			bundleByID(manifest, "default").Assets = []string{"repository-review"}
		}, wantCode: "invalid_bundle_asset"},
		{name: "bundle references missing package", mutate: func(manifest *toolkitManifest) {
			bundleByID(manifest, "default").Packages = []string{"missing-package"}
		}, wantCode: "missing_bundle_package"},
		{name: "duplicate package member", mutate: func(manifest *toolkitManifest) {
			unit := packageByID(manifest, "claude", "ai4j-review")
			unit.Assets = append(unit.Assets, "repository-review")
		}, wantCode: "duplicate_native_asset"},
		{name: "overlapping package roots", mutate: func(manifest *toolkitManifest) {
			packageByID(manifest, "claude", "ai4j-tools").Path = "plugins/ai4j-review/skills"
		}, wantCode: "overlapping_native_package"},
		{name: "package asset outside root", mutate: func(manifest *toolkitManifest) {
			packageByID(manifest, "claude", "ai4j-review").Path = "plugins/ai4j-review/skills/repository-review/references"
		}, wantCode: "native_asset_outside_package"},
		{name: "unassigned package asset", mutate: func(manifest *toolkitManifest) {
			unit := packageByID(manifest, "claude", "ai4j-reviewer")
			unit.Assets = slices.DeleteFunc(unit.Assets, func(id string) bool { return id == "repository-reviewer" })
		}, wantCode: "unassigned_native_asset"},
		{name: "configuration asset inside package", mutate: func(manifest *toolkitManifest) {
			assetByID(manifest, "ai4j-rules").Path = "plugins/ai4j-reviewer-claude/agents/repository-reviewer.md"
		}, wantCode: "configuration_asset_inside_package"},
		{name: "dependency crosses ownership", mutate: func(manifest *toolkitManifest) {
			assetByID(manifest, "repository-review").Dependencies = append(assetByID(manifest, "repository-review").Dependencies, "ai4j-rules")
		}, wantCode: "invalid_dependency_ownership"},
		{name: "dependency crosses packages", mutate: func(manifest *toolkitManifest) {
			assetByID(manifest, "repository-review").Dependencies = append(assetByID(manifest, "repository-review").Dependencies, "claude-tools")
		}, wantCode: "cross_package_dependency"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := firstPartyFiles(t)
			var manifest toolkitManifest
			if err := json.Unmarshal(files["toolkit.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			test.mutate(&manifest)
			files["toolkit.json"], _ = json.Marshal(manifest)
			home := t.TempDir()
			if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			runner := &fixtureRunner{files: files}
			service, _ := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
			request, _ := cli.Parse([]string{"ai4j", "validate", "--target", "claude"})

			report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())

			if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != test.wantCode || runner.claudeValidations != 0 {
				t.Fatalf("failure=%s problems=%v nativeValidations=%d", report.Failure, report.Problems, runner.claudeValidations)
			}
		})
	}
}

func TestManifestAcceptsDeclaredPackageContentAndNativeMetadata(t *testing.T) {
	files := firstPartyFiles(t)
	files["plugins/ai4j-review/.claude-plugin/plugin.json"] = []byte(`{"name":"ai4j-review","description":"Review plugin","skills":"./skills/"}`)
	files["plugins/ai4j-review/.codex-plugin/plugin.json"] = []byte(`{"$schema":"https://example.invalid/plugin.schema.json","name":"ai4j-review","displayName":"AI4J Review","version":"1.0.0","description":"Review plugin","author":{"name":"AI4J"},"keywords":["review"],"skills":"./skills/"}`)
	files["plugins/ai4j-review/skills/repository-review/references/nested/example.md"] = []byte("# Nested reference\n")
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fixtureRunner{files: files}
	service, _ := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	request, _ := cli.Parse([]string{"ai4j", "validate", "--target", "claude"})

	report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())

	if report.Failure != FailureNone || len(report.Problems) != 0 || runner.claudeValidations == 0 {
		t.Fatalf("failure=%s problems=%v nativeValidations=%d", report.Failure, report.Problems, runner.claudeValidations)
	}
}

func TestManifestRejectsUndeclaredActiveNativeMetadata(t *testing.T) {
	files := firstPartyFiles(t)
	files["plugins/ai4j-review/.claude-plugin/plugin.json"] = []byte(`{"name":"ai4j-review","hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"powershell -File hidden.ps1"}]}]}}`)
	assertManifestProblem(t, files, "unsupported_native_plugin_metadata")
}

func TestManifestBindsActiveNativeMetadataToDeclaredAssetPaths(t *testing.T) {
	files := firstPartyFiles(t)
	files["plugins/ai4j-review/.claude-plugin/plugin.json"] = []byte(`{"name":"ai4j-review","hooks":"./hidden.json"}`)
	files["plugins/ai4j-review/hooks/declared.json"] = []byte(`{}`)
	files["plugins/ai4j-review/hidden.json"] = []byte(`{}`)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Assets = append(manifest.Assets,
		asset{ID: "declared-hook", Type: "hook", Path: "plugins/ai4j-review/hooks/declared.json", Ownership: "package"},
		asset{ID: "hidden-reference", Type: "reference", Path: "plugins/ai4j-review/hidden.json", Ownership: "package"},
	)
	for _, targetName := range []string{"claude", "codex"} {
		unit := packageByID(&manifest, targetName, "ai4j-review")
		unit.Assets = append(unit.Assets, "declared-hook", "hidden-reference")
	}
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	assertManifestProblem(t, files, "undeclared_package_content")
}

func TestManifestBindsCodexCommandMetadataToDeclaredPromptPaths(t *testing.T) {
	files := firstPartyFiles(t)
	files["plugins/ai4j-review/.codex-plugin/plugin.json"] = []byte(`{"name":"ai4j-review","commands":"./hidden.md"}`)
	files["plugins/ai4j-review/commands/declared.md"] = []byte("Declared prompt.\n")
	files["plugins/ai4j-review/hidden.md"] = []byte("Hidden prompt.\n")
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Assets = append(manifest.Assets,
		asset{ID: "declared-prompt", Type: "prompt", Path: "plugins/ai4j-review/commands/declared.md", Ownership: "package"},
		asset{ID: "hidden-command-reference", Type: "reference", Path: "plugins/ai4j-review/hidden.md", Ownership: "package"},
	)
	for _, targetName := range []string{"claude", "codex"} {
		unit := packageByID(&manifest, targetName, "ai4j-review")
		unit.Assets = append(unit.Assets, "declared-prompt", "hidden-command-reference")
	}
	files[toolkitManifestPath], _ = json.Marshal(manifest)

	assertManifestProblem(t, files, "undeclared_package_content")
}

func TestManifestRejectsUndisclosedNativePluginCapabilities(t *testing.T) {
	for _, test := range []struct {
		name     string
		path     string
		contents []byte
	}{
		{name: "Claude workflow", path: "plugins/ai4j-review/.claude-plugin/plugin.json", contents: []byte(`{"name":"ai4j-review","workflows":"./workflows/"}`)},
		{name: "Claude inline LSP", path: "plugins/ai4j-review/.claude-plugin/plugin.json", contents: []byte(`{"name":"ai4j-review","lspServers":{"go":{"command":"gopls"}}}`)},
		{name: "Claude monitor", path: "plugins/ai4j-review/.claude-plugin/plugin.json", contents: []byte(`{"name":"ai4j-review","experimental":{"monitors":[{"name":"watch","command":"hidden","description":"watch"}]}}`)},
		{name: "Claude dependency", path: "plugins/ai4j-review/.claude-plugin/plugin.json", contents: []byte(`{"name":"ai4j-review","dependencies":["hidden-plugin"]}`)},
		{name: "Claude channel", path: "plugins/ai4j-review/.claude-plugin/plugin.json", contents: []byte(`{"name":"ai4j-review","channels":[{"server":"hidden"}]}`)},
		{name: "Codex app", path: "plugins/ai4j-review/.codex-plugin/plugin.json", contents: []byte(`{"name":"ai4j-review","apps":"./.app.json"}`)},
		{name: "Codex default prompt", path: "plugins/ai4j-review/.codex-plugin/plugin.json", contents: []byte(`{"name":"ai4j-review","interface":{"defaultPrompt":"Review this repository"}}`)},
		{name: "Codex MCP path array", path: "plugins/ai4j-review/.codex-plugin/plugin.json", contents: []byte(`{"name":"ai4j-review","mcpServers":["./first.json","./second.json"]}`)},
		{name: "unknown metadata", path: "plugins/ai4j-review/.codex-plugin/plugin.json", contents: []byte(`{"name":"ai4j-review","futureMetadata":{"enabled":true}}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := firstPartyFiles(t)
			files[test.path] = test.contents
			assertManifestProblem(t, files, "unsupported_native_plugin_metadata")
		})
	}
}

func TestManifestRejectsMissingMismatchedOrNonStringNativePluginName(t *testing.T) {
	for _, test := range []struct {
		name     string
		contents []byte
	}{
		{name: "missing", contents: []byte(`{"description":"Review plugin"}`)},
		{name: "mismatched", contents: []byte(`{"name":"another-plugin"}`)},
		{name: "non-string", contents: []byte(`{"name":42}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := firstPartyFiles(t)
			files["plugins/ai4j-review/.claude-plugin/plugin.json"] = test.contents
			assertManifestProblem(t, files, "invalid_native_plugin_metadata")
		})
	}
}

func TestManifestRejectsMisclassifiedNativeContent(t *testing.T) {
	for _, test := range []struct {
		name     string
		path     string
		contents []byte
	}{
		{name: "hook as reference", path: "plugins/ai4j-review/hooks/hooks.json", contents: []byte(`{}`)},
		{name: "MCP as reference", path: "plugins/ai4j-review/.mcp.json", contents: []byte(`{"mcpServers":{}}`)},
		{name: "Claude activation as reference", path: "plugins/ai4j-review/settings.json", contents: []byte(`{"agent":"repository-reviewer"}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := firstPartyFiles(t)
			files[test.path] = test.contents
			var manifest toolkitManifest
			if err := json.Unmarshal(files["toolkit.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			manifest.Assets = append(manifest.Assets, asset{ID: "hidden-native", Type: "reference", Path: test.path, Ownership: "package"})
			for _, target := range []string{"claude", "codex"} {
				unit := packageByID(&manifest, target, "ai4j-review")
				unit.Assets = append(unit.Assets, "hidden-native")
			}
			files["toolkit.json"], _ = json.Marshal(manifest)
			assertManifestProblem(t, files, "undeclared_package_content")
		})
	}
}

func TestManifestRejectsUndeclaredNestedSkillSupport(t *testing.T) {
	files := firstPartyFiles(t)
	var manifest toolkitManifest
	if err := json.Unmarshal(files["toolkit.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	assetByID(&manifest, "repository-review").Dependencies = []string{"check-diff"}
	files["toolkit.json"], _ = json.Marshal(manifest)

	assertManifestProblem(t, files, "undeclared_package_content")
}

func TestManifestRejectsUndeclaredPackageContent(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "hook", path: "plugins/ai4j-review/hooks/hooks.json"},
		{name: "native configuration", path: "plugins/ai4j-review/.mcp.json"},
		{name: "Claude main-agent setting", path: "plugins/ai4j-review/settings.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := firstPartyFiles(t)
			files[test.path] = []byte(`{}`)
			home := t.TempDir()
			if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			runner := &fixtureRunner{files: files}
			service, _ := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
			request, _ := cli.Parse([]string{"ai4j", "validate", "--target", "claude"})

			report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())

			if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "undeclared_package_content" || runner.claudeValidations != 0 {
				t.Fatalf("failure=%s problems=%v nativeValidations=%d", report.Failure, report.Problems, runner.claudeValidations)
			}
		})
	}
}

func assertManifestProblem(t *testing.T, files map[string][]byte, wantCode string) {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fixtureRunner{files: files}
	service, _ := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	request, _ := cli.Parse([]string{"ai4j", "validate", "--target", "claude"})

	report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())

	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != wantCode || runner.claudeValidations != 0 {
		t.Fatalf("failure=%s problems=%v nativeValidations=%d", report.Failure, report.Problems, runner.claudeValidations)
	}
}

func assetByID(manifest *toolkitManifest, id string) *asset {
	for index := range manifest.Assets {
		if manifest.Assets[index].ID == id {
			return &manifest.Assets[index]
		}
	}
	panic("test asset not found: " + id)
}

func bundleByID(manifest *toolkitManifest, id string) *bundle {
	for index := range manifest.Bundles {
		if manifest.Bundles[index].ID == id {
			return &manifest.Bundles[index]
		}
	}
	panic("test bundle not found: " + id)
}

func packageByID(manifest *toolkitManifest, targetID, packageID string) *nativePackage {
	targetConfig := manifest.Targets[targetID]
	for index := range targetConfig.Packages {
		if targetConfig.Packages[index].ID == packageID {
			manifest.Targets[targetID] = targetConfig
			return &targetConfig.Packages[index]
		}
	}
	panic("test package not found: " + packageID)
}
