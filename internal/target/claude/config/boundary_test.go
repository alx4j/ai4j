package config_test

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/target/claude/config"
)

func TestPureConfigPackageHasNoHostCompositionOrMutationDependency(t *testing.T) {
	t.Parallel()

	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	command := exec.Command(goBinary, "list", "-mod=readonly", "-json", ".")
	command.Dir = "."
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "CGO_ENABLED=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, output)
	}
	var pkg struct{ Imports []string }
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&pkg); err != nil {
		t.Fatal(err)
	}
	for _, dependency := range pkg.Imports {
		if dependency == "path/filepath" ||
			strings.Contains(dependency, "/internal/host/") ||
			strings.Contains(dependency, "/internal/registry") ||
			strings.Contains(dependency, "/internal/source/") ||
			strings.Contains(dependency, "/internal/testkit") ||
			strings.Contains(dependency, "/internal/lifecycle") {
			t.Fatalf("pure config imports %q", dependency)
		}
	}
}

func TestAmbientEnvironmentAccessIsIsolatedToFixedSource(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, imported := range parsed.Imports {
			if imported.Path.Value == `"os"` && filepath.Base(file) != "source.go" {
				t.Fatalf("ambient os import outside source.go: %s", file)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if ok && identifier.Name == "os" && selector.Sel.Name != "LookupEnv" {
				t.Fatalf("unexpected os call %s.%s in %s", identifier.Name, selector.Sel.Name, file)
			}
			return true
		})
	}
	typeOfSource := reflect.TypeOf((*config.StartupSource)(nil)).Elem()
	method, ok := typeOfSource.MethodByName("Capture")
	if !ok || method.Type.NumIn() != 1 || method.Type.In(0).String() != "context.Context" || method.Type.NumOut() != 2 {
		t.Fatalf("StartupSource.Capture signature = %v", method.Type)
	}
}

func TestPureObservationExposesNoHostOrTargetMutationAuthority(t *testing.T) {
	t.Parallel()

	version := mustClaudeVersion(t, "2.1.211")
	observation, err := config.ResolveCandidate(
		t.Context(), mustInput(t, testHome, true, "", false), mustHome(t, testHome), version,
		mustPolicy(t, version, config.AllowedOverrideDecision()),
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, asserted := range map[string]bool{
		"resource checker":   func() bool { _, ok := any(observation).(lifecycle.ResourceChecker); return ok }(),
		"atomic file writer": func() bool { _, ok := any(observation).(lifecycle.AtomicFileWriter); return ok }(),
		"target mutator":     func() bool { _, ok := any(observation).(lifecycle.TargetMutator); return ok }(),
		"process runner":     func() bool { _, ok := any(observation).(lifecycle.ProcessRunner); return ok }(),
	} {
		if asserted {
			t.Fatalf("candidate observation exposes %s", name)
		}
	}
	for _, forbiddenSymbol := range []string{"DirectoryQualifier", "DirectoryProof", "QualifiedHome"} {
		matches, globErr := filepath.Glob("*.go")
		if globErr != nil {
			t.Fatal(globErr)
		}
		for _, file := range matches {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			content, readErr := os.ReadFile(file)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(content), forbiddenSymbol) {
				t.Fatalf("target-owned shared authority symbol %q remains in %s", forbiddenSymbol, file)
			}
		}
	}
}
