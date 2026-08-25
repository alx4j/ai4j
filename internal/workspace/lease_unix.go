//go:build !windows

package workspace

import (
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

var errLeaseBusy = errors.New("AI4J workspace is live")

type lease struct {
	once sync.Once
	file *os.File
	err  error
}

func createLease(path string) (*lease, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &lease{file: file}, nil
}

func tryLease(path string) (*lease, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errLeaseBusy
		}
		return nil, err
	}
	return &lease{file: file}, nil
}

func (l *lease) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.once.Do(func() {
		unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
		closeErr := l.file.Close()
		l.err = errors.Join(unlockErr, closeErr)
	})
	return l.err
}
