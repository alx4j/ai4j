//go:build !darwin && !windows

package app

import "os"

func commitOwnedReplacement(temporaryPath, destinationPath string) error {
	if err := os.Remove(destinationPath); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destinationPath)
}
