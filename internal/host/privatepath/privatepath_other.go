//go:build !windows

package privatepath

import "os"

func prepareDirectory(string) error { return nil }

func createDirectory(path string) error { return os.MkdirAll(path, 0o700) }

func secureDirectory(path string) error { return os.Chmod(path, 0o700) }

func removeAll(path string) error { return os.RemoveAll(path) }
