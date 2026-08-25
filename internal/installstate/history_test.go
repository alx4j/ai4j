package installstate

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHistoryStagesCommitsLoadsAndPurgesOpaqueOwnedMaterial(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := testRecord()
	record.History = []string{"operation-history"}
	record.LastOperation = LastOperation{ID: "operation-history", Timestamp: time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)}
	entry := HistoryEntry{SchemaVersion: HistorySchemaVersion, Operation: "install", OperationID: "operation-history", InstallationID: record.InstallationID, Timestamp: record.LastOperation.Timestamp, Restorable: true, After: &record, CatalogAfter: []byte("catalog"), RulesAfter: []byte("rules"), NativeArtifactAfter: []byte("artifact")}
	if err := store.StageHistory(entry); err != nil {
		t.Fatal(err)
	}
	if entries, err := store.LoadHistory(record.InstallationID); err != nil || len(entries) != 0 {
		t.Fatalf("pending history = %#v, %v", entries, err)
	}
	if pending, present, err := store.LoadOperationHistory(record.InstallationID, entry.OperationID); err != nil || !present || pending.Committed {
		t.Fatalf("operation history = %#v, present:%t, err:%v", pending, present, err)
	}
	if err := store.CommitHistory(entry); err != nil {
		t.Fatal(err)
	}
	entries, err := store.LoadHistory(record.InstallationID)
	if err != nil || len(entries) != 1 || string(entries[0].RulesAfter) != "rules" {
		t.Fatalf("committed history = %#v, %v", entries, err)
	}
	info, err := os.Stat(store.HistoryPath(record.InstallationID, entry.OperationID))
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("history permissions = %v, %v", info.Mode(), err)
	}
	if err := store.DeleteHistory(record.InstallationID, []string{entry.OperationID}); err != nil {
		t.Fatal(err)
	}
	if entries, err := store.LoadHistory(record.InstallationID); err != nil || len(entries) != 0 {
		t.Fatalf("purged history = %#v, %v", entries, err)
	}
}

func TestHistoryCommitRejectsConcurrentJournalChange(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := testRecord()
	record.History = []string{"operation-history"}
	record.LastOperation = LastOperation{ID: "operation-history", Timestamp: "2026-08-25T10:00:00Z"}
	entry := HistoryEntry{SchemaVersion: HistorySchemaVersion, Operation: "install", OperationID: "operation-history", InstallationID: record.InstallationID, Timestamp: record.LastOperation.Timestamp, Restorable: true, After: &record, NativeArtifactAfter: []byte("artifact")}
	if err := store.StageHistory(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.HistoryPath(record.InstallationID, entry.OperationID), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitHistory(entry); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("CommitHistory() error = %v", err)
	}
}

func TestHistoryRejectsUnknownSchemaWithoutDisclosingContent(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := store.HistoryPath("installation-001", "operation-001")
	if err := os.MkdirAll(store.HistoryRoot("installation-001"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion":2,"secret":"SECRET_CANARY"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.LoadHistoryEntry("installation-001", "operation-001")
	if !errors.Is(err, ErrUnsupportedHistorySchema) || strings.Contains(err.Error(), "SECRET_CANARY") {
		t.Fatalf("history error = %v", err)
	}
}
