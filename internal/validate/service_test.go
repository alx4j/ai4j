package validate

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/hostprocess"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
)

func TestAuthenticatedGitEnvironmentAllowsConfiguredCredentialHelpersWithoutPrompting(t *testing.T) {
	t.Parallel()

	environment := gitAuthenticatedEnvironment()
	if slices.Contains(environment, "GIT_CONFIG_NOSYSTEM=1") || slices.Contains(environment, "GIT_CONFIG_GLOBAL=/dev/null") {
		t.Fatalf("authenticated Git environment disables configured credential helpers: %#v", environment)
	}
	if !slices.Contains(environment, "GIT_TERMINAL_PROMPT=0") || !slices.Contains(environment, "GIT_PROTOCOL_FROM_USER=0") {
		t.Fatalf("authenticated Git environment permits interactive or implicit protocol selection: %#v", environment)
	}
}

const (
	testCommit = "1111111111111111111111111111111111111111"
	testTree   = "2222222222222222222222222222222222222222"
	testBuild  = "3333333333333333333333333333333333333333"
)

func TestValidateCompletesBuiltInAndExplicitSourcesWithoutPersistentState(t *testing.T) {
	t.Parallel()

	files := firstPartyFiles(t)
	tests := []struct {
		name       string
		arguments  []string
		repository string
		selection  string
		kind       string
		transport  string
	}{
		{name: "built in default branch", arguments: []string{"ai4j", "validate", "--target", "claude"}, repository: "github.com/alx4j/ai4j", selection: "built_in_default", kind: "default_branch", transport: "https"},
		{name: "explicit branch", arguments: []string{"ai4j", "validate", "--repo", "https://github.com/example/toolkit.git", "--ref", "main", "--target", "claude"}, repository: "github.com/example/toolkit", selection: "explicit", kind: "branch", transport: "https"},
		{name: "explicit tag", arguments: []string{"ai4j", "validate", "--repo", "example/toolkit", "--ref", "refs/tags/v1", "--target", "claude"}, repository: "github.com/example/toolkit", selection: "explicit", kind: "tag", transport: "https"},
		{name: "explicit commit", arguments: []string{"ai4j", "validate", "--repo", "example/toolkit", "--ref", testCommit, "--target", "claude"}, repository: "github.com/example/toolkit", selection: "explicit", kind: "commit", transport: "https"},
		{name: "existing SSH authentication", arguments: []string{"ai4j", "validate", "--repo", "git@github.com:example/toolkit.git", "--ref", "main", "--target", "claude"}, repository: "github.com/example/toolkit", selection: "explicit", kind: "branch", transport: "ssh"},
		{name: "enterprise HTTPS tag", arguments: []string{"ai4j", "validate", "--repo", "https://git.everpure.example/platform/toolkits/ai4j.git", "--ref", "refs/tags/v1", "--target", "claude"}, repository: "git.everpure.example/platform/toolkits/ai4j", selection: "explicit", kind: "tag", transport: "https"},
		{name: "enterprise SSH tag", arguments: []string{"ai4j", "validate", "--repo", "git@gitlab.barclays.example:division/team/ai4j.git", "--ref", "refs/tags/v1", "--target", "claude"}, repository: "gitlab.barclays.example/division/team/ai4j", selection: "explicit", kind: "tag", transport: "ssh"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			temporary := t.TempDir()
			runner := &fixtureRunner{files: files}
			service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: temporary})
			if err != nil {
				t.Fatal(err)
			}
			request, err := cli.NewParser().Parse(test.arguments)
			if err != nil {
				t.Fatal(err)
			}

			report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())
			if report.Failure != FailureNone || len(report.Problems) != 0 || len(report.Content) != 6 {
				t.Fatalf("failure=%s problems=%d content=%d", report.Failure, len(report.Problems), len(report.Content))
			}
			if report.Source.Repository().String() != test.repository || report.Source.Selection().String() != test.selection ||
				report.Source.Transport().String() != test.transport ||
				report.Source.Commit().OID().String() != testCommit || report.Source.RootTree().String() != testTree ||
				string(report.Source.ResolvedRefKind()) != test.kind {
				t.Fatalf("source = repository=%s selection=%s commit=%s tree=%s", report.Source.Repository(), report.Source.Selection(), report.Source.Commit().OID(), report.Source.RootTree())
			}
			rulesDigest := sha256.Sum256(files["toolkit/rules/ai4j.md"])
			if !bytes.Equal(report.Rules, files["toolkit/rules/ai4j.md"]) || report.RulesChecksum != fmt.Sprintf("%x", rulesDigest) {
				t.Fatalf("validated rules bytes/checksum do not match the tracked rules file")
			}
			if runner.claudeValidations != 2 || runner.toolkitExecutions != 0 {
				t.Fatalf("claude validations=%d toolkit executions=%d", runner.claudeValidations, runner.toolkitExecutions)
			}
			if len(runner.claudeValidationDirectories) != 2 ||
				!strings.HasSuffix(runner.claudeValidationDirectories[0], filepath.Join("plugins", "ai4j-review")) ||
				!strings.HasSuffix(runner.claudeValidationDirectories[1], filepath.Join("plugins", "ai4j-tools")) {
				t.Fatalf("Claude validation directories = %v", runner.claudeValidationDirectories)
			}
			for _, item := range report.Content {
				if strings.HasSuffix(item.SourcePath(), ".go") || item.SourcePath() == "cmd/ai4j/main.go" {
					t.Fatalf("unrelated repository file entered disclosure: %s", item.SourcePath())
				}
			}
			entries, err := os.ReadDir(temporary)
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary root after validate = %v, %v", entries, err)
			}
		})
	}
}

func TestValidateRunsOnWindowsAMD64(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{GOOS: "windows", GOARCH: "amd64", Home: home, BuildCommit: testBuild, Runner: &fixtureRunner{files: firstPartyFiles(t)}, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.NewParser().Parse([]string{"ai4j.exe", "validate", "--target", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())
	if report.Failure != FailureNone || len(report.Problems) != 0 || len(report.Content) == 0 {
		t.Fatalf("Windows validation = failure:%s problems:%v content:%d", report.Failure, report.Problems, len(report.Content))
	}
}

func TestValidateCleansWorkspaceAfterNativeValidationFailure(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	runner := &fixtureRunner{files: firstPartyFiles(t), nativeExitCode: 1}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: temporary})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := cli.NewParser().Parse([]string{"ai4j", "validate", "--target", "claude"})
	report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())
	if report.Failure != FailureValidation || !report.HasSource() || len(report.Problems) != 1 || report.Problems[0].Code() != "native_validation_failed" {
		t.Fatalf("failure=%s problems=%v", report.Failure, report.Problems)
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary root after failure = %v, %v", entries, err)
	}
}

func TestValidateSnapshotsExplicitLocalDevelopmentSourceAndRequiresDirtyApproval(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	checkout, err := canonicalLocalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(checkout, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range firstPartyFiles(t) {
		destination := filepath.Join(checkout, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &fixtureRunner{files: firstPartyFiles(t), localRoot: checkout, localDirty: true}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	options, _ := cli.NewDevelopmentSourceOptions(checkout, false)
	rejected := service.Validate(context.Background(), options)
	if rejected.Failure != FailureSource || len(rejected.Problems) != 1 || rejected.Problems[0].Code() != "dirty_source_requires_approval" {
		t.Fatalf("dirty rejection = %#v", rejected)
	}
	options, _ = cli.NewDevelopmentSourceOptions(checkout, true)
	report := service.Validate(context.Background(), options)
	if report.Failure != FailureNone || !report.HasSource() || report.Source.Mode() != cli.SourceDevelopment || report.Source.Checkout() != checkout || !report.Source.Dirty() || !report.Source.SourceDigest().Valid() {
		t.Fatalf("local report = %#v", report)
	}
}

func TestValidateUpdateClassifiesFastForwardNoChangeAndRewrite(t *testing.T) {
	t.Parallel()
	files := firstPartyFiles(t)
	tests := []struct {
		name         string
		installed    string
		ancestorExit int
		want         gitsource.UpdateDisposition
		wantFailure  Failure
		wantChecks   int
	}{
		{name: "fast forward", installed: strings.Repeat("4", 40), want: gitsource.UpdateAvailable, wantChecks: 1},
		{name: "no change", installed: testCommit, want: gitsource.UpdateNoChange},
		{name: "rewritten", installed: strings.Repeat("4", 40), ancestorExit: 1, want: gitsource.UpdateRefRewritten, wantChecks: 1},
		{name: "ancestry error", installed: strings.Repeat("4", 40), ancestorExit: 2, want: gitsource.UpdateSourceError, wantFailure: FailureSource, wantChecks: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			temporary := t.TempDir()
			if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			runner := &fixtureRunner{files: files, ancestorExit: test.ancestorExit}
			service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: temporary})
			if err != nil {
				t.Fatal(err)
			}
			request, _ := cli.NewParser().Parse([]string{"ai4j", "validate", "--target", "claude"})
			installed, _ := domain.NewCommitOID(test.installed)
			update := service.ValidateUpdate(context.Background(), request.(cli.ValidateRequest).Source(), installed)
			if update.Report.Failure != test.wantFailure || update.Disposition != test.want || runner.ancestorChecks != test.wantChecks {
				t.Fatalf("update = failure:%s disposition:%s checks:%d", update.Report.Failure, update.Disposition, runner.ancestorChecks)
			}
			entries, err := os.ReadDir(temporary)
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary root after update check = %v, %v", entries, err)
			}
		})
	}
}

func TestValidateUpdateInspectsAncestryBeforePackageValidation(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := firstPartyFiles(t)
	files[toolkitManifestPath] = []byte("{")
	runner := &fixtureRunner{files: files, ancestorExit: 2}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := cli.NewParser().Parse([]string{"ai4j", "validate", "--target", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := domain.NewCommitOID(strings.Repeat("4", 40))
	if err != nil {
		t.Fatal(err)
	}
	update := service.ValidateUpdate(context.Background(), request.(cli.ValidateRequest).Source(), installed)
	if update.Report.Failure != FailureSource || len(update.Report.Problems) != 1 || update.Report.Problems[0].Code() != "source_history_unavailable" || runner.ancestorChecks != 1 || runner.claudeValidations != 0 {
		t.Fatalf("update = failure:%s problems:%v checks:%d native:%d", update.Report.Failure, update.Report.Problems, runner.ancestorChecks, runner.claudeValidations)
	}
}

func TestValidateRejectsLiteralSecretWithoutStartingNativeContent(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	files := firstPartyFiles(t)
	files["plugins/ai4j-tools/.mcp.json"] = []byte(`{"mcpServers":{"claude-tools":{"type":"stdio","command":"claude","args":["mcp","serve"],"env":{"AI4J_TOKEN":"secret-canary"}}}}`)
	runner := &fixtureRunner{files: files}
	service, err := NewService(Config{GOOS: "darwin", GOARCH: "arm64", Home: home, BuildCommit: testBuild, Runner: runner, TempRoot: temporary})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := cli.NewParser().Parse([]string{"ai4j", "validate", "--target", "claude"})
	report := service.Validate(context.Background(), request.(cli.ValidateRequest).Source())
	if report.Failure != FailureValidation || !report.HasSource() || len(report.Problems) != 1 ||
		report.Problems[0].Code() != "literal_secret" || strings.Contains(report.Problems[0].Message(), "secret-canary") {
		t.Fatalf("failure=%s problems=%v", report.Failure, report.Problems)
	}
	if runner.claudeValidations != 0 || runner.toolkitExecutions != 0 {
		t.Fatalf("native validation=%d toolkit executions=%d", runner.claudeValidations, runner.toolkitExecutions)
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary root after failure = %v, %v", entries, err)
	}
}

type fixtureRunner struct {
	files                       map[string][]byte
	nativeExitCode              int
	claudeValidations           int
	claudeValidationDirectories []string
	toolkitExecutions           int
	ancestorExit                int
	ancestorChecks              int
	localRoot                   string
	localDirty                  bool
}

func (r *fixtureRunner) LookPath(name string) (string, error) {
	if name == "git" || name == "claude" {
		return "/usr/bin/" + name, nil
	}
	return "", fmt.Errorf("not found")
}

func (r *fixtureRunner) Run(_ context.Context, directory, executable string, arguments, _ []string) (hostprocess.Result, error) {
	if !strings.HasSuffix(executable, "/claude") {
		return hostprocess.Result{}, fmt.Errorf("non-Claude process was not isolated: %s", executable)
	}
	return r.run(directory, executable, arguments)
}

func (r *fixtureRunner) RunIsolated(_ context.Context, directory, executable string, arguments, _ []string) (hostprocess.Result, error) {
	if !strings.HasSuffix(executable, "/git") {
		return hostprocess.Result{}, fmt.Errorf("non-Git process was isolated: %s", executable)
	}
	return r.run(directory, executable, arguments)
}

func (r *fixtureRunner) run(directory, executable string, arguments []string) (hostprocess.Result, error) {
	if strings.HasSuffix(executable, "/claude") {
		if slices.Equal(arguments, []string{"--version"}) {
			return hostprocess.Result{Stdout: []byte("2.1.211 (Claude Code)\n")}, nil
		}
		if slices.Equal(arguments, []string{"plugin", "validate", ".", "--strict"}) {
			r.claudeValidations++
			r.claudeValidationDirectories = append(r.claudeValidationDirectories, directory)
			return hostprocess.Result{ExitCode: r.nativeExitCode}, nil
		}
		r.toolkitExecutions++
		return hostprocess.Result{ExitCode: 1}, nil
	}
	if strings.HasSuffix(executable, "/git") && slices.Equal(arguments, []string{"--version"}) {
		return hostprocess.Result{Stdout: []byte("git version 2.39.5\n")}, nil
	}
	switch {
	case containsArgument(arguments, "init"):
		return hostprocess.Result{}, os.MkdirAll(filepath.Join(directory, ".git"), 0o700)
	case containsArgument(arguments, "ls-remote"):
		return hostprocess.Result{Stdout: []byte("ref: refs/heads/main\tHEAD\n" + testCommit + "\tHEAD\n" + testCommit + "\trefs/heads/main\n" + testCommit + "\trefs/tags/v1\n")}, nil
	case containsArgument(arguments, "fetch"):
		return hostprocess.Result{}, nil
	case containsArgument(arguments, "cat-file"):
		return hostprocess.Result{Stdout: []byte("commit\n")}, nil
	case containsArgument(arguments, "rev-parse"):
		if slices.Equal(arguments, []string{"rev-parse", "--show-toplevel"}) {
			return hostprocess.Result{Stdout: []byte(r.localRoot + "\n")}, nil
		}
		return hostprocess.Result{Stdout: []byte(testTree + "\n")}, nil
	case containsArgument(arguments, "ls-tree"):
		return hostprocess.Result{Stdout: r.treeOutput()}, nil
	case containsArgument(arguments, "read-tree"):
		return hostprocess.Result{}, nil
	case containsArgument(arguments, "check-attr"):
		return hostprocess.Result{Stdout: attributeOutput(arguments)}, nil
	case containsArgument(arguments, "checkout-index"):
		return hostprocess.Result{}, r.materialize(filepath.Dir(directory))
	case containsArgument(arguments, "checkout"):
		return hostprocess.Result{}, nil
	case containsArgument(arguments, "ls-files"):
		return hostprocess.Result{Stdout: r.indexOutput()}, nil
	case containsArgument(arguments, "status"):
		if r.localRoot != "" && directory == r.localRoot && r.localDirty {
			return hostprocess.Result{Stdout: []byte("?? local\x00")}, nil
		}
		return hostprocess.Result{}, nil
	case containsArgument(arguments, "merge-base"):
		r.ancestorChecks++
		return hostprocess.Result{ExitCode: r.ancestorExit}, nil
	default:
		return hostprocess.Result{}, fmt.Errorf("unexpected process: %s %v", executable, arguments)
	}
}

func (r *fixtureRunner) treeOutput() []byte {
	var output bytes.Buffer
	for _, path := range sortedKeys(r.files) {
		fmt.Fprintf(&output, "100644 blob %s %7d\t%s\x00", fixtureOID(path), len(r.files[path]), path)
	}
	return output.Bytes()
}

func (r *fixtureRunner) indexOutput() []byte {
	var output bytes.Buffer
	for _, path := range sortedKeys(r.files) {
		fmt.Fprintf(&output, "100644 %s 0\t%s\x00", fixtureOID(path), path)
	}
	return output.Bytes()
}

func (r *fixtureRunner) materialize(root string) error {
	for path, content := range r.files {
		destination := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(destination, content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func attributeOutput(arguments []string) []byte {
	lastSeparator := -1
	for index, argument := range arguments {
		if argument == "--" {
			lastSeparator = index
		}
	}
	paths := arguments[lastSeparator+1:]
	attributes := []string{"filter", "text", "eol", "crlf", "ident", "working-tree-encoding"}
	var output bytes.Buffer
	for _, path := range paths {
		for _, attribute := range attributes {
			output.WriteString(path + "\x00" + attribute + "\x00unspecified\x00")
		}
	}
	return output.Bytes()
}

func firstPartyFiles(t *testing.T) map[string][]byte {
	t.Helper()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"toolkit.json", ".claude-plugin/marketplace.json", "toolkit/rules/ai4j.md"}
	err = filepath.WalkDir(filepath.Join(repository, "plugins"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			relative, relativeErr := filepath.Rel(repository, path)
			if relativeErr != nil {
				return relativeErr
			}
			paths = append(paths, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"cmd/ai4j/main.go": []byte("package main\n")}
	for _, path := range paths {
		content, readErr := os.ReadFile(filepath.Join(repository, filepath.FromSlash(path)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		files[path] = content
	}
	return files
}

func fixtureOID(path string) string {
	digest := sha1.Sum([]byte(path))
	return hex.EncodeToString(digest[:])
}

func sortedKeys(values map[string][]byte) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func containsArgument(arguments []string, value string) bool {
	return slices.Contains(arguments, value)
}
