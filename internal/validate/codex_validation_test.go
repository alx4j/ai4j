package validate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
)

func TestCodexConfigLoadResult(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		valid    bool
		reported bool
	}{
		{name: "valid", output: `{"overallStatus":"error","checks":{"config.load":{"status":"ok","details":{}}}}`, valid: true, reported: true},
		{name: "null warning", output: `{"checks":{"config.load":{"status":"ok","details":{"startup warning":null}}}}`, valid: true, reported: true},
		{name: "empty warning", output: `{"checks":{"config.load":{"status":"ok","details":{"startup warning":""}}}}`, valid: true, reported: true},
		{name: "empty warning list", output: `{"checks":{"config.load":{"status":"ok","details":{"startup warning":[]}}}}`, valid: true, reported: true},
		{name: "load error", output: `{"checks":{"config.load":{"status":"error","details":{}}}}`, reported: true},
		{name: "warning status", output: `{"checks":{"config.load":{"status":"warning","details":{"startup warning":"Ignoring malformed agent role definition"}}}}`, reported: true},
		{name: "warning hidden behind ok status", output: `{"checks":{"config.load":{"status":"ok","details":{"startup warning":"Ignoring malformed agent role definition"}}}}`, reported: true},
		{name: "missing check", output: `{"checks":{}}`},
		{name: "malformed document", output: `{`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			valid, reported := codexConfigLoadResult([]byte(test.output))
			if valid != test.valid || reported != test.reported || validCodexConfigLoad([]byte(test.output)) != test.valid {
				t.Fatalf("codexConfigLoadResult() = %t, %t, want %t, %t", valid, reported, test.valid, test.reported)
			}
		})
	}
}

func TestBuildRequiresNativeCodexValidationForSelectedAgents(t *testing.T) {
	tests := []struct {
		name        string
		runner      *fixtureRunner
		wantFailure Failure
		wantCode    string
	}{
		{
			name:        "Codex unavailable",
			runner:      &fixtureRunner{missingExecutables: map[string]bool{"codex": true}},
			wantFailure: FailureEnvironment,
			wantCode:    "unsupported_capability",
		},
		{
			name:        "configuration rejected",
			runner:      &fixtureRunner{codexConfigLoadStatus: "error"},
			wantFailure: FailureValidation,
			wantCode:    "native_validation_failed",
		},
		{
			name:        "malformed role warning",
			runner:      &fixtureRunner{codexStartupWarning: "Ignoring malformed agent role definition"},
			wantFailure: FailureValidation,
			wantCode:    "native_validation_failed",
		},
		{
			name:        "validator process failure",
			runner:      &fixtureRunner{codexValidationErr: context.DeadlineExceeded},
			wantFailure: FailureEnvironment,
			wantCode:    "native_validation_unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.runner.files = firstPartyFiles(t)
			output := filepath.Join(t.TempDir(), "codex-build")
			service, err := NewService(Config{
				GOOS: "windows", GOARCH: "amd64", Home: t.TempDir(), BuildCommit: testBuild,
				Runner: test.runner, TempRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatal(err)
			}
			request, err := cli.Parse([]string{
				"ai4j", "build", "--target", "codex", "--host", "windows-amd64",
				"--output", output, "--bundle", "review",
			})
			if err != nil {
				t.Fatal(err)
			}
			report := service.Build(context.Background(), request.(cli.BuildRequest))
			if report.Failure != test.wantFailure || len(report.Problems) != 1 || report.Problems[0].Code() != test.wantCode {
				t.Fatalf("build = failure:%s problems:%v", report.Failure, report.Problems)
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatalf("failed build retained output: %v", err)
			}
		})
	}
}

func TestBuildWithoutCodexAgentDoesNotRequireCodex(t *testing.T) {
	runner := &fixtureRunner{
		files:              firstPartyFiles(t),
		missingExecutables: map[string]bool{"codex": true},
	}
	output := filepath.Join(t.TempDir(), "codex-build")
	service, err := NewService(Config{
		GOOS: "windows", GOARCH: "amd64", Home: t.TempDir(), BuildCommit: testBuild,
		Runner: runner, TempRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.Parse([]string{
		"ai4j", "build", "--target", "codex", "--host", "windows-amd64",
		"--output", output, "--asset", "ai4j-rules",
	})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Build(context.Background(), request.(cli.BuildRequest))
	if report.Failure != FailureNone || len(report.Problems) != 0 || runner.codexValidations != 0 {
		t.Fatalf("build = failure:%s problems:%v validations:%d", report.Failure, report.Problems, runner.codexValidations)
	}
	if _, err := os.Stat(filepath.Join(output, "configuration", "AGENTS.md")); err != nil {
		t.Fatalf("Codex rules output: %v", err)
	}
}
