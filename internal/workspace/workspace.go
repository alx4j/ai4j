// Package workspace owns AI4J's short-lived operation directories. A small
// marker plus an operating-system lease lets a later command distinguish a
// crashed AI4J workspace from a live or unrelated directory.
package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/alx4j/ai4j/internal/host/privatepath"
)

type Purpose string

const (
	ValidateSource Purpose = "validate-source"
	BuildSource    Purpose = "build-source"
	Lifecycle      Purpose = "lifecycle"
	BuildStage     Purpose = "build-stage"
	InitStage      Purpose = "init-stage"
	Recovery       Purpose = "recovery"
)

const (
	markerName         = ".ai4j-workspace.json"
	leaseName          = ".ai4j-workspace.lock"
	markerSchema       = 1
	maximumOrphanCount = 256
)

var errInvalidWorkspace = errors.New("invalid AI4J workspace")

type marker struct {
	SchemaVersion int     `json:"schemaVersion"`
	Owner         string  `json:"owner"`
	Purpose       Purpose `json:"purpose"`
	Directory     string  `json:"directory"`
}

func (m marker) valid() bool {
	prefix, ok := purposePrefix(m.Purpose)
	return ok && m.SchemaVersion == markerSchema && m.Owner == "ai4j" &&
		filepath.Base(m.Directory) == m.Directory && len(m.Directory) > len(prefix) &&
		len(m.Directory) <= 255 && len(prefix) <= len(m.Directory) && m.Directory[:len(prefix)] == prefix
}

type Workspace struct {
	mu        sync.Mutex
	path      string
	lease     *lease
	removeAll func(string) error
	closed    bool
	purpose   Purpose
}

func Create(root string, purpose Purpose) (*Workspace, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	if _, ok := purposePrefix(purpose); !ok {
		return nil, errInvalidWorkspace
	}
	if err := Scavenge(root); err != nil {
		return nil, err
	}
	prefix, _ := purposePrefix(purpose)
	path, err := os.MkdirTemp(root, prefix)
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(metadataPath(path, markerName))
			_ = os.Remove(metadataPath(path, leaseName))
			_ = privatepath.RemoveAll(path)
		}
	}()
	if err := privatepath.EnsureDirectory(path); err != nil {
		return nil, err
	}
	lease, err := createLease(metadataPath(path, leaseName))
	if err != nil {
		return nil, err
	}
	value := marker{SchemaVersion: markerSchema, Owner: "ai4j", Purpose: purpose, Directory: filepath.Base(path)}
	if err := writeMarker(metadataPath(path, markerName), value); err != nil {
		_ = lease.release()
		return nil, err
	}
	cleanup = false
	return &Workspace{path: path, lease: lease, removeAll: privatepath.RemoveAll, purpose: purpose}, nil
}

func (w *Workspace) Path() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ""
	}
	return w.path
}

func (w *Workspace) Close() error {
	if w == nil {
		return errInvalidWorkspace
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	if err := w.removeAll(w.path); err != nil {
		return err
	}
	if err := w.lease.release(); err != nil {
		return err
	}
	if err := os.Remove(metadataPath(w.path, leaseName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(metadataPath(w.path, markerName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	w.closed = true
	return nil
}

// Publish atomically renames a completed staging workspace to a new output
// path on the same filesystem. Metadata cleanup follows the committed rename,
// so a crash cannot strand an unmarked partial staging tree.
func (w *Workspace) Publish(output string) error {
	if w == nil || !filepath.IsAbs(output) || filepath.Clean(output) != output {
		return errInvalidWorkspace
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.purpose != BuildStage && w.purpose != InitStage || filepath.Dir(output) != filepath.Dir(w.path) {
		return errInvalidWorkspace
	}
	if _, err := os.Lstat(output); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(w.path, output); err != nil {
		return err
	}
	w.closed = true
	if err := w.lease.release(); err == nil {
		if err := os.Remove(metadataPath(w.path, leaseName)); err == nil || errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(metadataPath(w.path, markerName))
		}
	}
	return nil
}

func Scavenge(root string) error {
	return scavenge(root, privatepath.RemoveAll)
}

func scavenge(root string, removeAll func(string) error) error {
	root, err := canonicalRoot(root)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	candidates := orphanCandidates(entries)
	if len(candidates) > maximumOrphanCount {
		return errors.New("too many AI4J orphan-workspace candidates")
	}
	for _, name := range candidates {
		purpose, _ := purposeForDirectory(name)
		path := filepath.Join(root, name)
		info, pathErr := os.Lstat(path)
		pathPresent := pathErr == nil
		if pathPresent && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			continue
		}
		if pathErr != nil && !errors.Is(pathErr, os.ErrNotExist) {
			return pathErr
		}
		observed, err := readMarker(metadataPath(path, markerName))
		if err != nil || observed.Purpose != purpose || observed.Directory != name {
			continue
		}
		lease, err := tryLease(metadataPath(path, leaseName))
		if errors.Is(err, errLeaseBusy) {
			continue
		}
		if errors.Is(err, os.ErrNotExist) && !pathPresent {
			repeated, repeatedErr := readMarker(metadataPath(path, markerName))
			if repeatedErr == nil && repeated == observed {
				if err := os.Remove(metadataPath(path, markerName)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		repeated, repeatedErr := readMarker(metadataPath(path, markerName))
		if repeatedErr != nil || repeated != observed {
			_ = lease.release()
			continue
		}
		if pathPresent {
			if err := removeAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				_ = lease.release()
				return err
			}
		}
		if err := lease.release(); err != nil {
			return err
		}
		if err := os.Remove(metadataPath(path, leaseName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Remove(metadataPath(path, markerName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func orphanCandidates(entries []os.DirEntry) []string {
	candidates := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if _, ok := purposeForDirectory(name); ok && entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			candidates[name] = struct{}{}
		}
		if strings.HasPrefix(name, ".") && strings.HasSuffix(name, markerName) {
			candidate := strings.TrimSuffix(strings.TrimPrefix(name, "."), markerName)
			if _, ok := purposeForDirectory(candidate); ok {
				candidates[candidate] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func metadataPath(workspacePath, suffix string) string {
	return filepath.Join(filepath.Dir(workspacePath), "."+filepath.Base(workspacePath)+suffix)
}

func canonicalRoot(root string) (string, error) {
	if root == "" {
		root = os.TempDir()
	}
	absolute, err := filepath.Abs(root)
	if err != nil || filepath.Clean(absolute) != absolute {
		return "", errInvalidWorkspace
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", errInvalidWorkspace
	}
	return absolute, nil
}

func purposePrefix(purpose Purpose) (string, bool) {
	switch purpose {
	case ValidateSource:
		return "ai4j-validate-", true
	case BuildSource:
		return "ai4j-build-source-", true
	case Lifecycle:
		return "ai4j-lifecycle-", true
	case BuildStage:
		return ".ai4j-build-", true
	case InitStage:
		return ".ai4j-init-", true
	case Recovery:
		return ".ai4j-recovery-", true
	default:
		return "", false
	}
}

func purposeForDirectory(name string) (Purpose, bool) {
	purposes := []Purpose{ValidateSource, BuildSource, Lifecycle, BuildStage, InitStage, Recovery}
	for _, purpose := range purposes {
		prefix, _ := purposePrefix(purpose)
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			return purpose, true
		}
	}
	return "", false
}

func writeMarker(path string, value marker) error {
	contents, err := json.Marshal(value)
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func readMarker(path string) (marker, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024 {
		return marker{}, errInvalidWorkspace
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return marker{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var value marker
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || !value.valid() {
		return marker{}, errInvalidWorkspace
	}
	return value, nil
}
