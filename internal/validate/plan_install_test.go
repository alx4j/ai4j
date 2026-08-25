package validate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestInspectPlanInstallReportsOwnedAndNativeIdentityConflicts(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	state := filepath.Join(home, "Library", "Application Support", "ai4j", "state")
	rules := filepath.Join(home, ".claude", "rules")
	for _, directory := range []string{state, rules} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(state, "installation.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rules, "ai4j.md"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &inspectionRunner{
		marketplaces: []byte(`[{"name":"ai4j","source":"directory"}]`),
		plugins:      []byte(`[{"id":"ai4j-default@ai4j","enabled":true}]`),
	}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}

	conflicts, problem := service.InspectPlanInstall(context.Background())
	if problem != nil {
		t.Fatalf("problem = %v", problem)
	}
	codes := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		codes = append(codes, conflict.Code())
	}
	slices.Sort(codes)
	want := []string{"installation_exists", "marketplace_identity_conflict", "plugin_identity_conflict", "rules_destination_occupied"}
	if !slices.Equal(codes, want) {
		t.Fatalf("conflicts = %v, want %v", codes, want)
	}
	if runner.calls != 2 {
		t.Fatalf("native inspection calls = %d, want 2", runner.calls)
	}
}

func TestInspectPlanInstallAcceptsEmptyDestinations(t *testing.T) {
	t.Parallel()

	runner := &inspectionRunner{marketplaces: []byte(`[]`), plugins: []byte(`[]`)}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	conflicts, problem := service.InspectPlanInstall(context.Background())
	if problem != nil || len(conflicts) != 0 {
		t.Fatalf("conflicts=%v problem=%v", conflicts, problem)
	}
}

func TestInspectNativeStatusReportsDocumentedListObservations(t *testing.T) {
	t.Parallel()
	runner := &inspectionRunner{
		marketplaces: []byte(`[{"name":"ai4j"}]`),
		plugins:      []byte(`[{"id":"ai4j-default@ai4j","enabled":false}]`),
	}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}

	status, problem := service.InspectNativeStatus(context.Background())
	if problem != nil {
		t.Fatalf("problem = %v", problem)
	}
	if !status.MarketplaceRegistered || !status.PluginInstalled || status.PluginEnabled || runner.calls != 2 {
		t.Fatalf("status=%#v calls=%d", status, runner.calls)
	}
}

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

func TestInspectPlanExistingAcceptsMatchingOwnedAndNativeState(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	catalogPath := filepath.Join(home, "Library", "Application Support", "ai4j", "state", "catalog", ".claude-plugin", "marketplace.json")
	rulesPath := filepath.Join(home, ".claude", "rules", "ai4j.md")
	for _, path := range []string{catalogPath, rulesPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	catalogBytes := []byte("catalog")
	rulesBytes := []byte("rules")
	if err := os.WriteFile(catalogPath, catalogBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulesPath, rulesBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &inspectionRunner{
		marketplaces: []byte(`[{"name":"ai4j"}]`),
		plugins:      []byte(`[{"id":"ai4j-default@ai4j","enabled":true}]`),
	}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	catalogDigest := sha256.Sum256(catalogBytes)
	rulesDigest := sha256.Sum256(rulesBytes)
	conflicts, problem := service.InspectPlanExisting(context.Background(), fmt.Sprintf("%x", catalogDigest), fmt.Sprintf("%x", rulesDigest))
	if problem != nil || len(conflicts) != 0 {
		t.Fatalf("conflicts=%v problem=%v", conflicts, problem)
	}
	runner.plugins = []byte(`[{"id":"ai4j-default@ai4j","enabled":false}]`)
	conflicts, problem = service.InspectPlanExisting(context.Background(), fmt.Sprintf("%x", catalogDigest), fmt.Sprintf("%x", rulesDigest))
	if problem != nil || len(conflicts) != 1 || conflicts[0].Code() != "plugin_disabled" {
		t.Fatalf("disabled plugin conflicts=%v problem=%v", conflicts, problem)
	}
	conflicts, problem = service.InspectUninstall(context.Background(), fmt.Sprintf("%x", catalogDigest), fmt.Sprintf("%x", rulesDigest))
	if problem != nil || len(conflicts) != 0 {
		t.Fatalf("disabled plugin uninstall conflicts=%v problem=%v", conflicts, problem)
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
