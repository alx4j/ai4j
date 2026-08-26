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
				request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all"})
				report := service.Build(context.Background(), request.(cli.BuildRequest))
				failure = report.Failure
				if len(report.Problems) != 0 {
					code = report.Problems[0].Code()
				}
			} else {
				request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "validate", "--target", "claude"})
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
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "validate", "--target", "claude"})
	report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())
	if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != "unsupported_schema" {
		t.Fatalf("failure=%s problems=%v", report.Failure, report.Problems)
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
			packageByID(manifest, "claude", "ai4j-review").Path = "plugins/ai4j-review/skills"
		}, wantCode: "native_asset_outside_package"},
		{name: "unassigned package asset", mutate: func(manifest *toolkitManifest) {
			unit := packageByID(manifest, "claude", "ai4j-review")
			unit.Assets = slices.DeleteFunc(unit.Assets, func(id string) bool { return id == "repository-reviewer" })
		}, wantCode: "unassigned_native_asset"},
		{name: "configuration asset inside package", mutate: func(manifest *toolkitManifest) {
			assetByID(manifest, "ai4j-rules").Path = "plugins/ai4j-review/agents/repository-reviewer.md"
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
			request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "validate", "--target", "claude"})

			report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())

			if report.Failure != FailureValidation || len(report.Problems) != 1 || report.Problems[0].Code() != test.wantCode || runner.claudeValidations != 0 {
				t.Fatalf("failure=%s problems=%v nativeValidations=%d", report.Failure, report.Problems, runner.claudeValidations)
			}
		})
	}
}

func TestManifestAcceptsDeclaredPackageContentAndNativeMetadata(t *testing.T) {
	files := firstPartyFiles(t)
	files["plugins/ai4j-review/.codex-plugin/plugin.json"] = []byte(`{"$schema":"https://example.invalid/plugin.schema.json","name":"ai4j-review","displayName":"AI4J Review","version":"1.0.0","description":"Review plugin","author":{"name":"AI4J"},"keywords":["review"]}`)
	files["plugins/ai4j-review/skills/repository-review/references/nested/example.md"] = []byte("# Nested reference\n")
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fixtureRunner{files: files}
	service, _ := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "validate", "--target", "claude"})

	report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())

	if report.Failure != FailureNone || len(report.Problems) != 0 || runner.claudeValidations == 0 {
		t.Fatalf("failure=%s problems=%v nativeValidations=%d", report.Failure, report.Problems, runner.claudeValidations)
	}
}

func TestManifestRejectsActiveNativeMetadata(t *testing.T) {
	for _, test := range []struct {
		name     string
		path     string
		contents []byte
	}{
		{
			name:     "Claude inline hook",
			path:     "plugins/ai4j-review/.claude-plugin/plugin.json",
			contents: []byte(`{"name":"ai4j-review","hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"powershell -File hidden.ps1"}]}]}}`),
		},
		{
			name:     "Codex active component pointer",
			path:     "plugins/ai4j-review/.codex-plugin/plugin.json",
			contents: []byte(`{"name":"ai4j-review","skills":"./skills/"}`),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := firstPartyFiles(t)
			files[test.path] = test.contents
			assertManifestProblem(t, files, "undeclared_package_content")
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
			request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "validate", "--target", "claude"})

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
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "validate", "--target", "claude"})

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
