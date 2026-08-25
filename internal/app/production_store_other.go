//go:build !windows

package app

import "github.com/alx4j/ai4j/internal/installstate"

func productionStateStore(home string) (installstate.Store, error) {
	return installstate.NewStore(home)
}
