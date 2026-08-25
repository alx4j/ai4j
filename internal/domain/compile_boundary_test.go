package domain_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIdentityTypesFailAtCompileBoundary(t *testing.T) {
	t.Parallel()

	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	command := exec.Command(goBinary, "test", ".")
	command.Dir = filepath.Join("testdata", "type_mismatch")
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "CGO_ENABLED=0")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("type-mismatch fixture compiled successfully")
	}
	if !strings.Contains(string(output), "cannot use") {
		t.Fatalf("fixture failed for an unexpected reason: %v\n%s", err, output)
	}
}
