package discovery_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/environment/discovery"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestDiscoveryServiceExposesNoHostOrTargetMutationAuthority(t *testing.T) {
	t.Parallel()

	service, _, _, _, _ := newFixture(t)
	for name, asserted := range map[string]bool{
		"process runner":     func() bool { _, ok := any(service).(lifecycle.ProcessRunner); return ok }(),
		"atomic file writer": func() bool { _, ok := any(service).(lifecycle.AtomicFileWriter); return ok }(),
		"target mutator":     func() bool { _, ok := any(service).(lifecycle.TargetMutator); return ok }(),
		"resource checker":   func() bool { _, ok := any(service).(lifecycle.ResourceChecker); return ok }(),
	} {
		if asserted {
			t.Fatalf("discovery service exposes %s", name)
		}
	}
}

func TestDiscoveryProductionImportsStayNeutralAndPathOpaque(t *testing.T) {
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
		if dependency == "os" || dependency == "os/exec" || dependency == "path/filepath" ||
			strings.Contains(dependency, "/internal/host/") || strings.Contains(dependency, "/internal/target/") ||
			strings.Contains(dependency, "/internal/source/") || strings.Contains(dependency, "/internal/registry") ||
			strings.Contains(dependency, "/internal/testkit") {
			t.Fatalf("production discovery imports %q", dependency)
		}
	}
}

func TestProbeTimeoutTypesFailAtCompileBoundary(t *testing.T) {
	t.Parallel()

	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	command := exec.Command(goBinary, "test", ".")
	command.Dir = filepath.Join("testdata", "swapped_timeouts")
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "CGO_ENABLED=0")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("swapped timeout fixture compiled successfully")
	}
	if !strings.Contains(string(output), "cannot use profile.ClaudeTimeoutMaximum()") ||
		!strings.Contains(string(output), "lifecycle.GitTimeoutMaximum") {
		t.Fatalf("fixture failed for an unexpected reason: %v\n%s", err, output)
	}
}

var _ interface {
	DiscoverPrerequisites(context.Context) (discovery.PrerequisiteObservation, error)
} = (*discovery.Service)(nil)
