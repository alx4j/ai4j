package environment_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/environment"
)

func TestCandidateCapabilitySetIsNotMutationCapabilitySet(t *testing.T) {
	t.Parallel()

	candidateType := reflect.TypeOf(environment.CandidateCapabilitySet{})
	mutationType := reflect.TypeOf(domain.CapabilitySet{})
	if candidateType.AssignableTo(mutationType) || candidateType.ConvertibleTo(mutationType) {
		t.Fatal("candidate capabilities can cross the mutation capability boundary")
	}
}

func TestCandidateCapabilitySetFailsAtCompileBoundary(t *testing.T) {
	t.Parallel()

	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	command := exec.Command(goBinary, "test", ".")
	command.Dir = filepath.Join("testdata", "candidate_as_qualified")
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "CGO_ENABLED=0")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("candidate-as-qualified fixture compiled successfully")
	}
	if !strings.Contains(string(output), "cannot use candidate") || !strings.Contains(string(output), "domain.CapabilitySet") {
		t.Fatalf("fixture failed for an unexpected reason: %v\n%s", err, output)
	}
}
