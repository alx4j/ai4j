// Package diskcapacity provides the small cross-platform disk-space check used
// immediately before AI4J writes bounded data.
package diskcapacity

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

const filesystemMetadataBytes uint64 = 4 << 10

var (
	ErrInsufficient = errors.New("insufficient disk capacity")
	ErrUnavailable  = errors.New("disk capacity is unavailable")
)

// Require verifies that the filesystem containing path has room for payload
// plus one filesystem metadata block. Missing path components are resolved to
// their nearest existing ancestor.
func Require(path string, payload uint64) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || payload > math.MaxUint64-filesystemMetadataBytes {
		return ErrUnavailable
	}
	existing, err := existingAncestor(path)
	if err != nil {
		return ErrUnavailable
	}
	available, err := availableBytes(existing)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if available < payload+filesystemMetadataBytes {
		return ErrInsufficient
	}
	return nil
}

func existingAncestor(path string) (string, error) {
	current := path
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				current = filepath.Dir(current)
				continue
			}
			return current, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		current = parent
	}
}
