//go:build windows

package privatepath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func secureDirectory(path string) error {
	if err := rejectReparseComponents(path); err != nil {
		return err
	}
	descriptor, err := privateDescriptor()
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("create private Windows ACL")
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("apply private Windows ACL: %w", err)
	}
	actual, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("verify private Windows ACL: %w", err)
	}
	control, _, err := actual.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("private Windows ACL is not protected")
	}
	return nil
}

func privateDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("resolve current Windows user: %w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, fmt.Errorf("create private Windows ACL: %w", err)
	}
	return descriptor, nil
}

func prepareDirectory(path string) error {
	return visitComponents(path, true)
}

func createDirectory(path string) error {
	descriptor, err := privateDescriptor()
	if err != nil {
		return err
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	volume := filepath.VolumeName(path)
	root := volume + string(filepath.Separator)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		value, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}
		_, err = windows.GetFileAttributes(value)
		if err == nil {
			continue
		}
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return err
		}
		if err := windows.CreateDirectory(value, &attributes); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return err
		}
	}
	return nil
}

func rejectReparseComponents(path string) error {
	return visitComponents(path, false)
}

func visitComponents(path string, allowMissing bool) error {
	volume := filepath.VolumeName(path)
	root := volume + string(filepath.Separator)
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		return errors.New("private Windows path is invalid")
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		value, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(value)
		if allowMissing && (errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND)) {
			return nil
		}
		if err != nil {
			return err
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("private Windows path contains a reparse point")
		}
	}
	return nil
}

func removeAll(path string) error {
	if err := filepath.WalkDir(path, func(candidate string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		value, err := windows.UTF16PtrFromString(candidate)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(value)
		if err != nil {
			return err
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("private Windows cleanup contains a reparse point")
		}
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(path)
}
