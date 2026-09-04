package app

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
)

func TestLifecycleHistoryPurgeDoesNotOverwriteAStalePreparedRecord(t *testing.T) {
	harness := newLifecycleHarness(t)
	before := installUpdatedHistory(t, &harness)
	changed := before.Clone()
	changed.Health = "drifted"
	originalNow := harness.service.now
	nowCalls := 0
	var hookErr error
	harness.service.now = func() time.Time {
		nowCalls++
		if nowCalls == 2 {
			hookErr = harness.store.Replace(before, changed)
		}
		return originalNow()
	}
	purge := parseRequest[cli.HistoryPurgeRequest](t, "history", "purge", before.InstallationID, "--all", "--yes")

	response, err := harness.service.HistoryPurge(context.Background(), purge, CommandIO{})

	problems := response.Result().Errors()
	if err != nil || hookErr != nil || nowCalls != 2 || response.Result().Failure() != result.FailureRecovery || len(problems) != 1 || problems[0].Code() != "history_state_commit_failed" {
		t.Fatalf("stale history purge = %#v, problems=%v, nowCalls=%d, commandErr=%v, hookErr=%v", response.Result(), problems, nowCalls, err, hookErr)
	}
	stored, present, loadErr := harness.store.LoadByID(before.InstallationID)
	if loadErr != nil || !present || !reflect.DeepEqual(stored, changed) {
		t.Fatalf("externally changed state was overwritten: %#v, present=%t, err=%v", stored, present, loadErr)
	}
	if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || !present {
		t.Fatalf("recovery marker = present:%t err=%v", present, markerErr)
	}
	history, historyErr := harness.store.LoadHistory(before.InstallationID)
	if historyErr != nil || !slices.Equal(historyEntryIDs(history), before.History) {
		t.Fatalf("history changed before state commit: ids=%v want=%v err=%v", historyEntryIDs(history), before.History, historyErr)
	}
}

func TestLifecycleHistoryPurgeRecoveryRollsForwardPartialDeletion(t *testing.T) {
	harness := newLifecycleHarness(t)
	before := installUpdatedHistory(t, &harness)
	entries, err := harness.store.LoadHistory(before.InstallationID)
	if err != nil || len(entries) != 2 {
		t.Fatalf("history = %#v, %v", entries, err)
	}
	selected := historyEntryIDs(entries)
	desired := before.Clone()
	desired.History = nil
	stageHistoryPurge(t, &harness, before, &desired, selected)
	if err := harness.store.DeleteHistory(before.InstallationID, selected[:1]); err != nil {
		t.Fatal(err)
	}
	commandCount := len(harness.native.commands)

	recovered, err := harness.service.reconcileInterrupted(context.Background())

	if err != nil || !recovered {
		t.Fatalf("history purge recovery = %t, %v", recovered, err)
	}
	current, present, err := harness.store.LoadByID(before.InstallationID)
	if err != nil || !present || !reflect.DeepEqual(current, desired) {
		t.Fatalf("recovered record = %#v, %t, %v", current, present, err)
	}
	remaining, err := harness.store.LoadHistory(before.InstallationID)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining history = %#v, %v", remaining, err)
	}
	if _, present, err := harness.store.LoadMarker(); err != nil || present {
		t.Fatalf("recovery marker = present:%t err:%v", present, err)
	}
	if len(harness.native.commands) != commandCount {
		t.Fatalf("history recovery invoked native commands: %d/%d", len(harness.native.commands), commandCount)
	}
}

func TestLifecycleHistoryPurgeRecoveryRemovesArchivedTombstone(t *testing.T) {
	harness := newLifecycleHarness(t)
	before := installUpdatedHistory(t, &harness)
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", before.InstallationID, "--yes")
	if response, err := harness.service.Uninstall(context.Background(), uninstall, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}
	before, present, err := harness.store.LoadByID(before.InstallationID)
	if err != nil || !present || before.Lifecycle != "archived" {
		t.Fatalf("archived record = %#v, %t, %v", before, present, err)
	}
	entries, err := harness.store.LoadHistory(before.InstallationID)
	if err != nil || len(entries) != 3 {
		t.Fatalf("history = %#v, %v", entries, err)
	}
	selected := historyEntryIDs(entries)
	stageHistoryPurge(t, &harness, before, nil, selected)
	if err := harness.store.DeleteHistory(before.InstallationID, selected[:1]); err != nil {
		t.Fatal(err)
	}

	recovered, err := harness.service.reconcileInterrupted(context.Background())

	if err != nil || !recovered {
		t.Fatalf("archived history purge recovery = %t, %v", recovered, err)
	}
	if _, present, err := harness.store.LoadByID(before.InstallationID); err != nil || present {
		t.Fatalf("archived tombstone = present:%t err:%v", present, err)
	}
	remaining, err := harness.store.LoadHistory(before.InstallationID)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining history = %#v, %v", remaining, err)
	}
	if _, present, err := harness.store.LoadMarker(); err != nil || present {
		t.Fatalf("recovery marker = present:%t err:%v", present, err)
	}
}

func TestLifecycleHistoryPurgeRecoveryFailsClosedWhenRetainedHistoryIsMissing(t *testing.T) {
	harness := newLifecycleHarness(t)
	before := installUpdatedHistory(t, &harness)
	entries, err := harness.store.LoadHistory(before.InstallationID)
	if err != nil || len(entries) != 2 {
		t.Fatalf("history = %#v, %v", entries, err)
	}
	ids := historyEntryIDs(entries)
	selected := ids[:1]
	desired := before.Clone()
	desired.History = ids[1:]
	stageHistoryPurge(t, &harness, before, &desired, selected)
	if err := harness.store.DeleteHistory(before.InstallationID, ids); err != nil {
		t.Fatal(err)
	}

	recovered, err := harness.service.reconcileInterrupted(context.Background())

	if err != nil || recovered {
		t.Fatalf("ambiguous history recovery = %t, %v", recovered, err)
	}
	current, present, err := harness.store.LoadByID(before.InstallationID)
	if err != nil || !present || !reflect.DeepEqual(current, before) {
		t.Fatalf("ambiguous record = %#v, %t, %v", current, present, err)
	}
	if _, present, err := harness.store.LoadMarker(); err != nil || !present {
		t.Fatalf("ambiguous marker = present:%t err:%v", present, err)
	}
}

func installUpdatedHistory(t *testing.T, harness *lifecycleHarness) installstate.Record {
	t.Helper()
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
	if response, err := harness.service.Install(context.Background(), install, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", response.Result(), err)
	}
	record, present, err := harness.store.Load()
	if err != nil || !present {
		t.Fatalf("installed record = %#v, %t, %v", record, present, err)
	}
	harness.validator.update = true
	update := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--yes")
	if response, err := harness.service.Update(context.Background(), update, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("update = %#v, %v", response.Result(), err)
	}
	record, present, err = harness.store.LoadByID(record.InstallationID)
	if err != nil || !present || len(record.History) != 2 {
		t.Fatalf("updated record = %#v, %t, %v", record, present, err)
	}
	return record
}

func stageHistoryPurge(t *testing.T, harness *lifecycleHarness, before installstate.Record, desired *installstate.Record, selected []string) {
	t.Helper()
	marker, err := installstate.NewHistoryPurgeMarker(
		"operation-history-purge-recovery", recordSourceRevision(before),
		[]string{"history:" + before.InstallationID, "owned:state/installation.json"},
		selected, before, desired,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.store.SaveMarker(marker); err != nil {
		t.Fatal(err)
	}
}
