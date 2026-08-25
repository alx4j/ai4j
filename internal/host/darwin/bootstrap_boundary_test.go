package darwin_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/host/darwin"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/target/claude/config/qualification"
)

var _ qualification.ProofSource = (*darwin.Bootstrap)(nil)

func TestBootstrapExposesNoPolicyHostOrMutationFacet(t *testing.T) {
	t.Parallel()

	value := any((*darwin.Bootstrap)(nil))
	for name, asserted := range map[string]bool{
		"host resource policy provider": func() bool { _, ok := value.(lifecycle.HostResourcePolicyProvider); return ok }(),
		"host services":                 func() bool { _, ok := value.(lifecycle.HostServices); return ok }(),
		"resource checker":              func() bool { _, ok := value.(lifecycle.ResourceChecker); return ok }(),
		"atomic file writer":            func() bool { _, ok := value.(lifecycle.AtomicFileWriter); return ok }(),
		"process runner":                func() bool { _, ok := value.(lifecycle.ProcessRunner); return ok }(),
		"target mutator":                func() bool { _, ok := value.(lifecycle.TargetMutator); return ok }(),
	} {
		if asserted {
			t.Fatalf("Bootstrap exposes %s", name)
		}
	}
}

func TestUserDirectoryProductionSourceContainsNoWritePrimitive(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	sourcePaths := []string{
		filepath.Join(filepath.Dir(currentFile), "bootstrap_darwin.go"),
		filepath.Join(filepath.Dir(currentFile), "filesystem", "user_directory_darwin.go"),
	}
	for _, sourcePath := range sourcePaths {
		source, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		for _, forbidden := range []string{
			"O_WRONLY", "O_RDWR", "O_CREAT", "Mkdir", "MkdirAll", "Rename", "Unlink", "Remove(", "RemoveAll",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains forbidden primitive %q", filepath.Base(sourcePath), forbidden)
			}
		}
	}
}
