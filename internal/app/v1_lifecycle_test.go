package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/buildinfo"
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func TestV1ClaudeUserLifecycleRetainsRollbackHistoryAndTombstone(t *testing.T) {
	harness := newV1Harness(t)
	install := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || !response.Result().Changed() {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	records, err := harness.store.LoadAll()
	if err != nil || len(records) != 1 || records[0].Lifecycle != "active" || len(records[0].History) != 1 {
		t.Fatalf("installed records = %#v, %v", records, err)
	}
	installationID := records[0].InstallationID

	harness.validator.update = true
	update := parseV1[cli.UpdateRequest](t, "update", "--installation", installationID, "--yes")
	response, err = harness.service.Update(context.Background(), update, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("update = %#v, %v", response.Result(), err)
	}
	sync := parseV1[cli.SyncRequest](t, "sync", "--installation", installationID, "--asset", "minimal", "--yes")
	response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("sync = %#v, %v", response.Result(), err)
	}
	history := parseV1[cli.HistoryRequest](t, "history", "--installation", installationID)
	response, err = harness.service.History(context.Background(), history)
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || len(response.Data().(cli.HistoryData).Entries()) != 3 {
		t.Fatalf("history after sync = %#v, %v", response, err)
	}

	uninstall := parseV1[cli.UninstallRequest](t, "uninstall", "--installation", installationID, "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}
	record, present, err := harness.store.LoadByID(installationID)
	if err != nil || !present || record.Lifecycle != "archived" || len(record.History) != 4 {
		t.Fatalf("archived tombstone = %#v, %t, %v", record, present, err)
	}

	rollback := parseV1[cli.RollbackRequest](t, "rollback", "--installation", installationID, "--yes")
	response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("rollback uninstall = %#v, %v", response.Result(), err)
	}
	record, present, err = harness.store.LoadByID(installationID)
	if err != nil || !present || record.Lifecycle != "active" || record.Source.Commit != strings.Repeat("b", 40) {
		t.Fatalf("restored installation = %#v, %t, %v", record, present, err)
	}
}

func TestV1DryRunsReturnPlansWithoutLockingPromptingOrMutation(t *testing.T) {
	harness := newV1Harness(t)
	acquireCalls := 0
	harness.service.base.acquire = func(context.Context) (func() error, error) {
		acquireCalls++
		return func() error { return nil }, nil
	}

	assertDryRun := func(command cli.Command, response cli.Response, err error, prompt *bytes.Buffer, nativeCalls int) {
		t.Helper()
		if err != nil || response.Command() != command || response.Result().Mutation() != result.MutationNotStarted {
			t.Fatalf("%s dry run = %#v, %v", command, response, err)
		}
		if _, ok := response.Data().(cli.PlanData); !ok {
			t.Fatalf("%s dry-run data = %T, want cli.PlanData", command, response.Data())
		}
		if acquireCalls != 0 || prompt.Len() != 0 || len(harness.native.commands) != nativeCalls {
			t.Fatalf("%s dry run locked, prompted, or invoked native commands", command)
		}
	}

	prompt := new(bytes.Buffer)
	commandIO := CommandIO{Input: strings.NewReader("yes\n"), Output: prompt, Interactive: true}
	installDryRun := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--dry-run")
	response, err := harness.service.Install(context.Background(), installDryRun, commandIO)
	assertDryRun(cli.CommandInstall, response, err, prompt, 0)
	if records, loadErr := harness.store.LoadAll(); loadErr != nil || len(records) != 0 {
		t.Fatalf("install dry run changed state: %#v, %v", records, loadErr)
	}

	install := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	if _, err = harness.service.Install(context.Background(), install, CommandIO{}); err != nil {
		t.Fatal(err)
	}
	records, err := harness.store.LoadAll()
	if err != nil || len(records) != 1 {
		t.Fatalf("installed records = %#v, %v", records, err)
	}
	record := records[0]
	history, err := harness.store.LoadHistory(record.InstallationID)
	if err != nil || len(history) == 0 {
		t.Fatalf("installed history = %#v, %v", history, err)
	}
	acquireCalls = 0
	nativeCalls := len(harness.native.commands)
	harness.validator.update = true

	tests := []struct {
		command cli.Command
		run     func(CommandIO) (cli.Response, error)
	}{
		{cli.CommandUpdate, func(io CommandIO) (cli.Response, error) {
			request := parseV1[cli.UpdateRequest](t, "update", "--installation", record.InstallationID, "--dry-run")
			return harness.service.Update(context.Background(), request, io)
		}},
		{cli.CommandSync, func(io CommandIO) (cli.Response, error) {
			request := parseV1[cli.SyncRequest](t, "sync", "--installation", record.InstallationID, "--asset", "minimal", "--dry-run")
			return harness.service.Sync(context.Background(), request, io)
		}},
		{cli.CommandRollback, func(io CommandIO) (cli.Response, error) {
			request := parseV1[cli.RollbackRequest](t, "rollback", "--installation", record.InstallationID, "--dry-run")
			return harness.service.Rollback(context.Background(), request, io)
		}},
		{cli.CommandUninstall, func(io CommandIO) (cli.Response, error) {
			request := parseV1[cli.UninstallRequest](t, "uninstall", "--installation", record.InstallationID, "--dry-run")
			return harness.service.Uninstall(context.Background(), request, io)
		}},
		{cli.CommandHistoryPurge, func(io CommandIO) (cli.Response, error) {
			request := parseV1[cli.HistoryPurgeRequest](t, "history", "purge", "--installation", record.InstallationID, "--operation", history[0].OperationID, "--dry-run")
			return harness.service.HistoryPurge(context.Background(), request, io)
		}},
	}
	for _, test := range tests {
		prompt.Reset()
		response, err = test.run(commandIO)
		assertDryRun(test.command, response, err, prompt, nativeCalls)
	}

	current, present, err := harness.store.LoadByID(record.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if !present || current.LastOperation != record.LastOperation || !slices.Equal(current.History, record.History) {
		t.Fatalf("dry runs changed installation state: before=%#v after=%#v", record, current)
	}
}

func TestV1WindowsClaudeUserJourneyUsesWindowsStateAndHost(t *testing.T) {
	harness := newV1Harness(t)
	dataRoot := filepath.Join(t.TempDir(), "AI4J")
	store, err := installstate.NewStoreAt(harness.service.base.home, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	harness.store = store
	harness.service.base.state = store
	harness.service.base.build = buildinfo.New(buildinfo.Inputs{Version: "v1.0.0", TargetOS: "windows", TargetArch: "amd64"})

	dryRun := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--dry-run")
	if response, err := harness.service.Install(context.Background(), dryRun, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install dry run = %#v, %v", response.Result(), err)
	}
	install := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	records, err := store.LoadAll()
	if err != nil || len(records) != 1 || records[0].Host != "windows-amd64" {
		t.Fatalf("Windows installation = %#v, %v", records, err)
	}
	record := records[0]
	if catalog := harness.service.catalogPath(record); !strings.HasPrefix(catalog, dataRoot+string(filepath.Separator)) {
		t.Fatalf("catalog path = %s, want under %s", catalog, dataRoot)
	}

	harness.validator.update = true
	update := parseV1[cli.UpdateRequest](t, "update", "--installation", record.InstallationID, "--yes")
	if response, err := harness.service.Update(context.Background(), update, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("update = %#v, %v", response.Result(), err)
	}
	sync := parseV1[cli.SyncRequest](t, "sync", "--installation", record.InstallationID, "--asset", "minimal", "--yes")
	if response, err := harness.service.Sync(context.Background(), sync, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("sync = %#v, %v", response.Result(), err)
	}
	status := statusService{validation: harness.validator, state: store, home: harness.service.base.home}
	statusRequest := parseV1[cli.StatusRequest](t, "status", "--installation", record.InstallationID)
	if response, err := status.Status(context.Background(), statusRequest); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("status = %#v, %v", response.Result(), err)
	}
	listRequest := parseV1[cli.ListRequest](t, "list")
	if response, err := status.List(context.Background(), listRequest); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("list = %#v, %v", response.Result(), err)
	}
	history := parseV1[cli.HistoryRequest](t, "history", "--installation", record.InstallationID)
	if response, err := harness.service.History(context.Background(), history); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("history = %#v, %v", response.Result(), err)
	}
	rollback := parseV1[cli.RollbackRequest](t, "rollback", "--installation", record.InstallationID, "--yes")
	if response, err := harness.service.Rollback(context.Background(), rollback, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("rollback = %#v, %v", response.Result(), err)
	}
	uninstall := parseV1[cli.UninstallRequest](t, "uninstall", "--installation", record.InstallationID, "--yes")
	if response, err := harness.service.Uninstall(context.Background(), uninstall, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}
}

func TestV1ClaudeProjectLocalJourneyKeepsRulesGitLocallyExcluded(t *testing.T) {
	harness := newV1Harness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(project, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte("# existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	install := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-local", "--project", project, "--bundle", "default", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-local install = %#v, %v", response.Result(), err)
	}
	if len(harness.validator.inspectionDirectories) == 0 {
		t.Fatal("project-local native state was not inspected")
	}
	for _, directory := range harness.validator.inspectionDirectories {
		if directory != project {
			t.Fatalf("native inspection directory = %q, want %q", directory, project)
		}
	}
	records, err := harness.store.LoadAll()
	if err != nil || len(records) != 1 || records[0].Scope != "project-local" || records[0].ScopeRoot != project {
		t.Fatalf("project-local record = %#v, %v", records, err)
	}
	record := records[0]
	rulesPath := filepath.Join(project, filepath.FromSlash(record.Rules.Path))
	if contents, readErr := os.ReadFile(rulesPath); readErr != nil || string(contents) != "rules-default\n" {
		t.Fatalf("project-local rules = %q, %v", contents, readErr)
	}
	contents, err := os.ReadFile(exclude)
	if err != nil || !strings.Contains(string(contents), "/"+filepath.ToSlash(record.Rules.Path)+"\n") || !strings.Contains(string(contents), "# existing\n") {
		t.Fatalf("Git-local exclusion = %q, %v", contents, err)
	}
	for index, command := range harness.native.commands {
		if command[0] == "plugin" && (harness.native.directories[index] != project || !slices.Contains(command, "local")) {
			t.Fatalf("scoped command %q directory=%q", command, harness.native.directories[index])
		}
	}
	uninstall := parseV1[cli.UninstallRequest](t, "uninstall", "--installation", record.InstallationID, "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-local uninstall = %#v, %v", response.Result(), err)
	}
	if _, err := os.Stat(rulesPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project-local rules remain: %v", err)
	}
	contents, err = os.ReadFile(exclude)
	if err != nil || string(contents) != "# existing\n" {
		t.Fatalf("Git-local exclusion after uninstall = %q, %v", contents, err)
	}
}

func TestV1ClaudeProjectSharedJourneyPreservesUnrelatedSettings(t *testing.T) {
	harness := newV1Harness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\n  \"enabledPlugins\": {\"unrelated@other\": true},\n  \"extraKnownMarketplaces\": {\n    \"other\": {\"source\": {\"source\": \"github\", \"repo\": \"example/other\"}}\n  }\n}\n")
	settingsPath := filepath.Join(project, ".claude", "settings.json")
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	install := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-shared", "--project", project, "--bundle", "default", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared install = %#v, %v", response.Result(), err)
	}
	records, err := harness.store.LoadAll()
	if err != nil || len(records) != 1 || records[0].Scope != "project-shared" || records[0].DeclarationID != "ai4j" || records[0].Catalog.Path != ".claude/settings.json" {
		t.Fatalf("project-shared record = %#v, %v", records, err)
	}
	record := records[0]
	nativeCatalogPath := harness.service.projectSharedNativeCatalogPath(record)
	if _, err := os.Stat(nativeCatalogPath); err != nil {
		t.Fatalf("project-shared native registration catalog: %v", err)
	}
	report := harness.validator.SelectLifecycle(context.Background(), install.Source(), install.Selection().SelectAll(), install.Selection().Assets(), install.Selection().Bundles())
	cloneRoot := filepath.Join(t.TempDir(), "clone")
	cloneID := installationIDFor(report, cli.ScopeProjectShared, cloneRoot)
	cloneRecord := record
	cloneRecord.InstallationID = cloneID.String()
	cloneRecord.ScopeRoot = cloneRoot
	firstDeclaration, firstErr := projectMarketplaceEntry(record)
	cloneDeclaration, cloneErr := projectMarketplaceEntry(cloneRecord)
	if cloneID.String() == record.InstallationID || firstErr != nil || cloneErr != nil || !bytes.Equal(firstDeclaration, cloneDeclaration) {
		t.Fatalf("project-shared clone identity: first=%s clone=%s declarationEqual=%t errors=%v/%v", record.InstallationID, cloneID.String(), bytes.Equal(firstDeclaration, cloneDeclaration), firstErr, cloneErr)
	}
	settings, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Contains(settings, []byte(`"unrelated@other": true`)) || !bytes.Contains(settings, []byte(`"ai4j-default@ai4j": true`)) || !bytes.Contains(settings, []byte(`"source":"settings"`)) || !bytes.Contains(settings, []byte(`"source":"git-subdir"`)) || !bytes.Contains(settings, []byte(strings.Repeat("a", 40))) {
		t.Fatalf("project-shared settings = %s, %v", settings, err)
	}
	if bytes.Contains(settings, []byte(record.InstallationID)) || bytes.Contains(settings, []byte(project)) {
		t.Fatalf("tracked settings contain private identity: %s", settings)
	}
	for index, command := range harness.native.commands {
		if command[0] == "plugin" && (harness.native.directories[index] != project || !slices.Contains(command, "project")) {
			t.Fatalf("shared command %q directory=%q", command, harness.native.directories[index])
		}
	}

	harness.validator.update = true
	update := parseV1[cli.UpdateRequest](t, "update", "--installation", record.InstallationID, "--yes")
	if response, err = harness.service.Update(context.Background(), update, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared update = %#v, %v", response.Result(), err)
	}
	settings, _ = os.ReadFile(settingsPath)
	if !bytes.Contains(settings, []byte(strings.Repeat("b", 40))) || bytes.Contains(settings, []byte(strings.Repeat("a", 40))) {
		t.Fatalf("project-shared exact commit was not updated: %s", settings)
	}
	history, err := harness.store.LoadHistory(record.InstallationID)
	if err != nil || len(history) != 2 {
		t.Fatalf("project-shared history = %#v, %v", history, err)
	}
	for _, entry := range history {
		if bytes.Contains(entry.CatalogBefore, []byte("unrelated")) || bytes.Contains(entry.CatalogAfter, []byte("unrelated")) {
			t.Fatalf("history copied unrelated project settings: %#v", entry)
		}
	}
	status := statusService{validation: harness.validator, state: harness.store, home: harness.service.base.home}
	statusRequest := parseV1[cli.StatusRequest](t, "status", "--installation", record.InstallationID)
	harness.native.marketplaces[record.MarketplaceID] = false
	if response, err = status.Status(context.Background(), statusRequest); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared status = %#v, %v", response.Result(), err)
	}
	native := response.Data().(cli.StatusData).NativeState()
	if native.Registration() != cli.NativeRegistered || native.Installation() != cli.NativeInstalled || native.Enablement() != cli.NativeEnabled || native.Activation() != cli.NativeActivationNotObservable {
		t.Fatalf("project-shared declaration/current-user status = %#v", native)
	}
	harness.native.marketplaces[record.MarketplaceID] = true
	rollback := parseV1[cli.RollbackRequest](t, "rollback", "--installation", record.InstallationID, "--yes")
	if response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared rollback = %#v, %v", response.Result(), err)
	}
	settings, _ = os.ReadFile(settingsPath)
	if !bytes.Contains(settings, []byte(strings.Repeat("a", 40))) || bytes.Contains(settings, []byte(strings.Repeat("b", 40))) {
		t.Fatalf("project-shared rollback did not restore exact commit: %s", settings)
	}
	uninstall := parseV1[cli.UninstallRequest](t, "uninstall", "--installation", record.InstallationID, "--yes")
	if response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared uninstall = %#v, %v", response.Result(), err)
	}
	settings, err = os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(settings, original) {
		t.Fatalf("project-shared structural inverse = %s, want %s, %v", settings, original, err)
	}
	rollback = parseV1[cli.RollbackRequest](t, "rollback", "--installation", record.InstallationID, "--yes")
	if response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared uninstall rollback = %#v, %v", response.Result(), err)
	}
	settings, _ = os.ReadFile(settingsPath)
	if !bytes.Contains(settings, []byte(strings.Repeat("a", 40))) {
		t.Fatalf("project-shared uninstall rollback did not restore declaration: %s", settings)
	}
	uninstall = parseV1[cli.UninstallRequest](t, "uninstall", "--installation", record.InstallationID, "--yes")
	if response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("second project-shared uninstall = %#v, %v", response.Result(), err)
	}
	settings, err = os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(settings, original) {
		t.Fatalf("second project-shared structural inverse = %s, want %s, %v", settings, original, err)
	}
	if _, err := os.Stat(nativeCatalogPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project-shared native registration catalog remains: %v", err)
	}
	commands := harness.native.commands
	if len(commands) < 2 || commands[len(commands)-2][1] != "uninstall" || !slices.Equal(commands[len(commands)-1][:3], []string{"plugin", "marketplace", "remove"}) {
		t.Fatalf("shared uninstall order = %#v", commands)
	}
}

func TestProductionClaudeRootAcceptsContainedOverrideAndRejectsUnsafeValues(t *testing.T) {
	home := canonicalTestDirectory(t, t.TempDir())
	custom := filepath.Join(home, "custom-claude")
	if err := os.Mkdir(custom, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	if root, err := productionClaudeRoot(home); err != nil || root != custom {
		t.Fatalf("contained override = %q, %v", root, err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if _, err := productionClaudeRoot(home); err == nil {
		t.Fatal("empty override was accepted")
	}
	outside := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", outside)
	if _, err := productionClaudeRoot(home); err == nil {
		t.Fatal("outside-home override was accepted")
	}
}

func canonicalTestDirectory(t *testing.T, directory string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestV1UserLifecycleUsesEffectiveClaudeConfigurationRoot(t *testing.T) {
	harness := newV1Harness(t)
	custom := filepath.Join(harness.service.base.home, "custom-claude")
	if err := os.Mkdir(custom, 0o700); err != nil {
		t.Fatal(err)
	}
	harness.service.base.claudeRoot = custom
	install := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("effective-root install = %#v, %v", response.Result(), err)
	}
	records, err := harness.store.LoadAll()
	if err != nil || len(records) != 1 || records[0].ScopeRoot != custom || !strings.HasPrefix(records[0].Rules.Path, "rules/") {
		t.Fatalf("effective-root record = %#v, %v", records, err)
	}
	if contents, err := os.ReadFile(filepath.Join(custom, filepath.FromSlash(records[0].Rules.Path))); err != nil || string(contents) != "rules-default\n" {
		t.Fatalf("effective-root rules = %q, %v", contents, err)
	}
}

func TestV1HistoryPurgeDoesNotTouchActiveTargetAndRemovesFinalArchivedTombstone(t *testing.T) {
	harness := newV1Harness(t)
	install := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--all", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	records, _ := harness.store.LoadAll()
	id := records[0].InstallationID
	uninstall := parseV1[cli.UninstallRequest](t, "uninstall", "--installation", id, "--yes")
	if response, err := harness.service.Uninstall(context.Background(), uninstall, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}
	commandCount := len(harness.native.commands)
	purge := parseV1[cli.HistoryPurgeRequest](t, "history", "purge", "--installation", id, "--all", "--yes")
	response, err := harness.service.HistoryPurge(context.Background(), purge, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || len(harness.native.commands) != commandCount {
		t.Fatalf("purge = %#v, commands=%d/%d, %v", response.Result(), len(harness.native.commands), commandCount, err)
	}
	if _, present, err := harness.store.LoadByID(id); err != nil || present {
		t.Fatalf("purged tombstone = present:%t err:%v", present, err)
	}
}

func TestV1IndependentInstallationsAndOwnedConflictPolicies(t *testing.T) {
	harness := newV1Harness(t)
	first := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--all", "--yes")
	if response, err := harness.service.Install(context.Background(), first, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("first install = %#v, %v", response.Result(), err)
	}
	second := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--asset", "other", "--yes")
	if response, err := harness.service.Install(context.Background(), second, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("second install = %#v, %v", response.Result(), err)
	}
	records, err := harness.store.LoadAll()
	if err != nil || len(records) != 2 || records[0].NativeResources[0] == records[1].NativeResources[0] {
		t.Fatalf("independent records = %#v, %v", records, err)
	}
	firstRecord := records[0]
	if firstRecord.ToolkitID != "ai4j" {
		firstRecord = records[1]
	}
	rulesPath := harness.service.rulesPath(firstRecord)
	if err := os.WriteFile(rulesPath, []byte("USER MODIFICATION\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	harness.validator.update = true
	fail := parseV1[cli.UpdateRequest](t, "update", "--installation", firstRecord.InstallationID, "--yes")
	response, err := harness.service.Update(context.Background(), fail, CommandIO{})
	if err != nil || response.Result().Failure() != result.FailureConflict {
		t.Fatalf("default conflict policy = %#v, %v", response.Result(), err)
	}
	keep := parseV1[cli.UpdateRequest](t, "update", "--installation", firstRecord.InstallationID, "--conflict-policy", "keep", "--yes")
	response, err = harness.service.Update(context.Background(), keep, CommandIO{})
	if err != nil || response.Result().Status() != result.StatusDegraded || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("keep policy = %#v, %v", response.Result(), err)
	}
	if contents, err := os.ReadFile(rulesPath); err != nil || string(contents) != "USER MODIFICATION\n" {
		t.Fatalf("kept rules = %q, %v", contents, err)
	}
	replace := parseV1[cli.SyncRequest](t, "sync", "--installation", firstRecord.InstallationID, "--all", "--conflict-policy", "replace-owned", "--yes")
	response, err = harness.service.Sync(context.Background(), replace, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("replace-owned policy = %#v, %v", response.Result(), err)
	}
	if contents, err := os.ReadFile(rulesPath); err != nil || string(contents) != "rules-default\n" {
		t.Fatalf("replaced rules = %q, %v", contents, err)
	}
}

func TestV1UpdateMigratesExplicitGitHubSourceWithoutChangingInstallationIdentity(t *testing.T) {
	harness := newV1Harness(t)
	install := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--all", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	records, _ := harness.store.LoadAll()
	id := records[0].InstallationID
	commit := strings.Repeat("a", 40)
	update := parseV1[cli.UpdateRequest](t, "update", "--installation", id, "--repo", "example/toolkit", "--ref", "main", "--expected-commit", commit, "--yes")
	response, err := harness.service.Update(context.Background(), update, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("source migration = %#v, %v", response.Result(), err)
	}
	record, present, err := harness.store.LoadByID(id)
	if err != nil || !present || record.InstallationID != id || record.Source.Repository != "github.com/example/toolkit" || record.Source.Selection != "explicit" || len(record.History) != 2 {
		t.Fatalf("migrated record = %#v, present=%t, err=%v", record, present, err)
	}
}

func TestV1LocalDevelopmentInstallUpdateAndSyncUseImmutableBackingBundle(t *testing.T) {
	harness := newV1Harness(t)
	checkout := filepath.Join(harness.service.base.home, "checkout")
	harness.validator.localDigest = strings.Repeat("e", 64)
	install := parseV1[cli.InstallRequest](t, "install", "--source", checkout, "--target", "claude", "--scope", "user", "--all", "--expected-source-digest", harness.validator.localDigest, "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("local install = %#v, %v", response.Result(), err)
	}
	records, _ := harness.store.LoadAll()
	record := records[0]
	if record.Source.Mode != "development_source" || record.Source.Checkout != checkout || record.Source.SourceDigest != harness.validator.localDigest || !strings.HasPrefix(record.Catalog.Path, "state/bundles/") {
		t.Fatalf("local state = %#v", record)
	}
	bundleRoot := filepath.Dir(filepath.Dir(harness.service.catalogPath(record)))
	for _, path := range []string{filepath.Join(bundleRoot, ".ai4j-bundle.json"), filepath.Join(bundleRoot, "plugin", ".claude-plugin", "plugin.json")} {
		if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
			t.Fatalf("bundle file %s = %#v, %v", path, info, statErr)
		}
	}
	firstBundle := record.Source.BundleDigest
	harness.validator.localDigest = strings.Repeat("f", 64)
	update := parseV1[cli.UpdateRequest](t, "update", "--installation", record.InstallationID, "--allow-dirty", "--expected-source-digest", harness.validator.localDigest, "--yes")
	response, err = harness.service.Update(context.Background(), update, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("local update = %#v, %v", response.Result(), err)
	}
	record, _, _ = harness.store.LoadByID(record.InstallationID)
	if record.Source.BundleDigest == firstBundle || record.Source.SourceDigest != harness.validator.localDigest || len(record.History) != 2 {
		t.Fatalf("updated local state = %#v", record)
	}
	sync := parseV1[cli.SyncRequest](t, "sync", "--installation", record.InstallationID, "--asset", "minimal", "--allow-dirty", "--expected-source-digest", harness.validator.localDigest, "--yes")
	response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("local sync = %#v, %v", response.Result(), err)
	}
}

func TestV1AutomaticallyReconcilesUnambiguousInterruptedOperations(t *testing.T) {
	for _, test := range []struct {
		name      string
		advance   func(*testing.T, v1Harness, v1Execution, *installstate.Record, installstate.HistoryEntry)
		wantState result.Status
	}{
		{name: "discard prepared operation", wantState: result.StatusOK},
		{name: "discard marker before journal", wantState: result.StatusOK, advance: func(t *testing.T, harness v1Harness, _ v1Execution, _ *installstate.Record, entry installstate.HistoryEntry) {
			t.Helper()
			if err := harness.store.DeleteHistory(entry.InstallationID, []string{entry.OperationID}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "roll forward verified target", wantState: result.StatusNoChange, advance: func(t *testing.T, harness v1Harness, execution v1Execution, desired *installstate.Record, _ installstate.HistoryEntry) {
			t.Helper()
			if err := harness.service.applyTransition(context.Background(), execution.before, desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifact, cli.ConflictFail, false); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "finish committed cleanup", wantState: result.StatusNoChange, advance: func(t *testing.T, harness v1Harness, execution v1Execution, desired *installstate.Record, entry installstate.HistoryEntry) {
			t.Helper()
			if err := harness.service.applyTransition(context.Background(), execution.before, desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifact, cli.ConflictFail, false); err != nil {
				t.Fatal(err)
			}
			if err := harness.store.Save(*desired); err != nil {
				t.Fatal(err)
			}
			if err := harness.store.CommitHistory(entry); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newV1Harness(t)
			install := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--all", "--yes")
			response, err := harness.service.Install(context.Background(), install, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("install = %#v, %v", response.Result(), err)
			}
			record, _, _ := harness.store.Load()
			harness.validator.update = true
			request := parseV1[cli.UpdateRequest](t, "update", "--installation", record.InstallationID, "--yes")
			execution, _, stop := harness.service.prepareUpdate(context.Background(), request.InstallationID(), request.Source(), cli.ConflictFail)
			if stop {
				t.Fatal("update preparation stopped")
			}
			desired, entry, marker := stageInterruptedV1(t, harness, execution, "operation-recovery0001")
			if test.advance != nil {
				test.advance(t, harness, execution, desired, entry)
			}

			response, err = harness.service.Update(context.Background(), request, CommandIO{})
			if err != nil || response.Result().Status() != test.wantState {
				t.Fatalf("recovered update = status:%s exit:%d err:%v", response.Result().Status(), response.Result().ExitCode(), err)
			}
			if _, present, err := harness.store.LoadMarker(); err != nil || present {
				t.Fatalf("marker after recovery = present:%t err:%v marker:%#v", present, err, marker)
			}
		})
	}
}

func TestV1AutomaticRecoveryKeepsAmbiguousStateFailClosed(t *testing.T) {
	harness := newV1Harness(t)
	install := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--all", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	harness.validator.update = true
	request := parseV1[cli.UpdateRequest](t, "update", "--installation", record.InstallationID, "--yes")
	execution, _, stop := harness.service.prepareUpdate(context.Background(), request.InstallationID(), request.Source(), cli.ConflictFail)
	if stop {
		t.Fatal("update preparation stopped")
	}
	_, _, _ = stageInterruptedV1(t, harness, execution, "operation-recovery0002")
	if err := os.WriteFile(harness.service.rulesPath(record), []byte("ambiguous user change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	response, err := harness.service.Update(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitRecoveryRequired {
		t.Fatalf("ambiguous recovery = %#v, %v", response.Result(), err)
	}
	if _, present, err := harness.store.LoadMarker(); err != nil || !present {
		t.Fatalf("ambiguous marker = present:%t err:%v", present, err)
	}
}

func TestV1AutomaticRecoveryRejectsMissingJournalAfterTargetMutation(t *testing.T) {
	harness := newV1Harness(t)
	install := parseV1[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--all", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	harness.validator.update = true
	request := parseV1[cli.UpdateRequest](t, "update", "--installation", record.InstallationID, "--yes")
	execution, _, stop := harness.service.prepareUpdate(context.Background(), request.InstallationID(), request.Source(), cli.ConflictFail)
	if stop {
		t.Fatal("update preparation stopped")
	}
	desired, entry, _ := stageInterruptedV1(t, harness, execution, "operation-recovery0003")
	if err := harness.service.applyTransition(context.Background(), execution.before, desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifact, cli.ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if err := harness.store.DeleteHistory(entry.InstallationID, []string{entry.OperationID}); err != nil {
		t.Fatal(err)
	}

	response, err := harness.service.Update(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitRecoveryRequired {
		t.Fatalf("missing journal recovery = %#v, %v", response.Result(), err)
	}
	if _, present, err := harness.store.LoadMarker(); err != nil || !present {
		t.Fatalf("missing journal marker = present:%t err:%v", present, err)
	}
}

func stageInterruptedV1(t *testing.T, harness v1Harness, execution v1Execution, operationID string) (*installstate.Record, installstate.HistoryEntry, installstate.Marker) {
	t.Helper()
	desired := cloneRecordPtr(execution.desired)
	desired.LastOperation = installstate.LastOperation{ID: operationID, Timestamp: "2026-08-25T12:00:00Z"}
	desired.History = appendUnique(desired.History, operationID)
	beforeCatalog, beforeRules, err := harness.service.captureOwned(execution.before)
	if err != nil {
		t.Fatal(err)
	}
	entry := installstate.HistoryEntry{
		SchemaVersion: installstate.HistorySchemaVersion, Operation: execution.operation.String(), OperationID: operationID,
		InstallationID: desired.InstallationID, Timestamp: desired.LastOperation.Timestamp, Restorable: true,
		Before: cloneRecordPtr(execution.before), After: cloneRecordPtr(desired), CatalogBefore: beforeCatalog, RulesBefore: beforeRules,
		CatalogAfter: slices.Clone(execution.catalog), RulesAfter: slices.Clone(execution.rules),
		NativeArtifactBefore: harness.service.currentArtifact(execution.before), NativeArtifactAfter: slices.Clone(execution.artifact),
	}
	resources := []string{"history:" + desired.InstallationID, "owned:state/installation.json"}
	resources = append(resources, execution.before.NativeResources...)
	if execution.before.Catalog.Path != "" {
		resources = append(resources, "owned:"+execution.before.Catalog.Path)
	}
	if execution.before.Rules.Path != "" {
		resources = append(resources, "owned:"+execution.before.Rules.Path)
	}
	resources = append(resources, desired.NativeResources...)
	marker, err := installstate.NewResourceMarker(execution.operation.String(), operationID, desired.InstallationID, cliSourceRevision(execution.source.Source), resources)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.store.SaveMarker(marker); err != nil {
		t.Fatal(err)
	}
	if err := harness.store.StageHistory(entry); err != nil {
		t.Fatal(err)
	}
	return desired, entry, marker
}

type v1Harness struct {
	service   *v1LifecycleService
	validator *v1Validator
	native    *v1NativeRunner
	store     installstate.Store
	home      string
}

func newV1Harness(t *testing.T) v1Harness {
	t.Helper()
	home := t.TempDir()
	store, err := installstate.NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	native := &v1NativeRunner{marketplaces: map[string]bool{}, plugins: map[string]bool{}, enabled: map[string]bool{}}
	validator := &v1Validator{native: native}
	validator.source = func(options cli.SourceOptions, commit string) cli.Source {
		if options.HasCheckout() {
			digest, _ := domain.NewRenderedDigest(validator.localDigest)
			build, _ := domain.NewBuildCommit(strings.Repeat("d", 40))
			source, sourceErr := cli.NewDevelopmentSource(filepath.Clean(options.Checkout()), digest, digest, build, options.AllowDirty())
			if sourceErr != nil {
				t.Fatal(sourceErr)
			}
			return source
		}
		return testPlanSourceFrom(t, options, commit)
	}
	random := make([]byte, 512)
	for index := range random {
		random[index] = byte(index)
	}
	base := &installer{state: store, runner: native, home: home, build: buildinfo.New(buildinfo.Inputs{Version: "v1.0.0"}), random: bytes.NewReader(random), acquire: func(context.Context) (func() error, error) { return func() error { return nil }, nil }}
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	base.now = func() time.Time {
		now = now.Add(time.Second)
		return now
	}
	return v1Harness{service: newV1LifecycleService(base, validator), validator: validator, native: native, store: store, home: home}
}

func parseV1[T cli.Request](t *testing.T, arguments ...string) T {
	t.Helper()
	request, err := cli.NewParser("darwin").Parse(append([]string{"ai4j"}, arguments...))
	if err != nil {
		t.Fatal(err)
	}
	value, ok := request.(T)
	if !ok {
		t.Fatalf("request = %T", request)
	}
	return value
}

type v1Validator struct {
	native                *v1NativeRunner
	update                bool
	source                func(cli.SourceOptions, string) cli.Source
	localDigest           string
	inspectionDirectories []string
}

func (v *v1Validator) SelectLifecycle(_ context.Context, options cli.SourceOptions, all bool, assets, bundles []string) validation.LifecycleSelection {
	commit := strings.Repeat("a", 40)
	if options.HasReference() && (options.Reference() == strings.Repeat("a", 40) || options.Reference() == strings.Repeat("b", 40)) {
		commit = options.Reference()
	} else if v.update {
		commit = strings.Repeat("b", 40)
	}
	rules := []byte("rules-default\n")
	resolved := []string{"ai4j-rules", "repository-review"}
	if slices.Contains(assets, "minimal") {
		rules = []byte("rules-minimal\n")
		resolved = []string{"minimal"}
	}
	toolkitID, packageID, packagePath := "ai4j", "ai4j-default", "plugins/ai4j-default"
	if slices.Contains(assets, "other") {
		toolkitID, packageID, packagePath = "other-toolkit", "other-plugin", "plugins/other-plugin"
		rules = []byte("other rules\n")
		resolved = []string{"other"}
	}
	digest := sha256Digest(rules)
	item, _ := cli.NewContentItem(cli.ComponentSharedInstruction, resolved[0], "toolkit/rules/ai4j.md", digest, cli.ContentAdded, nil)
	return validation.LifecycleSelection{Source: v.source(options, commit), ToolkitID: toolkitID, ToolkitVersion: "1.0.0", PackageID: packageID, PackagePath: packagePath, Content: []cli.ContentItem{item}, Rules: rules, RulesChecksum: digest, NativeArtifact: v1TestArtifact(), Resolved: resolved}
}

func v1TestArtifact() []byte {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, file := range []struct{ name, contents string }{
		{name: ".claude-plugin/plugin.json", contents: "{}\n"},
		{name: ".mcp.json", contents: "{\"mcpServers\":{\"claude-tools\":{\"type\":\"stdio\",\"command\":\"claude\",\"args\":[\"mcp\",\"serve\"],\"env\":{\"AI4J_TOKEN\":\"${AI4J_TOKEN}\"}}}}\n"},
	} {
		header := &zip.FileHeader{Name: file.name, Method: zip.Store}
		header.SetMode(0o644)
		writer, _ := archive.CreateHeader(header)
		_, _ = writer.Write([]byte(file.contents))
	}
	_ = archive.Close()
	return output.Bytes()
}

func (v *v1Validator) ValidateUpdate(_ context.Context, options cli.SourceOptions, installed domain.CommitOID) validation.UpdateReport {
	if !v.update || installed.String() == strings.Repeat("b", 40) {
		return validation.UpdateReport{Report: validation.Report{Source: v.source(options, installed.String())}, Disposition: gitsource.UpdateNoChange}
	}
	return validation.UpdateReport{Report: validation.Report{Source: v.source(options, strings.Repeat("b", 40))}, Disposition: gitsource.UpdateAvailable}
}

func (v *v1Validator) InspectNativeStatusFor(_ context.Context, marketplaceID, pluginID string) (validation.NativeStatus, *result.Problem) {
	return validation.NativeStatus{MarketplaceRegistered: v.native.marketplaces[marketplaceID], PluginInstalled: v.native.plugins[pluginID], PluginEnabled: v.native.enabled[pluginID]}, nil
}

func (v *v1Validator) InspectNativeStatusAt(ctx context.Context, directory, marketplaceID, pluginID string) (validation.NativeStatus, *result.Problem) {
	v.inspectionDirectories = append(v.inspectionDirectories, directory)
	return v.InspectNativeStatusFor(ctx, marketplaceID, pluginID)
}

func (v *v1Validator) InspectNativeStatus(context.Context) (validation.NativeStatus, *result.Problem) {
	return validation.NativeStatus{}, nil
}

type v1NativeRunner struct {
	commands     [][]string
	directories  []string
	marketplaces map[string]bool
	plugins      map[string]bool
	enabled      map[string]bool
	projectRoot  string
}

func (r *v1NativeRunner) LookPath(name string) (string, error) {
	if name == "git" {
		return "/usr/bin/git", nil
	}
	if name == "claude" {
		return "/usr/bin/claude", nil
	}
	return "", errors.New("not found")
}

func (r *v1NativeRunner) Run(_ context.Context, directory string, _ string, arguments, _ []string) (validation.ProcessResult, error) {
	r.commands = append(r.commands, slices.Clone(arguments))
	r.directories = append(r.directories, directory)
	switch {
	case slices.Equal(arguments, []string{"rev-parse", "--show-toplevel"}):
		return validation.ProcessResult{Stdout: []byte(r.projectRoot + "\n")}, nil
	case slices.Equal(arguments, []string{"rev-parse", "--git-path", "info/exclude"}):
		return validation.ProcessResult{Stdout: []byte(filepath.Join(r.projectRoot, ".git", "info", "exclude") + "\n")}, nil
	case len(arguments) == 4 && slices.Equal(arguments[:3], []string{"ls-files", "--error-unmatch", "--"}):
		return validation.ProcessResult{ExitCode: 1}, nil
	case len(arguments) >= 4 && slices.Equal(arguments[:3], []string{"plugin", "marketplace", "add"}):
		contents, err := os.ReadFile(filepath.Join(arguments[3], ".claude-plugin", "marketplace.json"))
		if err != nil {
			return validation.ProcessResult{}, err
		}
		var document struct {
			Name    string `json:"name"`
			Plugins []struct {
				Name string `json:"name"`
			} `json:"plugins"`
		}
		if json.Unmarshal(contents, &document) != nil {
			return validation.ProcessResult{}, errors.New("invalid catalog")
		}
		if slices.Contains(arguments, "project") {
			entry := []byte(`{"source":{"source":"directory","path":` + quotedJSON(arguments[3]) + `}}`)
			if err := fakeNativeMarketplace(directory, document.Name, entry, true); err != nil {
				return validation.ProcessResult{}, err
			}
		}
		r.marketplaces[document.Name] = true
	case len(arguments) >= 3 && arguments[0] == "plugin" && arguments[1] == "install":
		if slices.Contains(arguments, "project") {
			if err := fakeNativeEnabledPlugin(directory, arguments[2], true); err != nil {
				return validation.ProcessResult{}, err
			}
		}
		r.plugins[arguments[2]] = true
		r.enabled[arguments[2]] = true
	case len(arguments) >= 3 && arguments[0] == "plugin" && arguments[1] == "enable":
		r.enabled[arguments[2]] = true
	case len(arguments) >= 4 && slices.Equal(arguments[:3], []string{"plugin", "marketplace", "update"}):
	case len(arguments) >= 3 && arguments[0] == "plugin" && arguments[1] == "update":
	case len(arguments) >= 3 && arguments[0] == "plugin" && arguments[1] == "uninstall":
		if slices.Contains(arguments, "project") {
			if err := fakeNativeEnabledPlugin(directory, arguments[2], false); err != nil {
				return validation.ProcessResult{}, err
			}
		}
		delete(r.plugins, arguments[2])
		delete(r.enabled, arguments[2])
	case len(arguments) >= 4 && slices.Equal(arguments[:3], []string{"plugin", "marketplace", "remove"}):
		if slices.Contains(arguments, "project") {
			if err := fakeNativeMarketplace(directory, arguments[3], nil, false); err != nil {
				return validation.ProcessResult{}, err
			}
		}
		delete(r.marketplaces, arguments[3])
	default:
		return validation.ProcessResult{}, errors.New("unexpected Claude command")
	}
	return validation.ProcessResult{}, nil
}

func fakeNativeMarketplace(directory, marketplaceID string, entry []byte, present bool) error {
	path := filepath.Join(directory, ".claude", "settings.json")
	before, _, err := readProjectSettings(path)
	if err != nil {
		return err
	}
	var after []byte
	if present {
		after, err = projectSettingsWithMarketplace(before, marketplaceID, entry, false)
	} else {
		after, err = projectSettingsWithoutMarketplace(before, marketplaceID, false)
	}
	if err != nil {
		return err
	}
	return applyProjectSettings(path, before, after)
}

func fakeNativeEnabledPlugin(directory, pluginID string, present bool) error {
	path := filepath.Join(directory, ".claude", "settings.json")
	before, filePresent, err := readProjectSettings(path)
	if err != nil {
		return err
	}
	if !filePresent {
		before = []byte("{}\n")
	}
	root, err := parseJSONObject(before)
	if err != nil {
		return err
	}
	enabled, exists := findJSONMember(root, "enabledPlugins")
	if !exists {
		if !present {
			return nil
		}
		after := insertJSONMember(before, root, "enabledPlugins", []byte("{"+quotedJSON(pluginID)+": true}"))
		return applyProjectSettings(path, before, after)
	}
	nestedContents := before[enabled.valueStart:enabled.valueEnd]
	nested, err := parseJSONObject(nestedContents)
	if err != nil {
		return err
	}
	member, exists := findJSONMember(nested, pluginID)
	if present {
		if exists {
			return nil
		}
		updated := insertJSONMember(nestedContents, nested, pluginID, []byte("true"))
		return applyProjectSettings(path, before, slicesReplace(before, enabled.valueStart, enabled.valueEnd, updated))
	}
	if !exists {
		return nil
	}
	updated := removeJSONMember(nestedContents, nested, member)
	updatedObject, err := parseJSONObject(updated)
	if err != nil {
		return err
	}
	if len(updatedObject.members) == 0 {
		return applyProjectSettings(path, before, removeJSONMember(before, root, enabled))
	}
	return applyProjectSettings(path, before, slicesReplace(before, enabled.valueStart, enabled.valueEnd, updated))
}
