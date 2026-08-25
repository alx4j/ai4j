package architecture_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// opaqueBytesConsumers is intentionally empty for the MVP. Future machine-
// protocol parsers must be approved here by exact repository-relative file;
// renderer, diagnostic, CLI, result, and logging packages are never eligible.
var opaqueBytesConsumers = map[string]struct{}{}

func TestOpaqueProcessBytesHaveNoUnapprovedProductionConsumers(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	violations, err := findOpaqueBytesConsumers(repositoryRoot, true, opaqueBytesConsumers)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("OpaqueBytes is restricted to approved machine-protocol parsers: %s", strings.Join(violations, ", "))
	}
}

func TestOpaqueProcessBytesGuardDetectsRendererCanary(t *testing.T) {
	fixture := filepath.Join("testdata", "opaque_forbidden")
	violations, err := findOpaqueBytesConsumers(fixture, false, map[string]struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || !strings.Contains(violations[0], "renderer.go") {
		t.Fatalf("renderer canary violations = %v", violations)
	}
}

func findOpaqueBytesConsumers(root string, skipTestdata bool, allowlist map[string]struct{}) ([]string, error) {
	set := token.NewFileSet()
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" || skipTestdata && name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if _, approved := allowlist[relative]; approved {
			return nil
		}
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "OpaqueBytes" {
				return true
			}
			position := set.Position(selector.Sel.Pos())
			violations = append(violations, fmt.Sprintf("%s:%d", relative, position.Line))
			return true
		})
		return nil
	})
	sort.Strings(violations)
	return violations, err
}
