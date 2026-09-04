package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/hostprocess"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
)

func TestProjectSharedPreexistingNativeCatalogIsRejectedBeforeJournal(t *testing.T) {
	harness, request, project := prepareProjectSharedHarness(t, "default")
	execution, _, stop, err := harness.service.prepareInstall(context.Background(), request.Source(), request.Target(), request.Scope(), project, true, request.Selection(), request.InstallationID(), request.HasInstallationID(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("prepare install: stop=%t err=%v", stop, err)
	}
	path := harness.service.projectSharedNativeCatalogPath(*execution.transition.desired)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	external := []byte("externally-created catalog\n")
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}

	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitConflict || response.Result().Mutation() != result.MutationNotStarted {
		t.Fatalf("occupied catalog install = %#v, %v", response.Result(), err)
	}
	if slices.ContainsFunc(harness.native.commands, func(command []string) bool { return len(command) != 0 && command[0] == "plugin" }) {
		t.Fatalf("target commands = %#v", harness.native.commands)
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
		t.Fatalf("operation marker = present:%t err:%v", present, markerErr)
	}
	if contents, readErr := os.ReadFile(path); readErr != nil || !bytes.Equal(contents, external) {
		t.Fatalf("external catalog changed: %q, %v", contents, readErr)
	}
}

func TestProjectSharedNativeCatalogDriftIsPreflightedAndReplaceable(t *testing.T) {
	harness, request, _ := prepareProjectSharedHarness(t, "full")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	path := harness.service.projectSharedNativeCatalogPath(record)
	if err := os.WriteFile(path, []byte("modified catalog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	harness.validator.update = true
	commandsBefore := len(harness.native.commands)

	fail := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--yes")
	response, err = harness.service.Update(context.Background(), fail, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitConflict || response.Result().Mutation() != result.MutationNotStarted {
		t.Fatalf("fail policy update = %#v, %v", response.Result(), err)
	}
	if len(harness.native.commands) != commandsBefore {
		t.Fatalf("fail policy ran native commands: before=%d after=%d", commandsBefore, len(harness.native.commands))
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
		t.Fatalf("fail policy marker = present:%t err:%v", present, markerErr)
	}

	replace := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--conflict-policy", "replace-owned", "--yes")
	response, err = harness.service.Update(context.Background(), replace, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("replace-owned update = %#v, %v", response.Result(), err)
	}
	updated, present, loadErr := harness.store.LoadByID(record.InstallationID)
	if loadErr != nil || !present || inspectFileDrift(path, updated.NativeCatalog.Checksum) != cli.DriftUnchanged {
		t.Fatalf("repaired catalog = %#v, present=%t err=%v", updated.NativeCatalog, present, loadErr)
	}
	commands := harness.native.commands[commandsBefore:]
	firstUpdate := slices.IndexFunc(commands, func(command []string) bool {
		return len(command) >= 3 && slices.Equal(command[:3], []string{"plugin", "marketplace", "update"})
	})
	if firstUpdate < len(record.Packages) {
		t.Fatalf("marketplace update preceded package removals: %#v", commands)
	}
	for index := range len(record.Packages) {
		if len(commands[index]) < 2 || !slices.Equal(commands[index][:2], []string{"plugin", "uninstall"}) {
			t.Fatalf("command %d = %#v, want uninstall", index, commands[index])
		}
	}
	if firstUpdate < 0 || firstUpdate+1 >= len(commands) || len(commands[firstUpdate+1]) < 2 || !slices.Equal(commands[firstUpdate+1][:2], []string{"plugin", "install"}) {
		t.Fatalf("catalog refresh/install order = %#v", commands)
	}
}

func TestProjectSharedReplaceOwnedRejectsNonRegularOwnedPathsBeforeJournal(t *testing.T) {
	for _, test := range []struct {
		name string
		path func(lifecycleHarness, installstate.Record) string
	}{
		{name: "native catalog", path: func(harness lifecycleHarness, record installstate.Record) string {
			return harness.service.projectSharedNativeCatalogPath(record)
		}},
		{name: "rules", path: func(harness lifecycleHarness, record installstate.Record) string {
			return harness.service.rulesPath(record)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness, request, _ := prepareProjectSharedHarness(t, "default")
			response, err := harness.service.Install(context.Background(), request, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("install = %#v, %v", response.Result(), err)
			}
			record, _, _ := harness.store.Load()
			path := test.path(harness, record)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			harness.validator.update = true
			commandsBefore := len(harness.native.commands)
			update := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--conflict-policy", "replace-owned", "--yes")

			response, err = harness.service.Update(context.Background(), update, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitConflict || response.Result().Mutation() != result.MutationNotStarted {
				t.Fatalf("non-regular %s = %#v, %v", test.name, response.Result(), err)
			}
			if len(harness.native.commands) != commandsBefore {
				t.Fatalf("non-regular %s ran commands: before=%d after=%d", test.name, commandsBefore, len(harness.native.commands))
			}
			if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
				t.Fatalf("non-regular %s marker = present:%t err=%v", test.name, present, markerErr)
			}
		})
	}
}

func TestProjectSharedSettingsDriftAfterPlanningIsRejectedBeforeJournal(t *testing.T) {
	harness, request, _ := prepareProjectSharedHarness(t, "default")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	harness.validator.update = true
	update := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--conflict-policy", "replace-owned", "--yes")
	execution, _, stop, err := harness.service.prepareUpdate(context.Background(), update.InstallationID(), update.Source(), cli.ConflictReplaceOwned)
	if err != nil || stop || len(execution.conflicts) != 0 {
		t.Fatalf("prepare update: stop=%t conflicts=%#v err=%v", stop, execution.conflicts, err)
	}
	settingsPath := projectSettingsPath(record)
	settings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	drifted := bytes.Replace(settings, []byte(strings.Repeat("a", 40)), []byte(strings.Repeat("c", 40)), 1)
	if bytes.Equal(drifted, settings) {
		t.Fatal("test did not change the owned declaration")
	}
	if err := os.WriteFile(settingsPath, drifted, 0o600); err != nil {
		t.Fatal(err)
	}
	commandsBefore := len(harness.native.commands)

	response, err = harness.service.commitExecution(context.Background(), cli.CommandUpdate, execution, cli.ConflictReplaceOwned, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitConflict || response.Result().Mutation() != result.MutationNotStarted {
		t.Fatalf("post-plan drift = %#v, %v", response.Result(), err)
	}
	if len(harness.native.commands) != commandsBefore {
		t.Fatalf("post-plan drift ran native commands: before=%d after=%d", commandsBefore, len(harness.native.commands))
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
		t.Fatalf("post-plan drift marker = present:%t err=%v", present, markerErr)
	}
}

func TestProjectSharedRulesDriftAfterPlanningIsRejectedBeforeJournal(t *testing.T) {
	harness, request, _ := prepareProjectSharedHarness(t, "default")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	harness.validator.update = true
	update := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--conflict-policy", "keep", "--yes")
	execution, _, stop, err := harness.service.prepareUpdate(context.Background(), update.InstallationID(), update.Source(), cli.ConflictKeep)
	if err != nil || stop || len(execution.conflicts) != 0 {
		t.Fatalf("prepare update: stop=%t conflicts=%#v err=%v", stop, execution.conflicts, err)
	}
	rulesPath := harness.service.rulesPath(record)
	external := []byte("external rules after planning\n")
	if err := os.WriteFile(rulesPath, external, 0o600); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(harness.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	historyBefore, err := harness.store.LoadHistory(record.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	commandsBefore := len(harness.native.commands)

	response, err = harness.service.commitExecution(context.Background(), cli.CommandUpdate, execution, cli.ConflictKeep, CommandIO{})

	if err != nil || response.Result().ExitCode() != result.ExitConflict || response.Result().Mutation() != result.MutationNotStarted {
		t.Fatalf("post-plan rules drift = %#v, %v", response.Result(), err)
	}
	if len(harness.native.commands) != commandsBefore {
		t.Fatalf("post-plan rules drift ran commands: before=%d after=%d", commandsBefore, len(harness.native.commands))
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
		t.Fatalf("post-plan rules drift marker = present:%t err=%v", present, markerErr)
	}
	staged, stagedErr := harness.store.LoadStagedHistory()
	if stagedErr != nil || len(staged) != 0 {
		t.Fatalf("staged history = %#v, %v", staged, stagedErr)
	}
	historyAfter, historyErr := harness.store.LoadHistory(record.InstallationID)
	if historyErr != nil || len(historyAfter) != len(historyBefore) {
		t.Fatalf("history entries = %d/%d, %v", len(historyAfter), len(historyBefore), historyErr)
	}
	stateAfter, stateErr := os.ReadFile(harness.store.Path())
	if stateErr != nil || !bytes.Equal(stateAfter, stateBefore) {
		t.Fatalf("installation state changed: %v", stateErr)
	}
	contents, readErr := os.ReadFile(rulesPath)
	if readErr != nil || !bytes.Equal(contents, external) {
		t.Fatalf("external rules changed: %q, %v", contents, readErr)
	}
}

func TestProjectSharedFreshDeclarationRaceIsNotOverwritten(t *testing.T) {
	harness, request, _ := prepareProjectSharedHarness(t, "default")
	external := []byte(`{"source":{"source":"github","repo":"external/toolkit"}}`)
	harness.service.runner = &projectSharedHookRunner{
		base:   harness.native,
		prefix: []string{"plugin", "marketplace", "add"},
		hook: func(_ string, _ []string) error {
			return forceProjectMarketplaceEntry(projectSettingsPathForRequest(request), "ai4j", external)
		},
	}

	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().Failure() != result.FailureRecovery {
		t.Fatalf("fresh declaration race = %#v, %v", response.Result(), err)
	}
	assertProjectMarketplaceEntry(t, projectSettingsPathForRequest(request), "ai4j", external)
}

func TestProjectSharedActiveDeclarationRaceIsNotOverwritten(t *testing.T) {
	harness, request, _ := prepareProjectSharedHarness(t, "default")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	external := []byte(`{"source":{"source":"github","repo":"external/toolkit"}}`)
	harness.service.runner = &projectSharedHookRunner{
		base:   harness.native,
		prefix: []string{"plugin", "uninstall"},
		hook: func(_ string, _ []string) error {
			return forceProjectMarketplaceEntry(projectSettingsPath(record), record.DeclarationID, external)
		},
	}
	harness.validator.update = true
	update := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--conflict-policy", "replace-owned", "--yes")

	response, err = harness.service.Update(context.Background(), update, CommandIO{})
	if err != nil || response.Result().Failure() != result.FailureRecovery {
		t.Fatalf("active declaration race = %#v, %v", response.Result(), err)
	}
	assertProjectMarketplaceEntry(t, projectSettingsPath(record), record.DeclarationID, external)
}

func TestProjectSharedStatusAndRecoveryIncludeNativeCatalog(t *testing.T) {
	harness, request, _ := prepareProjectSharedHarness(t, "default")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	if record.NativeCatalog.Path == "" || record.NativeCatalog.Checksum == "" {
		t.Fatalf("native catalog ownership was not recorded: %#v", record.NativeCatalog)
	}
	path := harness.service.projectSharedNativeCatalogPath(record)
	if err := os.WriteFile(path, []byte("status drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status := statusService{validation: harness.validator, state: harness.store}
	statusRequest := parseRequest[cli.StatusRequest](t, "status", record.InstallationID)
	response, err = status.Status(context.Background(), statusRequest)
	if err != nil {
		t.Fatal(err)
	}
	assertStatusDrift(t, response.Data().(cli.StatusData), record.NativeCatalog.Path, cli.DriftModified)
	if err := removeProjectMarketplace(record); err != nil {
		t.Fatal(err)
	}
	if record.Rules != (installstate.OwnedFile{}) {
		if err := os.Remove(harness.service.rulesPath(record)); err != nil {
			t.Fatal(err)
		}
	}
	if recoveryOwnedAbsent(harness.service, record) {
		t.Fatal("recovery treated a present project-shared native catalog as absent")
	}
}

func prepareProjectSharedHarness(t *testing.T, bundle string) (lifecycleHarness, cli.InstallRequest, string) {
	t.Helper()
	harness := newLifecycleHarness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := []byte("{\n  \"enabledPlugins\": {\"unrelated@other\": true},\n  \"unrelated\": {\"keep\": true}\n}\n")
	if err := os.WriteFile(filepath.Join(project, ".claude", "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-shared", "--project", project, "--bundle", bundle, "--yes")
	return harness, request, project
}

func projectSettingsPathForRequest(request cli.InstallRequest) string {
	project, _ := request.Project()
	return filepath.Join(project, ".claude", "settings.json")
}

func forceProjectMarketplaceEntry(path, declarationID string, entry []byte) error {
	before, _, err := readProjectSettings(path)
	if err != nil {
		return err
	}
	after, err := projectSettingsWithMarketplace(before, declarationID, entry, true)
	if err != nil {
		return err
	}
	return applyProjectSettings(path, before, after)
}

func assertProjectMarketplaceEntry(t *testing.T, path, declarationID string, want []byte) {
	t.Helper()
	settings, _, err := readProjectSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, present, err := projectMarketplaceFromSettings(settings, declarationID)
	if err != nil || !present || !jsonEqual(entry, want) {
		t.Fatalf("marketplace entry = %s, present=%t err=%v, want %s", entry, present, err, want)
	}
}

type projectSharedHookRunner struct {
	base   *lifecycleNativeRunner
	prefix []string
	hook   func(string, []string) error
	ran    bool
}

func (r *projectSharedHookRunner) LookPath(name string) (string, error) {
	return r.base.LookPath(name)
}

func (r *projectSharedHookRunner) Run(ctx context.Context, directory, executable string, arguments, environment []string) (hostprocess.Result, error) {
	process, err := r.base.Run(ctx, directory, executable, arguments, environment)
	if err == nil && process.ExitCode == 0 && !r.ran && len(arguments) >= len(r.prefix) && slices.Equal(arguments[:len(r.prefix)], r.prefix) {
		r.ran = true
		if hookErr := r.hook(directory, arguments); hookErr != nil {
			return hostprocess.Result{}, hookErr
		}
	}
	return process, err
}

func (r *projectSharedHookRunner) RunIsolated(ctx context.Context, directory, executable string, arguments, environment []string) (hostprocess.Result, error) {
	process, err := r.base.RunIsolated(ctx, directory, executable, arguments, environment)
	if err == nil && process.ExitCode == 0 && !r.ran && len(arguments) >= len(r.prefix) && slices.Equal(arguments[:len(r.prefix)], r.prefix) {
		r.ran = true
		if hookErr := r.hook(directory, arguments); hookErr != nil {
			return hostprocess.Result{}, hookErr
		}
	}
	return process, err
}

func stageProjectSharedInstall(t *testing.T, harness lifecycleHarness, execution lifecycleExecution, operationID string) (*installstate.Record, installstate.HistoryEntry) {
	t.Helper()
	desired := cloneRecordPtr(execution.transition.desired)
	desired.LastOperation = installstate.LastOperation{ID: operationID, Timestamp: "2026-08-26T12:00:00Z"}
	desired.History = appendUnique(desired.History, operationID)
	entry := installstate.HistoryEntry{
		SchemaVersion: installstate.HistorySchemaVersion, Operation: execution.operation.String(), OperationID: operationID,
		InstallationID: desired.InstallationID, Timestamp: desired.LastOperation.Timestamp, Restorable: true,
		After: desired, CatalogAfter: projectSharedOwnedEntry(desired), RulesAfter: slices.Clone(execution.transition.desiredRules),
		NativeArtifactsAfter: cloneNativeArtifacts(execution.transition.desiredArtifacts),
	}
	resources := append([]string{"history:" + desired.InstallationID, "owned:state/installation.json", "owned:" + desired.Catalog.Path, "owned:" + desired.NativeCatalog.Path}, desired.NativeResources...)
	if desired.Rules.Path != "" {
		resources = append(resources, "owned:"+desired.Rules.Path)
	}
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
	return desired, entry
}

func TestProjectSharedRecoveryCompletesPreparedNativeCatalog(t *testing.T) {
	harness, request, project := prepareProjectSharedHarness(t, "default")
	execution, _, stop, err := harness.service.prepareInstall(context.Background(), request.Source(), request.Target(), request.Scope(), project, true, request.Selection(), request.InstallationID(), request.HasInstallationID(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("prepare install: stop=%t err=%v", stop, err)
	}
	desired, _ := stageProjectSharedInstall(t, harness, execution, "operation-projectshared-recovery")
	if _, err := harness.service.writeProjectSharedNativeCatalog(nil, desired, cli.ConflictFail); err != nil {
		t.Fatal(err)
	}

	recovered, err := harness.service.reconcileInterrupted(context.Background())
	if err != nil || !recovered {
		t.Fatalf("recovery = %t, %v", recovered, err)
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
		t.Fatalf("marker = present:%t err=%v", present, markerErr)
	}
	record, present, loadErr := harness.store.LoadByID(desired.InstallationID)
	if loadErr != nil || !present || record.LastOperation.ID != desired.LastOperation.ID {
		t.Fatalf("state = %#v, present:%t err=%v", record, present, loadErr)
	}
	if err := harness.service.verifyTransition(context.Background(), *desired, nil); err != nil {
		t.Fatalf("recovered target: %v", err)
	}
}

func TestProjectSharedRecoveryCompletesNativeCatalogBeforeSettingsUpdate(t *testing.T) {
	harness, install, _ := prepareProjectSharedHarness(t, "default")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	before, _, _ := harness.store.Load()
	harness.validator.update = true
	request := parseRequest[cli.UpdateRequest](t, "update", before.InstallationID, "--yes")
	execution, _, stop, err := harness.service.prepareUpdate(context.Background(), request.InstallationID(), request.Source(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("prepare update: stop=%t err=%v", stop, err)
	}
	desired, _, _ := stageInterrupted(t, harness, execution, "operation-projectshared-update-recovery")
	for _, pkg := range before.Packages {
		if err := harness.service.runClaudeFor(context.Background(), before, []string{"plugin", "uninstall", nativePluginID(pkg, before.MarketplaceID), "--scope", nativeScope(before), "--keep-data"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := harness.service.writeProjectSharedNativeCatalog(&before, desired, cli.ConflictFail); err != nil {
		t.Fatal(err)
	}

	recovered, err := harness.service.reconcileInterrupted(context.Background())
	if err != nil || !recovered {
		t.Fatalf("recovery = %t, %v", recovered, err)
	}
	if err := harness.service.verifyTransition(context.Background(), *desired, &before); err != nil {
		t.Fatalf("recovered target: %v", err)
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
		t.Fatalf("marker = present:%t err=%v", present, markerErr)
	}
}

func TestProjectSharedRecoveryRejectsInstalledPackageBeforeSettingsUpdate(t *testing.T) {
	harness, install, _ := prepareProjectSharedHarness(t, "default")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	before, _, _ := harness.store.Load()
	harness.validator.update = true
	request := parseRequest[cli.UpdateRequest](t, "update", before.InstallationID, "--yes")
	execution, _, stop, err := harness.service.prepareUpdate(context.Background(), request.InstallationID(), request.Source(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("prepare update: stop=%t err=%v", stop, err)
	}
	desired, _, _ := stageInterrupted(t, harness, execution, "operation-projectshared-stale-package")
	if _, err := harness.service.writeProjectSharedNativeCatalog(&before, desired, cli.ConflictFail); err != nil {
		t.Fatal(err)
	}

	recovered, err := harness.service.reconcileInterrupted(context.Background())
	if err != nil || recovered {
		t.Fatalf("recovery = %t, %v", recovered, err)
	}
	if inspectProjectMarketplaceDrift(before) != cli.DriftUnchanged {
		t.Fatal("recovery rewrote the project declaration around an installed stale package")
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || !present {
		t.Fatalf("marker = present:%t err=%v", present, markerErr)
	}
}
