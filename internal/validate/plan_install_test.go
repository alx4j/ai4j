package validate

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

func TestInspectNativeStatusAtRunsQueriesFromProjectDirectory(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	runner := &inspectionRunner{marketplaces: []byte(`[{"name":"project-marketplace"}]`), plugins: []byte(`[{"id":"project-plugin@project-marketplace","enabled":true}]`)}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	status, problem := service.InspectNativeStatusAt(context.Background(), project, "project-marketplace", "project-plugin@project-marketplace")
	if problem != nil || !status.MarketplaceRegistered || !status.PluginInstalled || !status.PluginEnabled {
		t.Fatalf("status=%#v problem=%v", status, problem)
	}
	if len(runner.directories) != 2 || runner.directories[0] != project || runner.directories[1] != project {
		t.Fatalf("inspection directories = %v", runner.directories)
	}
}

type inspectionRunner struct {
	marketplaces []byte
	plugins      []byte
	calls        int
	directories  []string
}

func (r *inspectionRunner) LookPath(name string) (string, error) {
	if name != "claude" {
		return "", fmt.Errorf("not found")
	}
	return "/usr/bin/claude", nil
}

func (r *inspectionRunner) Run(_ context.Context, directory string, _ string, arguments, _ []string) (ProcessResult, error) {
	r.calls++
	r.directories = append(r.directories, directory)
	switch {
	case slices.Equal(arguments, []string{"plugin", "marketplace", "list", "--json"}):
		return ProcessResult{Stdout: r.marketplaces}, nil
	case slices.Equal(arguments, []string{"plugin", "list", "--json"}):
		return ProcessResult{Stdout: r.plugins}, nil
	default:
		return ProcessResult{}, fmt.Errorf("unexpected arguments: %v", arguments)
	}
}
