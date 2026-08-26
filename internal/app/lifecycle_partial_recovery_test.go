package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
)

func TestLifecycleRecoveryCompletesFreshInstallAfterSecondPackageFailure(t *testing.T) {
	harness := newLifecycleHarness(t)
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "full", "--yes")
	project, hasProject := request.Project()
	execution, _, stop, err := harness.service.prepareInstall(
		context.Background(), request.Source(), request.Target(), request.Scope(), project, hasProject,
		request.Selection(), request.InstallationID(), request.HasInstallationID(), cli.ConflictFail,
	)
	if err != nil || stop {
		t.Fatalf("install preparation stopped: %v", err)
	}
	secondPlugin := nativePluginID(execution.desired.Packages[1], execution.desired.MarketplaceID)
	harness.native.failPrefix = []string{"plugin", "install", secondPlugin}
	harness.native.failCount = 1

	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().Failure() != result.FailureRecovery {
		t.Fatalf("partial install = %#v, %v", response.Result(), err)
	}
	if !harness.native.plugins[nativePluginID(execution.desired.Packages[0], execution.desired.MarketplaceID)] || harness.native.plugins[secondPlugin] {
		t.Fatalf("native state is not stopped after package one: %#v", harness.native.plugins)
	}
	selectionCalls := harness.validator.selectionCalls

	recovered, err := harness.service.reconcileInterrupted(context.Background())
	if err != nil || !recovered {
		t.Fatalf("partial install recovery = %t, %v", recovered, err)
	}
	if harness.validator.selectionCalls != selectionCalls {
		t.Fatalf("recovery resolved the source: selection calls=%d, want %d", harness.validator.selectionCalls, selectionCalls)
	}
	current, present, err := harness.store.LoadByID(execution.desired.InstallationID)
	if err != nil || !present || current.Lifecycle != "active" || current.Selection.RequestedBundle != "full" {
		t.Fatalf("recovered installation = %#v, present=%t, err=%v", current, present, err)
	}
	assertRecoveryNativePackages(t, harness, current, true)
	assertRecoveryJournalCleared(t, harness)
}

func TestLifecycleRequiresExplicitPluginUninstallBeforeMarketplaceRemoval(t *testing.T) {
	harness := newLifecycleHarness(t)
	record := installRecoveryBundle(t, harness, "full")
	remove := []string{"plugin", "marketplace", "remove", record.MarketplaceID, "--scope", nativeScope(record)}
	if err := harness.service.runClaudeFor(context.Background(), record, remove); err == nil {
		t.Fatal("marketplace removal implicitly uninstalled its plugins")
	}
	assertRecoveryNativePackages(t, harness, record, true)
	if !harness.native.marketplaces[record.MarketplaceID] {
		t.Fatal("marketplace was removed while plugins remained installed")
	}
	for _, pluginID := range nativePluginIDs(record) {
		if err := harness.service.runClaudeFor(context.Background(), record, []string{"plugin", "uninstall", pluginID, "--scope", nativeScope(record), "--keep-data"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := harness.service.runClaudeFor(context.Background(), record, remove); err != nil {
		t.Fatal(err)
	}
	assertRecoveryNativePackages(t, harness, record, false)
	if harness.native.marketplaces[record.MarketplaceID] {
		t.Fatal("marketplace remains after explicit plugin uninstall")
	}
}

func TestLifecycleRecoveryHandlesSecondPackageFailuresDuringUpdateAndSync(t *testing.T) {
	for _, test := range []struct {
		name          string
		command       string
		initialBundle string
		desiredBundle string
		failureAction string
		forward       bool
	}{
		{name: "update compensates second uninstall failure", command: "update", initialBundle: "full", failureAction: "uninstall"},
		{name: "update completes second install failure", command: "update", initialBundle: "full", failureAction: "install", forward: true},
		{name: "sync compensates second uninstall failure", command: "sync", initialBundle: "full", desiredBundle: "minimal", failureAction: "uninstall"},
		{name: "sync completes second install failure", command: "sync", initialBundle: "default", desiredBundle: "full", failureAction: "install", forward: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newLifecycleHarness(t)
			before := installRecoveryBundle(t, harness, test.initialBundle)
			secondPlugin := nativePluginID(installstate.NativePackage{ID: "ai4j-tools"}, before.MarketplaceID)
			harness.native.failPrefix = []string{"plugin", test.failureAction, secondPlugin}
			harness.native.failCount = 1

			var response cli.Response
			var err error
			switch test.command {
			case "update":
				harness.validator.update = true
				request := parseRequest[cli.UpdateRequest](t, "update", before.InstallationID, "--yes")
				response, err = harness.service.Update(context.Background(), request, CommandIO{})
			case "sync":
				request := parseRequest[cli.SyncRequest](t, "sync", before.InstallationID, "--bundle", test.desiredBundle, "--yes")
				response, err = harness.service.Sync(context.Background(), request, CommandIO{})
			}
			if err != nil || response.Result().Failure() != result.FailureRecovery {
				t.Fatalf("partial %s = %#v, %v", test.command, response.Result(), err)
			}
			marker, present, err := harness.store.LoadMarker()
			if err != nil || !present {
				t.Fatalf("operation marker = %#v, present=%t, err=%v", marker, present, err)
			}
			staged, present, err := harness.store.LoadOperationHistory(before.InstallationID, marker.OperationID)
			if err != nil || !present || staged.Committed {
				t.Fatalf("staged history = %#v, present=%t, err=%v", staged, present, err)
			}
			selectionCalls := harness.validator.selectionCalls

			recovered, err := harness.service.reconcileInterrupted(context.Background())
			if err != nil || !recovered {
				t.Fatalf("partial %s recovery = %t, %v", test.command, recovered, err)
			}
			if harness.validator.selectionCalls != selectionCalls {
				t.Fatalf("%s recovery resolved the source: selection calls=%d, want %d", test.command, harness.validator.selectionCalls, selectionCalls)
			}
			current, present, err := harness.store.LoadByID(before.InstallationID)
			if err != nil || !present {
				t.Fatalf("recovered state = %#v, present=%t, err=%v", current, present, err)
			}
			if test.forward {
				if current.LastOperation.ID != marker.OperationID || staged.After == nil || !sameCurrentState(current, *staged.After) {
					t.Fatalf("operation did not converge to staged endpoint: current=%#v after=%#v", current, staged.After)
				}
				committed, present, err := harness.store.LoadOperationHistory(before.InstallationID, marker.OperationID)
				if err != nil || !present || !committed.Committed {
					t.Fatalf("committed recovery history = %#v, present=%t, err=%v", committed, present, err)
				}
			} else {
				if !reflect.DeepEqual(current, before) {
					t.Fatalf("operation did not compensate to prior endpoint: current=%#v before=%#v", current, before)
				}
				if _, present, err := harness.store.LoadOperationHistory(before.InstallationID, marker.OperationID); err != nil || present {
					t.Fatalf("compensated staged history remains: present=%t err=%v", present, err)
				}
			}
			assertRecoveryNativePackages(t, harness, current, true)
			assertRecoveryJournalCleared(t, harness)
		})
	}
}

func TestLifecycleRecoveryCompensatesCrashAfterFirstUpdatePackageUninstall(t *testing.T) {
	harness := newLifecycleHarness(t)
	before := installRecoveryBundle(t, harness, "full")
	harness.validator.update = true
	request := parseRequest[cli.UpdateRequest](t, "update", before.InstallationID, "--yes")
	execution, _, stop, err := harness.service.prepareUpdate(context.Background(), request.InstallationID(), request.Source(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("update preparation stopped: %v", err)
	}
	_, entry, _ := stageInterrupted(t, harness, execution, "operation-partialcrash0001")
	first := before.Packages[0]
	if err := harness.service.runClaudeFor(context.Background(), before, []string{"plugin", "uninstall", nativePluginID(first, before.MarketplaceID), "--scope", nativeScope(before), "--keep-data"}); err != nil {
		t.Fatal(err)
	}
	selectionCalls := harness.validator.selectionCalls

	recovered, err := harness.service.reconcileInterrupted(context.Background())
	if err != nil || !recovered {
		t.Fatalf("crash recovery = %t, %v", recovered, err)
	}
	if harness.validator.selectionCalls != selectionCalls {
		t.Fatalf("crash recovery resolved the source: selection calls=%d, want %d", harness.validator.selectionCalls, selectionCalls)
	}
	current, present, err := harness.store.LoadByID(before.InstallationID)
	if err != nil || !present || !reflect.DeepEqual(current, before) {
		t.Fatalf("compensated state = %#v, present=%t, err=%v", current, present, err)
	}
	if _, present, err := harness.store.LoadOperationHistory(entry.InstallationID, entry.OperationID); err != nil || present {
		t.Fatalf("crashed staged history remains: present=%t err=%v", present, err)
	}
	assertRecoveryNativePackages(t, harness, current, true)
	assertRecoveryJournalCleared(t, harness)
}

func TestLifecycleRecoveryHandlesProjectSharedSecondPackageFailure(t *testing.T) {
	for _, test := range []struct {
		name          string
		failureAction string
		forward       bool
	}{
		{name: "compensates package removal", failureAction: "uninstall"},
		{name: "completes package install", failureAction: "install", forward: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness, install, _ := prepareProjectSharedHarness(t, "full")
			response, err := harness.service.Install(context.Background(), install, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("project-shared install = %#v, %v", response.Result(), err)
			}
			before, _, _ := harness.store.Load()
			harness.validator.update = true
			secondPlugin := nativePluginID(before.Packages[1], before.MarketplaceID)
			harness.native.failPrefix = []string{"plugin", test.failureAction, secondPlugin}
			harness.native.failCount = 1
			update := parseRequest[cli.UpdateRequest](t, "update", before.InstallationID, "--yes")
			response, err = harness.service.Update(context.Background(), update, CommandIO{})
			if err != nil || response.Result().Failure() != result.FailureRecovery {
				t.Fatalf("partial project-shared update = %#v, %v", response.Result(), err)
			}
			marker, present, err := harness.store.LoadMarker()
			if err != nil || !present {
				t.Fatalf("project-shared marker = %#v, present=%t, err=%v", marker, present, err)
			}
			entry, present, err := harness.store.LoadOperationHistory(before.InstallationID, marker.OperationID)
			if err != nil || !present || entry.Before == nil || entry.After == nil {
				t.Fatalf("project-shared staged history = %#v, present=%t, err=%v", entry, present, err)
			}
			selectionCalls := harness.validator.selectionCalls

			recovered, err := harness.service.reconcileInterrupted(context.Background())
			if err != nil || !recovered {
				t.Fatalf("project-shared recovery = %t, %v", recovered, err)
			}
			if harness.validator.selectionCalls != selectionCalls {
				t.Fatalf("project-shared recovery resolved the source: selection calls=%d, want %d", harness.validator.selectionCalls, selectionCalls)
			}
			current, present, err := harness.store.LoadByID(before.InstallationID)
			if err != nil || !present || test.forward && current.LastOperation.ID != marker.OperationID || !test.forward && !reflect.DeepEqual(current, before) {
				t.Fatalf("project-shared recovered state = %#v, present=%t, err=%v", current, present, err)
			}
			if inspectProjectMarketplaceDrift(current) != cli.DriftUnchanged || harness.service.inspectProjectSharedNativeCatalogDrift(current) != cli.DriftUnchanged {
				t.Fatalf("project-shared endpoint is not exact: %#v", current)
			}
			assertRecoveryNativePackages(t, harness, current, true)
			assertRecoveryJournalCleared(t, harness)
		})
	}
}

func TestLifecycleRecoveryFinishesNonRestorableLocalUninstall(t *testing.T) {
	harness := newLifecycleHarness(t)
	checkout := filepath.Join(harness.service.home, "checkout")
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	harness.validator.localDigest = strings.Repeat("e", 64)
	install := parseRequest[cli.InstallRequest](t, "install", "--source", checkout, "--target", "claude", "--scope", "user", "--bundle", "full", "--expected-source-digest", harness.validator.localDigest, "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("local install = %#v, %v", response.Result(), err)
	}
	before, _, _ := harness.store.Load()
	purge := parseRequest[cli.HistoryPurgeRequest](t, "history", "purge", before.InstallationID, "--all", "--yes")
	response, err = harness.service.HistoryPurge(context.Background(), purge, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("history purge = %#v, %v", response.Result(), err)
	}
	secondPlugin := nativePluginID(before.Packages[1], before.MarketplaceID)
	harness.native.failPrefix = []string{"plugin", "uninstall", secondPlugin}
	harness.native.failCount = 1
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", before.InstallationID, "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().Failure() != result.FailureRecovery {
		t.Fatalf("partial uninstall = %#v, %v", response.Result(), err)
	}
	marker, present, err := harness.store.LoadMarker()
	if err != nil || !present {
		t.Fatalf("uninstall marker = %#v, present=%t, err=%v", marker, present, err)
	}
	entry, present, err := harness.store.LoadOperationHistory(before.InstallationID, marker.OperationID)
	if err != nil || !present || entry.Restorable || len(entry.NativeArtifactsBefore) != 0 {
		t.Fatalf("non-restorable staged uninstall = %#v, present=%t, err=%v", entry, present, err)
	}

	recovered, err := harness.service.reconcileInterrupted(context.Background())
	if err != nil || !recovered {
		t.Fatalf("non-restorable uninstall recovery = %t, %v", recovered, err)
	}
	current, present, err := harness.store.LoadByID(before.InstallationID)
	if err != nil || !present || current.Lifecycle != "archived" {
		t.Fatalf("archived state = %#v, present=%t, err=%v", current, present, err)
	}
	assertRecoveryNativePackages(t, harness, current, false)
	if !ownedFileAbsent(harness.service.catalogPath(before)) || !ownedFileAbsent(harness.service.rulesPath(before)) {
		t.Fatalf("owned uninstall files remain: catalog=%t rules=%t", ownedFileAbsent(harness.service.catalogPath(before)), ownedFileAbsent(harness.service.rulesPath(before)))
	}
	assertRecoveryJournalCleared(t, harness)
}

func TestLifecycleRecoveryRebindsLocalCatalogAtExactEndpoint(t *testing.T) {
	for _, test := range []struct {
		name          string
		failureAction string
	}{
		{name: "rebinds after old package removal stops", failureAction: "uninstall"},
		{name: "rebinds after new package install stops", failureAction: "install"},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newLifecycleHarness(t)
			checkout := filepath.Join(harness.service.home, "checkout")
			if err := os.MkdirAll(checkout, 0o700); err != nil {
				t.Fatal(err)
			}
			harness.validator.localDigest = strings.Repeat("e", 64)
			install := parseRequest[cli.InstallRequest](t, "install", "--source", checkout, "--target", "claude", "--scope", "user", "--bundle", "full", "--expected-source-digest", harness.validator.localDigest, "--yes")
			response, err := harness.service.Install(context.Background(), install, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("local install = %#v, %v", response.Result(), err)
			}
			before, _, _ := harness.store.Load()
			harness.validator.localDigest = strings.Repeat("f", 64)
			secondPlugin := nativePluginID(before.Packages[1], before.MarketplaceID)
			harness.native.failPrefix = []string{"plugin", test.failureAction, secondPlugin}
			harness.native.failCount = 1
			update := parseRequest[cli.UpdateRequest](t, "update", before.InstallationID, "--allow-dirty", "--expected-source-digest", harness.validator.localDigest, "--yes")
			response, err = harness.service.Update(context.Background(), update, CommandIO{})
			if err != nil || response.Result().Failure() != result.FailureRecovery {
				t.Fatalf("partial local update = %#v, %v", response.Result(), err)
			}
			marker, present, err := harness.store.LoadMarker()
			if err != nil || !present {
				t.Fatalf("local update marker = %#v, present=%t, err=%v", marker, present, err)
			}
			entry, present, err := harness.store.LoadOperationHistory(before.InstallationID, marker.OperationID)
			if err != nil || !present || entry.After == nil || before.Catalog.Path == entry.After.Catalog.Path {
				t.Fatalf("local staged history = %#v, present=%t, err=%v", entry, present, err)
			}

			recovered, err := harness.service.reconcileInterrupted(context.Background())
			if err != nil || !recovered {
				t.Fatalf("local catalog recovery = %t, %v", recovered, err)
			}
			current, present, err := harness.store.LoadByID(before.InstallationID)
			if err != nil || !present {
				t.Fatalf("local recovered state = %#v, present=%t, err=%v", current, present, err)
			}
			if current.Source.SourceDigest != strings.Repeat("f", 64) || current.Catalog.Path != entry.After.Catalog.Path || !ownedFileAbsent(harness.service.catalogPath(before)) {
				t.Fatalf("local update did not converge exactly: current=%#v oldCatalogAbsent=%t", current, ownedFileAbsent(harness.service.catalogPath(before)))
			}
			assertRecoveryNativePackages(t, harness, current, true)
			assertRecoveryJournalCleared(t, harness)
		})
	}
}

func installRecoveryBundle(t *testing.T, harness lifecycleHarness, bundle string) installstate.Record {
	t.Helper()
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", bundle, "--yes")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install %s = %#v, %v", bundle, response.Result(), err)
	}
	record, present, err := harness.store.Load()
	if err != nil || !present {
		t.Fatalf("installed state = %#v, present=%t, err=%v", record, present, err)
	}
	return record
}

func assertRecoveryNativePackages(t *testing.T, harness lifecycleHarness, record installstate.Record, present bool) {
	t.Helper()
	if harness.native.marketplaces[record.MarketplaceID] != present {
		t.Fatalf("marketplace %s present=%t, want %t", record.MarketplaceID, harness.native.marketplaces[record.MarketplaceID], present)
	}
	for _, pluginID := range nativePluginIDs(record) {
		if harness.native.plugins[pluginID] != present || harness.native.enabled[pluginID] != present {
			t.Fatalf("native package %s = installed:%t enabled:%t, want %t", pluginID, harness.native.plugins[pluginID], harness.native.enabled[pluginID], present)
		}
	}
}

func assertRecoveryJournalCleared(t *testing.T, harness lifecycleHarness) {
	t.Helper()
	if marker, present, err := harness.store.LoadMarker(); err != nil || present {
		t.Fatalf("operation marker = %#v, present=%t, err=%v", marker, present, err)
	}
}
