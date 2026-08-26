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

	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/host/privatepath"
)

const (
	MarkerSchemaVersion    = 1
	maximumMarkerResources = 1024
)

var (
	ErrUnsupportedMarkerSchema = errors.New("operation marker schema is unsupported")
	ErrMalformedMarker         = errors.New("operation marker is malformed")
)

type Marker struct {
	SchemaVersion  int      `json:"schemaVersion"`
	Operation      string   `json:"operation"`
	OperationID    string   `json:"operationId"`
	InstallationID string   `json:"installationId"`
	Commit         string   `json:"commit"`
	Resources      []string `json:"resources"`
}

func (m Marker) Validate() error {
	if m.SchemaVersion != MarkerSchemaVersion || !validMarkerOperation(m.Operation) ||
		!identifierPattern.MatchString(m.OperationID) || !identifierPattern.MatchString(m.InstallationID) {
		return ErrMalformedMarker
	}
	if _, err := domain.NewCommitOID(m.Commit); err != nil && !digestPattern.MatchString(m.Commit) {
		return ErrMalformedMarker
	}
	if len(m.Resources) == 0 || len(m.Resources) > maximumMarkerResources || !slices.IsSorted(m.Resources) {
		return ErrMalformedMarker
	}
	for index, resource := range m.Resources {
		if resource == "" || len(resource) > 512 || index > 0 && m.Resources[index-1] == resource {
			return ErrMalformedMarker
		}
	}
	return nil
}

func validMarkerOperation(operation string) bool {
	return operation == "install" || operation == "update" || operation == "sync" || operation == "rollback" || operation == "uninstall" || operation == "history_purge"
}

func NewResourceMarker(operation, operationID, installationID, commit string, resources []string) (Marker, error) {
	resources = slices.Clone(resources)
	slices.Sort(resources)
	resources = slices.Compact(resources)
	marker := Marker{
		SchemaVersion: MarkerSchemaVersion, Operation: operation, OperationID: operationID,
		InstallationID: installationID, Commit: commit, Resources: resources,
	}
	if err := marker.Validate(); err != nil {
		return Marker{}, err
	}
	return marker, nil
}

func (s Store) MarkerPath() string { return filepath.Join(s.root, "operation.json") }

func (s Store) LoadMarker() (Marker, bool, error) {
	info, err := os.Lstat(s.MarkerPath())
	if errors.Is(err, os.ErrNotExist) {
		return Marker{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return Marker{}, false, ErrMalformedMarker
	}
	file, err := os.Open(s.MarkerPath())
	if err != nil {
		return Marker{}, false, fmt.Errorf("open operation marker: %w", err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil || len(contents) > maximumBytes {
		return Marker{}, false, ErrMalformedMarker
	}
	var header struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if json.Unmarshal(contents, &header) != nil {
		return Marker{}, false, ErrMalformedMarker
	}
	if header.SchemaVersion != MarkerSchemaVersion {
		return Marker{}, false, ErrUnsupportedMarkerSchema
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var marker Marker
	if decoder.Decode(&marker) != nil || decoder.Decode(new(any)) != io.EOF || marker.Validate() != nil {
		return Marker{}, false, ErrMalformedMarker
	}
	return marker, true, nil
}

func (s Store) SaveMarker(marker Marker) error {
	if err := marker.Validate(); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encode operation marker: %w", err)
	}
	contents = append(contents, '\n')
	if err := diskcapacity.Require(s.root, uint64(len(contents))); err != nil {
		return err
	}
	if err := privatepath.EnsureDirectory(s.root); err != nil {
		return fmt.Errorf("create operation marker directory: %w", err)
	}
	if _, err := os.Lstat(s.MarkerPath()); err == nil {
		return ErrMalformedMarker
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect operation marker: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".operation-*.tmp")
	if err != nil {
		return fmt.Errorf("create operation marker temporary file: %w", err)
	}
	path := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(path)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure operation marker temporary file: %w", err)
	}
	if err := writeAndSync(temporary, contents); err != nil {
		return fmt.Errorf("write operation marker: %w", err)
	}
	if err := os.Rename(path, s.MarkerPath()); err != nil {
		return fmt.Errorf("commit operation marker: %w", err)
	}
	return nil
}

func (s Store) DeleteMarker() error {
	info, err := os.Lstat(s.MarkerPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return ErrMalformedMarker
	}
	return os.Remove(s.MarkerPath())
}

func writeAndSync(file *os.File, contents []byte) error {
	if _, err := file.Write(contents); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Close()
}
