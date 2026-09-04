package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
)

func TestCompositionInstallStatusSyncUninstallAndRollback(t *testing.T) {
	harness := newLifecycleHarness(t)
	arguments := []string{"install", "--git-root", "git@github.com:oleksii", "--bundle", "everpure@v1.2.0", "--bundle", "common@v2.0.0", "--target", "claude", "--scope", "user"}
	dryRun := parseRequest[cli.InstallRequest](t, append(arguments, "--dry-run")...)
	response, err := harness.service.Install(context.Background(), dryRun, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("composition dry-run = %#v, %v", response.Result(), err)
	}
	if _, ok := response.Data().(cli.PlanData); !ok {
		t.Fatalf("composition dry-run data = %T", response.Data())
	}
	if records, loadErr := harness.store.LoadAll(); loadErr != nil || len(records) != 0 {
		t.Fatalf("dry-run state = %#v, %v", records, loadErr)
	}

	install := parseRequest[cli.InstallRequest](t, append(arguments, "--yes")...)
	response, err = harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || !response.Result().Changed() {
		t.Fatalf("composition install = %#v, %v", response.Result(), err)
	}
	record, present, err := harness.store.Load()
	if err != nil || !present || record.ToolkitID != "composition" || len(record.Components) != 2 || record.Source.Mode != "" {
		t.Fatalf("composition record = %#v, %t, %v", record, present, err)
	}
	if !slices.Equal(componentNames(record), []string{"common", "everpure"}) || !slices.Equal(nativePackageIDs(record.Packages), []string{"common-plugin", "everpure-plugin"}) {
		t.Fatalf("composition ownership = components:%v packages:%v", componentNames(record), nativePackageIDs(record.Packages))
	}
	catalogBytes, err := os.ReadFile(harness.service.catalogPath(record))
	if err != nil || !bytes.Contains(catalogBytes, []byte(`"url": "git@github.com:oleksii/common.git"`)) || !bytes.Contains(catalogBytes, []byte(`"url": "git@github.com:oleksii/everpure.git"`)) {
		t.Fatalf("composition catalog = %s, %v", catalogBytes, err)
	}
	for _, pkg := range record.Packages {
		if !harness.native.plugins[nativePluginID(pkg, record.MarketplaceID)] {
			t.Fatalf("package %s was not installed", pkg.ID)
		}
	}

	status := statusService{validation: harness.validator, state: harness.store}
	statusRequest := parseRequest[cli.StatusRequest](t, "status", record.InstallationID)
	response, err = status.Status(context.Background(), statusRequest)
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || response.Data().(cli.StatusData).UpdateDisposition() != result.UpdatePinned {
		t.Fatalf("composition status = %#v, %v", response.Result(), err)
	}

	sync := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "composition", "--yes")
	response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("composition sync = %#v, %v", response.Result(), err)
	}

	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("composition uninstall = %#v, %v", response.Result(), err)
	}
	archived, present, err := harness.store.LoadByID(record.InstallationID)
	if err != nil || !present || archived.Lifecycle != "archived" || len(archived.Components) != 2 {
		t.Fatalf("composition tombstone = %#v, %t, %v", archived, present, err)
	}
	for pluginID, installed := range harness.native.plugins {
		if installed && strings.HasSuffix(pluginID, "@"+record.MarketplaceID) {
			t.Fatalf("orphaned composed plugin %s", pluginID)
		}
	}

	rollback := parseRequest[cli.RollbackRequest](t, "rollback", record.InstallationID, "--yes")
	response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("composition rollback = %#v, %v", response.Result(), err)
	}
	restored, present, err := harness.store.LoadByID(record.InstallationID)
	if err != nil || !present || restored.Lifecycle != "active" || len(restored.Components) != 2 {
		t.Fatalf("restored composition = %#v, %t, %v", restored, present, err)
	}
}

func TestCompositionReusesCanonicalScopeRootAcrossEquivalentSpellings(t *testing.T) {
	home := t.TempDir()
	scopeRoot := filepath.Join(home, ".claude")
	alias := strings.ToUpper(scopeRoot)
	if alias == scopeRoot || !installstate.SameScopeRoot(alias, scopeRoot) {
		t.Skip("host filesystem has no equivalent case alias")
	}
	harness := newLifecycleHarnessAt(t, home, alias)
	request := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "common@v1", "--bundle", "everpure@v2", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("initial composition = %#v, %v", response.Result(), err)
	}
	before, present, err := harness.store.Load()
	if err != nil || !present || before.ScopeRoot != alias {
		t.Fatalf("initial record = %#v, present=%t, err=%v", before, present, err)
	}
	commandsBefore := len(harness.native.commands)

	harness.service.claudeRoot = scopeRoot
	response, err = harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || response.Result().Changed() {
		t.Fatalf("alias composition = %#v, %v", response.Result(), err)
	}
	after, present, err := harness.store.LoadByID(before.InstallationID)
	if err != nil || !present || !reflect.DeepEqual(after, before) || len(harness.native.commands) != commandsBefore {
		t.Fatalf("alias composition changed state: after=%#v commands=%d, present=%t, err=%v", after, len(harness.native.commands), present, err)
	}
}

func TestCompositionSupportsThreeComponentsDeterministically(t *testing.T) {
	harness := newLifecycleHarness(t)
	first := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "team@v3", "--bundle", "common@v1", "--bundle", "everpure@v2", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), first, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("three-component install = %#v, %v", response.Result(), err)
	}
	firstRecord, present, err := harness.store.Load()
	if err != nil || !present || !slices.Equal(componentNames(firstRecord), []string{"common", "everpure", "team"}) {
		t.Fatalf("three-component state = %#v, %t, %v", firstRecord, present, err)
	}
	commandsBefore := len(harness.native.commands)

	permuted := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "everpure@v2", "--bundle", "team@v3", "--bundle", "common@v1", "--target", "claude", "--scope", "user", "--yes")
	response, err = harness.service.Install(context.Background(), permuted, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || response.Result().Changed() {
		t.Fatalf("permuted composition install = %#v, %v", response.Result(), err)
	}
	secondRecord, present, err := harness.store.Load()
	if err != nil || !present || secondRecord.InstallationID != firstRecord.InstallationID || !reflect.DeepEqual(secondRecord, firstRecord) {
		t.Fatalf("permuted composition state = %#v, want %#v, present=%t, err=%v", secondRecord, firstRecord, present, err)
	}
	if len(harness.native.commands) != commandsBefore {
		t.Fatalf("permuted composition ran native commands: before=%d after=%d", commandsBefore, len(harness.native.commands))
	}
}

func TestCompositionCanAddAnOptionalTeamWithoutChangingInstallation(t *testing.T) {
	harness := newLifecycleHarness(t)
	initial := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "common@v1", "--bundle", "everpure@v2", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), initial, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("initial composition = %#v, %v", response.Result(), err)
	}
	before, _, _ := harness.store.Load()

	withTeam := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "team@v3", "--bundle", "everpure@v2", "--bundle", "common@v1", "--target", "claude", "--scope", "user", "--yes")
	response, err = harness.service.Install(context.Background(), withTeam, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || !response.Result().Changed() {
		t.Fatalf("expanded composition = %#v, %v", response.Result(), err)
	}
	after, present, err := harness.store.LoadByID(before.InstallationID)
	if err != nil || !present || after.InstallationID != before.InstallationID || !slices.Equal(componentNames(after), []string{"common", "everpure", "team"}) || !harness.native.plugins[nativePluginID(after.Packages[2], after.MarketplaceID)] {
		t.Fatalf("expanded composition state = %#v, present=%t, err=%v", after, present, err)
	}
}

func TestCompositionRejectsAnOccupiedPluginWhenAddingATeam(t *testing.T) {
	harness := newLifecycleHarness(t)
	initial := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "common@v1", "--bundle", "everpure@v2", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), initial, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("initial composition = %#v, %v", response.Result(), err)
	}
	before, _, _ := harness.store.Load()
	teamPlugin := "team-plugin@" + before.MarketplaceID
	harness.native.plugins[teamPlugin] = true
	harness.native.enabled[teamPlugin] = true
	commandsBefore := len(harness.native.commands)

	withTeam := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "common@v1", "--bundle", "everpure@v2", "--bundle", "team@v3", "--target", "claude", "--scope", "user", "--yes")
	response, err = harness.service.Install(context.Background(), withTeam, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitConflict || response.Result().Changed() {
		t.Fatalf("occupied team plugin = %#v, %v", response.Result(), err)
	}
	after, present, err := harness.store.LoadByID(before.InstallationID)
	if err != nil || !present || !reflect.DeepEqual(after, before) || len(harness.native.commands) != commandsBefore {
		t.Fatalf("occupied team plugin changed state: after=%#v commands=%d, %v", after, len(harness.native.commands), err)
	}
}

func TestCompositionProjectSharedDeclarationIsPortable(t *testing.T) {
	harness := newLifecycleHarness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\n  \"enabledPlugins\": {\"unrelated@other\": true}\n}\n")
	settingsPath := filepath.Join(project, ".claude", "settings.json")
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	request := parseRequest[cli.InstallRequest](t, "install", "--git-root", "git@git.company.example:platform/toolkits", "--bundle", "common@v1", "--bundle", "everpure@v2", "--target", "claude", "--scope", "project-shared", "--project", project, "--yes")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared composition install = %#v, %v", response.Result(), err)
	}
	record, present, err := harness.store.Load()
	if err != nil || !present || record.MarketplaceID != "composition" || record.DeclarationID != "composition" {
		t.Fatalf("project-shared composition record = %#v, %t, %v", record, present, err)
	}
	settings, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Contains(settings, []byte("git@git.company.example:platform/toolkits/common.git")) ||
		!bytes.Contains(settings, []byte("git@git.company.example:platform/toolkits/everpure.git")) ||
		!bytes.Contains(settings, []byte(strings.Repeat("a", 40))) || !bytes.Contains(settings, []byte("unrelated@other")) {
		t.Fatalf("project-shared composition settings = %s, %v", settings, err)
	}
	if bytes.Contains(settings, []byte(record.InstallationID)) || bytes.Contains(settings, []byte(project)) {
		t.Fatalf("project-shared composition leaked local identity: %s", settings)
	}
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared composition uninstall = %#v, %v", response.Result(), err)
	}
	settings, err = os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(settings, original) {
		t.Fatalf("project-shared composition inverse = %s, want %s, %v", settings, original, err)
	}
}

func TestCompositionSupportsARulesOnlyComponent(t *testing.T) {
	harness := newLifecycleHarness(t)
	request := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "everpure@v2", "--bundle", "policy@v1", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("rules-only composition install = %#v, %v", response.Result(), err)
	}
	record, present, err := harness.store.Load()
	if err != nil || !present || len(record.Components) != 2 || len(record.Components[1].Packages) != 0 || !slices.Equal(record.Components[1].Selection.ResolvedAssets, []string{"policy-rules"}) {
		t.Fatalf("rules-only composition state = %#v, %t, %v", record, present, err)
	}
	status := statusService{validation: harness.validator, state: harness.store}
	response, err = status.Status(context.Background(), parseRequest[cli.StatusRequest](t, "status", record.InstallationID))
	installation, installed := response.Data().(cli.StatusData).Installation()
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || !installed || len(installation.Components()) != 2 {
		t.Fatalf("rules-only composition status = %#v, %v", response.Result(), err)
	}
	response, err = status.List(context.Background(), parseRequest[cli.ListRequest](t, "list"))
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || len(response.Data().(cli.ListData).Installations()[0].Components()) != 2 {
		t.Fatalf("rules-only composition list = %#v, %v", response.Result(), err)
	}
}

func TestCompositionSourceFailureStopsBeforeMutation(t *testing.T) {
	harness := newLifecycleHarness(t)
	harness.validator.failBundle = "everpure"
	request := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "team@v3", "--bundle", "everpure@v2", "--bundle", "common@v1", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSource || response.Result().Changed() {
		t.Fatalf("component source failure = %#v, %v", response.Result(), err)
	}
	problems := response.Result().Errors()
	if len(problems) != 1 || len(problems[0].Context()) != 1 || problems[0].Context()[0].Field() != "component" || problems[0].Context()[0].Value() != "everpure" {
		t.Fatalf("component source failure context = %#v", problems)
	}
	if harness.validator.selectionCalls != 2 {
		t.Fatalf("component selections = %d, want 2", harness.validator.selectionCalls)
	}
	if records, loadErr := harness.store.LoadAll(); loadErr != nil || len(records) != 0 {
		t.Fatalf("component source failure state = %#v, %v", records, loadErr)
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
		t.Fatalf("component source failure marker = %t, %v", present, markerErr)
	}
	if len(harness.native.commands) != 0 || len(harness.native.marketplaces) != 0 || len(harness.native.plugins) != 0 {
		t.Fatalf("component source failure mutated target: commands=%v marketplaces=%v plugins=%v", harness.native.commands, harness.native.marketplaces, harness.native.plugins)
	}
}

func TestCompositionRecoveryUsesTheCompleteSourceRevision(t *testing.T) {
	harness := newLifecycleHarness(t)
	request := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "common@v1", "--bundle", "everpure@v2", "--target", "claude", "--scope", "user", "--yes")
	root, _ := request.GitRoot()
	project, hasProject := request.Project()
	execution, _, stop, err := harness.service.prepareCompositionInstall(context.Background(), root, request.BundleCoordinates(), request.Target(), request.Scope(), project, hasProject, cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("composition preparation stopped: %t, %v", stop, err)
	}
	desired, entry, marker := stageInterrupted(t, harness, execution, "operation-composition-recovery")
	if marker.Commit != recordSourceRevision(*desired) || marker.Commit == desired.Components[0].Source.Commit {
		t.Fatalf("composition marker revision = %q", marker.Commit)
	}
	if recovered, recoverErr := harness.service.reconcileInterrupted(context.Background()); recoverErr != nil || !recovered {
		t.Fatalf("composition recovery = %t, %v", recovered, recoverErr)
	}
	if _, present, loadErr := harness.store.LoadMarker(); loadErr != nil || present {
		t.Fatalf("composition marker remains = %t, %v", present, loadErr)
	}
	if _, present, loadErr := harness.store.LoadOperationHistory(entry.InstallationID, entry.OperationID); loadErr != nil || present {
		t.Fatalf("composition history remains = %t, %v", present, loadErr)
	}
	if records, loadErr := harness.store.LoadAll(); loadErr != nil || len(records) != 0 || len(harness.native.commands) != 0 {
		t.Fatalf("composition recovery mutated state: records=%#v commands=%#v err=%v", records, harness.native.commands, loadErr)
	}
}

func TestCompositionTagRewriteIsReportedAndUpdateIsRefused(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "common@v1", "--bundle", "everpure@v2", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("composition install = %#v, %v", response.Result(), err)
	}
	recordBefore, present, err := harness.store.Load()
	if err != nil || !present {
		t.Fatalf("composition state = %#v, %t, %v", recordBefore, present, err)
	}
	historyBefore, err := harness.store.LoadHistory(recordBefore.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	commandsBefore := len(harness.native.commands)
	harness.validator.update = true

	status := statusService{validation: harness.validator, state: harness.store}
	statusRequest := parseRequest[cli.StatusRequest](t, "status", recordBefore.InstallationID)
	response, err = status.Status(context.Background(), statusRequest)
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || response.Result().Status() != result.StatusDegraded || response.Result().UpdateDisposition() != result.UpdateRefRewritten || response.Data().(cli.StatusData).UpdateDisposition() != result.UpdateRefRewritten {
		t.Fatalf("rewritten composition status = %#v, %v", response.Result(), err)
	}
	warnings := response.Result().Warnings()
	if harness.validator.updateCalls != 2 || len(warnings) != 2 || warnings[0].Context()[0].Value() != "common" || warnings[1].Context()[0].Value() != "everpure" {
		t.Fatalf("rewritten composition checks = %d warnings=%#v", harness.validator.updateCalls, warnings)
	}

	selectionsBefore := harness.validator.selectionCalls
	updateChecksBefore := harness.validator.updateCalls
	update := parseRequest[cli.UpdateRequest](t, "update", recordBefore.InstallationID, "--yes")
	response, err = harness.service.Update(context.Background(), update, CommandIO{})
	errors := response.Result().Errors()
	if err != nil || response.Result().ExitCode() != result.ExitConflict || response.Result().Changed() || len(errors) != 1 || errors[0].Code() != "ref_rewritten" {
		t.Fatalf("rewritten composition update = %#v, %v", response.Result(), err)
	}
	if harness.validator.selectionCalls != selectionsBefore+1 || harness.validator.updateCalls != updateChecksBefore {
		t.Fatalf("rewritten update source checks = selections %d updates %d", harness.validator.selectionCalls-selectionsBefore, harness.validator.updateCalls-updateChecksBefore)
	}
	recordAfter, present, err := harness.store.LoadByID(recordBefore.InstallationID)
	if err != nil || !present || !reflect.DeepEqual(recordAfter, recordBefore) {
		t.Fatalf("state after refused update = %#v, want %#v, present=%t, err=%v", recordAfter, recordBefore, present, err)
	}
	historyAfter, err := harness.store.LoadHistory(recordBefore.InstallationID)
	if err != nil || !reflect.DeepEqual(historyAfter, historyBefore) {
		t.Fatalf("history after refused update = %#v, want %#v, err=%v", historyAfter, historyBefore, err)
	}
	if len(harness.native.commands) != commandsBefore {
		t.Fatalf("rewritten composition update ran native commands: before=%d after=%d", commandsBefore, len(harness.native.commands))
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
		t.Fatalf("rewritten composition update marker = %t, %v", present, markerErr)
	}
}

func TestCompositionStatusReportsRewritesAlongsideUnavailableComponents(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "common@v1", "--bundle", "everpure@v2", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("composition install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	harness.validator.update = true
	harness.validator.failUpdateBundle = "everpure"

	status := statusService{validation: harness.validator, state: harness.store}
	response, err = status.Status(context.Background(), parseRequest[cli.StatusRequest](t, "status", record.InstallationID))
	warnings := response.Result().Warnings()
	problems := response.Result().Errors()
	if err != nil || response.Result().ExitCode() != result.ExitSource || response.Result().UpdateDisposition() != result.UpdateUnknown || harness.validator.updateCalls != 2 ||
		len(warnings) != 1 || warnings[0].Context()[0].Value() != "common" || len(problems) != 1 || problems[0].Context()[0].Value() != "everpure" {
		t.Fatalf("mixed composition status = %#v calls=%d, %v", response.Result(), harness.validator.updateCalls, err)
	}
}

func TestCompositionStatusNamesTheComponentWithNativeDrift(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "common@v1", "--bundle", "everpure@v2", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("composition install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	everpurePlugin := nativePluginID(record.Packages[1], record.MarketplaceID)
	harness.native.plugins[everpurePlugin] = false
	harness.native.enabled[everpurePlugin] = false

	status := statusService{validation: harness.validator, state: harness.store}
	response, err = status.Status(context.Background(), parseRequest[cli.StatusRequest](t, "status", record.InstallationID))
	warnings := response.Result().Warnings()
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || response.Result().Status() != result.StatusDegraded || len(warnings) != 1 ||
		warnings[0].Code() != "native_plugin_missing" || !diagnosticHasContext(warnings[0].Context(), "component", "everpure") || !diagnosticHasContext(warnings[0].Context(), "resource", everpurePlugin) {
		t.Fatalf("component native drift = %#v, %v", response.Result(), err)
	}
}

func TestCompositionStatusContinuesAfterAComponentInspectorError(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "common@v1", "--bundle", "everpure@v2", "--bundle", "team@v3", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("composition install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	harness.validator.inspectionDirectories = nil
	harness.validator.failNativePlugin = "common-plugin"

	status := statusService{validation: harness.validator, state: harness.store}
	response, err = status.Status(context.Background(), parseRequest[cli.StatusRequest](t, "status", record.InstallationID))
	warnings := response.Result().Warnings()
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || response.Result().Status() != result.StatusDegraded || len(harness.validator.inspectionDirectories) != 3 ||
		len(warnings) != 1 || warnings[0].Code() != "native_status_failed" || !diagnosticHasContext(warnings[0].Context(), "component", "common") {
		t.Fatalf("component native inspection = %#v calls=%d, %v", response.Result(), len(harness.validator.inspectionDirectories), err)
	}
}

func diagnosticHasContext(context []result.Context, field, value string) bool {
	for _, item := range context {
		if item.Field() == field && item.Value() == value {
			return true
		}
	}
	return false
}

func TestCompositionRejectsSingleSourceApplyPreconditions(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "common@v1", "--bundle", "everpure@v2", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("composition install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	commandsBefore := len(harness.native.commands)

	update := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--expected-commit", strings.Repeat("a", 40), "--yes")
	response, err = harness.service.Update(context.Background(), update, CommandIO{})
	problems := response.Result().Errors()
	if err != nil || response.Result().ExitCode() != result.ExitConflict || len(problems) != 1 || problems[0].Code() != "composition_precondition_unsupported" {
		t.Fatalf("composition commit precondition = %#v, %v", response.Result(), err)
	}

	sync := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "composition", "--expected-source-digest", strings.Repeat("a", 64), "--yes")
	response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
	problems = response.Result().Errors()
	if err != nil || response.Result().ExitCode() != result.ExitConflict || len(problems) != 1 || problems[0].Code() != "composition_precondition_unsupported" {
		t.Fatalf("composition digest precondition = %#v, %v", response.Result(), err)
	}
	if len(harness.native.commands) != commandsBefore {
		t.Fatalf("composition preconditions mutated target: before=%d after=%d", commandsBefore, len(harness.native.commands))
	}
}

func TestCompositionCollisionStopsBeforeMutation(t *testing.T) {
	harness := newLifecycleHarness(t)
	request := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "alpha@v1", "--bundle", "beta@v1", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitConflict || response.Result().Changed() {
		t.Fatalf("collision result = %#v, %v", response.Result(), err)
	}
	if records, loadErr := harness.store.LoadAll(); loadErr != nil || len(records) != 0 {
		t.Fatalf("collision state = %#v, %v", records, loadErr)
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present || len(harness.native.commands) != 0 {
		t.Fatalf("collision mutated target: marker=%t err=%v commands=%v", present, markerErr, harness.native.commands)
	}
}

func componentNames(record installstate.Record) []string {
	names := make([]string, len(record.Components))
	for index, component := range record.Components {
		names[index] = component.Name
	}
	return names
}
