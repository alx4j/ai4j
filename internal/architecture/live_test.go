package architecture_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/architecture"
)

type listedPackage struct {
	ImportPath string
	Imports    []string
}

func TestLiveProductionPackageGraph(t *testing.T) {
	root := repositoryRoot(t)
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	command := exec.Command(goBinary, "list", "-mod=readonly", "-json", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, output)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	graph := make(architecture.Graph)
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(pkg.ImportPath, "github.com/alx4j/ai4j/") {
			graph[pkg.ImportPath] = append([]string(nil), pkg.Imports...)
		}
	}
	if violations := architecture.Check(graph); len(violations) > 0 {
		t.Fatalf("architecture violations: %v", violations)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}
