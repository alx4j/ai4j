package human

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestResponseDataTypeSwitchIsExhaustiveAndHasDefault(t *testing.T) {
	t.Parallel()

	want := dataImplementations(t, "..")
	got, hasDefault := renderedDataCases(t, "render.go")
	if !hasDefault {
		t.Fatal("response data type switch has no fail-closed default")
	}
	if len(got) != len(want) {
		t.Fatalf("rendered data cases = %v, want all cli.Data implementations %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("rendered data cases = %v, want %v", got, want)
		}
	}
}

func dataImplementations(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", directory, err)
	}
	set := token.NewFileSet()
	implementations := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || filepath.Base(entry.Name()) == "doc.go" || len(entry.Name()) >= 8 && entry.Name()[len(entry.Name())-8:] == "_test.go" {
			continue
		}
		file, parseErr := parser.ParseFile(set, filepath.Join(directory, entry.Name()), nil, 0)
		if parseErr != nil {
			t.Fatalf("ParseFile(%s) error = %v", entry.Name(), parseErr)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "cliData" || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			if identifier, ok := function.Recv.List[0].Type.(*ast.Ident); ok {
				implementations[identifier.Name] = struct{}{}
			}
		}
	}
	values := make([]string, 0, len(implementations))
	for name := range implementations {
		values = append(values, name)
	}
	sort.Strings(values)
	return values
}

func renderedDataCases(t *testing.T, path string) ([]string, bool) {
	t.Helper()
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(%s) error = %v", path, err)
	}
	values := []string{}
	hasDefault := false
	ast.Inspect(file, func(node ast.Node) bool {
		typeSwitch, ok := node.(*ast.TypeSwitchStmt)
		if !ok {
			return true
		}
		for _, statement := range typeSwitch.Body.List {
			clause := statement.(*ast.CaseClause)
			if len(clause.List) == 0 {
				hasDefault = true
				continue
			}
			for _, expression := range clause.List {
				selector, ok := expression.(*ast.SelectorExpr)
				if ok {
					values = append(values, selector.Sel.Name)
				}
			}
		}
		return false
	})
	sort.Strings(values)
	return values, hasDefault
}
