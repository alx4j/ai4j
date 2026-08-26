package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestLifecycleClaudeUserLifecycleRetainsRollbackHistoryAndTombstone(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
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
	update := parseRequest[cli.UpdateRequest](t, "update", installationID, "--yes")
	response, err = harness.service.Update(context.Background(), update, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("update = %#v, %v", response.Result(), err)
	}
	sync := parseRequest[cli.SyncRequest](t, "sync", installationID, "--bundle", "minimal", "--yes")
	response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("sync = %#v, %v", response.Result(), err)
	}
	history := parseRequest[cli.HistoryRequest](t, "history", installationID)
	response, err = harness.service.History(context.Background(), history)
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || len(response.Data().(cli.HistoryData).Entries()) != 3 {
		t.Fatalf("history after sync = %#v, %v", response, err)
	}

	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", installationID, "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}
	record, present, err := harness.store.LoadByID(installationID)
	if err != nil || !present || record.Lifecycle != "archived" || len(record.History) != 4 {
		t.Fatalf("archived tombstone = %#v, %t, %v", record, present, err)
	}

	rollback := parseRequest[cli.RollbackRequest](t, "rollback", installationID, "--yes")
	response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("rollback uninstall = %#v, %v", response.Result(), err)
	}
	record, present, err = harness.store.LoadByID(installationID)
	if err != nil || !present || record.Lifecycle != "active" || record.Source.Commit != strings.Repeat("b", 40) {
		t.Fatalf("restored installation = %#v, %t, %v", record, present, err)
	}
}

func TestLifecycleSynchronizesAndRollsBackFlattenedPackageSet(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "full", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install flattened package set = %#v, %v", response.Result(), err)
	}
	record, present, err := harness.store.Load()
	if err != nil || !present {
		t.Fatalf("load flattened package set = %#v, %t, %v", record, present, err)
	}
	if record.Selection.RequestedBundle != "full" || !slices.Equal(record.Selection.ResolvedBundles, []string{"default", "full", "tools"}) || !slices.Equal(nativePackageIDs(record.Packages), []string{"ai4j-default", "ai4j-tools"}) {
		t.Fatalf("flattened installation state = %#v", record)
	}
	history, err := harness.store.LoadHistory(record.InstallationID)
	if err != nil || len(history) != 1 || !slices.Equal(nativeArtifactIDs(history[0].NativeArtifactsAfter), []string{"ai4j-default", "ai4j-tools"}) {
		t.Fatalf("flattened rollback material = %#v, %v", history, err)
	}
	for _, pluginID := range []string{"ai4j-default@" + record.MarketplaceID, "ai4j-tools@" + record.MarketplaceID} {
		if !harness.native.plugins[pluginID] {
			t.Fatalf("plugin %q was not installed", pluginID)
		}
	}

	sync := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "default", "--yes")
	response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("sync reduced package set = %#v, %v", response.Result(), err)
	}
	record, present, err = harness.store.LoadByID(record.InstallationID)
	if err != nil || !present || !slices.Equal(nativePackageIDs(record.Packages), []string{"ai4j-default"}) || harness.native.plugins["ai4j-tools@"+record.MarketplaceID] {
		t.Fatalf("reduced installation state = %#v, plugins=%#v, present=%t, err=%v", record, harness.native.plugins, present, err)
	}

	rollback := parseRequest[cli.RollbackRequest](t, "rollback", record.InstallationID, "--yes")
	response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("rollback flattened package set = %#v, %v", response.Result(), err)
	}
	record, present, err = harness.store.LoadByID(record.InstallationID)
	if err != nil || !present || !slices.Equal(nativePackageIDs(record.Packages), []string{"ai4j-default", "ai4j-tools"}) || !harness.native.plugins["ai4j-tools@"+record.MarketplaceID] {
		t.Fatalf("restored installation state = %#v, plugins=%#v, present=%t, err=%v", record, harness.native.plugins, present, err)
	}

	sync = parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "default", "--yes")
	response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("sync after retained rollback = %#v, %v", response.Result(), err)
	}
	record, present, err = harness.store.LoadByID(record.InstallationID)
	if err != nil || !present || !strings.HasPrefix(record.Catalog.Path, "state/catalogs/") || !slices.Equal(nativePackageIDs(record.Packages), []string{"ai4j-default"}) || harness.native.plugins["ai4j-tools@"+record.MarketplaceID] {
		t.Fatalf("post-rollback sync state = %#v, plugins=%#v, present=%t, err=%v", record, harness.native.plugins, present, err)
	}
}

func TestRemovalConflictsStopBeforeJournaling(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		policy    string
		conflict  string
		makeDrift func(*testing.T, lifecycleHarness, installstate.Record)
	}{
		{
			name: "uninstall replace-owned with missing plugin", command: "uninstall", policy: "replace-owned", conflict: "plugin_missing",
			makeDrift: func(_ *testing.T, harness lifecycleHarness, record installstate.Record) {
				delete(harness.native.plugins, nativePluginID(record.Packages[0], record.MarketplaceID))
			},
		},
		{
			name: "uninstall replace-owned with disabled plugin", command: "uninstall", policy: "replace-owned", conflict: "plugin_disabled",
			makeDrift: func(_ *testing.T, harness lifecycleHarness, record installstate.Record) {
				harness.native.enabled[nativePluginID(record.Packages[0], record.MarketplaceID)] = false
			},
		},
		{
			name: "uninstall keep with rules drift", command: "uninstall", policy: "keep", conflict: "rules_drift",
			makeDrift: func(t *testing.T, harness lifecycleHarness, record installstate.Record) {
				if err := os.WriteFile(harness.service.rulesPath(record), []byte("changed by user\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "rollback replace-owned with disabled plugin", command: "rollback", policy: "replace-owned", conflict: "plugin_disabled",
			makeDrift: func(_ *testing.T, harness lifecycleHarness, record installstate.Record) {
				harness.native.enabled[nativePluginID(record.Packages[0], record.MarketplaceID)] = false
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newLifecycleHarness(t)
			install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
			if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("install = %#v, %v", response.Result(), err)
			}
			record, _, _ := harness.store.Load()
			test.makeDrift(t, harness, record)
			stateBefore, err := os.ReadFile(harness.store.Path())
			if err != nil {
				t.Fatal(err)
			}
			beforeNativeCalls := len(harness.native.commands)
			arguments := []string{test.command, record.InstallationID, "--conflict-policy", test.policy, "--yes"}
			var response cli.Response
			if test.command == "uninstall" {
				request := parseRequest[cli.UninstallRequest](t, arguments...)
				response, err = harness.service.Uninstall(context.Background(), request, CommandIO{})
			} else {
				request := parseRequest[cli.RollbackRequest](t, arguments...)
				response, err = harness.service.Rollback(context.Background(), request, CommandIO{})
			}
			if err != nil {
				t.Fatal(err)
			}
			problems := response.Result().Errors()
			if response.Result().ExitCode() != result.ExitConflict || response.Result().Mutation() != result.MutationNotStarted || len(problems) == 0 || problems[0].Code() != test.conflict {
				t.Fatalf("blocked removal = %#v", response.Result())
			}
			stateAfter, err := os.ReadFile(harness.store.Path())
			if err != nil || !bytes.Equal(stateBefore, stateAfter) || len(harness.native.commands) != beforeNativeCalls {
				t.Fatalf("blocked removal mutated state: stateError=%v native=%d/%d", err, beforeNativeCalls, len(harness.native.commands))
			}
			if _, present, err := harness.store.LoadMarker(); err != nil || present {
				t.Fatalf("blocked removal marker = present:%t error:%v", present, err)
			}
		})
	}
}

func TestReplaceOwnedRejectsUnsafeOwnedFilesBeforeMutation(t *testing.T) {
	for _, command := range []string{"sync", "uninstall"} {
		t.Run(command, func(t *testing.T) {
			harness := newLifecycleHarness(t)
			install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
			if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("install = %#v, %v", response.Result(), err)
			}
			record, _, _ := harness.store.Load()
			rulesPath := harness.service.rulesPath(record)
			if err := os.Remove(rulesPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(rulesPath, 0o700); err != nil {
				t.Fatal(err)
			}
			stateBefore, err := os.ReadFile(harness.store.Path())
			if err != nil {
				t.Fatal(err)
			}
			beforeNativeCalls := len(harness.native.commands)
			var response cli.Response
			if command == "sync" {
				request := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "default", "--conflict-policy", "replace-owned", "--yes")
				response, err = harness.service.Sync(context.Background(), request, CommandIO{})
			} else {
				request := parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--conflict-policy", "replace-owned", "--yes")
				response, err = harness.service.Uninstall(context.Background(), request, CommandIO{})
			}
			if err != nil {
				t.Fatal(err)
			}
			problems := response.Result().Errors()
			if response.Result().ExitCode() != result.ExitConflict || response.Result().Mutation() != result.MutationNotStarted || len(problems) == 0 || problems[0].Code() != "rules_unsafe" {
				t.Fatalf("unsafe owned file = %#v", response.Result())
			}
			stateAfter, err := os.ReadFile(harness.store.Path())
			if err != nil || !bytes.Equal(stateBefore, stateAfter) || len(harness.native.commands) != beforeNativeCalls {
				t.Fatalf("unsafe owned file mutated state: stateError=%v native=%d/%d", err, beforeNativeCalls, len(harness.native.commands))
			}
			if info, err := os.Lstat(rulesPath); err != nil || !info.IsDir() {
				t.Fatalf("unsafe owned directory changed: info=%#v error=%v", info, err)
			}
			if _, present, err := harness.store.LoadMarker(); err != nil || present {
				t.Fatalf("unsafe owned file marker = present:%t error:%v", present, err)
			}
		})
	}
}

func TestLifecycleRollbackUsesLastOperationWhenHistoryTimestampsMatch(t *testing.T) {
	harness := newLifecycleHarness(t)
	harness.service.now = func() time.Time { return time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC) }
	harness.service.random = bytes.NewReader(append(append(append(bytes.Repeat([]byte{0xf0}, 12), bytes.Repeat([]byte{0x00}, 12)...), bytes.Repeat([]byte{0xa0}, 12)...), bytes.Repeat([]byte{0x10}, 12)...))

	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "full", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	for _, bundle := range []string{"default", "full"} {
		sync := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", bundle, "--yes")
		response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
		if err != nil || response.Result().ExitCode() != result.ExitSuccess {
			t.Fatalf("sync %s = %#v, %v", bundle, response.Result(), err)
		}
	}
	record, _, _ = harness.store.LoadByID(record.InstallationID)
	if record.LastOperation.ID != "operation-"+strings.Repeat("a0", 12) {
		t.Fatalf("last operation = %q", record.LastOperation.ID)
	}

	rollback := parseRequest[cli.RollbackRequest](t, "rollback", record.InstallationID, "--yes")
	response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("rollback = %#v, %v", response.Result(), err)
	}
	record, present, err := harness.store.LoadByID(record.InstallationID)
	if err != nil || !present || !slices.Equal(nativePackageIDs(record.Packages), []string{"ai4j-default"}) || harness.native.plugins["ai4j-tools@"+record.MarketplaceID] {
		t.Fatalf("rollback state = %#v, plugins=%#v, present=%t, err=%v", record, harness.native.plugins, present, err)
	}
}

func TestExpiredHistoryPreservesCurrentOperationWhenTimestampsMatch(t *testing.T) {
	timestamp := "2025-01-01T00:00:00Z"
	entries := []installstate.HistoryEntry{
		{OperationID: "operation-000000000000000000000000", Timestamp: timestamp},
		{OperationID: "operation-aaaaaaaaaaaaaaaaaaaaaaaa", Timestamp: timestamp},
		{OperationID: "operation-ffffffffffffffffffffffff", Timestamp: timestamp},
	}
	ids := selectedHistoryIDs(entries, cli.HistoryPurgeExpired, domain.OperationID{}, entries[1].OperationID, time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC))
	if !slices.Equal(ids, []string{entries[0].OperationID, entries[2].OperationID}) {
		t.Fatalf("expired history ids = %#v", ids)
	}
}

func TestLifecycleCatalogPathChangePreflightsDestinationBeforeNativeMutation(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "full", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	sync := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "default", "--yes")
	response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("sync = %#v, %v", response.Result(), err)
	}
	rollback := parseRequest[cli.RollbackRequest](t, "rollback", record.InstallationID, "--yes")
	response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("rollback = %#v, %v", response.Result(), err)
	}
	record, _, _ = harness.store.LoadByID(record.InstallationID)
	destination := filepath.Join(harness.store.DataRoot(), "state", "catalogs", record.InstallationID, ".claude-plugin", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("occupied\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commandsBefore := len(harness.native.commands)

	sync = parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "default", "--yes")
	response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitConflict || response.Result().Mutation() != result.MutationNotStarted {
		t.Fatalf("occupied destination = %#v, %v", response.Result(), err)
	}
	if len(harness.native.commands) != commandsBefore {
		t.Fatalf("native commands ran before destination conflict: before=%d after=%d", commandsBefore, len(harness.native.commands))
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
		t.Fatalf("operation marker after preflight conflict: present=%t err=%v", present, markerErr)
	}
	if !harness.native.plugins["ai4j-default@"+record.MarketplaceID] || !harness.native.plugins["ai4j-tools@"+record.MarketplaceID] {
		t.Fatalf("working plugins changed after destination conflict: %#v", harness.native.plugins)
	}
}

func TestWriteLocalBundleRejectsTamperedRetainedPackageContent(t *testing.T) {
	harness := newLifecycleHarness(t)
	checkout := filepath.Join(harness.service.home, "checkout")
	harness.validator.localDigest = strings.Repeat("e", 64)
	install := parseRequest[cli.InstallRequest](t, "install", "--source", checkout, "--target", "claude", "--scope", "user", "--bundle", "default", "--expected-source-digest", harness.validator.localDigest, "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	entry, present, err := harness.store.LoadHistoryEntry(record.InstallationID, record.LastOperation.ID)
	if err != nil || !present {
		t.Fatalf("history = %#v, present=%t, err=%v", entry, present, err)
	}
	bundleRoot := filepath.Dir(filepath.Dir(harness.service.catalogPath(record)))
	tampered := filepath.Join(bundleRoot, "plugins", "ai4j-default", ".mcp.json")
	if err := os.WriteFile(tampered, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := harness.service.writeLocalBundle(record, entry.CatalogAfter, entry.NativeArtifactsAfter); err == nil {
		t.Fatal("tampered retained package content was accepted")
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.WriteFile(tampered, []byte("{\"mcpServers\":{\"claude-tools\":{\"type\":\"stdio\",\"command\":\"claude\",\"args\":[\"mcp\",\"serve\"],\"env\":{\"AI4J_TOKEN\":\"${AI4J_TOKEN}\"}}}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(bundleRoot, "plugins", "ai4j-default", "scripts", "check.sh")
	if err := os.Chmod(script, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := harness.service.writeLocalBundle(record, entry.CatalogAfter, entry.NativeArtifactsAfter); err == nil {
		t.Fatal("tampered retained package executable mode was accepted")
	}
}

func TestTransitionVerificationRequiresRemovedPackagesToBeAbsent(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "full", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install flattened package set = %#v, %v", response.Result(), err)
	}
	before, present, err := harness.store.Load()
	if err != nil || !present {
		t.Fatalf("load flattened package set = %#v, %t, %v", before, present, err)
	}
	desired := cloneRecord(before)
	desired.Packages = slices.Clone(before.Packages[:1])
	desired.NativeResources = nativeResources(desired.Packages, desired.MarketplaceID)

	if harness.service.verifyTransition(context.Background(), desired, &before) == nil {
		t.Fatal("transition verified while a removed package remained installed")
	}
	removedPluginID := nativePluginID(before.Packages[1], before.MarketplaceID)
	delete(harness.native.plugins, removedPluginID)
	delete(harness.native.enabled, removedPluginID)
	if err := harness.service.verifyTransition(context.Background(), desired, &before); err != nil {
		t.Fatalf("transition with removed package absent = %v", err)
	}
}

func nativePackageIDs(packages []installstate.NativePackage) []string {
	ids := make([]string, len(packages))
	for index, pkg := range packages {
		ids[index] = pkg.ID
	}
	return ids
}

func nativeArtifactIDs(artifacts []installstate.NativeArtifact) []string {
	ids := make([]string, len(artifacts))
	for index, artifact := range artifacts {
		ids[index] = artifact.PackageID
	}
	return ids
}

func hasWarning(response cli.Response, code string) bool {
	for _, warning := range response.Result().Warnings() {
		if warning.Code() == code {
			return true
		}
	}
	return false
}

func TestLifecycleUninstallRejectsArchivedInstallation(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	records, err := harness.store.LoadAll()
	if err != nil || len(records) != 1 {
		t.Fatalf("installed records = %#v, %v", records, err)
	}
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", records[0].InstallationID, "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("initial uninstall = %#v, %v", response.Result(), err)
	}
	nativeCalls := len(harness.native.commands)

	for _, arguments := range [][]string{{"--dry-run"}, {"--yes"}} {
		request := parseRequest[cli.UninstallRequest](t, append([]string{"uninstall", records[0].InstallationID}, arguments...)...)
		response, err = harness.service.Uninstall(context.Background(), request, CommandIO{})
		if err != nil {
			t.Fatal(err)
		}
		problems := response.Result().Errors()
		if response.Result().ExitCode() != result.ExitConflict || len(problems) != 1 || problems[0].Code() != "installation_not_active" {
			t.Fatalf("repeat uninstall = %#v", response.Result())
		}
		if len(harness.native.commands) != nativeCalls {
			t.Fatal("repeat uninstall invoked native mutation")
		}
	}
}

func TestLifecyclePersistsCredentialFreeSSHSourceTransport(t *testing.T) {
	t.Parallel()
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--repo", "git@github.com:alx4j/ai4j.git", "--ref", "main", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")

	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("SSH install = %#v, %v", response.Result(), err)
	}
	records, err := harness.store.LoadAll()
	if err != nil || len(records) != 1 || records[0].Source.Transport != domain.SSHGitTransport().String() {
		t.Fatalf("stored SSH source = %#v, error=%v", records, err)
	}
	options, err := updateSourceOptions(records[0])
	if err != nil || options.Repository() != "git@github.com:alx4j/ai4j.git" {
		t.Fatalf("reconstructed SSH source = %q, error=%v", options.Repository(), err)
	}
}

func TestLifecycleDryRunsReturnPlansWithoutLockingPromptingOrMutation(t *testing.T) {
	harness := newLifecycleHarness(t)
	acquireCalls := 0
	harness.service.acquire = func(context.Context) (func() error, error) {
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
	installDryRun := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--dry-run")
	response, err := harness.service.Install(context.Background(), installDryRun, commandIO)
	assertDryRun(cli.CommandInstall, response, err, prompt, 0)
	if records, loadErr := harness.store.LoadAll(); loadErr != nil || len(records) != 0 {
		t.Fatalf("install dry run changed state: %#v, %v", records, loadErr)
	}

	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
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
			request := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--dry-run")
			return harness.service.Update(context.Background(), request, io)
		}},
		{cli.CommandSync, func(io CommandIO) (cli.Response, error) {
			request := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "minimal", "--dry-run")
			return harness.service.Sync(context.Background(), request, io)
		}},
		{cli.CommandRollback, func(io CommandIO) (cli.Response, error) {
			request := parseRequest[cli.RollbackRequest](t, "rollback", record.InstallationID, "--dry-run")
			return harness.service.Rollback(context.Background(), request, io)
		}},
		{cli.CommandUninstall, func(io CommandIO) (cli.Response, error) {
			request := parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--dry-run")
			return harness.service.Uninstall(context.Background(), request, io)
		}},
		{cli.CommandHistoryPurge, func(io CommandIO) (cli.Response, error) {
			request := parseRequest[cli.HistoryPurgeRequest](t, "history", "purge", record.InstallationID, "--operation", history[0].OperationID, "--dry-run")
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

func TestLifecycleWindowsClaudeUserJourneyUsesWindowsStateAndHost(t *testing.T) {
	harness := newLifecycleHarness(t)
	dataRoot := filepath.Join(t.TempDir(), "AI4J")
	store, err := installstate.NewStoreAt(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	harness.store = store
	harness.service.state = store
	harness.service.build = buildinfo.New(buildinfo.Inputs{Version: "0.0.0-dev", TargetOS: "windows", TargetArch: "amd64"})

	dryRun := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--dry-run")
	if response, err := harness.service.Install(context.Background(), dryRun, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install dry run = %#v, %v", response.Result(), err)
	}
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
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
	update := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--yes")
	if response, err := harness.service.Update(context.Background(), update, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("update = %#v, %v", response.Result(), err)
	}
	sync := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "minimal", "--yes")
	if response, err := harness.service.Sync(context.Background(), sync, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("sync = %#v, %v", response.Result(), err)
	}
	status := statusService{validation: harness.validator, state: store, home: harness.service.home}
	statusRequest := parseRequest[cli.StatusRequest](t, "status", record.InstallationID)
	if response, err := status.Status(context.Background(), statusRequest); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("status = %#v, %v", response.Result(), err)
	}
	listRequest := parseRequest[cli.ListRequest](t, "list")
	if response, err := status.List(context.Background(), listRequest); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("list = %#v, %v", response.Result(), err)
	}
	history := parseRequest[cli.HistoryRequest](t, "history", record.InstallationID)
	if response, err := harness.service.History(context.Background(), history); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("history = %#v, %v", response.Result(), err)
	}
	rollback := parseRequest[cli.RollbackRequest](t, "rollback", record.InstallationID, "--yes")
	if response, err := harness.service.Rollback(context.Background(), rollback, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("rollback = %#v, %v", response.Result(), err)
	}
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--yes")
	if response, err := harness.service.Uninstall(context.Background(), uninstall, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}
}

func TestLifecycleClaudeProjectLocalJourneyKeepsRulesGitLocallyExcluded(t *testing.T) {
	harness := newLifecycleHarness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(project, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte("# existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-local", "--project", project, "--bundle", "default", "--yes")
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
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--yes")
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

func TestLifecycleProjectLocalSyncTracksRuleExclusionOwnership(t *testing.T) {
	harness := newLifecycleHarness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(project, ".git", "info", "exclude")
	want := []byte("# existing\r\n/other\r\n\r\n")
	if err := os.WriteFile(exclude, want, 0o600); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-local", "--project", project, "--bundle", "tools", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("tools install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	if contents, err := os.ReadFile(exclude); err != nil || !bytes.Equal(contents, want) {
		t.Fatalf("tools exclusion = %q, %v", contents, err)
	}

	toReview := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "review", "--yes")
	response, err = harness.service.Sync(context.Background(), toReview, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("tools -> review = %#v, %v", response.Result(), err)
	}
	review, _, _ := harness.store.LoadByID(record.InstallationID)
	contents, err := os.ReadFile(exclude)
	if err != nil || !bytes.Contains(contents, []byte(projectExcludeLine(review)+"\r\n")) || !bytes.HasPrefix(contents, want) {
		t.Fatalf("review exclusion = %q, %v", contents, err)
	}

	toTools := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "tools", "--yes")
	response, err = harness.service.Sync(context.Background(), toTools, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("review -> tools = %#v, %v", response.Result(), err)
	}
	if contents, err := os.ReadFile(exclude); err != nil || !bytes.Equal(contents, want) {
		t.Fatalf("restored exclusion = %q, %v", contents, err)
	}
	if _, err := os.Stat(harness.service.rulesPath(review)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("review rules remain: %v", err)
	}
}

func TestLifecycleProjectLocalSyncRecoversExclusionTransition(t *testing.T) {
	for _, test := range []struct {
		name, beforeBundle, afterBundle string
		mutate                          func(context.Context, *lifecycleService, *installstate.Record, *installstate.Record) error
		wantPresent                     bool
		markerMissing                   bool
	}{
		{name: "discard interrupted add", beforeBundle: "tools", afterBundle: "review", mutate: func(ctx context.Context, service *lifecycleService, _ *installstate.Record, desired *installstate.Record) error {
			return service.ensureProjectLocalExclusion(ctx, *desired)
		}},
		{name: "compensate interrupted removal", beforeBundle: "review", afterBundle: "tools", wantPresent: true, mutate: func(ctx context.Context, service *lifecycleService, before, _ *installstate.Record) error {
			return service.removeProjectLocalExclusion(ctx, *before)
		}},
		{name: "clean orphaned interrupted add", beforeBundle: "tools", afterBundle: "review", markerMissing: true, mutate: func(ctx context.Context, service *lifecycleService, _ *installstate.Record, desired *installstate.Record) error {
			return service.ensureProjectLocalExclusion(ctx, *desired)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newLifecycleHarness(t)
			project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
			if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o700); err != nil {
				t.Fatal(err)
			}
			exclude := filepath.Join(project, ".git", "info", "exclude")
			unrelated := []byte("# existing\r\n/other\r\n\r\n")
			if err := os.WriteFile(exclude, unrelated, 0o600); err != nil {
				t.Fatal(err)
			}
			harness.native.projectRoot = project
			install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-local", "--project", project, "--bundle", test.beforeBundle, "--yes")
			response, err := harness.service.Install(context.Background(), install, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("install = %#v, %v", response.Result(), err)
			}
			before, _, _ := harness.store.Load()
			sync := parseRequest[cli.SyncRequest](t, "sync", before.InstallationID, "--bundle", test.afterBundle, "--yes")
			execution, _, stop, err := harness.service.prepareSync(context.Background(), sync.InstallationID(), sync.Selection(), sync.AllowDirty(), cli.ConflictFail)
			if err != nil || stop {
				t.Fatalf("prepare sync: stop=%t err=%v", stop, err)
			}
			desired, _, _ := stageInterrupted(t, harness, execution, "operation-project-local-exclusion-transition")
			if err := test.mutate(context.Background(), harness.service, &before, desired); err != nil {
				t.Fatal(err)
			}
			if test.markerMissing {
				if err := harness.store.DeleteMarker(); err != nil {
					t.Fatal(err)
				}
			}
			if recovered, err := harness.service.reconcileInterrupted(context.Background()); err != nil || !recovered {
				t.Fatalf("recovery = %t, %v", recovered, err)
			}
			contents, err := os.ReadFile(exclude)
			linePresent := before.Rules != (installstate.OwnedFile{}) && bytes.Contains(contents, []byte(projectExcludeLine(before))) ||
				desired.Rules != (installstate.OwnedFile{}) && bytes.Contains(contents, []byte(projectExcludeLine(*desired)))
			if err != nil || linePresent != test.wantPresent || !bytes.HasPrefix(contents, unrelated) {
				t.Fatalf("exclusion = %q, present=%t, err=%v", contents, linePresent, err)
			}
		})
	}
}

func TestLifecycleProjectLocalRejectsPreexistingOwnedExclusion(t *testing.T) {
	harness := newLifecycleHarness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-local", "--project", project, "--bundle", "default", "--yes")
	selection := harness.validator.SelectLifecycle(context.Background(), request.Source(), request.Selection().Bundle())
	record, _, err := harness.service.recordForSelection(selection, request.Source(), request.Selection(), installationIDFor(selection, request.Scope(), project), request.Scope(), project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".git", "info", "exclude"), []byte(projectExcludeLine(record)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().Failure() != result.FailureConflict {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
		t.Fatalf("marker = present:%t err=%v", present, markerErr)
	}
}

func TestLifecycleProjectLocalRejectsExclusionAppearingAfterPlanning(t *testing.T) {
	harness := newLifecycleHarness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-local", "--project", project, "--bundle", "default", "--yes")
	execution, _, stop, err := harness.service.prepareInstall(context.Background(), request.Source(), request.Target(), request.Scope(), project, true, request.Selection(), request.InstallationID(), request.HasInstallationID(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("prepare install: stop=%t err=%v", stop, err)
	}
	line := projectExcludeLine(*execution.desired) + "\n"
	if err := os.WriteFile(filepath.Join(project, ".git", "info", "exclude"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := harness.service.applyTransition(context.Background(), nil, execution.desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifacts, cli.ConflictFail, false); err == nil {
		t.Fatal("apply accepted an exclusion that appeared after planning")
	}
	contents, readErr := os.ReadFile(filepath.Join(project, ".git", "info", "exclude"))
	if readErr != nil || string(contents) != line {
		t.Fatalf("external exclusion = %q, %v", contents, readErr)
	}
	if len(harness.native.marketplaces) != 0 || len(harness.native.plugins) != 0 {
		t.Fatalf("native target mutated: %#v", harness.native)
	}
}

func TestLifecycleProjectLocalRollbackRecoveryRemovesFreshExclusion(t *testing.T) {
	harness := newLifecycleHarness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(project, ".git", "info", "exclude")
	want := []byte("# existing\r\n/other\r\n\r\n")
	if err := os.WriteFile(exclude, want, 0o600); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-local", "--project", project, "--bundle", "default", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}
	rollback := parseRequest[cli.RollbackRequest](t, "rollback", record.InstallationID, "--yes")
	execution, _, stop, err := harness.service.prepareRollback(context.Background(), rollback.InstallationID(), rollback.OperationID(), rollback.HasOperationID(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("prepare rollback: stop=%t err=%v", stop, err)
	}
	desired, _, _ := stageInterrupted(t, harness, execution, "operation-project-local-rollback-exclusion")
	if err := harness.service.ensureProjectLocalExclusion(context.Background(), *desired); err != nil {
		t.Fatal(err)
	}
	if recovered, err := harness.service.reconcileInterrupted(context.Background()); err != nil || !recovered {
		t.Fatalf("recovery = %t, %v", recovered, err)
	}
	contents, err := os.ReadFile(exclude)
	if err != nil || !bytes.Equal(contents, want) {
		t.Fatalf("Git-local exclusion = %q, %v", contents, err)
	}
}

func TestLifecycleProjectLocalRollbackRejectsPreexistingExclusion(t *testing.T) {
	harness := newLifecycleHarness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-local", "--project", project, "--bundle", "default", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}
	exclude := filepath.Join(project, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte(projectExcludeLine(record)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rollback := parseRequest[cli.RollbackRequest](t, "rollback", record.InstallationID, "--yes")
	response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{})
	if err != nil || response.Result().Failure() != result.FailureConflict {
		t.Fatalf("rollback = %#v, %v", response.Result(), err)
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
		t.Fatalf("marker = present:%t err=%v", present, markerErr)
	}
}

func TestLifecycleProjectLocalRollbackRejectsExclusionAppearingAfterPlanning(t *testing.T) {
	harness := newLifecycleHarness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-local", "--project", project, "--bundle", "default", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}
	rollback := parseRequest[cli.RollbackRequest](t, "rollback", record.InstallationID, "--yes")
	execution, _, stop, err := harness.service.prepareRollback(context.Background(), rollback.InstallationID(), rollback.OperationID(), rollback.HasOperationID(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("prepare rollback: stop=%t err=%v", stop, err)
	}
	exclude := filepath.Join(project, ".git", "info", "exclude")
	line := []byte(projectExcludeLine(*execution.desired) + "\n")
	if err := os.WriteFile(exclude, line, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := harness.service.applyTransition(context.Background(), execution.before, execution.desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifacts, cli.ConflictFail, true); err == nil {
		t.Fatal("rollback accepted an exclusion that appeared after planning")
	}
	contents, readErr := os.ReadFile(exclude)
	if readErr != nil || !bytes.Equal(contents, line) {
		t.Fatalf("external exclusion = %q, %v", contents, readErr)
	}
	if len(harness.native.marketplaces) != 0 || len(harness.native.plugins) != 0 {
		t.Fatalf("native target mutated: %#v", harness.native)
	}
}

func TestLifecycleClaudeProjectSharedJourneyPreservesUnrelatedSettings(t *testing.T) {
	harness := newLifecycleHarness(t)
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
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-shared", "--project", project, "--bundle", "default", "--yes")
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
	report := harness.validator.SelectLifecycle(context.Background(), install.Source(), install.Selection().Bundle())
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
	update := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--yes")
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
	status := statusService{validation: harness.validator, state: harness.store, home: harness.service.home}
	statusRequest := parseRequest[cli.StatusRequest](t, "status", record.InstallationID)
	harness.native.marketplaces[record.MarketplaceID] = false
	if response, err = status.Status(context.Background(), statusRequest); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared status = %#v, %v", response.Result(), err)
	}
	native := response.Data().(cli.StatusData).NativeState()
	if native.Registration() != cli.NativeRegistered || native.Installation() != cli.NativeInstalled || native.Enablement() != cli.NativeEnabled || native.Activation() != cli.NativeActivationNotObservable {
		t.Fatalf("project-shared declaration/current-user status = %#v", native)
	}
	harness.native.marketplaces[record.MarketplaceID] = true
	rollback := parseRequest[cli.RollbackRequest](t, "rollback", record.InstallationID, "--yes")
	if response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared rollback = %#v, %v", response.Result(), err)
	}
	settings, _ = os.ReadFile(settingsPath)
	if !bytes.Contains(settings, []byte(strings.Repeat("a", 40))) || bytes.Contains(settings, []byte(strings.Repeat("b", 40))) {
		t.Fatalf("project-shared rollback did not restore exact commit: %s", settings)
	}
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--yes")
	if response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared uninstall = %#v, %v", response.Result(), err)
	}
	settings, err = os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(settings, original) {
		t.Fatalf("project-shared structural inverse = %s, want %s, %v", settings, original, err)
	}
	rollback = parseRequest[cli.RollbackRequest](t, "rollback", record.InstallationID, "--yes")
	if response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("project-shared uninstall rollback = %#v, %v", response.Result(), err)
	}
	settings, _ = os.ReadFile(settingsPath)
	if !bytes.Contains(settings, []byte(strings.Repeat("a", 40))) {
		t.Fatalf("project-shared uninstall rollback did not restore declaration: %s", settings)
	}
	uninstall = parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--yes")
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

func TestLifecycleUserLifecycleUsesEffectiveClaudeConfigurationRoot(t *testing.T) {
	harness := newLifecycleHarness(t)
	custom := filepath.Join(harness.service.home, "custom-claude")
	if err := os.Mkdir(custom, 0o700); err != nil {
		t.Fatal(err)
	}
	harness.service.claudeRoot = custom
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
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

func TestLifecycleHistoryPurgeDoesNotTouchActiveTargetAndRemovesFinalArchivedTombstone(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	records, _ := harness.store.LoadAll()
	id := records[0].InstallationID
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", id, "--yes")
	if response, err := harness.service.Uninstall(context.Background(), uninstall, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}
	commandCount := len(harness.native.commands)
	purge := parseRequest[cli.HistoryPurgeRequest](t, "history", "purge", id, "--all", "--yes")
	response, err := harness.service.HistoryPurge(context.Background(), purge, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || len(harness.native.commands) != commandCount {
		t.Fatalf("purge = %#v, commands=%d/%d, %v", response.Result(), len(harness.native.commands), commandCount, err)
	}
	if _, present, err := harness.store.LoadByID(id); err != nil || present {
		t.Fatalf("purged tombstone = present:%t err:%v", present, err)
	}
}

func TestLifecycleIndependentInstallationsAndOwnedConflictPolicies(t *testing.T) {
	harness := newLifecycleHarness(t)
	first := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	if response, err := harness.service.Install(context.Background(), first, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("first install = %#v, %v", response.Result(), err)
	}
	second := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "other", "--yes")
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
	fail := parseRequest[cli.UpdateRequest](t, "update", firstRecord.InstallationID, "--yes")
	response, err := harness.service.Update(context.Background(), fail, CommandIO{})
	if err != nil || response.Result().Failure() != result.FailureConflict {
		t.Fatalf("default conflict policy = %#v, %v", response.Result(), err)
	}
	keep := parseRequest[cli.UpdateRequest](t, "update", firstRecord.InstallationID, "--conflict-policy", "keep", "--yes")
	response, err = harness.service.Update(context.Background(), keep, CommandIO{})
	if err != nil || response.Result().Failure() != result.FailureConflict || response.Result().Mutation() != result.MutationNotStarted {
		t.Fatalf("keep policy = %#v, %v", response.Result(), err)
	}
	if contents, err := os.ReadFile(rulesPath); err != nil || string(contents) != "USER MODIFICATION\n" {
		t.Fatalf("kept rules = %q, %v", contents, err)
	}
	replace := parseRequest[cli.SyncRequest](t, "sync", firstRecord.InstallationID, "--bundle", "default", "--conflict-policy", "replace-owned", "--yes")
	response, err = harness.service.Sync(context.Background(), replace, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("replace-owned policy = %#v, %v", response.Result(), err)
	}
	if contents, err := os.ReadFile(rulesPath); err != nil || string(contents) != "rules-default\n" {
		t.Fatalf("replaced rules = %q, %v", contents, err)
	}
}

func TestLifecycleCatalogDriftIsBlockedByKeepAndDisclosedForReplacement(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	if err := os.WriteFile(harness.service.catalogPath(record), []byte("modified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commandsBefore := len(harness.native.commands)

	keep := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "default", "--conflict-policy", "keep", "--yes")
	response, err = harness.service.Sync(context.Background(), keep, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitConflict || response.Result().Mutation() != result.MutationNotStarted {
		t.Fatalf("keep catalog drift = %#v, %v", response.Result(), err)
	}
	if len(harness.native.commands) != commandsBefore {
		t.Fatalf("keep catalog drift ran native commands: before=%d after=%d", commandsBefore, len(harness.native.commands))
	}

	preview := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "default", "--conflict-policy", "replace-owned", "--dry-run")
	response, err = harness.service.Sync(context.Background(), preview, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("replacement preview = %#v, %v", response.Result(), err)
	}
	kinds := map[cli.ActionKind]bool{}
	for _, action := range response.Data().(cli.PlanData).Actions() {
		kinds[action.Kind()] = true
	}
	for _, kind := range []cli.ActionKind{cli.ActionUninstallPlugin, cli.ActionWriteCatalog, cli.ActionRefreshMarketplace, cli.ActionInstallPlugin} {
		if !kinds[kind] {
			t.Fatalf("replacement preview does not disclose %q: %#v", kind, kinds)
		}
	}

	replace := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "default", "--conflict-policy", "replace-owned", "--yes")
	response, err = harness.service.Sync(context.Background(), replace, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("replace catalog drift = %#v, %v", response.Result(), err)
	}
	record, _, _ = harness.store.LoadByID(record.InstallationID)
	if inspectFileDrift(harness.service.catalogPath(record), record.Catalog.Checksum) != cli.DriftUnchanged {
		t.Fatal("catalog drift was not repaired")
	}
}

func TestLifecycleReplaceOwnedRejectsNativeDriftBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		name  string
		drift func(*lifecycleNativeRunner, installstate.Record)
	}{
		{name: "marketplace missing", drift: func(native *lifecycleNativeRunner, record installstate.Record) {
			delete(native.marketplaces, record.MarketplaceID)
		}},
		{name: "plugin missing", drift: func(native *lifecycleNativeRunner, record installstate.Record) {
			delete(native.plugins, nativePluginIDs(record)[0])
		}},
		{name: "plugin disabled", drift: func(native *lifecycleNativeRunner, record installstate.Record) {
			delete(native.enabled, nativePluginIDs(record)[0])
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newLifecycleHarness(t)
			install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
			response, err := harness.service.Install(context.Background(), install, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("install = %#v, %v", response.Result(), err)
			}
			record, _, _ := harness.store.Load()
			test.drift(harness.native, record)
			commandsBefore := len(harness.native.commands)
			sync := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "default", "--conflict-policy", "replace-owned", "--yes")
			response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitConflict || response.Result().Mutation() != result.MutationNotStarted {
				t.Fatalf("native drift = %#v, %v", response.Result(), err)
			}
			if len(harness.native.commands) != commandsBefore {
				t.Fatalf("native drift ran commands: before=%d after=%d", commandsBefore, len(harness.native.commands))
			}
			if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
				t.Fatalf("operation marker after native drift: present=%t err=%v", present, markerErr)
			}
		})
	}
}

func TestLifecycleNativeUninstallFailureDoesNotRemoveMarketplace(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	harness.native.failPrefix = []string{"plugin", "uninstall"}
	harness.native.failCount = 1
	commandsBefore := len(harness.native.commands)

	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", record.InstallationID, "--conflict-policy", "keep", "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().Failure() != result.FailureRecovery {
		t.Fatalf("failed native uninstall = %#v, %v", response.Result(), err)
	}
	commands := harness.native.commands[commandsBefore:]
	if len(commands) != 1 || len(commands[0]) < 2 || !slices.Equal(commands[0][:2], []string{"plugin", "uninstall"}) {
		t.Fatalf("commands after native failure = %#v", commands)
	}
	if !harness.native.marketplaces[record.MarketplaceID] || !harness.native.plugins[nativePluginIDs(record)[0]] {
		t.Fatalf("working native state was removed after uninstall failure: marketplaces=%#v plugins=%#v", harness.native.marketplaces, harness.native.plugins)
	}
}

func TestLifecycleUpdateChangesExplicitGitHubSourceWithoutChangingInstallationIdentity(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	records, _ := harness.store.LoadAll()
	id := records[0].InstallationID
	commit := strings.Repeat("a", 40)
	update := parseRequest[cli.UpdateRequest](t, "update", id, "--repo", "example/toolkit", "--ref", "main", "--expected-commit", commit, "--yes")
	response, err := harness.service.Update(context.Background(), update, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("source change = %#v, %v", response.Result(), err)
	}
	record, present, err := harness.store.LoadByID(id)
	if err != nil || !present || record.InstallationID != id || record.Source.Repository != "github.com/example/toolkit" || record.Source.Selection != "explicit" || len(record.History) != 2 {
		t.Fatalf("changed record = %#v, present=%t, err=%v", record, present, err)
	}
}

func TestLifecycleLocalDevelopmentInstallUpdateAndSyncUseImmutableBackingBundle(t *testing.T) {
	harness := newLifecycleHarness(t)
	checkout := filepath.Join(harness.service.home, "checkout")
	harness.validator.localDigest = strings.Repeat("e", 64)
	install := parseRequest[cli.InstallRequest](t, "install", "--source", checkout, "--target", "claude", "--scope", "user", "--bundle", "default", "--expected-source-digest", harness.validator.localDigest, "--yes")
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
	for _, path := range []string{filepath.Join(bundleRoot, ".ai4j-bundle.json"), filepath.Join(bundleRoot, "plugins", "ai4j-default", ".claude-plugin", "plugin.json")} {
		if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
			t.Fatalf("bundle file %s = %#v, %v", path, info, statErr)
		}
	}
	firstBundle := record.Source.BundleDigest
	harness.validator.localDigest = strings.Repeat("f", 64)
	update := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--allow-dirty", "--expected-source-digest", harness.validator.localDigest, "--yes")
	response, err = harness.service.Update(context.Background(), update, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("local update = %#v, %v", response.Result(), err)
	}
	record, _, _ = harness.store.LoadByID(record.InstallationID)
	if record.Source.BundleDigest == firstBundle || record.Source.SourceDigest != harness.validator.localDigest || len(record.History) != 2 {
		t.Fatalf("updated local state = %#v", record)
	}
	sync := parseRequest[cli.SyncRequest](t, "sync", record.InstallationID, "--bundle", "minimal", "--allow-dirty", "--expected-source-digest", harness.validator.localDigest, "--yes")
	response, err = harness.service.Sync(context.Background(), sync, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("local sync = %#v, %v", response.Result(), err)
	}
}

func TestLifecycleAutomaticallyReconcilesUnambiguousInterruptedOperations(t *testing.T) {
	for _, test := range []struct {
		name      string
		advance   func(*testing.T, lifecycleHarness, lifecycleExecution, *installstate.Record, installstate.HistoryEntry)
		wantState result.Status
	}{
		{name: "discard prepared operation", wantState: result.StatusOK},
		{name: "discard marker before journal", wantState: result.StatusOK, advance: func(t *testing.T, harness lifecycleHarness, _ lifecycleExecution, _ *installstate.Record, entry installstate.HistoryEntry) {
			t.Helper()
			if err := harness.store.DeleteHistory(entry.InstallationID, []string{entry.OperationID}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "roll forward verified target", wantState: result.StatusNoChange, advance: func(t *testing.T, harness lifecycleHarness, execution lifecycleExecution, desired *installstate.Record, _ installstate.HistoryEntry) {
			t.Helper()
			if err := harness.service.applyTransition(context.Background(), execution.before, desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifacts, cli.ConflictFail, false); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "finish committed cleanup", wantState: result.StatusNoChange, advance: func(t *testing.T, harness lifecycleHarness, execution lifecycleExecution, desired *installstate.Record, entry installstate.HistoryEntry) {
			t.Helper()
			if err := harness.service.applyTransition(context.Background(), execution.before, desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifacts, cli.ConflictFail, false); err != nil {
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
			harness := newLifecycleHarness(t)
			install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
			response, err := harness.service.Install(context.Background(), install, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("install = %#v, %v", response.Result(), err)
			}
			record, _, _ := harness.store.Load()
			harness.validator.update = true
			request := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--yes")
			execution, _, stop, err := harness.service.prepareUpdate(context.Background(), request.InstallationID(), request.Source(), cli.ConflictFail)
			if err != nil || stop {
				t.Fatalf("update preparation stopped: %v", err)
			}
			desired, entry, marker := stageInterrupted(t, harness, execution, "operation-recovery0001")
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

func TestLifecycleAutomaticRecoveryKeepsAmbiguousStateFailClosed(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	harness.validator.update = true
	request := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--yes")
	execution, _, stop, err := harness.service.prepareUpdate(context.Background(), request.InstallationID(), request.Source(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("update preparation stopped: %v", err)
	}
	_, _, _ = stageInterrupted(t, harness, execution, "operation-recovery0002")
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

func TestLifecycleAutomaticRecoveryRejectsMissingJournalAfterTargetMutation(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	harness.validator.update = true
	request := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--yes")
	execution, _, stop, err := harness.service.prepareUpdate(context.Background(), request.InstallationID(), request.Source(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("update preparation stopped: %v", err)
	}
	desired, entry, _ := stageInterrupted(t, harness, execution, "operation-recovery0003")
	if err := harness.service.applyTransition(context.Background(), execution.before, desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifacts, cli.ConflictFail, false); err != nil {
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

func TestLifecycleAutomaticRecoveryClearsFreshInstallPreparedBeforeMutation(t *testing.T) {
	harness := newLifecycleHarness(t)
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	project, hasProject := request.Project()
	execution, _, stop, err := harness.service.prepareInstall(
		context.Background(), request.Source(), request.Target(), request.Scope(), project, hasProject,
		request.Selection(), request.InstallationID(), request.HasInstallationID(), cli.ConflictFail,
	)
	if err != nil || stop {
		t.Fatalf("install preparation stopped: %v", err)
	}
	desired, entry, _ := stageInterrupted(t, harness, execution, "operation-recovery0004")

	recovered, err := harness.service.reconcileInterrupted(context.Background())
	if err != nil || !recovered {
		t.Fatalf("prepared install recovery = %t, %v", recovered, err)
	}
	if _, present, err := harness.store.LoadMarker(); err != nil || present {
		t.Fatalf("operation marker = present:%t err:%v", present, err)
	}
	if _, present, err := harness.store.LoadOperationHistory(entry.InstallationID, entry.OperationID); err != nil || present {
		t.Fatalf("staged history = present:%t err:%v", present, err)
	}
	if _, present, err := harness.store.LoadByID(desired.InstallationID); err != nil || present {
		t.Fatalf("installation state = present:%t err:%v", present, err)
	}
	if len(harness.native.commands) != 0 || len(harness.native.marketplaces) != 0 || len(harness.native.plugins) != 0 {
		t.Fatalf("native target changed before recovery: %#v", harness.native)
	}
}

func TestLifecycleAutomaticRecoveryRemovesFreshProjectLocalExclusion(t *testing.T) {
	harness := newLifecycleHarness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(project, ".git", "info", "exclude")
	want := []byte("# existing\r\n/other\r\n\r\n")
	if err := os.WriteFile(exclude, want, 0o600); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-local", "--project", project, "--bundle", "default", "--yes")
	execution, _, stop, err := harness.service.prepareInstall(context.Background(), request.Source(), request.Target(), request.Scope(), project, true, request.Selection(), request.InstallationID(), request.HasInstallationID(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("install preparation stopped: %v", err)
	}
	desired, entry, _ := stageInterrupted(t, harness, execution, "operation-project-local-exclusion")
	if err := harness.service.ensureProjectLocalExclusion(context.Background(), *desired); err != nil {
		t.Fatal(err)
	}
	if recovered, err := harness.service.reconcileInterrupted(context.Background()); err != nil || !recovered {
		t.Fatalf("recovery = %t, %v", recovered, err)
	}
	contents, err := os.ReadFile(exclude)
	if err != nil || !bytes.Equal(contents, want) {
		t.Fatalf("Git-local exclusion = %q, %v", contents, err)
	}
	if _, present, err := harness.store.LoadOperationHistory(entry.InstallationID, entry.OperationID); err != nil || present {
		t.Fatalf("staged history = present:%t err:%v", present, err)
	}
}

func TestLifecycleOrphanRecoveryRemovesFreshProjectLocalExclusion(t *testing.T) {
	harness := newLifecycleHarness(t)
	project := filepath.Join(canonicalTestDirectory(t, t.TempDir()), "project")
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(project, ".git", "info", "exclude")
	want := []byte("# existing\r\n/other\r\n\r\n")
	if err := os.WriteFile(exclude, want, 0o600); err != nil {
		t.Fatal(err)
	}
	harness.native.projectRoot = project
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "project-local", "--project", project, "--bundle", "default", "--yes")
	execution, _, stop, err := harness.service.prepareInstall(context.Background(), request.Source(), request.Target(), request.Scope(), project, true, request.Selection(), request.InstallationID(), request.HasInstallationID(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("install preparation stopped: %v", err)
	}
	desired, entry, _ := stageInterrupted(t, harness, execution, "operation-project-local-orphan-exclusion")
	if err := harness.service.ensureProjectLocalExclusion(context.Background(), *desired); err != nil {
		t.Fatal(err)
	}
	if err := harness.store.DeleteMarker(); err != nil {
		t.Fatal(err)
	}
	if recovered, err := harness.service.reconcileInterrupted(context.Background()); err != nil || !recovered {
		t.Fatalf("recovery = %t, %v", recovered, err)
	}
	contents, err := os.ReadFile(exclude)
	if err != nil || !bytes.Equal(contents, want) {
		t.Fatalf("Git-local exclusion = %q, %v", contents, err)
	}
	if _, present, err := harness.store.LoadOperationHistory(entry.InstallationID, entry.OperationID); err != nil || present {
		t.Fatalf("staged history = present:%t err:%v", present, err)
	}
}

func TestLifecycleAutomaticRecoveryCleansStagedHistoryWithoutMarker(t *testing.T) {
	harness := newLifecycleHarness(t)
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	project, hasProject := request.Project()
	execution, _, stop, err := harness.service.prepareInstall(
		context.Background(), request.Source(), request.Target(), request.Scope(), project, hasProject,
		request.Selection(), request.InstallationID(), request.HasInstallationID(), cli.ConflictFail,
	)
	if err != nil || stop {
		t.Fatalf("install preparation stopped: %v", err)
	}
	_, entry, _ := stageInterrupted(t, harness, execution, "operation-recovery0005")
	if err := harness.store.DeleteMarker(); err != nil {
		t.Fatal(err)
	}

	recovered, err := harness.service.reconcileInterrupted(context.Background())
	if err != nil || !recovered {
		t.Fatalf("orphan cleanup = %t, %v", recovered, err)
	}
	if _, present, err := harness.store.LoadOperationHistory(entry.InstallationID, entry.OperationID); err != nil || present {
		t.Fatalf("staged history = present:%t err:%v", present, err)
	}
}

func TestLifecycleAutomaticRecoveryRejectsStagedHistoryWithoutMarkerAfterMutation(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, _, _ := harness.store.Load()
	harness.validator.update = true
	request := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--yes")
	execution, _, stop, err := harness.service.prepareUpdate(context.Background(), request.InstallationID(), request.Source(), cli.ConflictFail)
	if err != nil || stop {
		t.Fatalf("update preparation stopped: %v", err)
	}
	desired, entry, _ := stageInterrupted(t, harness, execution, "operation-recovery0006")
	if err := harness.service.applyTransition(context.Background(), execution.before, desired, execution.catalogBefore, execution.catalog, execution.rules, execution.artifacts, cli.ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if err := harness.store.DeleteMarker(); err != nil {
		t.Fatal(err)
	}

	recovered, err := harness.service.reconcileInterrupted(context.Background())
	if err != nil || recovered {
		t.Fatalf("mutated orphan recovery = %t, %v", recovered, err)
	}
	if _, present, err := harness.store.LoadOperationHistory(entry.InstallationID, entry.OperationID); err != nil || !present {
		t.Fatalf("ambiguous staged history = present:%t err:%v", present, err)
	}
}

func stageInterrupted(t *testing.T, harness lifecycleHarness, execution lifecycleExecution, operationID string) (*installstate.Record, installstate.HistoryEntry, installstate.Marker) {
	t.Helper()
	desired := cloneRecordPtr(execution.desired)
	desired.LastOperation = installstate.LastOperation{ID: operationID, Timestamp: "2026-08-25T12:00:00Z"}
	desired.History = appendUnique(desired.History, operationID)
	beforeCatalog, beforeRules, err := harness.service.captureOwned(execution.before)
	if err != nil {
		t.Fatal(err)
	}
	afterCatalog := slices.Clone(execution.catalog)
	if desired.Scope == "project-shared" {
		beforeCatalog = projectSharedOwnedEntry(execution.before)
		afterCatalog = projectSharedOwnedEntry(desired)
	}
	entry := installstate.HistoryEntry{
		SchemaVersion: installstate.HistorySchemaVersion, Operation: execution.operation.String(), OperationID: operationID,
		InstallationID: desired.InstallationID, Timestamp: desired.LastOperation.Timestamp, Restorable: true,
		Before: cloneRecordPtr(execution.before), After: cloneRecordPtr(desired), CatalogBefore: beforeCatalog, RulesBefore: beforeRules,
		CatalogAfter: afterCatalog, RulesAfter: slices.Clone(execution.rules),
		NativeArtifactsBefore: harness.service.currentArtifacts(execution.before), NativeArtifactsAfter: cloneNativeArtifacts(execution.artifacts),
	}
	resources := []string{"history:" + desired.InstallationID, "owned:state/installation.json"}
	if execution.before != nil {
		resources = append(resources, execution.before.NativeResources...)
		if execution.before.Catalog.Path != "" {
			resources = append(resources, "owned:"+execution.before.Catalog.Path)
		}
		if execution.before.NativeCatalog.Path != "" {
			resources = append(resources, "owned:"+execution.before.NativeCatalog.Path)
		}
		if execution.before.Rules.Path != "" {
			resources = append(resources, "owned:"+execution.before.Rules.Path)
		}
	}
	resources = append(resources, desired.NativeResources...)
	if desired.Catalog.Path != "" {
		resources = append(resources, "owned:"+desired.Catalog.Path)
	}
	if desired.NativeCatalog.Path != "" {
		resources = append(resources, "owned:"+desired.NativeCatalog.Path)
	}
	if desired.Rules.Path != "" {
		resources = append(resources, "owned:"+desired.Rules.Path)
	}
	sourceRevision := recordSourceRevision(*desired)
	if execution.source.HasSource() {
		sourceRevision = cliSourceRevision(execution.source.Source)
	}
	marker, err := installstate.NewResourceMarker(execution.operation.String(), operationID, desired.InstallationID, sourceRevision, resources)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.store.StageHistory(entry); err != nil {
		t.Fatal(err)
	}
	if err := harness.store.SaveMarker(marker); err != nil {
		t.Fatal(err)
	}
	return desired, entry, marker
}

type lifecycleHarness struct {
	service   *lifecycleService
	validator *lifecycleValidator
	native    *lifecycleNativeRunner
	store     installstate.Store
	home      string
}

func newLifecycleHarness(t *testing.T) lifecycleHarness {
	t.Helper()
	home := t.TempDir()
	store, err := installstate.NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	native := &lifecycleNativeRunner{marketplaces: map[string]bool{}, plugins: map[string]bool{}, enabled: map[string]bool{}}
	validator := &lifecycleValidator{native: native}
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
	service := newLifecycleService(validator, store, native, home, buildinfo.New(buildinfo.Inputs{Version: "0.0.0-dev"}), func(context.Context) (func() error, error) {
		return func() error { return nil }, nil
	})
	service.random = bytes.NewReader(random)
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time {
		now = now.Add(time.Second)
		return now
	}
	return lifecycleHarness{service: service, validator: validator, native: native, store: store, home: home}
}

func parseRequest[T cli.Request](t *testing.T, arguments ...string) T {
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

type lifecycleValidator struct {
	native                *lifecycleNativeRunner
	update                bool
	selectionCalls        int
	updateCalls           int
	source                func(cli.SourceOptions, string) cli.Source
	localDigest           string
	inspectionDirectories []string
}

func (v *lifecycleValidator) SelectLifecycle(_ context.Context, options cli.SourceOptions, bundle string) validation.LifecycleSelection {
	v.selectionCalls++
	commit := strings.Repeat("a", 40)
	if options.HasReference() && (options.Reference() == strings.Repeat("a", 40) || options.Reference() == strings.Repeat("b", 40)) {
		commit = options.Reference()
	} else if v.update {
		commit = strings.Repeat("b", 40)
	}
	rules := []byte("rules-default\n")
	resolvedAssets := []string{"ai4j-rules", "repository-review"}
	if bundle == "minimal" {
		rules = []byte("rules-minimal\n")
		resolvedAssets = []string{"minimal"}
	}
	toolkitID, packageID, packagePath := "ai4j", "ai4j-default", "plugins/ai4j-default"
	if bundle == "tools" {
		packageID, packagePath = "ai4j-tools", "plugins/ai4j-tools"
		rules = nil
		resolvedAssets = nil
	}
	if bundle == "review" {
		packageID, packagePath = "ai4j-review", "plugins/ai4j-review"
	}
	if bundle == "other" {
		toolkitID, packageID, packagePath = "other-toolkit", "other-plugin", "plugins/other-plugin"
		rules = []byte("other rules\n")
		resolvedAssets = []string{"other"}
	}
	resolvedBundles := []string{bundle}
	packages := []validation.LifecyclePackage{{ID: packageID, Path: packagePath, NativeArtifact: testLifecycleArtifact()}}
	resolvedPackages := []string{packageID}
	if bundle == "full" {
		resolvedBundles = []string{"default", "full", "tools"}
		resolvedAssets = []string{"ai4j-rules", "claude-tools", "repository-review"}
		packages = append(packages, validation.LifecyclePackage{ID: "ai4j-tools", Path: "plugins/ai4j-tools", NativeArtifact: testLifecycleArtifact()})
		resolvedPackages = append(resolvedPackages, "ai4j-tools")
	}
	digest := ""
	var content []cli.ContentItem
	if len(rules) != 0 {
		digest = sha256Digest(rules)
		item, _ := cli.NewContentItem(cli.ComponentSharedInstruction, resolvedAssets[0], "toolkit/rules/ai4j.md", digest, cli.ContentAdded, nil)
		content = []cli.ContentItem{item}
	}
	return validation.LifecycleSelection{
		Source: v.source(options, commit), ToolkitID: toolkitID, DeclarationID: toolkitID, ToolkitVersion: "1.0.0",
		RequestedBundle: bundle, ResolvedBundles: resolvedBundles, ResolvedPackages: resolvedPackages, ResolvedAssets: resolvedAssets,
		Packages: packages,
		Content:  content, Rules: rules, RulesChecksum: digest,
	}
}

func testLifecycleArtifact() []byte {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, file := range []struct {
		name, contents string
		mode           os.FileMode
	}{
		{name: ".claude-plugin/plugin.json", contents: "{}\n", mode: 0o644},
		{name: ".mcp.json", contents: "{\"mcpServers\":{\"claude-tools\":{\"type\":\"stdio\",\"command\":\"claude\",\"args\":[\"mcp\",\"serve\"],\"env\":{\"AI4J_TOKEN\":\"${AI4J_TOKEN}\"}}}}\n", mode: 0o644},
		{name: "scripts/check.sh", contents: "#!/bin/sh\nexit 0\n", mode: 0o755},
	} {
		header := &zip.FileHeader{Name: file.name, Method: zip.Store}
		header.SetMode(file.mode)
		writer, _ := archive.CreateHeader(header)
		_, _ = writer.Write([]byte(file.contents))
	}
	_ = archive.Close()
	return output.Bytes()
}

func (v *lifecycleValidator) ValidateUpdate(_ context.Context, options cli.SourceOptions, installed domain.CommitOID) validation.UpdateReport {
	v.updateCalls++
	if !v.update || installed.String() == strings.Repeat("b", 40) {
		return validation.UpdateReport{Report: validation.Report{Source: v.source(options, installed.String())}, Disposition: gitsource.UpdateNoChange}
	}
	return validation.UpdateReport{Report: validation.Report{Source: v.source(options, strings.Repeat("b", 40))}, Disposition: gitsource.UpdateAvailable}
}

func (v *lifecycleValidator) InspectNativeStatusFor(_ context.Context, marketplaceID, pluginID string) (validation.NativeStatus, *result.Problem) {
	return validation.NativeStatus{MarketplaceRegistered: v.native.marketplaces[marketplaceID], PluginInstalled: v.native.plugins[pluginID], PluginEnabled: v.native.enabled[pluginID]}, nil
}

func (v *lifecycleValidator) InspectNativeStatusAt(ctx context.Context, directory, marketplaceID, pluginID string) (validation.NativeStatus, *result.Problem) {
	v.inspectionDirectories = append(v.inspectionDirectories, directory)
	return v.InspectNativeStatusFor(ctx, marketplaceID, pluginID)
}

func (v *lifecycleValidator) InspectNativeStatus(context.Context) (validation.NativeStatus, *result.Problem) {
	return validation.NativeStatus{}, nil
}

type lifecycleNativeRunner struct {
	commands     [][]string
	directories  []string
	marketplaces map[string]bool
	plugins      map[string]bool
	enabled      map[string]bool
	projectRoot  string
	failPrefix   []string
	failCount    int
}

func (r *lifecycleNativeRunner) LookPath(name string) (string, error) {
	if name == "git" {
		return "/usr/bin/git", nil
	}
	if name == "claude" {
		return "/usr/bin/claude", nil
	}
	return "", errors.New("not found")
}

func (r *lifecycleNativeRunner) Run(_ context.Context, directory string, _ string, arguments, _ []string) (validation.ProcessResult, error) {
	r.commands = append(r.commands, slices.Clone(arguments))
	r.directories = append(r.directories, directory)
	if r.failCount > 0 && len(arguments) >= len(r.failPrefix) && slices.Equal(arguments[:len(r.failPrefix)], r.failPrefix) {
		r.failCount--
		return validation.ProcessResult{ExitCode: 1}, nil
	}
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
		marketplaceID := arguments[3]
		for pluginID := range r.plugins {
			if strings.HasSuffix(pluginID, "@"+marketplaceID) {
				return validation.ProcessResult{}, errors.New("marketplace removal attempted before plugin uninstall")
			}
		}
		if slices.Contains(arguments, "project") {
			if err := fakeNativeMarketplace(directory, marketplaceID, nil, false); err != nil {
				return validation.ProcessResult{}, err
			}
		}
		delete(r.marketplaces, marketplaceID)
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
