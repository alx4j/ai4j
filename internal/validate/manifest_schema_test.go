package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestPublishedToolkitSchemaAcceptsFirstPartyAndGeneratedManifests(t *testing.T) {
	schema := compileToolkitSchema(t)
	firstParty, err := os.ReadFile(filepath.Join("..", "..", "toolkit.json"))
	if err != nil {
		t.Fatal(err)
	}
	validateToolkitDocument(t, schema, firstParty)

	root := t.TempDir()
	request, err := cli.Parse([]string{"ai4j", "init", "--target", "claude", "--target", "codex", "--output", filepath.Join(t.TempDir(), "toolkit"), "--examples"})
	if err != nil {
		t.Fatal(err)
	}
	if err := renderScaffold(root, request.(cli.InitRequest)); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(root, "toolkit.json"))
	if err != nil {
		t.Fatal(err)
	}
	validateToolkitDocument(t, schema, generated)
}

func TestPublishedToolkitSchemaAcceptsAgentActivation(t *testing.T) {
	schema := compileToolkitSchema(t)
	files := activatedFirstPartyFiles(t)
	validateToolkitDocument(t, schema, files[toolkitManifestPath])

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
	if report.Failure != FailureNone || len(report.Problems) != 0 || runner.claudeValidations == 0 {
		t.Fatalf("failure=%s problems=%v nativeValidations=%d", report.Failure, report.Problems, runner.claudeValidations)
	}
}

func TestPublishedToolkitSchemaRejectsStructurallyInvalidAgentActivations(t *testing.T) {
	schema := compileToolkitSchema(t)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "direct path", mutate: func(asset map[string]any) {
			delete(asset, "variants")
			asset["path"] = "plugins/ai4j-review/settings.json"
		}},
		{name: "configuration ownership", mutate: func(asset map[string]any) {
			asset["ownership"] = "configuration"
		}},
		{name: "Codex target", mutate: func(asset map[string]any) {
			asset["variants"].([]any)[0].(map[string]any)["targets"] = []any{"codex"}
		}},
		{name: "missing dependency", mutate: func(asset map[string]any) {
			delete(asset, "dependencies")
		}},
		{name: "multiple dependencies", mutate: func(asset map[string]any) {
			asset["dependencies"] = []any{"repository-reviewer", "other-agent"}
		}},
		{name: "executable variant", mutate: func(asset map[string]any) {
			asset["variants"].([]any)[0].(map[string]any)["executable"] = map[string]any{"command": "claude"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := os.ReadFile(filepath.Join("..", "..", "toolkit.json"))
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatal(err)
			}
			activation := map[string]any{
				"id": "root-orchestrator", "type": "agent_activation", "ownership": "package", "dependencies": []any{"repository-reviewer"},
				"variants": []any{map[string]any{"id": "claude", "path": "plugins/ai4j-review/settings.json", "targets": []any{"claude"}, "hosts": []any{"darwin-arm64", "windows-amd64"}}},
			}
			test.mutate(activation)
			document["assets"] = append(document["assets"].([]any), activation)

			if err := schema.Validate(document); err == nil {
				t.Fatal("toolkit schema accepted an invalid agent activation")
			}
		})
	}
}

func TestPublishedToolkitSchemaAndRuntimeAcceptAssetOverlay(t *testing.T) {
	schema := compileToolkitSchema(t)
	files := firstPartyFiles(t)
	var manifest toolkitManifest
	if err := json.Unmarshal(files[toolkitManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	assetByID(&manifest, "repository-review").Overlays = map[string]targetOverlay{
		"codex": {
			Model:       "gpt-example",
			Tools:       []string{"shell"},
			Environment: []string{"AI4J_CONTEXT"},
			HookEvents:  []string{"SessionStart"},
		},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files[toolkitManifestPath] = encoded
	validateToolkitDocument(t, schema, encoded)

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
	if report.Failure != FailureNone || len(report.Problems) != 0 {
		t.Fatalf("failure=%s problems=%v", report.Failure, report.Problems)
	}
}

func TestPublishedToolkitSchemaRejectsUnknownAssetOverlayField(t *testing.T) {
	schema := compileToolkitSchema(t)
	encoded, err := os.ReadFile(filepath.Join("..", "..", "toolkit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	assets := document["assets"].([]any)
	asset := assets[0].(map[string]any)
	asset["overlays"] = map[string]any{"codex": map[string]any{"secret": "literal-value"}}
	if err := schema.Validate(document); err == nil {
		t.Fatal("toolkit schema accepted an inline target-overlay secret field")
	}
}

func compileToolkitSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "toolkit", "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource("https://github.com/alx4j/ai4j/schemas/toolkit/v1.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("https://github.com/alx4j/ai4j/schemas/toolkit/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateToolkitDocument(t *testing.T, schema *jsonschema.Schema, encoded []byte) {
	t.Helper()
	var document any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("toolkit schema validation failed: %v", err)
	}
}
