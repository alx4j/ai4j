package validate

import (
	"bytes"
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
	request, err := cli.NewParser("darwin").Parse([]string{"ai4j", "init", "--target", "claude", "--target", "codex", "--output", filepath.Join(t.TempDir(), "toolkit"), "--examples"})
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

func TestPublishedToolkitSchemaRejectsInlineOverlaySecretField(t *testing.T) {
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
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "toolkit", "v2.json"))
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
	if err := compiler.AddResource("https://github.com/alx4j/ai4j/schemas/toolkit/v2.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("https://github.com/alx4j/ai4j/schemas/toolkit/v2.json")
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
