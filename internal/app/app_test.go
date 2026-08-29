package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/app"
	"github.com/alx4j/ai4j/internal/buildinfo"
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/human"
	"github.com/alx4j/ai4j/internal/cli/jsonout"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaBase = "https://github.com/alx4j/ai4j/schemas/v1/"

func TestApplicationVersionMatchesHumanAndJSONGoldens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		arguments  []string
		golden     string
		schemaName string
	}{
		{name: "human", arguments: []string{"ai4j", "version"}, golden: "version.human.golden"},
		{name: "json", arguments: []string{"ai4j", "version", "--json"}, golden: "version.json.golden", schemaName: "version.json"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			factoryCalls := 0
			application := newTestApplication(t, validBuild(), func() (app.CommandHandler, error) {
				factoryCalls++
				panic("version constructed non-version dependencies")
			}, human.Render, jsonout.Render)
			stdout := new(bytes.Buffer)
			stderr := new(bytes.Buffer)

			exitCode := application.Run(test.arguments, panicReader{}, stdout, stderr)

			if exitCode != result.ExitSuccess.Int() {
				t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			if factoryCalls != 0 {
				t.Fatalf("other-command factory calls = %d, want 0", factoryCalls)
			}
			want, err := os.ReadFile(filepath.Join("testdata", test.golden))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if !bytes.Equal(stdout.Bytes(), want) {
				t.Fatalf("output mismatch\n got: %s\nwant: %s", stdout.Bytes(), want)
			}
			if test.schemaName != "" {
				validateSchema(t, test.schemaName, stdout.Bytes())
			}
		})
	}
}

func TestApplicationVersionWorksWithoutOtherCommandFactory(t *testing.T) {
	t.Parallel()

	application := newTestApplication(t, validBuild(), nil, human.Render, jsonout.Render)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	if exitCode := application.Run([]string{"ai4j", "version", "--json"}, panicReader{}, stdout, stderr); exitCode != result.ExitSuccess.Int() {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	validateSchema(t, "version.json", stdout.Bytes())
}

func TestApplicationPropagatesContextToCommandHandler(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request-context")
	var observed string
	application := newTestApplication(t, validBuild(), func() (app.CommandHandler, error) {
		return func(handlerContext context.Context, _ cli.Request, _ app.CommandIO) (cli.Response, error) {
			observed, _ = handlerContext.Value(contextKey{}).(string)
			return cli.Response{}, errors.New("stop after observing context")
		}, nil
	}, human.Render, jsonout.Render)

	application.RunContext(ctx, []string{"ai4j", "status", "installation-001"}, nil, io.Discard, io.Discard)

	if observed != "request-context" {
		t.Fatalf("handler context value = %q", observed)
	}
}

func TestApplicationRendersHandlerCancellation(t *testing.T) {
	t.Parallel()

	application := newTestApplication(t, validBuild(), func() (app.CommandHandler, error) {
		return func(context.Context, cli.Request, app.CommandIO) (cli.Response, error) {
			return cli.Response{}, context.Canceled
		}, nil
	}, human.Render, jsonout.Render)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	exitCode := application.RunContext(context.Background(), []string{"ai4j", "status", "installation-001", "--json"}, nil, stdout, stderr)

	if exitCode != result.ExitCancelled.Int() {
		t.Fatalf("exit code = %d, want %d; output=%q", exitCode, result.ExitCancelled.Int(), stdout.String())
	}
	if stderr.Len() != 0 || !strings.Contains(stdout.String(), `"status":"cancelled"`) || !strings.Contains(stdout.String(), `"code":"command_cancelled"`) {
		t.Fatalf("cancelled output = %q, stderr = %q", stdout.String(), stderr.String())
	}
	validateSchema(t, "status.json", stdout.Bytes())
}

func TestApplicationCodexLifecycleFailsBeforeDependencyConstruction(t *testing.T) {
	t.Parallel()

	commands := [][]string{
		{"ai4j", "validate", "--target", "codex", "--json"},
		{"ai4j", "install", "--target", "codex", "--scope", "user", "--bundle", "default", "--yes", "--json"},
	}
	for _, arguments := range commands {
		factoryCalls := 0
		application := newTestApplication(t, validBuild(), func() (app.CommandHandler, error) {
			factoryCalls++
			panic("Codex capability gate constructed lifecycle dependencies")
		}, human.Render, jsonout.Render)
		stdout := new(bytes.Buffer)
		stderr := new(bytes.Buffer)

		exitCode := application.Run(arguments, panicReader{}, stdout, stderr)

		if exitCode != result.ExitEnvironment.Int() {
			t.Fatalf("%v exit code = %d, want %d; output=%q", arguments, exitCode, result.ExitEnvironment.Int(), stdout.String())
		}
		if factoryCalls != 0 || stderr.Len() != 0 {
			t.Fatalf("%v factory calls = %d, stderr = %q", arguments, factoryCalls, stderr.String())
		}
		if !strings.Contains(stdout.String(), "unsupported_capability") || !strings.Contains(stdout.String(), "interactive native plugin browser") {
			t.Fatalf("%v Codex response = %q", arguments, stdout.String())
		}
	}
}

func TestApplicationUsageBypassesDependenciesAndRedactsArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		arguments  []string
		json       bool
		forbidden  string
		schemaName string
	}{
		{name: "human missing command", arguments: []string{"ai4j"}},
		{name: "json unknown option", arguments: []string{"ai4j", "validate", "--credential=do-not-disclose", "--json"}, json: true, forbidden: "do-not-disclose", schemaName: "usage.json"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			factoryCalls := 0
			application := newTestApplication(t, validBuild(), func() (app.CommandHandler, error) {
				factoryCalls++
				panic("usage constructed non-version dependencies")
			}, human.Render, jsonout.Render)
			stdout := new(bytes.Buffer)
			stderr := new(bytes.Buffer)

			exitCode := application.Run(test.arguments, panicReader{}, stdout, stderr)

			if exitCode != result.ExitUsageOrApproval.Int() {
				t.Fatalf("exit code = %d, want 2", exitCode)
			}
			if factoryCalls != 0 {
				t.Fatalf("other-command factory calls = %d, want 0", factoryCalls)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			if test.forbidden != "" && strings.Contains(stdout.String(), test.forbidden) {
				t.Fatalf("stdout disclosed raw argument: %q", stdout.String())
			}
			if test.json {
				if !bytes.HasPrefix(stdout.Bytes(), []byte("{")) {
					t.Fatalf("JSON usage rendered as %q", stdout.String())
				}
				validateSchema(t, test.schemaName, stdout.Bytes())
			} else if !bytes.HasPrefix(stdout.Bytes(), []byte("No AI4J command was provided.\n")) {
				t.Fatalf("human usage rendered as %q", stdout.String())
			}
		})
	}
}

func TestApplicationEveryUsageIssueBypassesDependenciesAndStdin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		issue     cli.UsageIssue
		json      bool
		forbidden string
	}{
		{name: "missing executable", arguments: nil, issue: cli.UsageMissingExecutable},
		{name: "missing command", arguments: []string{"ai4j"}, issue: cli.UsageMissingCommand},
		{name: "unknown command", arguments: []string{"ai4j", "secret-command"}, issue: cli.UsageUnknownCommand, forbidden: "secret-command"},
		{name: "removed plan command", arguments: []string{"ai4j", "plan"}, issue: cli.UsageUnknownCommand},
		{name: "removed plan command arguments", arguments: []string{"ai4j", "plan", "secret-subcommand"}, issue: cli.UsageUnknownCommand, forbidden: "secret-subcommand"},
		{name: "unexpected argument", arguments: []string{"ai4j", "version", "secret-argument"}, issue: cli.UsageUnexpectedArgument, forbidden: "secret-argument"},
		{name: "unknown option", arguments: []string{"ai4j", "version", "--secret=do-not-disclose"}, issue: cli.UsageUnknownOption, forbidden: "do-not-disclose"},
		{name: "misplaced option JSON", arguments: []string{"ai4j", "--json"}, issue: cli.UsageMisplacedOption, json: true},
		{name: "inapplicable option", arguments: []string{"ai4j", "version", "--repo", "github.com/example/repo"}, issue: cli.UsageInapplicableOption},
		{name: "inapplicable version option", arguments: []string{"ai4j", "version", "--target", "claude"}, issue: cli.UsageInapplicableOption},
		{name: "duplicate option JSON", arguments: []string{"ai4j", "version", "--json", "--json"}, issue: cli.UsageDuplicateOption, json: true},
		{name: "missing option value", arguments: []string{"ai4j", "validate", "--repo"}, issue: cli.UsageMissingOptionValue},
		{name: "empty option value", arguments: []string{"ai4j", "validate", "--repo="}, issue: cli.UsageEmptyOptionValue},
		{name: "unexpected option value", arguments: []string{"ai4j", "version", "--json=true"}, issue: cli.UsageUnexpectedOptionValue},
		{name: "invalid option value", arguments: []string{"ai4j", "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--expected-commit", "not-a-commit"}, issue: cli.UsageInvalidOptionValue, forbidden: "not-a-commit"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			factoryCalls := 0
			application := newTestApplication(t, validBuild(), func() (app.CommandHandler, error) {
				factoryCalls++
				panic("usage constructed non-version dependencies")
			}, human.Render, jsonout.Render)
			stdout := new(bytes.Buffer)
			stderr := new(bytes.Buffer)

			exitCode := application.Run(test.arguments, panicReader{}, stdout, stderr)

			if exitCode != result.ExitUsageOrApproval.Int() {
				t.Fatalf("exit code = %d, want 2", exitCode)
			}
			if factoryCalls != 0 {
				t.Fatalf("other-command factory calls = %d, want 0", factoryCalls)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			if !strings.Contains(stdout.String(), string(test.issue)) {
				t.Fatalf("stdout %q does not disclose typed issue %q", stdout.String(), test.issue)
			}
			if test.forbidden != "" && strings.Contains(stdout.String(), test.forbidden) {
				t.Fatalf("stdout disclosed raw input %q: %q", test.forbidden, stdout.String())
			}
			if test.json {
				validateSchema(t, "usage.json", stdout.Bytes())
			}
		})
	}
}

func TestApplicationStatusUsageDescribesPositionalInstallation(t *testing.T) {
	t.Parallel()
	application := newTestApplication(t, validBuild(), func() (app.CommandHandler, error) {
		panic("usage constructed non-version dependencies")
	}, human.Render, jsonout.Render)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	exitCode := application.Run([]string{"ai4j", "status"}, panicReader{}, stdout, stderr)

	if exitCode != result.ExitUsageOrApproval.Int() || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Argument: <INSTALLATION_ID>\n") || !strings.Contains(stdout.String(), "Usage: ai4j status <INSTALLATION_ID>\n") {
		t.Fatalf("status usage does not explain the positional installation ID: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "--installation") {
		t.Fatalf("status usage still advertises the removed flag: %q", stdout.String())
	}
}

func TestApplicationPreservesKnownOptionInUsageResponse(t *testing.T) {
	t.Parallel()

	application := newTestApplication(t, validBuild(), func() (app.CommandHandler, error) {
		panic("usage constructed non-version dependencies")
	}, human.Render, jsonout.Render)
	stdout := new(bytes.Buffer)

	exitCode := application.Run([]string{"ai4j", "update", "installation-001", "--dry-run", "--dry-run", "--json"}, panicReader{}, stdout, new(bytes.Buffer))

	if exitCode != result.ExitUsageOrApproval.Int() {
		t.Fatalf("exit code = %d, want 2", exitCode)
	}
	if !strings.Contains(stdout.String(), `"field":"option","value":"dry-run"`) {
		t.Fatalf("usage response omitted the known option: %s", stdout.String())
	}
	validateSchema(t, "usage.json", stdout.Bytes())
}

func TestApplicationSelectsExactlyOneRenderer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		wantHuman int
		wantJSON  int
	}{
		{name: "version human", arguments: []string{"ai4j", "version"}, wantHuman: 1},
		{name: "version JSON", arguments: []string{"ai4j", "version", "--json"}, wantJSON: 1},
		{name: "usage human", arguments: []string{"ai4j"}, wantHuman: 1},
		{name: "usage JSON", arguments: []string{"ai4j", "unknown", "--json"}, wantJSON: 1},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			humanCalls := 0
			jsonCalls := 0
			humanSpy := func(_ io.Writer, response cli.Response) (result.ExitCode, error) {
				humanCalls++
				return response.Result().ExitCode(), nil
			}
			jsonSpy := func(_ io.Writer, response cli.Response) (result.ExitCode, error) {
				jsonCalls++
				return response.Result().ExitCode(), nil
			}
			application := newTestApplication(t, validBuild(), func() (app.CommandHandler, error) {
				panic("unexpected dependency construction")
			}, humanSpy, jsonSpy)

			application.Run(test.arguments, panicReader{}, io.Discard, io.Discard)

			if humanCalls != test.wantHuman || jsonCalls != test.wantJSON {
				t.Fatalf("renderer calls = human %d JSON %d, want human %d JSON %d", humanCalls, jsonCalls, test.wantHuman, test.wantJSON)
			}
		})
	}
}

func TestApplicationInvalidBuildFactsReturnBoundedUnavailableVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		build     buildinfo.Info
		forbidden string
	}{
		{name: "VCS unavailable", build: buildinfo.New(validBuildInputs(func(inputs *buildinfo.Inputs) { inputs.VCSAvailable = false }))},
		{name: "missing build time", build: buildinfo.New(validBuildInputs(func(inputs *buildinfo.Inputs) { inputs.BuildTime = time.Time{} }))},
		{name: "malformed commit", build: buildinfo.New(validBuildInputs(func(inputs *buildinfo.Inputs) { inputs.Revision = "do-not-disclose-revision" })), forbidden: "do-not-disclose-revision"},
		{name: "unsafe version", build: buildinfo.New(validBuildInputs(func(inputs *buildinfo.Inputs) { inputs.Version = "do-not-disclose\u009bversion" })), forbidden: "do-not-disclose"},
		{name: "wrong VCS", build: buildinfo.New(validBuildInputs(func(inputs *buildinfo.Inputs) { inputs.VCS = "hg" }))},
		{name: "copied module", build: buildinfo.New(validBuildInputs(func(inputs *buildinfo.Inputs) { inputs.ModulePath = "github.com/example/copied" }))},
		{name: "renamed command package", build: buildinfo.New(validBuildInputs(func(inputs *buildinfo.Inputs) { inputs.PackagePath = buildinfo.Module + "/cmd/copied" }))},
		{name: "out of range build year", build: buildinfo.New(validBuildInputs(func(inputs *buildinfo.Inputs) { inputs.BuildTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }))},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := newTestApplication(t, test.build, func() (app.CommandHandler, error) {
				panic("version constructed non-version dependencies")
			}, human.Render, jsonout.Render)
			stdout := new(bytes.Buffer)
			stderr := new(bytes.Buffer)

			exitCode := application.Run([]string{"ai4j", "version", "--json"}, panicReader{}, stdout, stderr)

			if exitCode != result.ExitUnexpectedInternal.Int() {
				t.Fatalf("exit code = %d, want 9", exitCode)
			}
			if test.forbidden != "" && strings.Contains(stdout.String(), test.forbidden) {
				t.Fatalf("response disclosed invalid build fact: %q", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			validateSchema(t, "version.json", stdout.Bytes())
			var document struct {
				Data   any `json:"data"`
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
				t.Fatal(err)
			}
			if document.Data != nil || len(document.Errors) != 1 || document.Errors[0].Code != "version_data_unavailable" {
				t.Fatalf("unavailable response = %#v", document)
			}
		})
	}
}

func TestApplicationMarksModifiedDevelopmentBuild(t *testing.T) {
	t.Parallel()

	dirtyBuild := buildinfo.New(validBuildInputs(func(inputs *buildinfo.Inputs) {
		inputs.Version = "(devel)"
		inputs.VCSModified = true
	}))
	application := newTestApplication(t, dirtyBuild, nil, human.Render, jsonout.Render)
	stdout := new(bytes.Buffer)
	if exitCode := application.Run([]string{"ai4j", "version", "--json"}, panicReader{}, stdout, io.Discard); exitCode != result.ExitSuccess.Int() {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	var document struct {
		Data struct {
			CLIVersion string `json:"cliVersion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Data.CLIVersion != buildinfo.DevelopmentVersion+"+dirty" {
		t.Fatalf("cliVersion = %q, want dirty development marker", document.Data.CLIVersion)
	}
	validateSchema(t, "version.json", stdout.Bytes())
}

func TestApplicationRendererFailureUsesFixedBoundedStderrAndExitNine(t *testing.T) {
	t.Parallel()

	failedRenderer := func(_ io.Writer, response cli.Response) (result.ExitCode, error) {
		return response.Result().ExitCode(), errors.New("do-not-disclose-renderer-secret")
	}
	application := newTestApplication(t, validBuild(), nil, failedRenderer, jsonout.Render)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	exitCode := application.Run([]string{"ai4j", "version"}, panicReader{}, stdout, stderr)

	if exitCode != result.ExitUnexpectedInternal.Int() {
		t.Fatalf("exit code = %d, want 9", exitCode)
	}
	if stderr.String() != "ai4j: output failed\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Fatalf("stderr disclosed renderer error: %q", stderr.String())
	}
}

func TestApplicationOutputWriterFailuresUseFixedBoundedStderr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
	}{
		{name: "human", arguments: []string{"ai4j", "version"}},
		{name: "JSON", arguments: []string{"ai4j", "version", "--json"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := newTestApplication(t, validBuild(), nil, human.Render, jsonout.Render)
			stderr := new(bytes.Buffer)

			exitCode := application.Run(test.arguments, panicReader{}, failingWriter{}, stderr)

			if exitCode != result.ExitUnexpectedInternal.Int() {
				t.Fatalf("exit code = %d, want 9", exitCode)
			}
			if stderr.String() != "ai4j: output failed\n" {
				t.Fatalf("stderr = %q", stderr.String())
			}
			if strings.Contains(stderr.String(), "writer-secret") {
				t.Fatalf("stderr disclosed writer error: %q", stderr.String())
			}
		})
	}
}

func TestApplicationRejectsRendererExitCodeDisagreement(t *testing.T) {
	t.Parallel()

	mismatchedRenderer := func(_ io.Writer, _ cli.Response) (result.ExitCode, error) {
		return result.ExitSource, nil
	}
	application := newTestApplication(t, validBuild(), nil, mismatchedRenderer, jsonout.Render)
	stderr := new(bytes.Buffer)
	if exitCode := application.Run([]string{"ai4j", "version"}, panicReader{}, io.Discard, stderr); exitCode != result.ExitUnexpectedInternal.Int() {
		t.Fatalf("exit code = %d, want 9", exitCode)
	}
	if stderr.String() != "ai4j: output failed\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func newTestApplication(t *testing.T, build buildinfo.Info, factory app.OtherCommandsFactory, humanRenderer, jsonRenderer app.Renderer) app.Application {
	t.Helper()
	repository, err := domain.NewRepositoryIdentity(buildinfo.RepositoryIdentity)
	if err != nil {
		t.Fatal(err)
	}
	defaultSource, err := cli.NewDefaultSource(repository, "", cli.DefaultRepositoryBranch)
	if err != nil {
		t.Fatal(err)
	}
	application, err := app.NewApplication(app.Dependencies{Build: build, DefaultSource: defaultSource, OtherCommands: factory, Human: humanRenderer, JSON: jsonRenderer})
	if err != nil {
		t.Fatal(err)
	}
	return application
}

func validBuild() buildinfo.Info { return buildinfo.New(validBuildInputs(nil)) }

func validBuildInputs(change func(*buildinfo.Inputs)) buildinfo.Inputs {
	inputs := buildinfo.Inputs{
		ModulePath:   buildinfo.Module,
		PackagePath:  "github.com/alx4j/ai4j/cmd/ai4j",
		Version:      "",
		GoVersion:    "go1.26.6",
		VCS:          "git",
		Revision:     strings.Repeat("b", 40),
		BuildTime:    time.Date(2026, 8, 18, 12, 0, 0, 987654321, time.FixedZone("fixture", 2*60*60)),
		TargetOS:     "darwin",
		TargetArch:   "arm64",
		VCSAvailable: true,
	}
	if change != nil {
		change(&inputs)
	}
	return inputs
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("stdin was read") }

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("writer-secret") }

func validateSchema(t *testing.T, name string, encoded []byte) {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	for _, resource := range []string{"common.json", name} {
		contents, err := os.ReadFile(filepath.Join("..", "..", "schemas", "v1", resource))
		if err != nil {
			t.Fatalf("read schema %s: %v", resource, err)
		}
		var document any
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.UseNumber()
		if err := decoder.Decode(&document); err != nil {
			t.Fatalf("decode schema %s: %v", resource, err)
		}
		if err := compiler.AddResource(schemaBase+resource, document); err != nil {
			t.Fatalf("add schema %s: %v", resource, err)
		}
	}
	schema, err := compiler.Compile(schemaBase + name)
	if err != nil {
		t.Fatalf("compile schema %s: %v", name, err)
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("schema validation: %v\n%s", err, encoded)
	}
}
