//go:build darwin

package installlock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const waitMaximum = 5 * time.Second

func acquire(ctx context.Context, path string) (*Handle, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.NewTimer(waitMaximum)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return &Handle{release: func() error {
				unlockErr := unix.Flock(fd, unix.LOCK_UN)
				closeErr := unix.Close(fd)
				if unlockErr != nil {
					return unlockErr
				}
				return closeErr
			}}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = unix.Close(fd)
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = unix.Close(fd)
			return nil, ctx.Err()
		case <-deadline.C:
			_ = unix.Close(fd)
			return nil, ErrBusy
		case <-ticker.C:
		}
	}
}
