package app

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	githubsource "github.com/alx4j/ai4j/internal/source/github"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func TestPlanUpdateShowsAvailableDesiredStateWithoutMutation(t *testing.T) {
	t.Parallel()
	report := testLifecycleReport(t)
	service := &planValidationStub{
		record: testInstallationRecord("branch", strings.Repeat("1", 40)), present: true,
		report: testLifecycleInstalledReport(t),
		update: validation.UpdateReport{Report: report, Disposition: gitsource.UpdateAvailable},
	}
	request, err := cli.NewParser("darwin").Parse([]string{"ai4j", "plan", "update", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := newCommandHandler(service)(request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := response.Data().(cli.PlanData)
	if !ok || data.Operation() != cli.OperationUpdate || len(data.Actions()) != 7 || len(data.Conflicts()) != 0 {
		t.Fatalf("update plan = %#v", response.Data())
	}
	if response.Result().Status() != result.StatusOK || response.Result().UpdateDisposition() != result.UpdateAvailable || response.Result().DurableChange() != result.DurableChangeNone {
		t.Fatalf("update result = %s/%s/%s", response.Result().Status(), response.Result().UpdateDisposition(), response.Result().DurableChange())
	}
	for _, item := range data.ActiveContent() {
		if item.Change() != cli.ContentChanged {
			t.Fatalf("content change = %s", item.Change())
		}
	}
}

func TestCodexLifecycleFailsBeforeMutationWithNativeHandoff(t *testing.T) {
	t.Parallel()
	request, err := cli.NewParser("darwin").Parse([]string{"ai4j", "install", "--target", "codex", "--scope", "user", "--all", "--yes", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := newCommandHandler(&planValidationStub{})(request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	problems := response.Result().Errors()
	if response.Result().ExitCode() != result.ExitEnvironment || response.Result().Mutation() != result.MutationNotStarted || len(problems) != 1 || problems[0].Code() != "unsupported_capability" {
		t.Fatalf("Codex response = %#v", response)
	}
	if !strings.Contains(problems[0].Message(), "interactive native plugin browser") || !strings.Contains(problems[0].Message(), "/plugins") {
		t.Fatalf("Codex message = %q", problems[0].Message())
	}
}

func TestPlanUpdateReportsPinnedWithoutActions(t *testing.T) {
	t.Parallel()
	report := testLifecycleReport(t)
	service := &planValidationStub{record: testInstallationRecord("tag", strings.Repeat("a", 40)), present: true, report: report}
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "plan", "update"})
	response, err := newCommandHandler(service)(request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.PlanData)
	if len(data.Actions()) != 0 || response.Result().Status() != result.StatusNoChange || response.Result().UpdateDisposition() != result.UpdatePinned {
		t.Fatalf("pinned result = actions:%d status:%s disposition:%s", len(data.Actions()), response.Result().Status(), response.Result().UpdateDisposition())
	}
}

func TestPlanUpdateBlocksRewrittenReference(t *testing.T) {
	t.Parallel()
	report := testLifecycleReport(t)
	service := &planValidationStub{
		record: testInstallationRecord("branch", strings.Repeat("1", 40)), present: true,
		update: validation.UpdateReport{Report: report, Disposition: gitsource.UpdateRefRewritten},
	}
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "plan", "update"})
	response, err := newCommandHandler(service)(request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.PlanData)
	if response.Result().ExitCode() != result.ExitConflict || len(data.Actions()) != 0 || len(data.Conflicts()) != 1 || data.Conflicts()[0].Code() != "ref_rewritten" {
		t.Fatalf("rewritten plan = exit:%d actions:%d conflicts:%v", response.Result().ExitCode(), len(data.Actions()), data.Conflicts())
	}
}

func TestPlanUpdateDisclosesActionsAlongsideOwnedStateConflicts(t *testing.T) {
	t.Parallel()
	conflict, err := cli.NewConflict("rules_drift", "Claude user rules/ai4j.md", "the AI4J rules file is modified")
	if err != nil {
		t.Fatal(err)
	}
	service := &planValidationStub{
		record:    testInstallationRecord("branch", strings.Repeat("1", 40)),
		present:   true,
		report:    testLifecycleInstalledReport(t),
		update:    validation.UpdateReport{Report: testLifecycleReport(t), Disposition: gitsource.UpdateAvailable},
		conflicts: []cli.Conflict{conflict},
	}
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "plan", "update"})
	response, err := newCommandHandler(service)(request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.PlanData)
	if response.Result().ExitCode() != result.ExitConflict || len(data.Actions()) != 7 || len(data.Conflicts()) != 1 {
		t.Fatalf("conflicting update plan = exit:%d actions:%d conflicts:%d", response.Result().ExitCode(), len(data.Actions()), len(data.Conflicts()))
	}
}

func TestPlanUninstallShowsOnlyOwnedRemovalActions(t *testing.T) {
	t.Parallel()
	service := &planValidationStub{record: testInstallationRecord("branch", strings.Repeat("a", 40)), present: true, report: testLifecycleReport(t)}
	request, _ := cli.NewParser("darwin").Parse([]string{"ai4j", "plan", "uninstall", "--json"})
	response, err := newCommandHandler(service)(request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.PlanData)
	if len(data.Actions()) != 7 || len(data.Conflicts()) != 0 || data.ExpectedFinalState().Installation() != cli.StateAbsent || data.ExpectedFinalState().Native() != cli.StateAbsent || data.ExpectedFinalState().OwnedState() != cli.StateAbsent {
		t.Fatalf("uninstall plan = actions:%d conflicts:%d final:%#v", len(data.Actions()), len(data.Conflicts()), data.ExpectedFinalState())
	}
	for _, item := range data.ActiveContent() {
		if item.Change() != cli.ContentRemoved {
			t.Fatalf("content change = %s", item.Change())
		}
	}
}

func TestStoredLifecyclePlanRequiresInstallationState(t *testing.T) {
	t.Parallel()
	for _, argv := range [][]string{{"ai4j", "plan", "update"}, {"ai4j", "plan", "uninstall"}} {
		request, _ := cli.NewParser("darwin").Parse(argv)
		response, err := newCommandHandler(&planValidationStub{})(request, CommandIO{})
		if err != nil {
			t.Fatal(err)
		}
		if response.Result().ExitCode() != result.ExitConflict {
			t.Fatalf("%v exit = %d", argv, response.Result().ExitCode())
		}
		if _, ok := response.Data().(cli.UnavailableData); !ok {
			t.Fatalf("%v data = %T", argv, response.Data())
		}
	}
}

func TestStoredBuiltInSourceSelectionRemainsOmitted(t *testing.T) {
	t.Parallel()
	record := testInstallationRecord("default_branch", strings.Repeat("a", 40))
	record.Source.Selection = "built_in_default"
	record.Source.RequestedRef = nil
	tracking, err := updateSourceOptions(record)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := exactSourceOptions(record)
	if err != nil {
		t.Fatal(err)
	}
	if tracking.HasRepository() || tracking.HasReference() || exact.HasRepository() || !exact.HasReference() || exact.Reference() != record.Source.Commit {
		t.Fatalf("tracking=%#v exact=%#v", tracking, exact)
	}
}

func TestStoredExplicitSourceReconstructsAcceptedHTTPSRemote(t *testing.T) {
	t.Parallel()
	record := testInstallationRecord("branch", strings.Repeat("a", 40))
	for _, buildOptions := range []func(installstate.Record) (cli.SourceOptions, error){updateSourceOptions, exactSourceOptions} {
		options, err := buildOptions(record)
		if err != nil {
			t.Fatal(err)
		}
		if options.Repository() != "https://github.com/alx4j/ai4j.git" || !options.HasRepository() {
			t.Fatalf("repository = %q/%t", options.Repository(), options.HasRepository())
		}
		input, err := githubsource.NewSelectionInput(options.Repository(), options.HasRepository(), options.Reference(), options.HasReference())
		if err != nil {
			t.Fatal(err)
		}
		effective, err := githubsource.Resolve(input)
		if err != nil || effective.Repository().String() != record.Source.Repository {
			t.Fatalf("effective repository = %q, err = %v", effective.Repository().String(), err)
		}
	}
}

func TestUpdateContentDiffReportsAddedRemovedChangedAndUnchanged(t *testing.T) {
	t.Parallel()
	item := func(identifier, checksum string) cli.ContentItem {
		value, err := cli.NewContentItem(cli.ComponentSkill, identifier, "plugins/ai4j-default/skills/"+identifier, checksum, cli.ContentAdded, nil)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	installed := []cli.ContentItem{
		item("changed", strings.Repeat("1", 64)),
		item("removed", strings.Repeat("2", 64)),
		item("unchanged", strings.Repeat("3", 64)),
	}
	desired := []cli.ContentItem{
		item("added", strings.Repeat("4", 64)),
		item("changed", strings.Repeat("5", 64)),
		item("unchanged", strings.Repeat("3", 64)),
	}
	content, err := diffActiveContent(installed, desired)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(map[string]cli.ContentChange, len(content))
	for _, value := range content {
		changes[value.Identifier()] = value.Change()
	}
	want := map[string]cli.ContentChange{
		"added": cli.ContentAdded, "changed": cli.ContentChanged,
		"removed": cli.ContentRemoved, "unchanged": cli.ContentUnchanged,
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
}

func testLifecycleReport(t *testing.T) validation.Report {
	t.Helper()
	skill, err := cli.NewContentItem(cli.ComponentSkill, "repository-review", "plugins/ai4j-default/skills/repository-review", strings.Repeat("e", 64), cli.ContentAdded, nil)
	if err != nil {
		t.Fatal(err)
	}
	return validation.Report{Source: testPlanSource(t), Content: []cli.ContentItem{skill, testPlanRules(t)}, Rules: []byte("rules"), RulesChecksum: strings.Repeat("f", 64)}
}

func testLifecycleInstalledReport(t *testing.T) validation.Report {
	t.Helper()
	skill, err := cli.NewContentItem(cli.ComponentSkill, "repository-review", "plugins/ai4j-default/skills/repository-review", strings.Repeat("2", 64), cli.ContentAdded, nil)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := cli.NewContentItem(cli.ComponentSharedInstruction, "ai4j-rules", "toolkit/rules/ai4j.md", strings.Repeat("1", 64), cli.ContentAdded, nil)
	if err != nil {
		t.Fatal(err)
	}
	return validation.Report{Source: testPlanSource(t), Content: []cli.ContentItem{skill, rules}, Rules: []byte("old rules"), RulesChecksum: strings.Repeat("1", 64)}
}

func testInstallationRecord(refKind, commit string) installstate.Record {
	requested := "main"
	scopeRoot, _ := filepath.Abs(".")
	return installstate.Record{
		SchemaVersion: installstate.SchemaVersion, InstallationID: "installation-001", ToolkitID: "ai4j", PluginID: "ai4j-default",
		Source: installstate.Source{Mode: "github", Selection: "explicit", Repository: "github.com/alx4j/ai4j", RequestedRef: &requested, RefKind: refKind, Commit: commit, RenderedDigest: strings.Repeat("e", 64)},
		Target: "claude", Host: "darwin-arm64", Scope: "user", ScopeRoot: scopeRoot, Lifecycle: "active",
		Selection:       installstate.Selection{All: true, Assets: []string{}, Bundles: []string{}, Resolved: []string{"ai4j-rules"}},
		NativeResources: []string{"claude:ai4j-default@ai4j", "claude:marketplace:ai4j"}, Health: "healthy", AI4JVersion: "0.0.0-dev",
		Catalog:       installstate.OwnedFile{Path: "state/catalog/.claude-plugin/marketplace.json", Checksum: strings.Repeat("b", 64)},
		Rules:         installstate.OwnedFile{Path: ".claude/rules/ai4j.md", Checksum: strings.Repeat("f", 64)},
		LastOperation: installstate.LastOperation{ID: "operation-001", Timestamp: "2026-08-24T12:00:00Z"},
	}
}
