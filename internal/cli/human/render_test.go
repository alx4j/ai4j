package human_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/human"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	githubsource "github.com/alx4j/ai4j/internal/source/github"
)

func TestRenderMatchesIndependentGolden(t *testing.T) {
	t.Parallel()

	response := versionResponse(t)
	writer := &countingWriter{}
	exitCode, err := human.Render(writer, response)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if exitCode != response.Result().ExitCode() {
		t.Fatalf("Render() exit = %d, want %d", exitCode, response.Result().ExitCode())
	}
	if writer.writes != 1 {
		t.Fatalf("Write calls = %d, want 1", writer.writes)
	}
	want, err := os.ReadFile("testdata/version.golden")
	if err != nil {
		t.Fatalf("ReadFile(golden) error = %v", err)
	}
	if !bytes.Equal(writer.Bytes(), want) {
		t.Fatalf("Render() bytes differ\ngot:\n%s\nwant:\n%s", writer.Bytes(), want)
	}
}

func TestRenderRecoveryWithUnavailableRecordMatchesGolden(t *testing.T) {
	t.Parallel()

	response := recoveryResponse(t)
	writer := &countingWriter{}
	exitCode, err := human.Render(writer, response)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if exitCode != result.ExitRecoveryRequired {
		t.Fatalf("Render() exit = %d, want %d", exitCode, result.ExitRecoveryRequired)
	}
	want, err := os.ReadFile("testdata/recovery-unavailable.golden")
	if err != nil {
		t.Fatalf("ReadFile(golden) error = %v", err)
	}
	if !bytes.Equal(writer.Bytes(), want) {
		t.Fatalf("Render() bytes differ\ngot:\n%s\nwant:\n%s", writer.Bytes(), want)
	}
}

func TestRenderSupportsEveryTypedDataVariant(t *testing.T) {
	t.Parallel()

	for _, fixture := range responseFixtures(t) {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()

			writer := &countingWriter{}
			exitCode, err := human.Render(writer, fixture.response)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if exitCode != fixture.response.Result().ExitCode() {
				t.Fatalf("Render() exit = %d, want %d", exitCode, fixture.response.Result().ExitCode())
			}
			if writer.writes != 1 || writer.Len() == 0 {
				t.Fatalf("Render() made %d writes and emitted %d bytes", writer.writes, writer.Len())
			}
			if bytes.Contains(writer.Bytes(), []byte{0x1b}) {
				t.Fatalf("Render() contains ANSI escape: %q", writer.Bytes())
			}
			if bytes.HasPrefix(bytes.TrimSpace(writer.Bytes()), []byte("{")) {
				t.Fatalf("human output looks like JSON: %q", writer.Bytes())
			}
		})
	}
}

func TestRenderLifecycleUsageRequiresOneBundle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command cli.Command
		usage   string
	}{
		{cli.CommandInstall, "Usage: ai4j install [source options] --target claude --scope <SCOPE> --bundle <ID>\n"},
		{cli.CommandSync, "Usage: ai4j sync <INSTALLATION_ID> --bundle <ID> [options]\n"},
	}
	for _, test := range tests {
		data, err := cli.NewDetailedUsageData(cli.UsageMissingOptionValue, "bundle", test.command)
		if err != nil {
			t.Fatal(err)
		}
		response, err := cli.NewResponse("", failedResult(t, result.FailureUsage), nil, data)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if _, err := human.Render(&output, response); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), test.usage) || strings.Contains(output.String(), "--all | --asset") {
			t.Fatalf("Render(%s) = %q", test.command, output.String())
		}
	}
}

func TestRenderExplainsFlattenedBundleForListAndStatus(t *testing.T) {
	t.Parallel()

	source := testSource(t)
	recorded, err := cli.NewRecordedSource(source.Selection(), source.Repository(), source.RequestedRef(), source.HasRequestedRef(), source.ResolvedRefKind(), source.Commit().OID())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := domain.NewInstallationID("installation_001")
	summary, err := cli.NewInstallationSummary(
		id, "ai4j", cli.BuildTargetClaude, cli.ScopeUser, t.TempDir(), "active", recorded, "default",
		[]string{"tools", "default", "review"}, []string{"ai4j-tools", "ai4j-review"}, []string{"repository-review", "claude-tools"}, "healthy",
	)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := cli.NewInstallation(id, "ai4j", []string{"ai4j-tools", "ai4j-review"}, recorded, "1.0.0", "1.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	native, _ := cli.NewNativeState(cli.NativeRegistered, cli.NativeInstalled, cli.NativeEnabled, cli.NativeInactive, cli.NativeReloadNotRequired, cli.NativeNextSessionRequired, cli.NativePolicyAllowed, "", cli.NativeVersionNotApplicable)
	recovery, _ := cli.NewRecoveryState(cli.RecoveryStateNone, "")
	status, err := cli.NewDetailedStatusData(&installation, &summary, native, nil, recovery, result.UpdateNotChecked)
	if err != nil {
		t.Fatal(err)
	}
	list, err := cli.NewListData([]cli.InstallationSummary{summary})
	if err != nil {
		t.Fatal(err)
	}

	statusResponse, _ := cli.NewResponse(cli.CommandStatus, successResult(t, result.UpdateNotChecked), nil, status)
	listResponse, _ := cli.NewResponse(cli.CommandList, successResult(t, result.UpdateNotChecked), nil, list)
	for name, response := range map[string]cli.Response{"status": statusResponse, "list": listResponse} {
		var output bytes.Buffer
		if _, err := human.Render(&output, response); err != nil {
			t.Fatalf("Render(%s) error = %v", name, err)
		}
		for _, want := range []string{
			"Requested bundle: default",
			"Resolved bundles: default, review, tools",
			"Native packages: ai4j-review, ai4j-tools",
			"Resolved assets: claude-tools, repository-review",
		} {
			if !strings.Contains(output.String(), want) {
				t.Fatalf("Render(%s) = %q, want %q", name, output.String(), want)
			}
		}
		if name == "status" && !strings.Contains(output.String(), "Claude plugins: ai4j-review, ai4j-tools") {
			t.Fatalf("Render(status) = %q", output.String())
		}
	}
}

func TestRenderIsByteIdenticalForShuffledInputsAndRepeatedRuns(t *testing.T) {
	t.Parallel()

	var want []byte
	for run := 0; run < 40; run++ {
		response := validationResponse(t, run%2 != 0)
		var output bytes.Buffer
		exitCode, err := human.Render(&output, response)
		if err != nil {
			t.Fatalf("run %d Render() error = %v", run, err)
		}
		if exitCode != result.ExitValidation {
			t.Fatalf("run %d exit = %d, want %d", run, exitCode, result.ExitValidation)
		}
		if run == 0 {
			want = append([]byte(nil), output.Bytes()...)
			continue
		}
		if !bytes.Equal(output.Bytes(), want) {
			t.Fatalf("run %d output differs\ngot:  %q\nwant: %q", run, output.Bytes(), want)
		}
	}
}

func TestRenderRejectsWriterFailuresAfterOneWrite(t *testing.T) {
	t.Parallel()

	response := versionResponse(t)
	sentinel := errors.New("writer failed")
	cases := []struct {
		name    string
		writer  *controlledWriter
		wantErr error
	}{
		{name: "short write", writer: &controlledWriter{short: true}, wantErr: io.ErrShortWrite},
		{name: "writer error", writer: &controlledWriter{err: sentinel}, wantErr: sentinel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exitCode, err := human.Render(tc.writer, response)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Render() error = %v, want %v", err, tc.wantErr)
			}
			if exitCode != response.Result().ExitCode() {
				t.Fatalf("Render() exit = %d, want %d", exitCode, response.Result().ExitCode())
			}
			if tc.writer.writes != 1 {
				t.Fatalf("Write calls = %d, want 1", tc.writer.writes)
			}
		})
	}
}

func TestRenderValidationFailurePerformsNoWrite(t *testing.T) {
	t.Parallel()

	writer := &countingWriter{}
	exitCode, err := human.Render(writer, cli.Response{})
	if err == nil {
		t.Fatal("Render() error = nil, want invalid response rejected")
	}
	if exitCode != result.ExitUnexpectedInternal {
		t.Fatalf("Render() exit = %d, want %d", exitCode, result.ExitUnexpectedInternal)
	}
	if writer.writes != 0 || writer.Len() != 0 {
		t.Fatalf("invalid response performed %d writes and emitted %d bytes", writer.writes, writer.Len())
	}
}

func TestRenderBoundsMaterializedOutputBeforeWriting(t *testing.T) {
	t.Parallel()

	source := testSource(t)
	item, err := cli.NewContentItem(cli.ComponentSkill, "large", "plugins/ai4j-default/skills/large", strings.Repeat("a", 64), cli.ContentAdded, nil)
	if err != nil {
		t.Fatalf("NewContentItem() error = %v", err)
	}
	content := make([]cli.ContentItem, 12_000)
	for index := range content {
		content[index] = item
	}
	data, err := cli.NewValidateData(source, true, 0, 0, content)
	if err != nil {
		t.Fatalf("NewValidateData() error = %v", err)
	}
	commandResult := successResult(t, result.UpdateNotChecked)
	response, err := cli.NewResponse(cli.CommandValidate, commandResult, nil, data)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}

	writer := &countingWriter{}
	exitCode, err := human.Render(writer, response)
	if !errors.Is(err, human.ErrOutputTooLarge) {
		t.Fatalf("Render() error = %v, want ErrOutputTooLarge", err)
	}
	if exitCode != result.ExitSuccess {
		t.Fatalf("Render() exit = %d, want %d", exitCode, result.ExitSuccess)
	}
	if writer.writes != 0 || writer.Len() != 0 {
		t.Fatalf("oversized response performed %d writes and emitted %d bytes", writer.writes, writer.Len())
	}
}

type namedResponse struct {
	name     string
	response cli.Response
}

func responseFixtures(t *testing.T) []namedResponse {
	t.Helper()
	source := testSource(t)
	readResult := successResult(t, result.UpdateNotChecked)

	usageData, _ := cli.NewUsageData(cli.UsageMissingCommand, "")
	usageResponse, err := cli.NewResponse("", failedResult(t, result.FailureUsage), nil, usageData)
	if err != nil {
		t.Fatalf("NewResponse(usage) error = %v", err)
	}

	content, _ := cli.NewContentItem(cli.ComponentSkill, "review", "plugins/ai4j-default/skills/review", strings.Repeat("a", 64), cli.ContentAdded, nil)
	validateData, _ := cli.NewValidateData(source, true, 0, 0, []cli.ContentItem{content})
	validateResponse, err := cli.NewResponse(cli.CommandValidate, readResult, nil, validateData)
	if err != nil {
		t.Fatalf("NewResponse(validate) error = %v", err)
	}

	id, _ := domain.NewInstallationID("installation_001")
	absent, _ := cli.NewCondition(cli.ConditionAbsent, "")
	present, _ := cli.NewCondition(cli.ConditionPresent, "")
	action, _ := cli.NewAction(1, cli.ActionOwnerAI4J, cli.ActionCommitState, "installation", absent, present, cli.RecoveryNone)
	finalPresent, _ := cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	planData, _ := cli.NewPlanData(cli.OperationInstall, source, id, []cli.Action{action}, []cli.ContentItem{content}, nil, finalPresent, result.UpdateNotChecked)
	planResponse, err := cli.NewResponse(cli.CommandInstall, readResult, nil, planData)
	if err != nil {
		t.Fatalf("NewResponse(plan) error = %v", err)
	}

	committed := committedResult(t)
	mutationData, _ := cli.NewMutationData(cli.OperationInstall, committed, &id, []cli.Action{action}, finalPresent, result.UpdateNotChecked)
	mutationResponse, err := cli.NewResponse(cli.CommandInstall, committed, nil, mutationData)
	if err != nil {
		t.Fatalf("NewResponse(mutation) error = %v", err)
	}

	native, _ := cli.NewNativeState(cli.NativeNotRegistered, cli.NativeNotInstalled, cli.NativeDisabled, cli.NativeInactive, cli.NativeReloadNotRequired, cli.NativeNextSessionNotRequired, cli.NativePolicyAllowed, "", cli.NativeVersionNotApplicable)
	recovery, _ := cli.NewRecoveryState(cli.RecoveryStateNone, "")
	statusData, _ := cli.NewStatusData(nil, native, nil, recovery, result.UpdateNotInstalled)
	statusResult := successResult(t, result.UpdateNotInstalled)
	statusResponse, err := cli.NewResponse(cli.CommandStatus, statusResult, nil, statusData)
	if err != nil {
		t.Fatalf("NewResponse(status) error = %v", err)
	}

	unavailableResponse, err := cli.NewResponse(cli.CommandValidate, failedResult(t, result.FailureEnvironment), nil, cli.UnavailableData{})
	if err != nil {
		t.Fatalf("NewResponse(unavailable) error = %v", err)
	}

	return []namedResponse{
		{name: "usage", response: usageResponse},
		{name: "validate", response: validateResponse},
		{name: "plan", response: planResponse},
		{name: "mutation", response: mutationResponse},
		{name: "status", response: statusResponse},
		{name: "version", response: versionResponse(t)},
		{name: "recovery-unavailable", response: recoveryResponse(t)},
		{name: "unavailable", response: unavailableResponse},
	}
}

func recoveryResponse(t *testing.T) cli.Response {
	t.Helper()
	native, err := cli.NewNativeState(cli.NativeRegistrationUnknown, cli.NativeInstallationUnknown, cli.NativeEnablementUnknown, cli.NativeActivationUnknown, cli.NativeReloadUnknown, cli.NativeNextSessionUnknown, cli.NativePolicyUnknown, "", cli.NativeVersionNotApplicable)
	if err != nil {
		t.Fatalf("NewNativeState() error = %v", err)
	}
	recovery, err := cli.NewRecoveryState(cli.RecoveryUnsupportedSchema, "")
	if err != nil {
		t.Fatalf("NewRecoveryState() error = %v", err)
	}
	data, err := cli.NewStatusData(nil, native, nil, recovery, result.UpdateNotChecked)
	if err != nil {
		t.Fatalf("NewStatusData() error = %v", err)
	}
	commandResult := failedResult(t, result.FailureRecovery)
	response, err := cli.NewResponse(cli.CommandStatus, commandResult, nil, data)
	if err != nil {
		t.Fatalf("NewResponse(recovery) error = %v", err)
	}
	return response
}

func versionResponse(t *testing.T) cli.Response {
	t.Helper()
	repository, _ := domain.NewRepositoryIdentity("github.com/alx4j/ai4j")
	commit, _ := domain.NewBuildCommit(strings.Repeat("b", 40))
	defaultSource, _ := cli.NewDefaultSource(repository, "", cli.DefaultRepositoryBranch)
	data, err := cli.NewVersionData(
		"AI4J", "ai4j", "0.0.0-dev", repository, commit, "go1.26.6",
		time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC), "darwin", "arm64", defaultSource,
	)
	if err != nil {
		t.Fatalf("NewVersionData() error = %v", err)
	}
	response, err := cli.NewResponse(cli.CommandVersion, successResult(t, result.UpdateNotChecked), nil, data)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	return response
}

func validationResponse(t *testing.T, reverse bool) cli.Response {
	t.Helper()
	alpha, _ := cli.NewContentItem(cli.ComponentSkill, "alpha", "plugins/ai4j-default/skills/alpha", strings.Repeat("a", 64), cli.ContentAdded, nil)
	zulu, _ := cli.NewContentItem(cli.ComponentAgent, "zulu", "plugins/ai4j-default/agents/zulu.md", strings.Repeat("b", 64), cli.ContentChanged, nil)
	content := []cli.ContentItem{alpha, zulu}
	contextAlpha, _ := result.NewContext("resource", "alpha")
	contextZulu, _ := result.NewContext("resource", "zulu")
	problemAlpha, _ := result.NewProblem("a_failure", "validation failed", []result.Context{contextAlpha})
	problemZulu, _ := result.NewProblem("z_failure", "validation failed", []result.Context{contextZulu})
	warningAlpha, _ := result.NewWarning("a_warning", "review alpha", []result.Context{contextAlpha})
	warningZulu, _ := result.NewWarning("z_warning", "review zulu", []result.Context{contextZulu})
	problems := []result.Problem{problemAlpha, problemZulu}
	warnings := []result.Warning{warningAlpha, warningZulu}
	if reverse {
		content[0], content[1] = content[1], content[0]
		problems[0], problems[1] = problems[1], problems[0]
		warnings[0], warnings[1] = warnings[1], warnings[0]
	}
	data, err := cli.NewValidateData(testSource(t), false, 2, 2, content)
	if err != nil {
		t.Fatalf("NewValidateData() error = %v", err)
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureValidation, UpdateDisposition: result.UpdateNotChecked,
		Warnings: warnings, Errors: problems,
	})
	if err != nil {
		t.Fatalf("result.New() error = %v", err)
	}
	response, err := cli.NewResponse(cli.CommandValidate, commandResult, nil, data)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	return response
}

func testSource(t *testing.T) cli.Source {
	t.Helper()
	input, _ := githubsource.NewSelectionInput("", false, "", false)
	effective, _ := githubsource.Resolve(input)
	request, _ := gitsource.NewResolutionRequest(effective)
	commitOID := strings.Repeat("a", 40)
	advertisement, err := gitsource.ParseRemoteAdvertisement(request, []byte(
		"ref: refs/heads/main\tHEAD\n"+commitOID+"\tHEAD\n"+commitOID+"\trefs/heads/main\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := gitsource.ResolveReference(request, advertisement)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := gitsource.NewSelectedObjectProof(resolution, []byte("commit\n"))
	if err != nil {
		t.Fatal(err)
	}
	commit, err := gitsource.NewDirectProvenCommit(selected)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := gitsource.NewCommitTreeProof(commit, []byte(strings.Repeat("b", 40)+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := gitsource.NewSourceProvenance(proof)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := domain.NewRenderedDigest(strings.Repeat("c", 64))
	build, _ := domain.NewBuildCommit(strings.Repeat("d", 40))
	rendered, _ := gitsource.NewRenderedProvenance(provenance, digest, build)
	source, err := cli.NewSource(rendered)
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}
	return source
}

func successResult(t *testing.T, disposition result.UpdateDisposition) result.Result {
	t.Helper()
	commandResult, err := result.New(result.Facts{
		Status: result.StatusOK, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureNone, UpdateDisposition: disposition,
	})
	if err != nil {
		t.Fatalf("result.New(success) error = %v", err)
	}
	return commandResult
}

func committedResult(t *testing.T) result.Result {
	t.Helper()
	commandResult, err := result.New(result.Facts{
		Status: result.StatusOK, Phase: result.PhaseComplete, Outcome: result.OutcomeCommitted,
		Mutation: result.MutationStarted, DurableChange: result.DurableCommittedWithDiff,
		Failure: result.FailureNone, UpdateDisposition: result.UpdateNotChecked,
	})
	if err != nil {
		t.Fatalf("result.New(committed) error = %v", err)
	}
	return commandResult
}

func failedResult(t *testing.T, failure result.Failure) result.Result {
	t.Helper()
	problem, _ := result.NewProblem("operation_failed", "the operation failed", nil)
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: failure, UpdateDisposition: result.UpdateNotChecked, Errors: []result.Problem{problem},
	})
	if err != nil {
		t.Fatalf("result.New(%s) error = %v", failure, err)
	}
	return commandResult
}

type countingWriter struct {
	bytes.Buffer
	writes int
}

func (w *countingWriter) Write(value []byte) (int, error) {
	w.writes++
	return w.Buffer.Write(value)
}

type controlledWriter struct {
	writes int
	short  bool
	err    error
}

func (w *controlledWriter) Write(value []byte) (int, error) {
	w.writes++
	if w.err != nil {
		return 0, w.err
	}
	if w.short {
		return len(value) - 1, nil
	}
	return len(value), nil
}
