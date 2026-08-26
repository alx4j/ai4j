package installstate

import (
	"errors"
	"os"
	"path/filepath"
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
	entry := HistoryEntry{SchemaVersion: HistorySchemaVersion, Operation: "install", OperationID: "operation-history", InstallationID: record.InstallationID, Timestamp: record.LastOperation.Timestamp, Restorable: true, After: &record, CatalogAfter: []byte("catalog"), RulesAfter: []byte("rules"), NativeArtifactsAfter: []NativeArtifact{{PackageID: "ai4j-default", Bytes: []byte("artifact")}}}
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

func TestLoadStagedHistoryRemovesRegularCrashTemporaryAndKeepsJournal(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := testRecord()
	record.History = []string{"operation-history"}
	record.LastOperation = LastOperation{ID: "operation-history", Timestamp: "2026-08-25T10:00:00Z"}
	entry := HistoryEntry{
		SchemaVersion: HistorySchemaVersion, Operation: "install", OperationID: "operation-history",
		InstallationID: record.InstallationID, Timestamp: record.LastOperation.Timestamp, Restorable: true,
		After: &record, NativeArtifactsAfter: []NativeArtifact{{PackageID: "ai4j-default", Bytes: []byte("artifact")}},
	}
	if err := store.StageHistory(entry); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(store.HistoryRoot(record.InstallationID), ".history-123.tmp")
	if err := os.WriteFile(temporary, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if entries, err := store.LoadHistory(record.InstallationID); err != nil || len(entries) != 0 {
		t.Fatalf("committed history = %#v, %v", entries, err)
	}
	staged, err := store.LoadStagedHistory()
	if err != nil || len(staged) != 1 || staged[0].OperationID != entry.OperationID {
		t.Fatalf("staged history = %#v, %v", staged, err)
	}
	if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary history file remains: %v", err)
	}
}

func TestLoadStagedHistoryRejectsNonregularCrashTemporary(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := store.HistoryRoot("install-aaaaaaaaaaaa")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".history-123.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadStagedHistory(); !errors.Is(err, ErrMalformedHistory) {
		t.Fatalf("LoadStagedHistory() error = %v", err)
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
	entry := HistoryEntry{SchemaVersion: HistorySchemaVersion, Operation: "install", OperationID: "operation-history", InstallationID: record.InstallationID, Timestamp: record.LastOperation.Timestamp, Restorable: true, After: &record, NativeArtifactsAfter: []NativeArtifact{{PackageID: "ai4j-default", Bytes: []byte("artifact")}}}
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
	if err := os.WriteFile(path, []byte(`{"schemaVersion":3,"secret":"SECRET_CANARY"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.LoadHistoryEntry("installation-001", "operation-001")
	if !errors.Is(err, ErrUnsupportedHistorySchema) || strings.Contains(err.Error(), "SECRET_CANARY") {
		t.Fatalf("history error = %v", err)
	}
}

func TestHistoryRejectsMalformedEntryWithoutDisclosingContent(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := store.HistoryPath("installation-001", "operation-001")
	if err := os.MkdirAll(store.HistoryRoot("installation-001"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"secret":"SECRET_CANARY"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.LoadHistoryEntry("installation-001", "operation-001")
	if !errors.Is(err, ErrMalformedHistory) || strings.Contains(err.Error(), "SECRET_CANARY") {
		t.Fatalf("history error = %v", err)
	}
}

func TestHistoryLoadersRejectEntryIdentityThatDoesNotMatchItsPath(t *testing.T) {
	t.Parallel()
	const (
		storedInstallation = "installation-001"
		otherInstallation  = "installation-002"
		storedOperation    = "operation-history"
		otherOperation     = "operation-other"
	)
	tests := []struct {
		name           string
		installationID string
		operationID    string
		staged         bool
		load           func(Store, string, string) error
	}{
		{
			name: "operation loader rejects operation mismatch", installationID: storedInstallation, operationID: otherOperation,
			load: func(store Store, installationID, operationID string) error {
				_, _, err := store.LoadOperationHistory(installationID, operationID)
				return err
			},
		},
		{
			name: "entry loader rejects operation mismatch", installationID: storedInstallation, operationID: otherOperation,
			load: func(store Store, installationID, operationID string) error {
				_, _, err := store.LoadHistoryEntry(installationID, operationID)
				return err
			},
		},
		{
			name: "entry loader rejects staged operation mismatch", installationID: storedInstallation, operationID: otherOperation, staged: true,
			load: func(store Store, installationID, operationID string) error {
				_, _, err := store.LoadHistoryEntry(installationID, operationID)
				return err
			},
		},
		{
			name: "list loader rejects operation mismatch", installationID: storedInstallation, operationID: otherOperation,
			load: func(store Store, installationID, _ string) error {
				_, err := store.LoadHistory(installationID)
				return err
			},
		},
		{
			name: "entry loader rejects installation mismatch", installationID: otherInstallation, operationID: storedOperation,
			load: func(store Store, installationID, operationID string) error {
				_, _, err := store.LoadHistoryEntry(installationID, operationID)
				return err
			},
		},
		{
			name: "list loader rejects installation mismatch", installationID: otherInstallation, operationID: storedOperation,
			load: func(store Store, installationID, _ string) error {
				_, err := store.LoadHistory(installationID)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store, err := NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			record := testRecord()
			record.History = []string{storedOperation}
			record.LastOperation = LastOperation{ID: storedOperation, Timestamp: "2026-08-25T10:00:00Z"}
			entry := HistoryEntry{
				SchemaVersion: HistorySchemaVersion, Operation: "install", OperationID: storedOperation,
				InstallationID: storedInstallation, Timestamp: record.LastOperation.Timestamp, Committed: !test.staged, Restorable: true,
				After: &record, NativeArtifactsAfter: []NativeArtifact{{PackageID: "ai4j-default", Bytes: []byte("artifact")}},
			}
			contents, err := marshalHistory(entry)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(store.HistoryRoot(test.installationID), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(store.HistoryPath(test.installationID, test.operationID), contents, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := test.load(store, test.installationID, test.operationID); !errors.Is(err, ErrMalformedHistory) {
				t.Fatalf("history load error = %v", err)
			}
		})
	}
}

func TestRestorableHistoryRequiresCanonicalArtifactSetForEveryActivePackage(t *testing.T) {
	t.Parallel()
	record := testRecord()
	record.Packages = []NativePackage{
		{ID: "alpha-plugin", Path: "plugins/alpha-plugin"},
		{ID: "zeta-plugin", Path: "plugins/zeta-plugin"},
	}
	record.NativeResources = []string{
		"claude:alpha-plugin@ai4j",
		"claude:marketplace:ai4j",
		"claude:zeta-plugin@ai4j",
	}
	base := HistoryEntry{
		SchemaVersion:  HistorySchemaVersion,
		Operation:      "sync",
		OperationID:    "operation-history",
		InstallationID: record.InstallationID,
		Timestamp:      record.LastOperation.Timestamp,
		Restorable:     true,
		After:          &record,
		NativeArtifactsAfter: []NativeArtifact{
			{PackageID: "alpha-plugin", Bytes: []byte("alpha")},
			{PackageID: "zeta-plugin", Bytes: []byte("zeta")},
		},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid history error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*HistoryEntry)
	}{
		{name: "missing artifact", mutate: func(entry *HistoryEntry) {
			entry.NativeArtifactsAfter = entry.NativeArtifactsAfter[:1]
		}},
		{name: "wrong package", mutate: func(entry *HistoryEntry) {
			entry.NativeArtifactsAfter[1].PackageID = "other-plugin"
		}},
		{name: "unsorted artifacts", mutate: func(entry *HistoryEntry) {
			entry.NativeArtifactsAfter[0], entry.NativeArtifactsAfter[1] = entry.NativeArtifactsAfter[1], entry.NativeArtifactsAfter[0]
		}},
		{name: "empty artifact", mutate: func(entry *HistoryEntry) {
			entry.NativeArtifactsAfter[0].Bytes = nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := base
			entry.NativeArtifactsAfter = append([]NativeArtifact(nil), base.NativeArtifactsAfter...)
			test.mutate(&entry)
			if err := entry.Validate(); !errors.Is(err, ErrMalformedHistory) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}
