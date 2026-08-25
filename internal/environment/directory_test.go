package environment_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/environment"
)

func TestDirectoryEnumsAreClosedAndCanonical(t *testing.T) {
	t.Parallel()

	roles := []struct {
		value string
		want  environment.DirectoryRole
	}{
		{"claude_configuration", environment.ClaudeConfigurationDirectory()},
		{"claude_rules", environment.ClaudeRulesDirectory()},
		{"ai4j_state", environment.AI4JStateDirectory()},
		{"ai4j_recovery", environment.AI4JRecoveryDirectory()},
	}
	for _, test := range roles {
		got, err := environment.NewDirectoryRole(test.value)
		if err != nil || got != test.want || got.String() != test.value {
			t.Fatalf("NewDirectoryRole(%q) = %v, %v", test.value, got, err)
		}
	}
	sources := []struct {
		value string
		want  environment.DirectorySource
	}{
		{"default", environment.DefaultDirectorySource()},
		{"environment_override", environment.EnvironmentOverrideDirectorySource()},
		{"private_runtime", environment.PrivateRuntimeDirectorySource()},
	}
	for _, test := range sources {
		got, err := environment.NewDirectorySource(test.value)
		if err != nil || got != test.want || got.String() != test.value {
			t.Fatalf("NewDirectorySource(%q) = %v, %v", test.value, got, err)
		}
	}
	presences := []struct {
		value string
		want  environment.DirectoryPresence
	}{{"present", environment.PresentDirectory()}, {"absent", environment.AbsentDirectory()}}
	for _, test := range presences {
		got, err := environment.NewDirectoryPresence(test.value)
		if err != nil || got != test.want || got.String() != test.value {
			t.Fatalf("NewDirectoryPresence(%q) = %v, %v", test.value, got, err)
		}
	}
	_, roleErr := environment.NewDirectoryRole("CLAUDE_RULES")
	requireCode(t, roleErr, environment.CodeInvalidDirectoryRole)
	_, sourceErr := environment.NewDirectorySource("ambient")
	requireCode(t, sourceErr, environment.CodeInvalidDirectorySource)
	_, presenceErr := environment.NewDirectoryPresence("unknown")
	requireCode(t, presenceErr, environment.CodeInvalidDirectoryPresence)
	if (environment.DirectoryRole{}).Valid() || (environment.DirectorySource{}).Valid() || (environment.DirectoryPresence{}).Valid() {
		t.Fatal("zero directory enums must be invalid")
	}
}

func TestDirectoryRequiresCanonicalAbsolutePathAndCoherentSource(t *testing.T) {
	t.Parallel()

	valid := []struct {
		role   environment.DirectoryRole
		source environment.DirectorySource
		path   string
	}{
		{environment.ClaudeConfigurationDirectory(), environment.DefaultDirectorySource(), "/Users/alex/.claude"},
		{environment.ClaudeRulesDirectory(), environment.EnvironmentOverrideDirectorySource(), "/Users/alex/Claude Config/rules"},
		{environment.AI4JStateDirectory(), environment.PrivateRuntimeDirectorySource(), "/Users/alex/Library/Application Support/ai4j/state"},
		{environment.AI4JRecoveryDirectory(), environment.PrivateRuntimeDirectorySource(), "/Users/alex/Library/Application Support/ai4j/recovery"},
	}
	for _, test := range valid {
		directory, err := environment.NewDirectory(test.role, test.source, environment.PresentDirectory(), test.path)
		if err != nil || !directory.Valid() || directory.AbsolutePath() != test.path {
			t.Fatalf("NewDirectory(%s) = %v, %v", test.path, directory, err)
		}
	}
	invalidPaths := []string{
		"", ".claude", "/", "/Users//alex/.claude", "/Users/alex/./.claude", "/Users/alex/../alex/.claude",
		"/Users/alex/.claude/", "/Users/alex/line\nbreak", string([]byte{'/', 0xff}), "/" + strings.Repeat("a", 4096),
	}
	for _, value := range invalidPaths {
		_, err := environment.NewDirectory(
			environment.ClaudeConfigurationDirectory(), environment.DefaultDirectorySource(), environment.PresentDirectory(), value,
		)
		requireCode(t, err, environment.CodeInvalidDirectory)
	}
	for _, test := range []struct {
		role   environment.DirectoryRole
		source environment.DirectorySource
	}{
		{environment.ClaudeConfigurationDirectory(), environment.PrivateRuntimeDirectorySource()},
		{environment.ClaudeRulesDirectory(), environment.PrivateRuntimeDirectorySource()},
		{environment.AI4JStateDirectory(), environment.DefaultDirectorySource()},
		{environment.AI4JRecoveryDirectory(), environment.EnvironmentOverrideDirectorySource()},
	} {
		_, err := environment.NewDirectory(test.role, test.source, environment.PresentDirectory(), "/Users/alex/value")
		requireCode(t, err, environment.CodeInvalidDirectory)
	}
	if (environment.Directory{}).Valid() {
		t.Fatal("zero directory must be invalid")
	}
}

func TestDirectoryFormattingAndJSONRedactPath(t *testing.T) {
	t.Parallel()

	directory := mustDirectory(
		t,
		environment.ClaudeConfigurationDirectory(),
		environment.EnvironmentOverrideDirectorySource(),
		environment.PresentDirectory(),
		"/Users/alex/"+pathCanary,
	)
	formatted := fmt.Sprintf("%v|%+v|%#v|%q", directory, directory, directory, directory)
	encoded, err := json.Marshal(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{formatted, string(encoded)} {
		if strings.Contains(value, pathCanary) || strings.Contains(value, "/Users/alex") {
			t.Fatalf("directory disclosure = %q", value)
		}
	}
	if !strings.Contains(string(encoded), `"path":"redacted"`) {
		t.Fatalf("MarshalJSON() = %s", encoded)
	}
}
