package qualification_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/lifecycle"
	claudeconfig "github.com/alx4j/ai4j/internal/target/claude/config"
	"github.com/alx4j/ai4j/internal/target/claude/config/qualification"
)

func TestQualificationPackageKeepsTargetOwnedReadOnlyDependencyDirection(t *testing.T) {
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
		if strings.Contains(dependency, "/internal/host/") || strings.Contains(dependency, "/internal/registry") ||
			strings.Contains(dependency, "/internal/source/") || strings.Contains(dependency, "/internal/testkit") {
			t.Fatalf("qualification imports %q", dependency)
		}
	}
}

func TestQualifiedObservationExposesNoMutationOrHostOperation(t *testing.T) {
	t.Parallel()

	source, _ := newProofFixture(t, lifecycle.AbsentDirectoryLeaf(), lifecycle.DirectoryLeafPresence{})
	service, err := qualification.NewService(source)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := service.ResolveAndQualify(t.Context(), mustStartup(t, "", false), mustVersion(t), mustPolicy(t, claudeconfig.AllowedOverrideDecision()))
	if err != nil {
		t.Fatal(err)
	}
	for name, asserted := range map[string]bool{
		"resource checker":   func() bool { _, ok := any(observation).(lifecycle.ResourceChecker); return ok }(),
		"atomic file writer": func() bool { _, ok := any(observation).(lifecycle.AtomicFileWriter); return ok }(),
		"target mutator":     func() bool { _, ok := any(observation).(lifecycle.TargetMutator); return ok }(),
		"process runner":     func() bool { _, ok := any(observation).(lifecycle.ProcessRunner); return ok }(),
		"proof source":       func() bool { _, ok := any(observation).(qualification.ProofSource); return ok }(),
	} {
		if asserted {
			t.Fatalf("qualified facts expose %s", name)
		}
	}

	serviceType := reflect.TypeOf((*qualification.Service)(nil))
	if serviceType.NumMethod() != 1 || serviceType.Method(0).Name != "ResolveAndQualify" {
		t.Fatalf("Service exports an unpaired qualification method: %v", serviceType)
	}
}
