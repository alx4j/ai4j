//go:build windows

package workspace

import (
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

var errLeaseBusy = errors.New("AI4J workspace is live")

type lease struct {
	once   sync.Once
	handle windows.Handle
	err    error
}

func createLease(path string) (*lease, error) { return openLease(path, windows.CREATE_NEW) }
func tryLease(path string) (*lease, error)    { return openLease(path, windows.OPEN_EXISTING) }

func openLease(path string, disposition uint32) (*lease, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, disposition, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errLeaseBusy
		}
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return &lease{handle: handle}, nil
}

func (l *lease) release() error {
	if l == nil || l.handle == 0 {
		return nil
	}
	l.once.Do(func() { l.err = windows.CloseHandle(l.handle) })
	return l.err
}
