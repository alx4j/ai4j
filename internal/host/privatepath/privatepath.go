// Package privatepath creates AI4J runtime directories with host-native privacy.
package privatepath

import (
	"fmt"
	"path/filepath"
)

func EnsureDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("private directory path must be absolute and clean")
	}
	if err := prepareDirectory(path); err != nil {
		return err
	}
	if err := createDirectory(path); err != nil {
		return err
	}
	return secureDirectory(path)
}

func RemoveAll(path string) error { return removeAll(path) }
