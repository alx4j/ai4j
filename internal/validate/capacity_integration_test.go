package validate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/diskcapacity"
)

func TestCommandsFailBeforeBoundedWritesWhenCapacityIsInsufficient(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{
		GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild,
		Runner: &fixtureRunner{files: firstPartyFiles(t)}, TempRoot: t.TempDir(),
		Capacity: func(string, uint64) error { return diskcapacity.ErrInsufficient },
	})
	if err != nil {
		t.Fatal(err)
	}

	validateRequest, _ := cli.Parse([]string{"ai4j", "validate", "--target", "claude"})
	validateReport := service.Validate(context.Background(), validateRequest.(cli.ValidateRequest).Source())
	if validateReport.Failure != FailureEnvironment || len(validateReport.Problems) != 1 || validateReport.Problems[0].Code() != "insufficient_disk_space" {
		t.Fatalf("validate report = failure:%s problems:%v", validateReport.Failure, validateReport.Problems)
	}

	output := filepath.Join(t.TempDir(), "build")
	buildRequest, _ := cli.Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", output, "--all"})
	buildReport := service.Build(context.Background(), buildRequest.(cli.BuildRequest))
	if buildReport.Failure != FailureEnvironment || len(buildReport.Problems) != 1 || buildReport.Problems[0].Code() != "insufficient_disk_space" {
		t.Fatalf("build report = failure:%s problems:%v", buildReport.Failure, buildReport.Problems)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("build output exists after failed preflight: %v", err)
	}

	initOutput := filepath.Join(t.TempDir(), "toolkit")
	initRequest, _ := cli.Parse([]string{"ai4j", "init", "--target", "codex", "--output", initOutput})
	initReport := service.Init(context.Background(), initRequest.(cli.InitRequest))
	if initReport.Failure != FailureEnvironment || len(initReport.Problems) != 1 || initReport.Problems[0].Code() != "insufficient_disk_space" {
		t.Fatalf("init report = failure:%s problems:%v", initReport.Failure, initReport.Problems)
	}
}
