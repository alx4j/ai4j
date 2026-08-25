//go:build windows

package installlock

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/alx4j/ai4j/internal/host/privatepath"
	"golang.org/x/sys/windows"
)

const waitMaximum = 5 * time.Second

func acquire(ctx context.Context, path string) (*Handle, error) {
	if err := privatepath.EnsureDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	deadline := time.NewTimer(waitMaximum)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, err
		}
		handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err == nil {
			return &Handle{release: func() error { return windows.CloseHandle(handle) }}, nil
		}
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, ErrBusy
		case <-ticker.C:
		}
	}
}
