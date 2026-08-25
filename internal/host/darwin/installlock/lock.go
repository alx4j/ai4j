package installlock

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
)

var (
	ErrBusy        = errors.New("another AI4J mutation is running")
	ErrUnsupported = errors.New("installation locking is unsupported on this host")
)

type Locker struct{ path string }

func New(home string) (Locker, error) {
	if !filepath.IsAbs(home) {
		return Locker{}, fmt.Errorf("installation lock home must be absolute")
	}
	return Locker{path: filepath.Join(home, "Library", "Application Support", "ai4j", "state", "mutation.lock")}, nil
}

func NewAt(stateRoot string) (Locker, error) {
	if !filepath.IsAbs(stateRoot) || filepath.Clean(stateRoot) != stateRoot {
		return Locker{}, fmt.Errorf("installation lock state root must be absolute and clean")
	}
	return Locker{path: filepath.Join(stateRoot, "mutation.lock")}, nil
}

func (l Locker) Path() string { return l.path }

type Handle struct {
	once    sync.Once
	release func() error
	err     error
}

func (h *Handle) Release() error {
	if h == nil || h.release == nil {
		return nil
	}
	h.once.Do(func() { h.err = h.release() })
	return h.err
}

func (l Locker) Acquire(ctx context.Context) (*Handle, error) {
	if ctx == nil {
		return nil, ErrBusy
	}
	return acquire(ctx, l.path)
}
