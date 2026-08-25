//go:build windows

package app

import (
	"golang.org/x/sys/windows"
)

func hostPathUnsafe(path string) bool {
	value, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(value)
	return err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
