package environment_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/environment"
)

func TestToolIsClosedToGitAndClaude(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		value string
		want  environment.Tool
	}{{"git", environment.GitTool()}, {"claude", environment.ClaudeTool()}} {
		got, err := environment.NewTool(test.value)
		if err != nil || got != test.want || got.String() != test.value || !got.Valid() {
			t.Fatalf("NewTool(%q) = %v, %v", test.value, got, err)
		}
	}
	for _, value := range []string{"", "Git", "claude-code", "git\n", string([]byte{0xff})} {
		_, err := environment.NewTool(value)
		requireCode(t, err, environment.CodeInvalidTool)
	}
	if (environment.Tool{}).Valid() {
		t.Fatal("zero tool must be invalid")
	}
}

func TestSemanticVersionIsExactThreeComponentCore(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"0.0.0", "2.1.234", "4294967295.0.1"} {
		version, err := environment.NewSemanticVersion(value)
		if err != nil || !version.Valid() || version.String() != value {
			t.Fatalf("NewSemanticVersion(%q) = %v, %v", value, version, err)
		}
	}
	invalid := []string{
		"", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03", "1.2.x", "1.2.3-rc.1",
		"1.2.3+build", "1.2.3\n", "4294967296.0.0", strings.Repeat("1", 129),
	}
	for _, value := range invalid {
		_, err := environment.NewSemanticVersion(value)
		requireCode(t, err, environment.CodeInvalidSemanticVersion)
	}
	if (environment.SemanticVersion{}).Valid() {
		t.Fatal("zero semantic version must be invalid")
	}
}

func TestAppleGitRevisionPreservesVendorForm(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"0", "154", "154.3", "154.3.1.2"} {
		revision, err := environment.NewAppleGitRevision(value)
		if err != nil || !revision.Valid() || revision.String() != value {
			t.Fatalf("NewAppleGitRevision(%q) = %v, %v", value, revision, err)
		}
	}
	for _, value := range []string{"", "01", "154.", ".154", "154.3.1.2.5", "154-a", "154\n", "4294967296"} {
		_, err := environment.NewAppleGitRevision(value)
		requireCode(t, err, environment.CodeInvalidAppleGitRevision)
	}
	if (environment.AppleGitRevision{}).Valid() {
		t.Fatal("zero Apple Git revision must be invalid")
	}
}

func TestToolVersionKeepsAppleGitDistinctFromSemanticGit(t *testing.T) {
	t.Parallel()

	semantic := mustSemanticVersion(t, "2.39.5")
	plain, err := environment.NewSemanticToolVersion(environment.GitTool(), semantic)
	if err != nil || plain.String() != "2.39.5" || plain.Form() != environment.SemanticToolVersionForm() {
		t.Fatalf("plain Git version = %v, %v", plain, err)
	}
	revision, _ := environment.NewAppleGitRevision("154.3")
	apple, err := environment.NewAppleGitToolVersion(semantic, revision)
	if err != nil || apple.String() != "2.39.5 (Apple Git-154.3)" || apple.Form() != environment.AppleGitToolVersionForm() {
		t.Fatalf("Apple Git version = %v, %v", apple, err)
	}
	if plain == apple {
		t.Fatal("plain and Apple Git versions must remain distinct")
	}
	gotRevision, ok := apple.AppleRevision()
	if !ok || gotRevision != revision {
		t.Fatalf("AppleRevision() = %v, %t", gotRevision, ok)
	}
	claude, err := environment.NewSemanticToolVersion(environment.ClaudeTool(), mustSemanticVersion(t, "2.1.234"))
	if err != nil || claude.Tool() != environment.ClaudeTool() || !claude.Valid() {
		t.Fatalf("Claude version = %v, %v", claude, err)
	}
	if _, err := environment.NewSemanticToolVersion(environment.Tool{}, semantic); err == nil {
		t.Fatal("zero tool accepted")
	} else {
		requireCode(t, err, environment.CodeInvalidToolVersion)
	}
	if _, err := environment.NewAppleGitToolVersion(environment.SemanticVersion{}, revision); err == nil {
		t.Fatal("zero semantic version accepted")
	} else {
		requireCode(t, err, environment.CodeInvalidToolVersion)
	}
	if (environment.ToolVersion{}).Valid() || (environment.ToolVersionForm{}).Valid() {
		t.Fatal("zero tool version values must be invalid")
	}
	encoded, err := json.Marshal(apple)
	if err != nil || !strings.Contains(string(encoded), `"form":"apple_git"`) {
		t.Fatalf("MarshalJSON() = %s, %v", encoded, err)
	}
}
