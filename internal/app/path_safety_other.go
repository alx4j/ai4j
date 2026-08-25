//go:build !windows

package app

func hostPathUnsafe(string) bool { return false }
