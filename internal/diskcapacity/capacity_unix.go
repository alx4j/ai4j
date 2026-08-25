//go:build !windows

package diskcapacity

import (
	"errors"
	"math"

	"golang.org/x/sys/unix"
)

func availableBytes(path string) (uint64, error) {
	var status unix.Statfs_t
	if err := unix.Statfs(path, &status); err != nil {
		return 0, err
	}
	blockSize := uint64(status.Bsize)
	availableBlocks := uint64(status.Bavail)
	if blockSize == 0 || availableBlocks > math.MaxUint64/blockSize {
		return 0, errors.New("invalid filesystem capacity")
	}
	return availableBlocks * blockSize, nil
}
