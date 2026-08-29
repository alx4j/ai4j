package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
)

func TestLifecycleLocalRollbackUsesRetainedHistoryWithoutCheckout(t *testing.T) {
	harness := newLifecycleHarness(t)
	installed, updated, checkout := installAndUpdateLocalLifecycle(t, harness)
	if err := os.RemoveAll(checkout); err != nil {
		t.Fatal(err)
	}
	harness.validator.localDigest = strings.Repeat("a", 64)
	selectionCalls := harness.validator.selectionCalls

	preview := parseRequest[cli.RollbackRequest](t, "rollback", updated.InstallationID, "--dry-run")
	response, err := harness.service.Rollback(context.Background(), preview, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("rollback preview = %#v, %v", response.Result(), err)
	}
	plan := response.Data().(cli.PlanData)
	if plan.HasSource() || harness.validator.selectionCalls != selectionCalls {
		t.Fatalf("rollback preview source=%t selectionCalls=%d/%d", plan.HasSource(), harness.validator.selectionCalls, selectionCalls)
	}

	rollback := parseRequest[cli.RollbackRequest](t, "rollback", updated.InstallationID, "--yes")
	response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("rollback = %#v, %v", response.Result(), err)
	}
	rolledBack, present, err := harness.store.LoadByID(updated.InstallationID)
	if err != nil || !present || rolledBack.Lifecycle != "active" {
		t.Fatalf("rolled back record = %#v, present=%t, error=%v", rolledBack, present, err)
	}
	if rolledBack.Source.SourceDigest != installed.Source.SourceDigest || rolledBack.Source.BundleDigest != installed.Source.BundleDigest {
		t.Fatalf("rolled back source = %#v, want %#v", rolledBack.Source, installed.Source)
	}
	if harness.validator.selectionCalls != selectionCalls {
		t.Fatalf("rollback resolved missing checkout: selectionCalls=%d/%d", harness.validator.selectionCalls, selectionCalls)
	}
}

func TestLifecycleLocalUninstallDoesNotRetainUnverifiedCheckoutBytes(t *testing.T) {
	harness := newLifecycleHarness(t)
	installed, _, checkout := installAndUpdateLocalLifecycle(t, harness)
	purge := parseRequest[cli.HistoryPurgeRequest](t, "history", "purge", installed.InstallationID, "--all", "--yes")
	response, err := harness.service.HistoryPurge(context.Background(), purge, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("history purge = %#v, %v", response.Result(), err)
	}
	if err := os.RemoveAll(checkout); err != nil {
		t.Fatal(err)
	}
	harness.validator.localDigest = strings.Repeat("a", 64)
	selectionCalls := harness.validator.selectionCalls

	preview := parseRequest[cli.UninstallRequest](t, "uninstall", installed.InstallationID, "--dry-run")
	response, err = harness.service.Uninstall(context.Background(), preview, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall preview = %#v, %v", response.Result(), err)
	}
	plan := response.Data().(cli.PlanData)
	if plan.HasSource() || !hasWarning(response, "rollback_unavailable") || harness.validator.selectionCalls != selectionCalls {
		t.Fatalf("uninstall preview source=%t warnings=%#v selectionCalls=%d/%d", plan.HasSource(), response.Result().Warnings(), harness.validator.selectionCalls, selectionCalls)
	}

	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", installed.InstallationID, "--yes")
	response, err = harness.service.Uninstall(context.Background(), uninstall, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}
	if harness.validator.selectionCalls != selectionCalls {
		t.Fatalf("uninstall resolved missing checkout: selectionCalls=%d/%d", harness.validator.selectionCalls, selectionCalls)
	}
	archived, present, err := harness.store.LoadByID(installed.InstallationID)
	if err != nil || !present || archived.Lifecycle != "archived" {
		t.Fatalf("archived record = %#v, present=%t, error=%v", archived, present, err)
	}
	entries, err := harness.store.LoadHistory(installed.InstallationID)
	if err != nil || len(entries) != 1 || entries[0].Restorable || len(entries[0].NativeArtifactsBefore) != 0 {
		t.Fatalf("uninstall history = %#v, error=%v", entries, err)
	}
}

func TestLifecycleLocalRollbackRecoveryMatchesStoredRevision(t *testing.T) {
	for _, test := range []struct {
		name         string
		mutateTarget bool
		wantDigest   string
		wantHistory  bool
	}{
		{name: "before target mutation", wantDigest: strings.Repeat("f", 64)},
		{name: "after target mutation", mutateTarget: true, wantDigest: strings.Repeat("e", 64), wantHistory: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newLifecycleHarness(t)
			_, updated, checkout := installAndUpdateLocalLifecycle(t, harness)
			if err := os.RemoveAll(checkout); err != nil {
				t.Fatal(err)
			}
			harness.validator.localDigest = strings.Repeat("a", 64)
			selectionCalls := harness.validator.selectionCalls
			request := parseRequest[cli.RollbackRequest](t, "rollback", updated.InstallationID, "--yes")
			execution, _, stop, err := harness.service.prepareRollback(context.Background(), request.InstallationID(), request.OperationID(), request.HasOperationID(), cli.ConflictFail)
			if err != nil || stop {
				t.Fatalf("rollback preparation stopped: %v", err)
			}
			if execution.source.HasSource() || harness.validator.selectionCalls != selectionCalls {
				t.Fatalf("rollback preparation source=%t selectionCalls=%d/%d", execution.source.HasSource(), harness.validator.selectionCalls, selectionCalls)
			}
			desired, entry, marker := stageInterrupted(t, harness, execution, "operation-localrollback0001")
			if marker.Commit != recordSourceRevision(*desired) || marker.Commit != strings.Repeat("e", 64) {
				t.Fatalf("rollback marker revision = %q, desired=%q", marker.Commit, recordSourceRevision(*desired))
			}
			if test.mutateTarget {
				if err := harness.service.applyTransition(context.Background(), execution.transition.withDesired(desired), cli.ConflictFail); err != nil {
					t.Fatal(err)
				}
			}

			recovered, err := harness.service.reconcileInterrupted(context.Background())
			if err != nil || !recovered {
				t.Fatalf("rollback recovery = %t, %v", recovered, err)
			}
			if _, present, err := harness.store.LoadMarker(); err != nil || present {
				t.Fatalf("rollback marker = present:%t error:%v", present, err)
			}
			current, present, err := harness.store.LoadByID(updated.InstallationID)
			if err != nil || !present || current.Source.SourceDigest != test.wantDigest {
				t.Fatalf("recovered record = %#v, present=%t, error=%v", current, present, err)
			}
			committed, present, err := harness.store.LoadOperationHistory(entry.InstallationID, entry.OperationID)
			if test.wantHistory {
				if err != nil || !present || !committed.Committed {
					t.Fatalf("committed rollback history = %#v, present=%t, error=%v", committed, present, err)
				}
			} else if err != nil || present {
				t.Fatalf("discarded rollback history = %#v, present=%t, error=%v", committed, present, err)
			}
		})
	}
}

func TestLifecycleLocalRollbackSupportsOnePassUpdateAndSync(t *testing.T) {
	for _, test := range []struct {
		name       string
		command    string
		arguments  []string
		wantBundle string
	}{
		{name: "update", command: "update", arguments: []string{"--allow-dirty", "--expected-source-digest", strings.Repeat("a", 64), "--yes"}, wantBundle: "default"},
		{name: "sync", command: "sync", arguments: []string{"--bundle", "minimal", "--allow-dirty", "--expected-source-digest", strings.Repeat("a", 64), "--yes"}, wantBundle: "minimal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newLifecycleHarness(t)
			_, updated, _ := installAndUpdateLocalLifecycle(t, harness)
			rollback := parseRequest[cli.RollbackRequest](t, "rollback", updated.InstallationID, "--yes")
			response, err := harness.service.Rollback(context.Background(), rollback, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("rollback = %#v, %v", response.Result(), err)
			}
			harness.validator.localDigest = strings.Repeat("a", 64)
			selectionCalls := harness.validator.selectionCalls
			arguments := append([]string{test.command, updated.InstallationID}, test.arguments...)
			switch test.command {
			case "update":
				request := parseRequest[cli.UpdateRequest](t, arguments...)
				response, err = harness.service.Update(context.Background(), request, CommandIO{})
			case "sync":
				request := parseRequest[cli.SyncRequest](t, arguments...)
				response, err = harness.service.Sync(context.Background(), request, CommandIO{})
			}
			if err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("%s after rollback = %#v, %v", test.command, response.Result(), err)
			}
			if harness.validator.selectionCalls != selectionCalls+1 {
				t.Fatalf("%s selection calls = %d, want %d", test.command, harness.validator.selectionCalls, selectionCalls+1)
			}
			current, present, err := harness.store.LoadByID(updated.InstallationID)
			if err != nil || !present || current.Source.SourceDigest != strings.Repeat("a", 64) || current.Selection.RequestedBundle != test.wantBundle {
				t.Fatalf("%s record = %#v, present=%t, error=%v", test.command, current, present, err)
			}
			entry, present, err := harness.store.LoadHistoryEntry(current.InstallationID, current.LastOperation.ID)
			if err != nil || !present || !entry.Restorable || len(entry.NativeArtifactsBefore) == 0 {
				t.Fatalf("%s history = %#v, present=%t, error=%v", test.command, entry, present, err)
			}
		})
	}
}

func TestLifecycleLocalUpdateFailsWithoutExactRollbackMaterial(t *testing.T) {
	harness := newLifecycleHarness(t)
	_, updated, _ := installAndUpdateLocalLifecycle(t, harness)
	purge := parseRequest[cli.HistoryPurgeRequest](t, "history", "purge", updated.InstallationID, "--all", "--yes")
	response, err := harness.service.HistoryPurge(context.Background(), purge, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("history purge = %#v, %v", response.Result(), err)
	}
	harness.validator.localDigest = strings.Repeat("a", 64)
	selectionCalls := harness.validator.selectionCalls
	nativeCalls := len(harness.native.commands)
	update := parseRequest[cli.UpdateRequest](t, "update", updated.InstallationID, "--allow-dirty", "--expected-source-digest", harness.validator.localDigest, "--yes")
	response, err = harness.service.Update(context.Background(), update, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	problems := response.Result().Errors()
	if response.Result().ExitCode() != result.ExitConflict || len(problems) != 1 || problems[0].Code() != "rollback_artifact_unavailable" {
		t.Fatalf("update without rollback material = %#v", response.Result())
	}
	if harness.validator.selectionCalls != selectionCalls+1 || len(harness.native.commands) != nativeCalls {
		t.Fatalf("blocked update calls: selections=%d/%d native=%d/%d", harness.validator.selectionCalls, selectionCalls+1, len(harness.native.commands), nativeCalls)
	}
	current, present, err := harness.store.LoadByID(updated.InstallationID)
	if err != nil || !present || current.Source.SourceDigest != updated.Source.SourceDigest || current.LastOperation != updated.LastOperation {
		t.Fatalf("blocked update changed state = %#v, present=%t, error=%v", current, present, err)
	}
}

func installAndUpdateLocalLifecycle(t *testing.T, harness lifecycleHarness) (installstate.Record, installstate.Record, string) {
	t.Helper()
	checkout := filepath.Join(harness.service.home, "checkout")
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	harness.validator.localDigest = strings.Repeat("e", 64)
	install := parseRequest[cli.InstallRequest](t, "install", "--source", checkout, "--target", "claude", "--scope", "user", "--bundle", "default", "--expected-source-digest", harness.validator.localDigest, "--yes")
	response, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("local install = %#v, %v", response.Result(), err)
	}
	installed, present, err := harness.store.Load()
	if err != nil || !present {
		t.Fatalf("installed record = %#v, present=%t, error=%v", installed, present, err)
	}
	harness.validator.localDigest = strings.Repeat("f", 64)
	selectionCalls := harness.validator.selectionCalls
	update := parseRequest[cli.UpdateRequest](t, "update", installed.InstallationID, "--allow-dirty", "--expected-source-digest", harness.validator.localDigest, "--yes")
	response, err = harness.service.Update(context.Background(), update, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("local update = %#v, %v", response.Result(), err)
	}
	if harness.validator.selectionCalls != selectionCalls+1 {
		t.Fatalf("local update selection calls = %d, want %d", harness.validator.selectionCalls, selectionCalls+1)
	}
	updated, present, err := harness.store.LoadByID(installed.InstallationID)
	if err != nil || !present {
		t.Fatalf("updated record = %#v, present=%t, error=%v", updated, present, err)
	}
	return installed, updated, checkout
}
