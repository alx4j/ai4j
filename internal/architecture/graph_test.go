package architecture_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alx4j/ai4j/internal/architecture"
)

func TestDependencyFixtures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		fixture        string
		wantViolations bool
	}{
		{name: "allowed", fixture: "allowed.json"},
		{name: "forbidden edge", fixture: "forbidden.json", wantViolations: true},
		{name: "forbidden nested edge", fixture: "forbidden_nested.json", wantViolations: true},
		{name: "cycle", fixture: "cycle.json", wantViolations: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contents, err := os.ReadFile(filepath.Join("testdata", test.fixture))
			if err != nil {
				t.Fatal(err)
			}
			var graph architecture.Graph
			if err := json.Unmarshal(contents, &graph); err != nil {
				t.Fatal(err)
			}
			got := architecture.Check(graph)
			if (len(got) > 0) != test.wantViolations {
				t.Fatalf("violations = %v", got)
			}
		})
	}
}

func TestDarwinHostCannotDependOnConsumerOrCompositionPackages(t *testing.T) {
	t.Parallel()
	const host = "github.com/alx4j/ai4j/internal/host/darwin"
	for _, dependency := range []string{
		"github.com/alx4j/ai4j/internal/environment",
		"github.com/alx4j/ai4j/internal/registry",
		"github.com/alx4j/ai4j/internal/app",
		"github.com/alx4j/ai4j/internal/target/claude",
		"github.com/alx4j/ai4j/internal/source/github",
	} {
		violations := architecture.Check(architecture.Graph{host: {dependency}})
		if len(violations) != 1 {
			t.Fatalf("dependency %s violations = %v", dependency, violations)
		}
	}
}

func TestPathsafeCannotDependOnRepositoryImplementationPackages(t *testing.T) {
	t.Parallel()
	const pathPackage = "github.com/alx4j/ai4j/internal/pathsafe"
	for _, dependency := range []string{
		"github.com/alx4j/ai4j/internal/domain",
		"github.com/alx4j/ai4j/internal/lifecycle",
		"github.com/alx4j/ai4j/internal/registry",
		"github.com/alx4j/ai4j/internal/environment",
		"github.com/alx4j/ai4j/internal/host/darwin",
		"github.com/alx4j/ai4j/internal/source/github",
		"github.com/alx4j/ai4j/internal/target/claude",
	} {
		violations := architecture.Check(architecture.Graph{pathPackage: {dependency}})
		if len(violations) != 1 {
			t.Fatalf("dependency %s violations = %v", dependency, violations)
		}
	}
}
