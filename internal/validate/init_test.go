package validate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
)

func TestInitCreatesValidDeterministicTargetScaffold(t *testing.T) {
	parent := t.TempDir()
	service, err := NewService(Config{GOOS: "windows", GOARCH: "amd64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: firstPartyFiles(t)}, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var manifests [][]byte
	for _, name := range []string{"first", "second"} {
		output := filepath.Join(parent, name)
		request, parseErr := cli.NewParser().Parse([]string{"ai4j.exe", "init", "--target", "claude", "--target", "codex", "--output", output, "--examples"})
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		report := service.Init(context.Background(), request.(cli.InitRequest))
		if report.Failure != FailureNone || len(report.Problems) != 0 || len(report.Artifacts) != 7 {
			t.Fatalf("init failure=%s problems=%v artifacts=%d", report.Failure, report.Problems, len(report.Artifacts))
		}
		for _, expected := range []string{"toolkit.json", ".gitignore", "README.md", "targets/claude/example-toolkit/.claude-plugin/plugin.json", "targets/claude/example-toolkit/skills/example-skill/SKILL.md", "targets/codex/example-toolkit/.codex-plugin/plugin.json", "targets/codex/example-toolkit/skills/example-skill/SKILL.md"} {
			if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(expected))); err != nil {
				t.Errorf("generated %s: %v", expected, err)
			}
		}
		manifest, readErr := os.ReadFile(filepath.Join(output, "toolkit.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		manifests = append(manifests, manifest)
	}
	if !bytes.Equal(manifests[0], manifests[1]) {
		t.Fatal("identical init requests produced different manifests")
	}
	files := readBuildTree(t, filepath.Join(parent, "first"))
	buildService, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: files}, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	buildOutput := filepath.Join(parent, "built")
	buildRequest, err := cli.NewParser().Parse([]string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", buildOutput, "--bundle", "examples"})
	if err != nil {
		t.Fatal(err)
	}
	build := buildService.Build(context.Background(), buildRequest.(cli.BuildRequest))
	if build.Failure != FailureNone || len(build.Selection) != 1 || build.Selection[0].Variant() != "codex" {
		t.Fatalf("generated toolkit build failure=%s problems=%v selection=%v", build.Failure, build.Problems, build.Selection)
	}
	if _, err := os.Stat(filepath.Join(buildOutput, "plugin", ".codex-plugin", "plugin.json")); err != nil {
		t.Fatalf("generated toolkit native output: %v", err)
	}
}

func TestInitRefusesOccupiedOutputWithoutChangingIt(t *testing.T) {
	output := filepath.Join(t.TempDir(), "occupied")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(output, "keep")
	if err := os.WriteFile(marker, []byte("user-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(Config{GOOS: "windows", GOARCH: "amd64", Home: t.TempDir(), BuildCommit: testBuild, Runner: &fixtureRunner{files: firstPartyFiles(t)}, TempRoot: t.TempDir()})
	request, _ := cli.NewParser().Parse([]string{"ai4j.exe", "init", "--target", "codex", "--output", output})
	report := service.Init(context.Background(), request.(cli.InitRequest))
	content, readErr := os.ReadFile(marker)
	if report.Failure != FailureConflict || len(report.Problems) != 1 || report.Problems[0].Code() != "output_occupied" || readErr != nil || string(content) != "user-owned" {
		t.Fatalf("failure=%s problems=%v marker=%q error=%v", report.Failure, report.Problems, content, readErr)
	}
}
