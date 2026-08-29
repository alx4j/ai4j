//go:build !windows

package hostprocess

func transientProcessIDReadError(error) bool {
	return false
}
