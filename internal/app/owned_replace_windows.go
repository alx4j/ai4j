//go:build windows

package app

import "os"

func commitOwnedReplacement(temporaryPath, destinationPath string) error {
	return os.Rename(temporaryPath, destinationPath)
}
