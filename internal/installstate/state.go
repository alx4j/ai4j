// Package installstate stores AI4J installation ownership records.
package installstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/host/privatepath"
)

const (
	LegacySchemaVersion = 1
	SchemaVersion       = 2
	maximumBytes        = 1 << 20
)

var (
	ErrUnsupportedSchema             = errors.New("installation state schema is unsupported")
	ErrMalformedState                = errors.New("installation state is malformed")
	ErrStateOccupied                 = errors.New("installation state destination is occupied")
	ErrStateChanged                  = errors.New("installation state changed")
	ErrInstallationSelectionRequired = errors.New("installation selection is required")
	identifierPattern                = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	digestPattern                    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Source struct {
	Mode           string  `json:"mode,omitempty"`
	Selection      string  `json:"selection"`
	Repository     string  `json:"repository"`
	RequestedRef   *string `json:"requestedRef"`
	RefKind        string  `json:"refKind"`
	Commit         string  `json:"commit"`
	RenderedDigest string  `json:"renderedDigest,omitempty"`
	Checkout       string  `json:"checkout,omitempty"`
	SourceDigest   string  `json:"sourceDigest,omitempty"`
	Dirty          bool    `json:"dirty,omitempty"`
	BundleDigest   string  `json:"bundleDigest,omitempty"`
}

type Selection struct {
	All      bool     `json:"all"`
	Assets   []string `json:"assets"`
	Bundles  []string `json:"bundles"`
	Resolved []string `json:"resolved"`
}

type OwnedFile struct {
	Path     string `json:"path"`
	Checksum string `json:"checksum"`
}

type LastOperation struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

type Record struct {
	SchemaVersion   int           `json:"schemaVersion"`
	InstallationID  string        `json:"installationId"`
	ToolkitID       string        `json:"toolkitId"`
	DeclarationID   string        `json:"declarationId,omitempty"`
	SettingsCreated bool          `json:"settingsCreated,omitempty"`
	ToolkitVersion  string        `json:"toolkitVersion,omitempty"`
	PluginID        string        `json:"pluginId"`
	PackagePath     string        `json:"packagePath,omitempty"`
	MarketplaceID   string        `json:"marketplaceId,omitempty"`
	Source          Source        `json:"source"`
	Target          string        `json:"target"`
	Host            string        `json:"host,omitempty"`
	Scope           string        `json:"scope"`
	ScopeRoot       string        `json:"scopeRoot,omitempty"`
	Lifecycle       string        `json:"lifecycle,omitempty"`
	Selection       Selection     `json:"selection,omitempty"`
	NativeResources []string      `json:"nativeResources,omitempty"`
	History         []string      `json:"history,omitempty"`
	Health          string        `json:"health,omitempty"`
	AI4JVersion     string        `json:"ai4jVersion"`
	Catalog         OwnedFile     `json:"catalog"`
	Rules           OwnedFile     `json:"rules"`
	LastOperation   LastOperation `json:"lastOperation"`
}

func (r Record) Validate() error {
	if r.SchemaVersion != LegacySchemaVersion && r.SchemaVersion != SchemaVersion {
		return ErrMalformedState
	}
	if !identifierPattern.MatchString(r.InstallationID) || !identifierPattern.MatchString(r.ToolkitID) ||
		!identifierPattern.MatchString(r.PluginID) ||
		r.AI4JVersion == "" || len(r.AI4JVersion) > 128 {
		return ErrMalformedState
	}
	if _, err := domain.NewInstallationID(r.InstallationID); err != nil {
		return ErrMalformedState
	}
	if !validStateSource(r.Source) {
		return ErrMalformedState
	}
	if _, err := domain.NewOperationID(r.LastOperation.ID); err != nil {
		return ErrMalformedState
	}
	timestamp, err := time.Parse(time.RFC3339, r.LastOperation.Timestamp)
	if err != nil || timestamp.Location() != time.UTC {
		return ErrMalformedState
	}
	if r.SchemaVersion == LegacySchemaVersion {
		if r.Target != "claude" || r.Scope != "user" || !validOwnedFile(r.Catalog, "state/catalog/.claude-plugin/marketplace.json") || !validOwnedFile(r.Rules, ".claude/rules/ai4j.md") {
			return ErrMalformedState
		}
		return nil
	}
	if (r.Source.RenderedDigest != "" && !digestPattern.MatchString(r.Source.RenderedDigest)) ||
		(r.Target != "claude" && r.Target != "codex") || (r.Host != "darwin-arm64" && r.Host != "windows-amd64") ||
		(r.Scope != "user" && r.Scope != "project-local" && r.Scope != "project-shared") || !filepath.IsAbs(r.ScopeRoot) ||
		(r.Lifecycle != "active" && r.Lifecycle != "archived") ||
		(r.Health != "healthy" && r.Health != "drifted" && r.Health != "unknown" && r.Health != "recovery_required") ||
		(!r.Selection.All && len(r.Selection.Assets) == 0 && len(r.Selection.Bundles) == 0) ||
		(r.Selection.All && (len(r.Selection.Assets) != 0 || len(r.Selection.Bundles) != 0)) ||
		!validIdentifiers(r.Selection.Assets) || !validIdentifiers(r.Selection.Bundles) || !validIdentifiers(r.Selection.Resolved) ||
		!sortedUnique(r.NativeResources) || !validIdentifiers(r.History) ||
		(r.MarketplaceID != "" && !identifierPattern.MatchString(r.MarketplaceID)) || (r.DeclarationID != "" && !identifierPattern.MatchString(r.DeclarationID)) || len(r.ToolkitVersion) > 64 || len(r.PackagePath) > 1024 || (r.PackagePath != "" && (path.Clean(r.PackagePath) != r.PackagePath || path.IsAbs(r.PackagePath) || path.Clean(r.PackagePath) == ".." || strings.HasPrefix(path.Clean(r.PackagePath), "../"))) {
		return ErrMalformedState
	}
	ownedEmpty := r.Catalog == (OwnedFile{}) && r.Rules == (OwnedFile{})
	catalogValid := validCatalogFile(r.Catalog, r.InstallationID) || r.Scope == "project-shared" && validOwnedFile(r.Catalog, ".claude/settings.json")
	ownedClaude := catalogValid && (r.Rules == (OwnedFile{}) || validRulesFile(r.Rules, r.InstallationID, r.DeclarationID))
	if r.SettingsCreated && r.Scope != "project-shared" {
		return ErrMalformedState
	}
	if r.Target == "claude" && r.Lifecycle == "active" && !ownedClaude || r.Lifecycle == "archived" && !ownedEmpty && !ownedClaude {
		return ErrMalformedState
	}
	return nil
}

func validStateSource(source Source) bool {
	if source.Mode == "development_source" {
		return source.Selection == domain.ExplicitSource().String() && source.Repository == "" && source.RequestedRef == nil && source.RefKind == "" && source.Commit == "" && filepath.IsAbs(source.Checkout) && filepath.Clean(source.Checkout) == source.Checkout && digestPattern.MatchString(source.SourceDigest) && digestPattern.MatchString(source.BundleDigest)
	}
	if source.Mode != "github" && source.Mode != "" {
		return false
	}
	if _, err := domain.NewRepositoryIdentity(source.Repository); err != nil {
		return false
	}
	if _, err := domain.NewCommitOID(source.Commit); err != nil {
		return false
	}
	if source.Selection != domain.BuiltInDefaultSource().String() && source.Selection != domain.ExplicitSource().String() || source.Selection == domain.BuiltInDefaultSource().String() && source.Repository != "github.com/alx4j/ai4j" || source.RefKind != "default_branch" && source.RefKind != "branch" && source.RefKind != "tag" && source.RefKind != "commit" || source.RequestedRef != nil && (*source.RequestedRef == "" || len(*source.RequestedRef) > 512) || (source.RefKind == "default_branch") != (source.RequestedRef == nil) || source.RefKind == "commit" && source.RequestedRef != nil && *source.RequestedRef != source.Commit {
		return false
	}
	return source.Checkout == "" && source.SourceDigest == "" && source.BundleDigest == "" && !source.Dirty
}

func validIdentifiers(values []string) bool {
	if len(values) > 4096 {
		return false
	}
	for index, value := range values {
		if !identifierPattern.MatchString(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func sortedUnique(values []string) bool {
	if len(values) > 4096 {
		return false
	}
	for index, value := range values {
		if value == "" || len(value) > 512 || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func validOwnedFile(file OwnedFile, expectedPath string) bool {
	return file.Path == expectedPath && digestPattern.MatchString(file.Checksum)
}

func validCatalogFile(file OwnedFile, installationID string) bool {
	return validOwnedFile(file, "state/catalog/.claude-plugin/marketplace.json") ||
		validOwnedFile(file, "state/catalogs/"+installationID+"/.claude-plugin/marketplace.json") ||
		strings.HasPrefix(file.Path, "state/bundles/") && strings.HasSuffix(file.Path, "/.claude-plugin/marketplace.json") && len(strings.TrimSuffix(strings.TrimPrefix(file.Path, "state/bundles/"), "/.claude-plugin/marketplace.json")) == 64 && digestPattern.MatchString(strings.TrimSuffix(strings.TrimPrefix(file.Path, "state/bundles/"), "/.claude-plugin/marketplace.json")) && digestPattern.MatchString(file.Checksum)
}

func validRulesFile(file OwnedFile, installationID, declarationID string) bool {
	return validOwnedFile(file, ".claude/rules/ai4j.md") || validOwnedFile(file, ".claude/rules/ai4j-"+installationID+".md") || validOwnedFile(file, "rules/ai4j-"+installationID+".md") || declarationID != "" && validOwnedFile(file, ".claude/rules/"+declarationID+".md")
}

type Snapshot struct {
	SchemaVersion     int
	Installations     []Record
	MigrationRequired bool
}

type stateDocument struct {
	SchemaVersion int      `json:"schemaVersion"`
	Installations []Record `json:"installations"`
}

type migrationJournal struct {
	SchemaVersion int    `json:"schemaVersion"`
	FromVersion   int    `json:"fromVersion"`
	ToVersion     int    `json:"toVersion"`
	SourceDigest  string `json:"sourceDigest"`
}

type Store struct {
	root string
	home string
}

type stateExpectation struct {
	contents []byte
	present  bool
}

func NewStore(home string) (Store, error) {
	if !filepath.IsAbs(home) {
		return Store{}, fmt.Errorf("installation state home must be absolute")
	}
	return Store{root: filepath.Join(home, "Library", "Application Support", "ai4j", "state"), home: filepath.Clean(home)}, nil
}

func NewStoreAt(home, dataRoot string) (Store, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(dataRoot) || filepath.Clean(dataRoot) != dataRoot {
		return Store{}, fmt.Errorf("installation state paths must be absolute and clean")
	}
	return Store{root: filepath.Join(dataRoot, "state"), home: filepath.Clean(home)}, nil
}

func (s Store) Path() string          { return filepath.Join(s.root, "installation.json") }
func (s Store) MigrationPath() string { return filepath.Join(s.root, "migration.json") }
func (s Store) Root() string          { return s.root }
func (s Store) DataRoot() string      { return filepath.Dir(s.root) }

func (s Store) MigrationPending() (bool, error) {
	_, present, err := s.loadMigration()
	return present, err
}

func (s Store) loadMigration() (migrationJournal, bool, error) {
	info, err := os.Lstat(s.MigrationPath())
	if errors.Is(err, os.ErrNotExist) {
		return migrationJournal{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return migrationJournal{}, false, ErrMalformedState
	}
	contents, err := os.ReadFile(s.MigrationPath())
	if err != nil || len(contents) > maximumBytes {
		return migrationJournal{}, false, ErrMalformedState
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var journal migrationJournal
	if decoder.Decode(&journal) != nil || decoder.Decode(new(any)) != io.EOF || journal.SchemaVersion != 1 || journal.FromVersion != LegacySchemaVersion || journal.ToVersion != SchemaVersion || !digestPattern.MatchString(journal.SourceDigest) {
		return migrationJournal{}, false, ErrMalformedState
	}
	return journal, true, nil
}

func (s Store) Snapshot() (Snapshot, error) {
	contents, present, err := s.readState()
	if err != nil || !present {
		return Snapshot{}, err
	}
	var header struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if json.Unmarshal(contents, &header) != nil {
		return Snapshot{}, ErrMalformedState
	}
	switch header.SchemaVersion {
	case LegacySchemaVersion:
		record, err := decodeRecord(contents, LegacySchemaVersion)
		if err != nil {
			return Snapshot{}, err
		}
		return Snapshot{SchemaVersion: LegacySchemaVersion, Installations: []Record{migrateRecord(record, s.home)}, MigrationRequired: true}, nil
	case SchemaVersion:
		document, err := decodeDocument(contents)
		if err != nil {
			return Snapshot{}, err
		}
		return Snapshot{SchemaVersion: SchemaVersion, Installations: slices.Clone(document.Installations)}, nil
	default:
		return Snapshot{}, ErrUnsupportedSchema
	}
}

func (s Store) LoadAll() ([]Record, error) {
	snapshot, err := s.Snapshot()
	if err != nil {
		return nil, err
	}
	return slices.Clone(snapshot.Installations), nil
}

func (s Store) LoadByID(id string) (Record, bool, error) {
	records, err := s.LoadAll()
	if err != nil {
		return Record{}, false, err
	}
	for _, record := range records {
		if record.InstallationID == id {
			return record, true, nil
		}
	}
	return Record{}, false, nil
}

func (s Store) Load() (Record, bool, error) {
	records, err := s.LoadAll()
	if err != nil {
		return Record{}, false, err
	}
	if len(records) == 0 {
		return Record{}, false, nil
	}
	if len(records) != 1 {
		return Record{}, false, ErrInstallationSelectionRequired
	}
	return records[0], true, nil
}

func (s Store) Save(record Record) error {
	if err := normalizeRecord(&record, s.home); err != nil {
		return err
	}
	records, migration, expected, err := s.recordsForWrite()
	if err != nil {
		return err
	}
	found := false
	for index := range records {
		if records[index].InstallationID == record.InstallationID {
			records[index] = record
			found = true
			break
		}
	}
	if !found && len(records) != 0 {
		return ErrStateChanged
	}
	if !found {
		records = append(records, record)
	}
	return s.commit(records, migration, expected)
}

func (s Store) SaveNew(record Record) error {
	if err := normalizeRecord(&record, s.home); err != nil {
		return err
	}
	records, migration, expected, err := s.recordsForWrite()
	if err != nil {
		return err
	}
	for _, current := range records {
		if current.InstallationID == record.InstallationID || sameLogicalIdentity(current, record) {
			return ErrStateOccupied
		}
	}
	records = append(records, record)
	return s.commit(records, migration, expected)
}

func (s Store) Delete(record Record) error {
	if err := normalizeRecord(&record, s.home); err != nil {
		return err
	}
	records, migration, expected, err := s.recordsForWrite()
	if err != nil {
		return err
	}
	encodedExpected, _ := encodeRecord(record)
	index := -1
	for candidate, current := range records {
		encodedCurrent, _ := encodeRecord(current)
		if current.InstallationID == record.InstallationID && bytes.Equal(encodedCurrent, encodedExpected) {
			index = candidate
			break
		}
	}
	if index < 0 {
		return ErrStateChanged
	}
	records = append(records[:index], records[index+1:]...)
	if len(records) == 0 {
		if migration {
			return s.commit(records, true, expected)
		}
		if !s.stateMatches(expected) {
			return ErrStateChanged
		}
		if err := os.Remove(s.Path()); err != nil {
			return fmt.Errorf("remove installation state: %w", err)
		}
		return nil
	}
	return s.commit(records, migration, expected)
}

func (s Store) Migrate() (bool, error) {
	expectedContents, expectedPresent, expectationErr := s.readState()
	if expectationErr != nil {
		return false, expectationErr
	}
	snapshot, err := s.Snapshot()
	if err != nil {
		return false, err
	}
	pending, err := s.MigrationPending()
	if err != nil {
		return false, err
	}
	if !snapshot.MigrationRequired && !pending {
		return false, nil
	}
	return true, s.commit(snapshot.Installations, true, stateExpectation{contents: expectedContents, present: expectedPresent})
}

func (s Store) recordsForWrite() ([]Record, bool, stateExpectation, error) {
	expectedContents, expectedPresent, err := s.readState()
	if err != nil {
		return nil, false, stateExpectation{}, err
	}
	snapshot, err := s.Snapshot()
	if err != nil {
		return nil, false, stateExpectation{}, err
	}
	pending, err := s.MigrationPending()
	if err != nil {
		return nil, false, stateExpectation{}, err
	}
	return slices.Clone(snapshot.Installations), snapshot.MigrationRequired || pending, stateExpectation{contents: expectedContents, present: expectedPresent}, nil
}

func (s Store) readState() ([]byte, bool, error) {
	info, err := os.Lstat(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, false, ErrMalformedState
	}
	file, err := os.Open(s.Path())
	if err != nil {
		return nil, false, fmt.Errorf("open installation state: %w", err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("read installation state: %w", err)
	}
	if len(contents) > maximumBytes {
		return nil, false, ErrMalformedState
	}
	return contents, true, nil
}

func decodeRecord(contents []byte, version int) (Record, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var record Record
	if decoder.Decode(&record) != nil || decoder.Decode(new(any)) != io.EOF || record.SchemaVersion != version || record.Validate() != nil {
		return Record{}, ErrMalformedState
	}
	return record, nil
}

func decodeDocument(contents []byte) (stateDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var document stateDocument
	if decoder.Decode(&document) != nil || decoder.Decode(new(any)) != io.EOF || document.SchemaVersion != SchemaVersion || document.Installations == nil || len(document.Installations) > 4096 {
		return stateDocument{}, ErrMalformedState
	}
	for index, record := range document.Installations {
		if record.Validate() != nil || record.SchemaVersion != SchemaVersion || index > 0 && document.Installations[index-1].InstallationID >= record.InstallationID {
			return stateDocument{}, ErrMalformedState
		}
	}
	return document, nil
}

func normalizeRecord(record *Record, home string) error {
	if record.SchemaVersion == LegacySchemaVersion {
		*record = migrateRecord(*record, home)
	}
	if record.SchemaVersion != SchemaVersion {
		return ErrMalformedState
	}
	slices.Sort(record.Selection.Assets)
	slices.Sort(record.Selection.Bundles)
	slices.Sort(record.Selection.Resolved)
	slices.Sort(record.NativeResources)
	slices.Sort(record.History)
	return record.Validate()
}

func migrateRecord(record Record, home string) Record {
	record.SchemaVersion = SchemaVersion
	record.Source.Mode = "github"
	record.Host = "darwin-arm64"
	record.ScopeRoot = filepath.Clean(home)
	record.Lifecycle = "active"
	record.Selection = Selection{All: true, Assets: []string{}, Bundles: []string{}, Resolved: []string{}}
	record.NativeResources = []string{"claude:ai4j-default@ai4j", "claude:marketplace:ai4j"}
	record.MarketplaceID = "ai4j"
	record.ToolkitVersion = "unversioned"
	record.Health = "unknown"
	return record
}

func sameLogicalIdentity(left, right Record) bool {
	return left.Target == right.Target && left.Scope == right.Scope && filepath.Clean(left.ScopeRoot) == filepath.Clean(right.ScopeRoot) && left.ToolkitID == right.ToolkitID
}

func encodeRecord(record Record) ([]byte, error) {
	if err := record.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

func (s Store) commit(records []Record, migration bool, expected stateExpectation) error {
	for index := range records {
		if err := normalizeRecord(&records[index], s.home); err != nil {
			return err
		}
	}
	slices.SortFunc(records, func(left, right Record) int {
		return bytes.Compare([]byte(left.InstallationID), []byte(right.InstallationID))
	})
	document := stateDocument{SchemaVersion: SchemaVersion, Installations: records}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode installation state: %w", err)
	}
	contents = append(contents, '\n')
	if len(contents) > maximumBytes {
		return ErrMalformedState
	}
	if err := diskcapacity.Require(s.root, uint64(len(contents))); err != nil {
		return err
	}
	if err := privatepath.EnsureDirectory(s.root); err != nil {
		return fmt.Errorf("create installation state directory: %w", err)
	}
	if migration {
		journal, pending, err := s.loadMigration()
		if err != nil {
			return err
		}
		if !pending {
			if err := s.beginMigration(); err != nil {
				return err
			}
		} else if legacy, present, readErr := s.readState(); readErr != nil {
			return readErr
		} else if present {
			var header struct {
				SchemaVersion int `json:"schemaVersion"`
			}
			if json.Unmarshal(legacy, &header) == nil && header.SchemaVersion == LegacySchemaVersion {
				digest := sha256.Sum256(legacy)
				if hex.EncodeToString(digest[:]) != journal.SourceDigest {
					return ErrStateChanged
				}
			}
		}
	}
	if err := s.writeState(contents, false, expected); err != nil {
		return err
	}
	if migration {
		if err := os.Remove(s.MigrationPath()); err != nil {
			return fmt.Errorf("complete installation state migration: %w", err)
		}
	}
	return nil
}

func (s Store) beginMigration() error {
	legacy, present, err := s.readState()
	if err != nil || !present {
		return err
	}
	digest := sha256.Sum256(legacy)
	journal := migrationJournal{SchemaVersion: 1, FromVersion: LegacySchemaVersion, ToVersion: SchemaVersion, SourceDigest: hex.EncodeToString(digest[:])}
	contents, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	if err := diskcapacity.Require(s.root, uint64(len(contents))); err != nil {
		return err
	}
	file, err := os.OpenFile(s.MigrationPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("begin installation state migration: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return fmt.Errorf("write installation state migration: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync installation state migration: %w", err)
	}
	return file.Close()
}

func (s Store) writeState(contents []byte, createOnly bool, expected stateExpectation) error {
	temporary, err := os.CreateTemp(s.root, ".installation-*.tmp")
	if err != nil {
		return fmt.Errorf("create installation state temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure installation state temporary file: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		return fmt.Errorf("write installation state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync installation state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close installation state: %w", err)
	}
	if !s.stateMatches(expected) {
		return ErrStateChanged
	}
	if createOnly {
		if err := os.Link(temporaryPath, s.Path()); err != nil {
			return fmt.Errorf("commit new installation state: %w", err)
		}
		return nil
	}
	if err := os.Rename(temporaryPath, s.Path()); err != nil {
		return fmt.Errorf("commit installation state: %w", err)
	}
	removeTemporary = false
	return nil
}

func (s Store) stateMatches(expected stateExpectation) bool {
	contents, present, err := s.readState()
	return err == nil && present == expected.present && bytes.Equal(contents, expected.contents)
}
