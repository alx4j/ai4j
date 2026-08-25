package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/jsonout"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	githubsource "github.com/alx4j/ai4j/internal/source/github"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func TestPlanInstallRunsValidationAndReturnsRunnableReadOnlyPlan(t *testing.T) {
	t.Parallel()

	source := testPlanSource(t)
	content, err := cli.NewContentItem(cli.ComponentSkill, "repository-review", "plugins/ai4j-default/skills/repository-review", strings.Repeat("e", 64), cli.ContentAdded, nil)
	if err != nil {
		t.Fatal(err)
	}
	warning, err := result.NewWarning("active_content_trust", "review active content before installation", nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &planValidationStub{report: validation.Report{
		Source: source, Content: []cli.ContentItem{content}, Rules: []byte("rules"), RulesChecksum: strings.Repeat("f", 64), Warnings: []result.Warning{warning},
	}}
	service.report.Content = append(service.report.Content, testPlanRules(t))
	request, err := cli.NewParser("darwin").Parse([]string{"ai4j", "plan", "install", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := newCommandHandler(service)(request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}

	if service.calls != 1 || response.Command() != cli.CommandPlanInstall || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("plan response = calls:%d command:%q exit:%d", service.calls, response.Command(), response.Result().ExitCode())
	}
	data, ok := response.Data().(cli.PlanData)
	if !ok {
		t.Fatalf("data = %T, want cli.PlanData", response.Data())
	}
	if got := data.Source().Commit().OID().String(); got != source.Commit().OID().String() {
		t.Fatalf("commit = %q, want %q", got, source.Commit().OID().String())
	}
	if got := data.InstallationID().String(); got != "install-aaaaaaaaaaaa" {
		t.Fatalf("installation ID = %q", got)
	}
	if len(data.Actions()) != 8 || len(data.ActiveContent()) != 2 || len(data.Conflicts()) != 0 {
		t.Fatalf("plan counts = actions:%d content:%d conflicts:%d", len(data.Actions()), len(data.ActiveContent()), len(data.Conflicts()))
	}
	final := data.ExpectedFinalState()
	if final.Installation() != cli.StatePresent || final.Native() != cli.StatePresent || final.OwnedState() != cli.StatePresent {
		t.Fatalf("unexpected final state: %#v", final)
	}
	if len(response.Result().Warnings()) != 1 || response.Result().DurableChange() != result.DurableChangeNone {
		t.Fatalf("read-only result = warnings:%d durable:%s", len(response.Result().Warnings()), response.Result().DurableChange())
	}
	var output bytes.Buffer
	if _, err := jsonout.Render(&output, response); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Command string `json:"command"`
		Changed bool   `json:"changed"`
		Data    struct {
			Actions   []any `json:"actions"`
			Conflicts []any `json:"conflicts"`
			Source    struct {
				Commit struct {
					OID string `json:"oid"`
				} `json:"commit"`
			} `json:"source"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Command != "plan.install" || document.Changed || len(document.Data.Actions) != 8 || len(document.Data.Conflicts) != 0 || document.Data.Source.Commit.OID != strings.Repeat("a", 40) {
		t.Fatalf("rendered plan = %#v", document)
	}
}

func TestPlanInstallValidationFailureDoesNotProduceActions(t *testing.T) {
	t.Parallel()

	problem, err := result.NewProblem("invalid_toolkit", "toolkit validation failed", nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &planValidationStub{report: validation.Report{
		Problems: []result.Problem{problem}, Failure: validation.FailureValidation,
	}}
	request, err := cli.NewParser("darwin").Parse([]string{"ai4j", "plan", "install"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := newCommandHandler(service)(request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	if response.Result().ExitCode() != result.ExitValidation {
		t.Fatalf("exit = %d, want %d", response.Result().ExitCode(), result.ExitValidation)
	}
	if _, ok := response.Data().(cli.UnavailableData); !ok {
		t.Fatalf("data = %T, want cli.UnavailableData", response.Data())
	}
}

func TestPlanInstallReturnsTypedConflictWithoutSuppressingDisclosure(t *testing.T) {
	t.Parallel()

	conflict, err := cli.NewConflict("plugin_identity_conflict", "ai4j-default@ai4j", "the Claude plugin identity already exists")
	if err != nil {
		t.Fatal(err)
	}
	service := &planValidationStub{
		report:    validation.Report{Source: testPlanSource(t), Content: []cli.ContentItem{testPlanRules(t)}, Rules: []byte("rules"), RulesChecksum: strings.Repeat("f", 64)},
		conflicts: []cli.Conflict{conflict},
	}
	request, err := cli.NewParser("darwin").Parse([]string{"ai4j", "plan", "install"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := newCommandHandler(service)(request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := response.Data().(cli.PlanData)
	if !ok || len(data.Actions()) != 8 || len(data.Conflicts()) != 1 {
		t.Fatalf("conflict plan = %T actions/conflicts unavailable", response.Data())
	}
	if response.Result().ExitCode() != result.ExitConflict || response.Result().DurableChange() != result.DurableChangeNone {
		t.Fatalf("conflict result = exit:%d durable:%s", response.Result().ExitCode(), response.Result().DurableChange())
	}
}

func testPlanRules(t *testing.T) cli.ContentItem {
	t.Helper()
	item, err := cli.NewContentItem(cli.ComponentSharedInstruction, "ai4j-rules", "toolkit/rules/ai4j.md", strings.Repeat("f", 64), cli.ContentAdded, nil)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

type planValidationStub struct {
	report    validation.Report
	update    validation.UpdateReport
	record    installstate.Record
	present   bool
	loadErr   error
	conflicts []cli.Conflict
	problem   *result.Problem
	calls     int
}

func (s *planValidationStub) Validate(context.Context, cli.SourceOptions) validation.Report {
	s.calls++
	return s.report
}

func (s *planValidationStub) InspectPlanInstall(context.Context) ([]cli.Conflict, *result.Problem) {
	return s.conflicts, s.problem
}

func (s *planValidationStub) ValidateUpdate(context.Context, cli.SourceOptions, domain.CommitOID) validation.UpdateReport {
	if s.update.Disposition.Valid() || s.update.Report.HasSource() || len(s.update.Report.Problems) != 0 {
		return s.update
	}
	return validation.UpdateReport{Report: s.report}
}

func (s *planValidationStub) InspectPlanExisting(context.Context, string, string) ([]cli.Conflict, *result.Problem) {
	return s.conflicts, s.problem
}

func (s *planValidationStub) InspectUninstall(context.Context, string, string) ([]cli.Conflict, *result.Problem) {
	return s.conflicts, s.problem
}

func (s *planValidationStub) LoadInstallation() (installstate.Record, bool, error) {
	return s.record, s.present, s.loadErr
}

func (s *planValidationStub) Install(context.Context, cli.InstallRequest, CommandIO) (cli.Response, error) {
	return cli.Response{}, errors.New("install is not configured in plan tests")
}

func (s *planValidationStub) Update(context.Context, cli.UpdateRequest, CommandIO) (cli.Response, error) {
	return cli.Response{}, errors.New("update is not configured in plan tests")
}

func (s *planValidationStub) Uninstall(context.Context, cli.UninstallRequest, CommandIO) (cli.Response, error) {
	return cli.Response{}, errors.New("uninstall is not configured in plan tests")
}

func (s *planValidationStub) Status(context.Context, cli.StatusRequest) (cli.Response, error) {
	return cli.Response{}, errors.New("status is not configured in plan tests")
}

func testPlanSource(t *testing.T) cli.Source {
	return testPlanSourceAt(t, strings.Repeat("a", 40))
}

func testPlanSourceAt(t *testing.T, commit string) cli.Source {
	return testPlanSourceFrom(t, cli.SourceOptions{}, commit)
}

func testPlanSourceFrom(t *testing.T, options cli.SourceOptions, commit string) cli.Source {
	t.Helper()
	input, err := githubsource.NewSelectionInput(options.Repository(), options.HasRepository(), options.Reference(), options.HasReference())
	if err != nil {
		t.Fatal(err)
	}
	effective, err := githubsource.Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	request, err := gitsource.NewResolutionRequest(effective)
	if err != nil {
		t.Fatal(err)
	}
	advertisement, err := gitsource.ParseRemoteAdvertisement(request, []byte("ref: refs/heads/main\tHEAD\n"+commit+"\tHEAD\n"+commit+"\trefs/heads/main\n"))
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
	provenCommit, err := gitsource.NewDirectProvenCommit(selected)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := gitsource.NewCommitTreeProof(provenCommit, []byte(strings.Repeat("b", 40)+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := gitsource.NewSourceProvenance(proof)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := domain.NewRenderedDigest(strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	build, err := domain.NewBuildCommit(strings.Repeat("d", 40))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := gitsource.NewRenderedProvenance(provenance, digest, build)
	if err != nil {
		t.Fatal(err)
	}
	source, err := cli.NewSource(rendered)
	if err != nil {
		t.Fatal(err)
	}
	return source
}
