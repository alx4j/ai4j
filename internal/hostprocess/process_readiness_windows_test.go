//go:build windows

package hostprocess

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func transientProcessIDReadError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func TestWaitForProcessIDRetriesWindowsSharingErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "sharing violation", err: windows.ERROR_SHARING_VIOLATION},
		{name: "lock violation", err: windows.ERROR_LOCK_VIOLATION},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reads := 0
			pid, outcome, err := waitForProcessIDWithReader("child.pid", make(chan processOutcome), func(path string) ([]byte, error) {
				reads++
				if reads == 1 {
					return nil, &os.PathError{Op: "open", Path: path, Err: test.err}
				}
				return []byte("42"), nil
			})

			if err != nil || pid != 42 || outcome != nil || reads != 2 {
				t.Fatalf("waitForProcessIDWithReader() = %d, %#v, %v after %d reads", pid, outcome, err, reads)
			}
		})
	}
}
