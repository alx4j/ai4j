//go:build darwin && arm64

package darwin

import (
	"os"

	"golang.org/x/sys/unix"
)

type systemHostOperations struct{}

func (systemHostOperations) ProductVersion() (string, error) {
	return unix.Sysctl("kern.osproductversion")
}

func (systemHostOperations) EnvironmentPresent(name string) bool {
	_, present := os.LookupEnv(name)
	return present
}
