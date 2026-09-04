package installstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/host/privatepath"
)

const (
	HistorySchemaVersion  = 1
	maximumHistoryBytes   = 16 << 20
	maximumHistoryEntries = 16384
)

var (
	ErrUnsupportedHistorySchema = errors.New("history schema is unsupported")
	ErrMalformedHistory         = errors.New("history entry is malformed")
)

type NativeArtifact struct {
	PackageID string `json:"packageId"`
	Bytes     []byte `json:"bytes"`
}

// HistoryEntry retains only installation structure, owned structural entries,
// and opaque toolkit-owned bytes. User or target configuration files are never copied here.
type HistoryEntry struct {
	SchemaVersion         int              `json:"schemaVersion"`
	Operation             string           `json:"operation"`
	OperationID           string           `json:"operationId"`
	InstallationID        string           `json:"installationId"`
	Timestamp             string           `json:"timestamp"`
	Committed             bool             `json:"committed"`
	Restorable            bool             `json:"restorable"`
	Before                *Record          `json:"before,omitempty"`
	After                 *Record          `json:"after,omitempty"`
	CatalogBefore         []byte           `json:"catalogBefore,omitempty"`
	RulesBefore           []byte           `json:"rulesBefore,omitempty"`
	CatalogAfter          []byte           `json:"catalogAfter,omitempty"`
	RulesAfter            []byte           `json:"rulesAfter,omitempty"`
	NativeArtifactsBefore []NativeArtifact `json:"nativeArtifactsBefore,omitempty"`
	NativeArtifactsAfter  []NativeArtifact `json:"nativeArtifactsAfter,omitempty"`
}

func (e HistoryEntry) Validate() error {
	if e.SchemaVersion != HistorySchemaVersion || !validHistoryOperation(e.Operation) {
		return ErrMalformedHistory
	}
	if _, err := domain.NewOperationID(e.OperationID); err != nil {
		return ErrMalformedHistory
	}
	if _, err := domain.NewInstallationID(e.InstallationID); err != nil {
		return ErrMalformedHistory
	}
	timestamp, err := time.Parse(time.RFC3339, e.Timestamp)
	if err != nil || timestamp.Location() != time.UTC || e.Before == nil && e.After == nil {
		return ErrMalformedHistory
	}
	for _, record := range []*Record{e.Before, e.After} {
		if record != nil && (record.InstallationID != e.InstallationID || record.Validate() != nil) {
			return ErrMalformedHistory
		}
	}
	if e.Before != nil && e.After != nil && !samePersistedLogicalIdentity(*e.Before, *e.After) {
		return ErrMalformedHistory
	}
	if !validNativeArtifacts(e.Before, e.NativeArtifactsBefore, e.Restorable) || !validNativeArtifacts(e.After, e.NativeArtifactsAfter, e.Restorable) {
		return ErrMalformedHistory
	}
	retainedBytes := len(e.CatalogBefore) + len(e.RulesBefore) + len(e.CatalogAfter) + len(e.RulesAfter)
	for _, artifact := range e.NativeArtifactsBefore {
		retainedBytes += len(artifact.Bytes)
	}
	for _, artifact := range e.NativeArtifactsAfter {
		retainedBytes += len(artifact.Bytes)
	}
	if retainedBytes > maximumHistoryBytes {
		return ErrMalformedHistory
	}
	return nil
}

func validNativeArtifacts(record *Record, artifacts []NativeArtifact, required bool) bool {
	if record == nil || record.Lifecycle != "active" {
		return len(artifacts) == 0
	}
	if len(artifacts) == 0 {
		return !required
	}
	if len(artifacts) != len(record.Packages) {
		return false
	}
	for index, artifact := range artifacts {
		if artifact.PackageID != record.Packages[index].ID || len(artifact.Bytes) == 0 || index > 0 && artifacts[index-1].PackageID >= artifact.PackageID {
			return false
		}
	}
	return true
}

func validHistoryOperation(operation string) bool {
	return operation == "install" || operation == "update" || operation == "sync" || operation == "rollback" || operation == "uninstall"
}

func (s Store) HistoryRoot(installationID string) string {
	return filepath.Join(s.root, "history", installationID)
}

func (s Store) HistoryPath(installationID, operationID string) string {
	return filepath.Join(s.HistoryRoot(installationID), operationID+".json")
}

func (s Store) StageHistory(entry HistoryEntry) error {
	entry.Committed = false
	return s.writeHistory(entry, true, nil)
}

func (s Store) CommitHistory(entry HistoryEntry) error {
	staged := entry
	staged.Committed = false
	expectedEntry, err := marshalHistory(staged)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(s.HistoryPath(entry.InstallationID, entry.OperationID))
	if err != nil {
		return ErrStateChanged
	}
	currentEntry, err := decodeHistory(current)
	if err != nil {
		return ErrStateChanged
	}
	currentNormalized, err := marshalHistory(currentEntry)
	if err != nil || !bytes.Equal(currentNormalized, expectedEntry) {
		return ErrStateChanged
	}
	entry.Committed = true
	return s.writeHistory(entry, false, current)
}

func (s Store) writeHistory(entry HistoryEntry, createOnly bool, expected []byte) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	contents, err := marshalHistory(entry)
	if err != nil {
		return err
	}
	root := s.HistoryRoot(entry.InstallationID)
	if err := diskcapacity.Require(root, uint64(len(contents))); err != nil {
		return err
	}
	if err := privatepath.EnsureDirectory(root); err != nil {
		return fmt.Errorf("create history directory: %w", err)
	}
	path := s.HistoryPath(entry.InstallationID, entry.OperationID)
	if createOnly {
		if _, err := os.Lstat(path); err == nil {
			return ErrStateOccupied
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else if _, err := os.Lstat(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(root, ".history-*.tmp")
	if err != nil {
		return fmt.Errorf("create history temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure history temporary file: %w", err)
	}
	if err := writeAndSync(temporary, contents); err != nil {
		return fmt.Errorf("write history entry: %w", err)
	}
	if createOnly {
		if err := os.Link(temporaryPath, path); err != nil {
			if _, statErr := os.Lstat(path); statErr == nil {
				return ErrStateOccupied
			}
			return fmt.Errorf("commit new history entry: %w", err)
		}
		return nil
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, expected) {
		return ErrStateChanged
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit history entry: %w", err)
	}
	return nil
}

func marshalHistory(entry HistoryEntry) ([]byte, error) {
	contents, err := json.MarshalIndent(entry, "", "  ")
	if err != nil || len(contents) > maximumHistoryBytes {
		return nil, ErrMalformedHistory
	}
	return append(contents, '\n'), nil
}

// LoadOperationHistory returns the staged or committed history record bound to
// one operation. It is used only while reconciling that operation's marker.
func (s Store) LoadOperationHistory(installationID, operationID string) (HistoryEntry, bool, error) {
	if _, err := domain.NewInstallationID(installationID); err != nil {
		return HistoryEntry{}, false, ErrMalformedHistory
	}
	if _, err := domain.NewOperationID(operationID); err != nil {
		return HistoryEntry{}, false, ErrMalformedHistory
	}
	entry, err := s.loadHistoryFile(s.HistoryPath(installationID, operationID))
	if errors.Is(err, os.ErrNotExist) {
		return HistoryEntry{}, false, nil
	}
	if err != nil {
		return HistoryEntry{}, false, err
	}
	if entry.InstallationID != installationID || entry.OperationID != operationID {
		return HistoryEntry{}, false, ErrMalformedHistory
	}
	return entry, true, nil
}

func (s Store) LoadHistory(installationID string) ([]HistoryEntry, error) {
	if _, err := domain.NewInstallationID(installationID); err != nil {
		return nil, ErrMalformedHistory
	}
	entries, err := os.ReadDir(s.HistoryRoot(installationID))
	if errors.Is(err, os.ErrNotExist) {
		return []HistoryEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		if historyTemporaryName(entry.Name()) {
			if !historyTemporaryRegular(s.HistoryRoot(installationID), entry.Name()) {
				return nil, ErrMalformedHistory
			}
			continue
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return nil, ErrMalformedHistory
		}
		operationID := entry.Name()[:len(entry.Name())-len(filepath.Ext(entry.Name()))]
		if _, err := domain.NewOperationID(operationID); err != nil {
			return nil, ErrMalformedHistory
		}
		value, err := s.loadHistoryFile(filepath.Join(s.HistoryRoot(installationID), entry.Name()))
		if err != nil {
			return nil, err
		}
		if value.InstallationID != installationID || value.OperationID != operationID {
			return nil, ErrMalformedHistory
		}
		if !value.Committed {
			continue
		}
		result = append(result, value)
	}
	slices.SortFunc(result, func(left, right HistoryEntry) int {
		if left.Timestamp == right.Timestamp {
			return bytes.Compare([]byte(left.OperationID), []byte(right.OperationID))
		}
		return bytes.Compare([]byte(left.Timestamp), []byte(right.Timestamp))
	})
	return result, nil
}

func (s Store) LoadHistoryEntry(installationID, operationID string) (HistoryEntry, bool, error) {
	if _, err := domain.NewInstallationID(installationID); err != nil {
		return HistoryEntry{}, false, ErrMalformedHistory
	}
	if _, err := domain.NewOperationID(operationID); err != nil {
		return HistoryEntry{}, false, ErrMalformedHistory
	}
	entry, err := s.loadHistoryFile(s.HistoryPath(installationID, operationID))
	if errors.Is(err, os.ErrNotExist) {
		return HistoryEntry{}, false, nil
	}
	if err != nil {
		return HistoryEntry{}, false, err
	}
	if entry.InstallationID != installationID || entry.OperationID != operationID {
		return HistoryEntry{}, false, ErrMalformedHistory
	}
	if !entry.Committed {
		return HistoryEntry{}, false, nil
	}
	return entry, true, nil
}

// LoadStagedHistory returns every structurally valid, uncommitted history
// entry. A staged entry without an operation marker can only be left before
// target mutation begins; the lifecycle verifies that invariant before
// removing it.
func (s Store) LoadStagedHistory() ([]HistoryEntry, error) {
	root := filepath.Join(s.root, "history")
	installations, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []HistoryEntry{}, nil
	}
	if err != nil || len(installations) > 4096 {
		return nil, ErrMalformedHistory
	}
	result := []HistoryEntry{}
	total := 0
	for _, installation := range installations {
		installationID := installation.Name()
		if !installation.IsDir() {
			return nil, ErrMalformedHistory
		}
		if _, err := domain.NewInstallationID(installationID); err != nil {
			return nil, ErrMalformedHistory
		}
		entries, err := os.ReadDir(filepath.Join(root, installationID))
		if err != nil || total > maximumHistoryEntries-len(entries) {
			return nil, ErrMalformedHistory
		}
		total += len(entries)
		for _, item := range entries {
			if historyTemporaryName(item.Name()) {
				if !historyTemporaryRegular(filepath.Join(root, installationID), item.Name()) || os.Remove(filepath.Join(root, installationID, item.Name())) != nil {
					return nil, ErrMalformedHistory
				}
				continue
			}
			if item.IsDir() || filepath.Ext(item.Name()) != ".json" {
				return nil, ErrMalformedHistory
			}
			operationID := strings.TrimSuffix(item.Name(), ".json")
			if _, err := domain.NewOperationID(operationID); err != nil {
				return nil, ErrMalformedHistory
			}
			entry, err := s.loadHistoryFile(filepath.Join(root, installationID, item.Name()))
			if err != nil || entry.InstallationID != installationID || entry.OperationID != operationID {
				return nil, ErrMalformedHistory
			}
			if !entry.Committed {
				result = append(result, entry)
			}
		}
	}
	slices.SortFunc(result, func(left, right HistoryEntry) int {
		if left.InstallationID == right.InstallationID {
			return strings.Compare(left.OperationID, right.OperationID)
		}
		return strings.Compare(left.InstallationID, right.InstallationID)
	})
	return result, nil
}

func historyTemporaryName(name string) bool {
	return strings.HasPrefix(name, ".history-") && strings.HasSuffix(name, ".tmp") && len(name) > len(".history-.tmp")
}

func historyTemporaryRegular(root, name string) bool {
	info, err := os.Lstat(filepath.Join(root, name))
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func (s Store) loadHistoryFile(path string) (HistoryEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return HistoryEntry{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maximumHistoryBytes {
		return HistoryEntry{}, ErrMalformedHistory
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return HistoryEntry{}, err
	}
	return decodeHistory(contents)
}

func decodeHistory(contents []byte) (HistoryEntry, error) {
	var header struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if json.Unmarshal(contents, &header) != nil {
		return HistoryEntry{}, ErrMalformedHistory
	}
	if header.SchemaVersion != HistorySchemaVersion {
		return HistoryEntry{}, ErrUnsupportedHistorySchema
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var entry HistoryEntry
	if decoder.Decode(&entry) != nil || decoder.Decode(new(any)) != io.EOF || entry.Validate() != nil {
		return HistoryEntry{}, ErrMalformedHistory
	}
	return entry, nil
}

func (s Store) DeleteHistory(installationID string, operationIDs []string) error {
	if _, err := domain.NewInstallationID(installationID); err != nil {
		return ErrMalformedHistory
	}
	for _, operationID := range operationIDs {
		if _, err := domain.NewOperationID(operationID); err != nil {
			return ErrMalformedHistory
		}
		if err := os.Remove(s.HistoryPath(installationID, operationID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	entries, err := os.ReadDir(s.HistoryRoot(installationID))
	if err == nil && len(entries) == 0 {
		_ = os.Remove(s.HistoryRoot(installationID))
	}
	return nil
}
