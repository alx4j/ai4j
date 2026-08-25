package discovery

import (
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/environment"
)

func TestParseGitVersionOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		output   string
		want     string
		wantForm environment.ToolVersionForm
		valid    bool
	}{
		{name: "upstream", output: "git version 2.51.0\n", want: "2.51.0", wantForm: environment.SemanticToolVersionForm(), valid: true},
		{name: "no newline", output: "git version 2.51.0", want: "2.51.0", wantForm: environment.SemanticToolVersionForm(), valid: true},
		{name: "crlf", output: "git version 2.51.0\r\n", want: "2.51.0", wantForm: environment.SemanticToolVersionForm(), valid: true},
		{name: "Apple vendor", output: "git version 2.39.5 (Apple Git-154.3)\n", want: "2.39.5 (Apple Git-154.3)", wantForm: environment.AppleGitToolVersionForm(), valid: true},
		{name: "empty"},
		{name: "wrong prefix", output: "Git version 2.51.0\n"},
		{name: "leading zero", output: "git version 02.51.0\n"},
		{name: "prerelease", output: "git version 2.51.0-rc1\n"},
		{name: "two components", output: "git version 2.51\n"},
		{name: "extra line", output: "git version 2.51.0\nextra\n"},
		{name: "double newline", output: "git version 2.51.0\n\n"},
		{name: "bare carriage return", output: "git version 2.51.0\r"},
		{name: "Apple missing close", output: "git version 2.39.5 (Apple Git-154.3\n"},
		{name: "Apple bad revision", output: "git version 2.39.5 (Apple Git-0154)\n"},
		{name: "oversized", output: "git version 2.51.0" + strings.Repeat("x", int(probeOutputLimitBytes))},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseVersionOutput(environment.GitTool(), test.output)
			if ok != test.valid {
				t.Fatalf("parseVersionOutput() valid = %v, want %v", ok, test.valid)
			}
			if test.valid && (!got.Valid() || got.Tool() != environment.GitTool() || got.String() != test.want || got.Form() != test.wantForm) {
				t.Fatalf("parseVersionOutput() = %v", got)
			}
		})
	}
}

func TestParseClaudeVersionOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		valid  bool
	}{
		{name: "documented", output: "2.1.211 (Claude Code)\n", valid: true},
		{name: "no newline", output: "2.1.211 (Claude Code)", valid: true},
		{name: "crlf", output: "2.1.211 (Claude Code)\r\n", valid: true},
		{name: "empty"},
		{name: "semantic only", output: "2.1.211\n"},
		{name: "wrong suffix case", output: "2.1.211 (claude code)\n"},
		{name: "leading space", output: " 2.1.211 (Claude Code)\n"},
		{name: "leading zero", output: "2.01.211 (Claude Code)\n"},
		{name: "extra line", output: "2.1.211 (Claude Code)\nextra\n"},
		{name: "tab", output: "2.1.211\t(Claude Code)\n"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseVersionOutput(environment.ClaudeTool(), test.output)
			if ok != test.valid {
				t.Fatalf("parseVersionOutput() valid = %v, want %v", ok, test.valid)
			}
			if test.valid && (!got.Valid() || got.Tool() != environment.ClaudeTool() || got.String() != "2.1.211") {
				t.Fatalf("parseVersionOutput() = %v", got)
			}
		})
	}
}

func TestParseVersionOutputRejectsUnknownTool(t *testing.T) {
	t.Parallel()

	if _, ok := parseVersionOutput(environment.Tool{}, "2.1.211 (Claude Code)\n"); ok {
		t.Fatal("unknown tool accepted")
	}
}
