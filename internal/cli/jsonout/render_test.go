package jsonout_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/jsonout"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	gitremote "github.com/alx4j/ai4j/internal/source/gitremote"
)

func TestRenderMatchesIndependentGolden(t *testing.T) {
	t.Parallel()

	response := versionResponse(t)
	writer := &countingWriter{}
	exitCode, err := jsonout.Render(writer, response)
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
		t.Fatalf("Render() bytes differ\ngot:  %q\nwant: %q", writer.Bytes(), want)
	}
}

func TestRenderWritesExactlyOneJSONDocumentAndEOF(t *testing.T) {
	t.Parallel()

	response := versionResponse(t)
	writer := &countingWriter{}
	exitCode, err := jsonout.Render(writer, response)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if writer.writes != 1 {
		t.Fatalf("Write calls = %d, want 1", writer.writes)
	}
	if !bytes.HasSuffix(writer.Bytes(), []byte("\n")) {
		t.Fatalf("Render() = %q, want one trailing newline", writer.Bytes())
	}
	if bytes.Contains(writer.Bytes(), []byte{0x1b}) {
		t.Fatalf("Render() contains ANSI escape: %q", writer.Bytes())
	}

	decoder := json.NewDecoder(bytes.NewReader(writer.Bytes()))
	decoder.UseNumber()
	var document map[string]any
	if decodeErr := decoder.Decode(&document); decodeErr != nil {
		t.Fatalf("Decode(first) error = %v", decodeErr)
	}
	var trailing any
	if decodeErr := decoder.Decode(&trailing); !errors.Is(decodeErr, io.EOF) {
		t.Fatalf("Decode(second) error = %v, want EOF", decodeErr)
	}
	if document["exitCode"] != json.Number(strconv.Itoa(exitCode.Int())) {
		t.Fatalf("document exitCode = %#v, want %d", document["exitCode"], exitCode)
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
		{
			name:    "short write",
			writer:  &controlledWriter{short: true},
			wantErr: io.ErrShortWrite,
		},
		{
			name:    "writer error",
			writer:  &controlledWriter{err: sentinel},
			wantErr: sentinel,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exitCode, err := jsonout.Render(tc.writer, response)
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
	exitCode, err := jsonout.Render(writer, cli.Response{})
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

func TestRenderRequiresWriterWithoutChangingTypedExit(t *testing.T) {
	t.Parallel()

	response := validationResponse(t, false)
	exitCode, err := jsonout.Render(nil, response)
	if err == nil {
		t.Fatal("Render() error = nil, want missing writer rejected")
	}
	if exitCode != result.ExitValidation {
		t.Fatalf("Render() exit = %d, want %d", exitCode, result.ExitValidation)
	}
}

func TestRenderIsByteIdenticalForShuffledInputsAndRepeatedRuns(t *testing.T) {
	t.Parallel()

	var want []byte
	for run := 0; run < 40; run++ {
		response := validationResponse(t, run%2 != 0)
		var output bytes.Buffer
		exitCode, err := jsonout.Render(&output, response)
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

func TestRenderExitCodeAgreesForEveryResultFamily(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		response cli.Response
		want     result.ExitCode
	}{
		{name: "success", response: versionResponse(t), want: result.ExitSuccess},
		{name: "cancelled", response: unavailableResponse(t, cancelledResult(t)), want: result.ExitCancelled},
		{name: "usage", response: unavailableResponse(t, failedResult(t, result.FailureUsage)), want: result.ExitUsageOrApproval},
		{name: "environment", response: unavailableResponse(t, failedResult(t, result.FailureEnvironment)), want: result.ExitEnvironment},
		{name: "source", response: unavailableResponse(t, failedResult(t, result.FailureSource)), want: result.ExitSource},
		{name: "validation", response: validationResponse(t, false), want: result.ExitValidation},
		{name: "conflict", response: unavailableResponse(t, failedResult(t, result.FailureConflict)), want: result.ExitConflict},
		{name: "compensated", response: compensatedResponse(t), want: result.ExitCompensated},
		{name: "recovery", response: unavailableResponse(t, failedResult(t, result.FailureRecovery)), want: result.ExitRecoveryRequired},
		{name: "internal", response: unavailableResponse(t, failedResult(t, result.FailureInternal)), want: result.ExitUnexpectedInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			got, err := jsonout.Render(&output, tc.response)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if got != tc.want || got != tc.response.Result().ExitCode() {
				t.Fatalf("Render() exit = %d, response exit = %d, want %d", got, tc.response.Result().ExitCode(), tc.want)
			}
			var document struct {
				ExitCode int `json:"exitCode"`
			}
			if decodeErr := json.Unmarshal(output.Bytes(), &document); decodeErr != nil {
				t.Fatalf("Unmarshal() error = %v", decodeErr)
			}
			if document.ExitCode != tc.want.Int() {
				t.Fatalf("document exitCode = %d, want %d", document.ExitCode, tc.want)
			}
		})
	}
}

func versionResponse(t *testing.T) cli.Response {
	t.Helper()
	repository, err := domain.NewRepositoryIdentity("github.com/alx4j/ai4j")
	if err != nil {
		t.Fatalf("NewRepositoryIdentity() error = %v", err)
	}
	commit, err := domain.NewBuildCommit(strings.Repeat("b", 40))
	if err != nil {
		t.Fatalf("NewBuildCommit() error = %v", err)
	}
	defaultSource, err := cli.NewDefaultSource(repository, "", cli.DefaultRepositoryBranch)
	if err != nil {
		t.Fatalf("NewDefaultSource() error = %v", err)
	}
	data, err := cli.NewVersionData(
		"AI4J",
		"ai4j",
		"0.0.0-dev",
		repository,
		commit,
		"go1.26.6",
		time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC),
		"darwin",
		"arm64",
		defaultSource,
	)
	if err != nil {
		t.Fatalf("NewVersionData() error = %v", err)
	}
	commandResult := successResult(t)
	response, err := cli.NewResponse(cli.CommandVersion, commandResult, nil, data)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	return response
}

func validationResponse(t *testing.T, reverse bool) cli.Response {
	t.Helper()
	source := testSource(t)
	alpha, err := cli.NewContentItem(cli.ComponentSkill, "alpha", "plugins/ai4j-default/skills/alpha", strings.Repeat("a", 64), cli.ContentAdded, nil)
	if err != nil {
		t.Fatalf("NewContentItem(alpha) error = %v", err)
	}
	zulu, err := cli.NewContentItem(cli.ComponentAgent, "zulu", "plugins/ai4j-default/agents/zulu.md", strings.Repeat("b", 64), cli.ContentChanged, nil)
	if err != nil {
		t.Fatalf("NewContentItem(zulu) error = %v", err)
	}
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

	data, err := cli.NewValidateData(source, false, 2, 2, content)
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
	input, _ := gitremote.NewSelectionInput("", false, "", false)
	effective, _ := gitremote.Resolve(input)
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

func successResult(t *testing.T) result.Result {
	t.Helper()
	commandResult, err := result.New(result.Facts{
		Status: result.StatusOK, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureNone, UpdateDisposition: result.UpdateNotChecked,
	})
	if err != nil {
		t.Fatalf("result.New() error = %v", err)
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

func cancelledResult(t *testing.T) result.Result {
	t.Helper()
	problem, _ := result.NewProblem("cancelled", "the operation was cancelled", nil)
	commandResult, err := result.New(result.Facts{
		Status: result.StatusCancelled, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureCancellation, UpdateDisposition: result.UpdateNotChecked,
		Errors: []result.Problem{problem},
	})
	if err != nil {
		t.Fatalf("result.New(cancelled) error = %v", err)
	}
	return commandResult
}

func unavailableResponse(t *testing.T, commandResult result.Result) cli.Response {
	t.Helper()
	response, err := cli.NewResponse(cli.CommandValidate, commandResult, nil, cli.UnavailableData{})
	if err != nil {
		t.Fatalf("NewResponse(unavailable) error = %v", err)
	}
	return response
}

func compensatedResponse(t *testing.T) cli.Response {
	t.Helper()
	problem, _ := result.NewProblem("operation_failed", "the operation failed", nil)
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseCompleteRolledBack, Outcome: result.OutcomeRolledBack,
		Mutation: result.MutationStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureValidation, UpdateDisposition: result.UpdateNotChecked,
		Errors: []result.Problem{problem},
	})
	if err != nil {
		t.Fatalf("result.New(compensated) error = %v", err)
	}
	final, _ := cli.NewFinalState(cli.StateAbsent, cli.StateAbsent, cli.StateAbsent)
	data, err := cli.NewMutationData(cli.OperationInstall, commandResult, nil, nil, final, result.UpdateNotChecked)
	if err != nil {
		t.Fatalf("NewMutationData() error = %v", err)
	}
	response, err := cli.NewResponse(cli.CommandInstall, commandResult, nil, data)
	if err != nil {
		t.Fatalf("NewResponse(compensated) error = %v", err)
	}
	return response
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
