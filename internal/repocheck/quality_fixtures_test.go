package repocheck

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestQualityGateFailureFixtures(t *testing.T) {
	goCommand := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goCommand += ".exe"
	}

	t.Run("formatting drift", func(t *testing.T) {
		root := t.TempDir()
		writeFixture(t, root, "drift.go", "package fixture\nfunc drift( ){ }\n")
		if err := CheckFormat(root, []string{"drift.go"}); err == nil {
			t.Fatal("formatting gate succeeded for drift fixture")
		}
	})

	t.Run("go mod drift", func(t *testing.T) {
		root := newFixtureModule(t)
		writeFixture(t, root, "go.mod", "module example.com/fixture\n\ngo 1.26.0\n\nrequire example.com/unused v0.0.0\n\nreplace example.com/unused => ./unused\n")
		writeFixture(t, root, "unused/go.mod", "module example.com/unused\n\ngo 1.26.0\n")
		assertGoCommandFails(t, goCommand, root, "mod", "tidy", "-diff")
	})

	t.Run("go sum drift", func(t *testing.T) {
		root := newFixtureModule(t)
		writeFixture(t, root, "go.sum", "example.com/dead v1.0.0 h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n")
		assertGoCommandFails(t, goCommand, root, "mod", "tidy", "-diff")
	})

	t.Run("unit test failure", func(t *testing.T) {
		root := newFixtureModule(t)
		writeFixture(t, root, "fixture_test.go", "package fixture\nimport \"testing\"\nfunc TestFailure(t *testing.T) { t.Fatal(\"fixture failure\") }\n")
		assertGoCommandFails(t, goCommand, root, "test", "./...")
	})

	t.Run("vet finding", func(t *testing.T) {
		root := newFixtureModule(t)
		writeFixture(t, root, "fixture.go", "package fixture\nimport \"fmt\"\nfunc finding() { fmt.Printf(\"%d\", \"wrong\") }\n")
		assertGoCommandFails(t, goCommand, root, "vet", "./...")
	})

	t.Run("race failure", func(t *testing.T) {
		root := newFixtureModule(t)
		writeFixture(t, root, "fixture_test.go", `package fixture
import "testing"
func TestRace(t *testing.T) {
	start := make(chan struct{})
	done := make(chan struct{})
	value := 0
	go func() {
		<-start
		value++
		close(done)
	}()
	close(start)
	value++
	<-done
}
`)
		output := runFailingGoCommand(t, goCommand, root, "1", "test", "-race", "./...")
		if strings.Contains(output, "C compiler") || strings.Contains(output, "cgo:") {
			t.Skip("native race toolchain is unavailable on this host")
		}
		if !strings.Contains(output, "DATA RACE") {
			t.Fatalf("race gate failed without detecting the fixture race:\n%s", output)
		}
	})
}

func newFixtureModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/fixture\n\ngo 1.26.0\n")
	writeFixture(t, root, "fixture.go", "package fixture\n")
	return root
}

func writeFixture(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertGoCommandFails(t *testing.T, goCommand, root string, args ...string) {
	t.Helper()
	assertGoCommandFailsWithCGO(t, goCommand, root, "0", args...)
}

func assertGoCommandFailsWithCGO(t *testing.T, goCommand, root, cgo string, args ...string) {
	t.Helper()
	runFailingGoCommand(t, goCommand, root, cgo, args...)
}

func runFailingGoCommand(t *testing.T, goCommand, root, cgo string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, goCommand, args...)
	command.Dir = root
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "CGO_ENABLED="+cgo)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("go %s timed out: %v", strings.Join(args, " "), ctx.Err())
	}
	if err == nil {
		t.Fatalf("go %s succeeded, want gate failure\n%s", strings.Join(args, " "), output)
	}
	return string(output)
}
