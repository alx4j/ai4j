//go:build windows

package app

import (
	"os"
	"path/filepath"

	"github.com/alx4j/ai4j/internal/installstate"
)

func productionStateStore(home string) (installstate.Store, error) {
	localAppData, err := os.UserCacheDir()
	if err != nil {
		return installstate.Store{}, err
	}
	return installstate.NewStoreAt(home, filepath.Join(localAppData, "AI4J"))
}
