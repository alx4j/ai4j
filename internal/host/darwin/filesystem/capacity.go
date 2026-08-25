//go:build darwin && arm64

package filesystem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/fault"
	"github.com/alx4j/ai4j/internal/host/darwin/resource"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"golang.org/x/sys/unix"
)

// MaximumMountTableEntries is the published fixed bound for one cached Darwin
// mount snapshot. Filling the entire buffer is treated as truncation.
const MaximumMountTableEntries = 256

type capacityOperations interface {
	Fstatfs(int, *unix.Statfs_t) error
	Getfsstat([]unix.Statfs_t, int) (int, error)
}

type realCapacityOperations struct{}

func (realCapacityOperations) Fstatfs(fd int, stat *unix.Statfs_t) error {
	return unix.Fstatfs(fd, stat)
}

func (realCapacityOperations) Getfsstat(stats []unix.Statfs_t, flags int) (int, error) {
	return unix.Getfsstat(stats, flags)
}

var _ resource.FilesystemSampler = (*Filesystem)(nil)

// qualifyCapacityRoot performs one constructor-time, descriptor-bound fstatfs
// qualification call. This is an explicit supported-host residual;
// request-path capacity inspection uses only bounded Getfsstat(MNT_NOWAIT)
// cached mount facts.
func qualifyCapacityRoot(
	ctx context.Context,
	root *rootedDirectory,
	uid uint32,
	operations capacityOperations,
) (uint64, error) {
	if ctx == nil {
		return 0, invalid("capacity_context", fault.ReasonEmpty)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := revalidateRoot(root, uid); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var stat unix.Statfs_t
	observationErr := operations.Fstatfs(int(root.directory.Fd()), &stat)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if observationErr != nil {
		return 0, fmt.Errorf("qualify configured-root filesystem: %w", observationErr)
	}
	identity, ok := qualifiedCapacityMount(stat)
	if !ok {
		return 0, conflict("configured_root", "unsupported_filesystem", nil)
	}
	if err := revalidateRoot(root, uid); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return identity, nil
}

// SampleFilesystems observes cached caller-available bytes from one bounded
// Darwin mount-table snapshot. The full constructor-bound FSID binds every
// sample to its pre/post-revalidated configured-root authority; no mount path
// or device-number fallback participates. MNT_NOWAIT prevents the request path
// from asking a filesystem to refresh statistics.
func (f *Filesystem) SampleFilesystems(ctx context.Context, roles []lifecycle.RootRole) ([]resource.FilesystemSample, error) {
	return f.sampleFilesystemsWithHooks(ctx, roles, capacityInspectionHooks{})
}

type capacityInspectionHooks struct {
	afterPrevalidation   func()
	beforePostvalidation func()
}

func (f *Filesystem) sampleFilesystemsWithHooks(ctx context.Context, roles []lifecycle.RootRole, hooks capacityInspectionHooks) ([]resource.FilesystemSample, error) {
	if ctx == nil {
		return nil, invalid("capacity_context", fault.ReasonEmpty)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(roles) == 0 || len(roles) > 4 {
		return nil, invalid("capacity_roots", fault.ReasonOutOfRange)
	}
	roots := make([]*rootedDirectory, len(roles))
	seen := make(map[lifecycle.RootRole]struct{}, len(roles))
	for index, role := range roles {
		if _, duplicate := seen[role]; duplicate {
			return nil, invalid("capacity_roots", fault.ReasonInvalidFormat)
		}
		seen[role] = struct{}{}
		root, err := f.root(role)
		if err != nil {
			return nil, err
		}
		if root.capacityFilesystem == 0 || root.capacityFilesystem == math.MaxUint64 {
			return nil, conflict("configured_root", "missing_filesystem_identity", nil)
		}
		if err := revalidateRoot(root, f.currentUID); err != nil {
			return nil, err
		}
		roots[index] = root
	}
	if hooks.afterPrevalidation != nil {
		hooks.afterPrevalidation()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stats, tableErr := f.cachedMountTable(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if hooks.beforePostvalidation != nil {
		hooks.beforePostvalidation()
	}
	for _, root := range roots {
		if err := revalidateRoot(root, f.currentUID); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	samples := make([]resource.FilesystemSample, len(roots))
	for index, root := range roots {
		samples[index] = resource.FilesystemSample{
			Role: roles[index], Root: root.identity, Filesystem: root.capacityFilesystem,
		}
	}
	if tableErr != nil {
		return samples, nil
	}

	for sampleIndex, root := range roots {
		var matched *unix.Statfs_t
		matchCount := 0
		for statIndex := range stats {
			identity := packedFilesystemID(stats[statIndex].Fsid)
			if identity != root.capacityFilesystem {
				continue
			}
			matchCount++
			if matchCount == 1 {
				matched = &stats[statIndex]
			}
		}
		if matchCount != 1 {
			continue
		}
		identity, ok := qualifiedCapacityMount(*matched)
		if !ok || identity != root.capacityFilesystem {
			return nil, conflict("configured_root", "filesystem_policy_changed", nil)
		}
		if !knownCapacityFields(*matched) {
			continue
		}
		samples[sampleIndex].Available = matched.Bavail * uint64(matched.Bsize)
		samples[sampleIndex].Known = true
	}
	return samples, nil
}

func (f *Filesystem) cachedMountTable(ctx context.Context) ([]unix.Statfs_t, error) {
	stats := make([]unix.Statfs_t, MaximumMountTableEntries+1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	observed, err := f.capacityOps.Getfsstat(stats, unix.MNT_NOWAIT)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil || observed <= 0 || observed >= len(stats) {
		return nil, errors.Join(err, errors.New("bounded mount table unavailable or truncated"))
	}
	return stats[:observed], nil
}

func qualifiedCapacityMount(stat unix.Statfs_t) (uint64, bool) {
	filesystemType, ok := statfsText(stat.Fstypename[:])
	if !ok || filesystemType != "apfs" {
		return 0, false
	}
	rejected := uint32(unix.MNT_AUTOMOUNTED | unix.MNT_RDONLY | unix.MNT_REMOVABLE | unix.MNT_IGNORE_OWNERSHIP | unix.MNT_UNION)
	if stat.Flags&unix.MNT_LOCAL == 0 || stat.Flags&rejected != 0 {
		return 0, false
	}
	identity := packedFilesystemID(stat.Fsid)
	if identity == 0 || identity == math.MaxUint64 {
		return 0, false
	}
	return identity, true
}

func knownCapacityFields(stat unix.Statfs_t) bool {
	if stat.Bsize == 0 || stat.Bsize == math.MaxUint32 || stat.Bavail == math.MaxUint64 {
		return false
	}
	return stat.Bavail <= math.MaxUint64/uint64(stat.Bsize)
}

func packedFilesystemID(fsid unix.Fsid) uint64 {
	return uint64(uint32(fsid.Val[0])) | uint64(uint32(fsid.Val[1]))<<32
}

func statfsText(value []byte) (string, bool) {
	end := bytes.IndexByte(value, 0)
	if end <= 0 {
		return "", false
	}
	text := string(value[:end])
	return text, utf8.ValidString(text) && !containsControl(text)
}
