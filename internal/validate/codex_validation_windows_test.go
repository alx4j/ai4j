//go:build windows

package validate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alx4j/ai4j/internal/hostprocess"
)

type codexShimRunner struct {
	node string
}

func (r codexShimRunner) LookPath(name string) (string, error) {
	if name == "node" {
		return r.node, nil
	}
	return "", errors.New("executable not found")
}

func (codexShimRunner) Run(ctx context.Context, directory, executable string, arguments, environment []string) (hostprocess.Result, error) {
	return (hostprocess.OSRunner{}).Run(ctx, directory, executable, arguments, environment)
}

func (codexShimRunner) RunIsolated(ctx context.Context, directory, executable string, arguments, environment []string) (hostprocess.Result, error) {
	return (hostprocess.OSRunner{}).RunIsolated(ctx, directory, executable, arguments, environment)
}

func TestCodexValidationRunsWindowsNPMStyleShimWithIsolatedEnvironment(t *testing.T) {
	codexDirectory := t.TempDir()
	nodeDirectory := t.TempDir()
	codex := filepath.Join(codexDirectory, "codex.cmd")
	node := filepath.Join(nodeDirectory, "node.cmd")
	if err := os.WriteFile(codex, []byte("@echo off\r\nnode %*\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, []byte("@echo off\r\necho {\"checks\":{\"config.load\":{\"status\":\"ok\",\"details\":{}}}}\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	agents := filepath.Join(stage, "configuration", ".codex", "agents")
	if err := os.MkdirAll(agents, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "reviewer.toml"), []byte("name = \"reviewer\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	service := Service{config: Config{Runner: codexShimRunner{node: node}, TempRoot: temporary}}
	if err := service.validateRenderedCodexAgents(context.Background(), stage, codex); err != nil {
		t.Fatalf("validateRenderedCodexAgents() = %v", err)
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary validation homes = %v, %v", entries, err)
	}
}
