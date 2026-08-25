//go:build darwin && arm64

package filesystem

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/host/darwin/resource"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"golang.org/x/sys/unix"
)

type fakeCapacityOperations struct {
	fstat              unix.Statfs_t
	fstatErr           error
	mounts             []unix.Statfs_t
	getErr             error
	getCount           int
	useGetCount        bool
	hook               func()
	fstatHook          func(int)
	fstatCalls         []int
	getfsstatCallFlags []int
}

func (o *fakeCapacityOperations) Fstatfs(fd int, stat *unix.Statfs_t) error {
	o.fstatCalls = append(o.fstatCalls, fd)
	*stat = o.fstat
	if o.fstatHook != nil {
		o.fstatHook(len(o.fstatCalls))
	}
	return o.fstatErr
}

func (o *fakeCapacityOperations) Getfsstat(stats []unix.Statfs_t, flags int) (int, error) {
	o.getfsstatCallFlags = append(o.getfsstatCallFlags, flags)
	if o.hook != nil {
		o.hook()
	}
	copy(stats, o.mounts)
	if o.useGetCount {
		return o.getCount, o.getErr
	}
	return len(o.mounts), o.getErr
}

func TestConstructorBindsQualifiedFullFilesystemIdentityFromRootDescriptors(t *testing.T) {
	operations := &fakeCapacityOperations{fstat: qualifiedCapacityStat(packedTestFSID(-7, -2), 4096, 7)}
	value, err := newForUIDWithCapacityOperations(testConfig(t), uint32(os.Geteuid()), operations)
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
	if len(operations.fstatCalls) != 2 || len(operations.getfsstatCallFlags) != 0 {
		t.Fatalf("constructor capacity calls: fstatfs=%d getfsstat=%d", len(operations.fstatCalls), len(operations.getfsstatCallFlags))
	}
	for role, root := range value.roots {
		if root.capacityFilesystem != packedTestFSID(-7, -2) {
			t.Fatalf("root %s filesystem = %#x", role, root.capacityFilesystem)
		}
	}
}

func TestConstructorRejectsUnsupportedFilesystemTypeFlagsAndIdentity(t *testing.T) {
	base := qualifiedCapacityStat(packedTestFSID(1, 2), 4096, 7)
	for name, mutate := range map[string]func(*unix.Statfs_t){
		"non APFS":            func(stat *unix.Statfs_t) { setStatfsText(stat.Fstypename[:], "hfs") },
		"not local":           func(stat *unix.Statfs_t) { stat.Flags &^= unix.MNT_LOCAL },
		"automounted":         func(stat *unix.Statfs_t) { stat.Flags |= unix.MNT_AUTOMOUNTED },
		"read only":           func(stat *unix.Statfs_t) { stat.Flags |= unix.MNT_RDONLY },
		"removable":           func(stat *unix.Statfs_t) { stat.Flags |= unix.MNT_REMOVABLE },
		"unknown permissions": func(stat *unix.Statfs_t) { stat.Flags |= unix.MNT_UNKNOWNPERMISSIONS },
		"union":               func(stat *unix.Statfs_t) { stat.Flags |= unix.MNT_UNION },
		"zero FSID":           func(stat *unix.Statfs_t) { stat.Fsid = unix.Fsid{} },
		"all ones FSID":       func(stat *unix.Statfs_t) { stat.Fsid = unix.Fsid{Val: [2]int32{-1, -1}} },
	} {
		t.Run(name, func(t *testing.T) {
			stat := base
			mutate(&stat)
			config := testConfig(t)
			value, err := newForUIDWithCapacityOperations(config, uint32(os.Geteuid()), &fakeCapacityOperations{fstat: stat})
			if err == nil || value != nil {
				if value != nil {
					_ = value.Close()
				}
				t.Fatalf("unsupported filesystem constructed: %#v, %v", value, err)
			}
			for _, privateName := range []string{
				config.StatePath, config.RecoveryPath, config.TemporarySourcePath, config.StagingPath,
			} {
				if _, statErr := os.Lstat(filepath.Join(config.BaseRoot, privateName)); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("unsupported capacity qualification created %q: %v", privateName, statErr)
				}
			}
		})
	}
}

func TestConstructorAllowsDontBrowseQualifiedAPFS(t *testing.T) {
	stat := qualifiedCapacityStat(packedTestFSID(1, 2), 4096, 7)
	stat.Flags |= unix.MNT_DONTBROWSE
	value, err := newForUIDWithCapacityOperations(testConfig(t), uint32(os.Geteuid()), &fakeCapacityOperations{fstat: stat})
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
}

func TestConstructorContextExpiryDuringFstatfsPreventsActivationAndClosesDescriptors(t *testing.T) {
	for _, test := range []struct {
		name    string
		context func(*fakeCapacityOperations) (context.Context, context.CancelFunc)
		want    error
	}{
		{
			name: "caller cancellation",
			context: func(operations *fakeCapacityOperations) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				operations.fstatHook = func(call int) {
					if call == 2 {
						cancel()
					}
				}
				return ctx, cancel
			},
			want: context.Canceled,
		},
		{
			name: "configured budget deadline",
			context: func(operations *fakeCapacityOperations) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				operations.fstatHook = func(call int) {
					if call == 2 {
						<-ctx.Done()
					}
				}
				return ctx, cancel
			},
			want: context.DeadlineExceeded,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig(t)
			operations := &fakeCapacityOperations{
				fstat: qualifiedCapacityStat(packedTestFSID(1, 2), 4096, 7),
			}
			ctx, cancel := test.context(operations)
			defer cancel()
			value, err := newForUIDWithContextAndCapacityOperations(ctx, config, uint32(os.Geteuid()), operations)
			if value != nil || !errors.Is(err, test.want) {
				t.Fatalf("constructor value/error = %#v / %v", value, err)
			}
			if len(operations.fstatCalls) != 2 {
				t.Fatalf("Fstatfs calls = %v", operations.fstatCalls)
			}
			for _, descriptor := range operations.fstatCalls {
				var stat unix.Stat_t
				if statErr := unix.Fstat(descriptor, &stat); !errors.Is(statErr, unix.EBADF) {
					t.Fatalf("constructor descriptor %d remains open: %v", descriptor, statErr)
				}
			}
			for _, privateName := range []string{
				config.StatePath, config.RecoveryPath, config.TemporarySourcePath, config.StagingPath,
			} {
				if _, statErr := os.Lstat(filepath.Join(config.BaseRoot, privateName)); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("expired constructor created %q: %v", privateName, statErr)
				}
			}
		})
	}
}

func TestSampleFilesystemUsesBoundedNowaitSnapshotAndCallerAvailableBytes(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	root := value.roots[lifecycle.StateRoot]
	stat := qualifiedCapacityStat(root.capacityFilesystem, 4096, 7)
	stat.Bfree = 999
	operations := &fakeCapacityOperations{mounts: []unix.Statfs_t{stat}}
	value.capacityOps = operations

	sample, err := sampleOneFilesystem(value, context.Background(), lifecycle.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !sample.Valid() || !sample.Known || sample.Root != root.identity ||
		sample.Filesystem != root.capacityFilesystem || sample.Available != 7*4096 {
		t.Fatalf("sample = %+v", sample)
	}
	if len(operations.fstatCalls) != 0 || len(operations.getfsstatCallFlags) != 1 ||
		operations.getfsstatCallFlags[0] != unix.MNT_NOWAIT {
		t.Fatalf("request capacity calls: fstatfs=%v getfsstat=%v", operations.fstatCalls, operations.getfsstatCallFlags)
	}
}

func TestSampleFilesystemSelectsBothFilesystemIdentityWords(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	root := value.roots[lifecycle.StateRoot]
	firstWord := uint32(root.capacityFilesystem)
	differentSecondWord := uint32(root.capacityFilesystem>>32) + 1
	alias := uint64(firstWord) | uint64(differentSecondWord)<<32
	value.capacityOps = &fakeCapacityOperations{mounts: []unix.Statfs_t{
		qualifiedCapacityStat(alias, 1, 99),
		qualifiedCapacityStat(root.capacityFilesystem, 1, 7),
	}}

	sample, err := sampleOneFilesystem(value, context.Background(), lifecycle.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !sample.Known || sample.Filesystem != root.capacityFilesystem || sample.Available != 7 {
		t.Fatalf("full-FSID sample = %+v", sample)
	}

	value.capacityOps = &fakeCapacityOperations{mounts: []unix.Statfs_t{qualifiedCapacityStat(alias, 1, 99)}}
	sample, err = sampleOneFilesystem(value, context.Background(), lifecycle.StateRoot)
	if err != nil || sample.Known || sample.Available != 0 {
		t.Fatalf("same-first-word-only sample = %+v, %v", sample, err)
	}
}

func TestSampleFilesystemsUsesOneSnapshotForEveryRequestedRole(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	roles := []lifecycle.RootRole{lifecycle.StateRoot, lifecycle.RecoveryRoot, lifecycle.ManagedOutputRoot}
	mountsByFilesystem := make(map[uint64]unix.Statfs_t)
	for _, role := range roles {
		root := value.roots[role]
		mountsByFilesystem[root.capacityFilesystem] = qualifiedCapacityStat(root.capacityFilesystem, 4096, 7)
	}
	mounts := make([]unix.Statfs_t, 0, len(mountsByFilesystem))
	for _, stat := range mountsByFilesystem {
		mounts = append(mounts, stat)
	}
	operations := &fakeCapacityOperations{mounts: mounts}
	value.capacityOps = operations

	samples, err := value.SampleFilesystems(context.Background(), roles)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != len(roles) {
		t.Fatalf("sample count = %d, want %d", len(samples), len(roles))
	}
	for index, sample := range samples {
		if !sample.Valid() || !sample.Known || sample.Role != roles[index] {
			t.Fatalf("sample[%d] = %+v", index, sample)
		}
	}
	if len(operations.getfsstatCallFlags) != 1 || operations.getfsstatCallFlags[0] != unix.MNT_NOWAIT {
		t.Fatalf("snapshot flags = %v", operations.getfsstatCallFlags)
	}
}

func TestSampleFilesystemTreatsUnavailableTruncatedAndAmbiguousSnapshotsAsUnknown(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	root := value.roots[lifecycle.StateRoot]
	matching := qualifiedCapacityStat(root.capacityFilesystem, 4096, 7)
	other := qualifiedCapacityStat(root.capacityFilesystem+1, 4096, 7)

	for name, operations := range map[string]*fakeCapacityOperations{
		"snapshot error": {mounts: []unix.Statfs_t{matching}, getErr: errors.New("injected snapshot failure")},
		"empty snapshot": {},
		"full bounded buffer": {
			mounts: make([]unix.Statfs_t, MaximumMountTableEntries+1), getCount: MaximumMountTableEntries + 1, useGetCount: true,
		},
		"missing FSID":   {mounts: []unix.Statfs_t{other}},
		"duplicate FSID": {mounts: []unix.Statfs_t{matching, matching}},
	} {
		t.Run(name, func(t *testing.T) {
			value.capacityOps = operations
			sample, err := sampleOneFilesystem(value, context.Background(), lifecycle.StateRoot)
			if err != nil || !sample.Valid() || sample.Known || sample.Available != 0 ||
				sample.Filesystem != root.capacityFilesystem {
				t.Fatalf("sample = %+v, error = %v", sample, err)
			}
		})
	}
}

func TestSampleFilesystemRejectsUndefinedCapacitySentinels(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	root := value.roots[lifecycle.StateRoot]
	base := qualifiedCapacityStat(root.capacityFilesystem, 4096, 7)
	for name, mutate := range map[string]func(*unix.Statfs_t){
		"zero block size":            func(stat *unix.Statfs_t) { stat.Bsize = 0 },
		"undefined block size":       func(stat *unix.Statfs_t) { stat.Bsize = math.MaxUint32 },
		"undefined available blocks": func(stat *unix.Statfs_t) { stat.Bsize, stat.Bavail = 1, math.MaxUint64 },
		"multiplication overflow":    func(stat *unix.Statfs_t) { stat.Bsize, stat.Bavail = 2, math.MaxUint64/2+1 },
	} {
		t.Run(name, func(t *testing.T) {
			stat := base
			mutate(&stat)
			value.capacityOps = &fakeCapacityOperations{mounts: []unix.Statfs_t{stat}}
			sample, err := sampleOneFilesystem(value, context.Background(), lifecycle.StateRoot)
			if err != nil || !sample.Valid() || sample.Known || sample.Available != 0 {
				t.Fatalf("sample = %+v, error = %v", sample, err)
			}
		})
	}
}

func TestSampleFilesystemRejectsPostConstructionPolicyDrift(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	root := value.roots[lifecycle.StateRoot]
	base := qualifiedCapacityStat(root.capacityFilesystem, 4096, 7)
	for name, mutate := range map[string]func(*unix.Statfs_t){
		"type":             func(stat *unix.Statfs_t) { setStatfsText(stat.Fstypename[:], "hfs") },
		"local flag":       func(stat *unix.Statfs_t) { stat.Flags &^= unix.MNT_LOCAL },
		"automounted flag": func(stat *unix.Statfs_t) { stat.Flags |= unix.MNT_AUTOMOUNTED },
		"readonly flag":    func(stat *unix.Statfs_t) { stat.Flags |= unix.MNT_RDONLY },
		"removable flag":   func(stat *unix.Statfs_t) { stat.Flags |= unix.MNT_REMOVABLE },
		"ownership flag":   func(stat *unix.Statfs_t) { stat.Flags |= unix.MNT_IGNORE_OWNERSHIP },
		"union flag":       func(stat *unix.Statfs_t) { stat.Flags |= unix.MNT_UNION },
	} {
		t.Run(name, func(t *testing.T) {
			stat := base
			mutate(&stat)
			value.capacityOps = &fakeCapacityOperations{mounts: []unix.Statfs_t{stat}}
			if sample, err := sampleOneFilesystem(value, context.Background(), lifecycle.StateRoot); err == nil || sample.Valid() {
				t.Fatalf("drift sample = %+v, error = %v", sample, err)
			}
		})
	}
}

func TestSampleFilesystemRevalidatesAuthorityBeforeAndAfterSnapshot(t *testing.T) {
	t.Run("unsafe before sample", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		root := value.roots[lifecycle.StateRoot]
		if err := os.Chmod(root.absolute, 0o777); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(root.absolute, 0o700) })
		operations := &fakeCapacityOperations{mounts: []unix.Statfs_t{qualifiedCapacityStat(root.capacityFilesystem, 4096, 7)}}
		value.capacityOps = operations
		if sample, err := sampleOneFilesystem(value, context.Background(), lifecycle.StateRoot); err == nil || sample.Valid() {
			t.Fatalf("sample = %+v, error = %v", sample, err)
		}
		if len(operations.getfsstatCallFlags) != 0 {
			t.Fatalf("unsafe authority reached snapshot: %v", operations.getfsstatCallFlags)
		}
	})

	t.Run("unsafe after sample", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		root := value.roots[lifecycle.StateRoot]
		operations := &fakeCapacityOperations{mounts: []unix.Statfs_t{qualifiedCapacityStat(root.capacityFilesystem, 4096, 7)}}
		operations.hook = func() {
			if err := os.Chmod(root.absolute, 0o777); err != nil {
				t.Fatal(err)
			}
		}
		value.capacityOps = operations
		t.Cleanup(func() { _ = os.Chmod(root.absolute, 0o700) })
		if sample, err := sampleOneFilesystem(value, context.Background(), lifecycle.StateRoot); err == nil || sample.Valid() {
			t.Fatalf("sample = %+v, error = %v", sample, err)
		}
	})
}

func TestSampleFilesystemCancellationPrecedesAndRejectsLateSnapshot(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	root := value.roots[lifecycle.StateRoot]
	operations := &fakeCapacityOperations{mounts: []unix.Statfs_t{qualifiedCapacityStat(root.capacityFilesystem, 4096, 7)}}
	value.capacityOps = operations

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if sample, err := sampleOneFilesystem(value, cancelled, lifecycle.StateRoot); !errors.Is(err, context.Canceled) || sample.Valid() {
		t.Fatalf("pre-cancel sample = %+v, %v", sample, err)
	}
	if len(operations.getfsstatCallFlags) != 0 {
		t.Fatalf("pre-cancel reached snapshot: %v", operations.getfsstatCallFlags)
	}

	ctx, cancelDeadline := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelDeadline()
	operations.hook = func() { <-ctx.Done() }
	if sample, err := sampleOneFilesystem(value, ctx, lifecycle.StateRoot); !errors.Is(err, context.DeadlineExceeded) || sample.Valid() {
		t.Fatalf("late snapshot = %+v, %v", sample, err)
	}
	if _, err := root.directory.Stat(); err != nil {
		t.Fatalf("late snapshot lost root authority descriptor: %v", err)
	}
}

func TestSampleFilesystemChecksDeadlineAtExactSnapshotBoundaries(t *testing.T) {
	t.Run("expiry after final prevalidation skips snapshot", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		root := value.roots[lifecycle.StateRoot]
		operations := &fakeCapacityOperations{mounts: []unix.Statfs_t{
			qualifiedCapacityStat(root.capacityFilesystem, 4096, 7),
		}}
		value.capacityOps = operations
		ctx, cancel := context.WithCancel(context.Background())
		samples, err := value.sampleFilesystemsWithHooks(ctx, []lifecycle.RootRole{lifecycle.StateRoot}, capacityInspectionHooks{
			afterPrevalidation: cancel,
		})
		if !errors.Is(err, context.Canceled) || len(samples) != 0 {
			t.Fatalf("boundary result = %+v, %v", samples, err)
		}
		if len(operations.getfsstatCallFlags) != 0 {
			t.Fatalf("expired prevalidation reached snapshot: %v", operations.getfsstatCallFlags)
		}
	})

	t.Run("expiry inside snapshot skips postvalidation", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		root := value.roots[lifecycle.StateRoot]
		ctx, cancel := context.WithCancel(context.Background())
		operations := &fakeCapacityOperations{mounts: []unix.Statfs_t{
			qualifiedCapacityStat(root.capacityFilesystem, 4096, 7),
		}, hook: cancel}
		value.capacityOps = operations
		postvalidations := 0
		samples, err := value.sampleFilesystemsWithHooks(ctx, []lifecycle.RootRole{lifecycle.StateRoot}, capacityInspectionHooks{
			beforePostvalidation: func() { postvalidations++ },
		})
		if !errors.Is(err, context.Canceled) || len(samples) != 0 {
			t.Fatalf("boundary result = %+v, %v", samples, err)
		}
		if len(operations.getfsstatCallFlags) != 1 || postvalidations != 0 {
			t.Fatalf("snapshot calls = %v, postvalidations = %d", operations.getfsstatCallFlags, postvalidations)
		}
	})
}

func TestSampleFilesystemCachedAvailabilityIsAdvisory(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	root := value.roots[lifecycle.StateRoot]
	for _, available := range []uint64{1_000, 1} {
		value.capacityOps = &fakeCapacityOperations{mounts: []unix.Statfs_t{
			qualifiedCapacityStat(root.capacityFilesystem, 1, available),
		}}
		sample, err := sampleOneFilesystem(value, context.Background(), lifecycle.StateRoot)
		if err != nil || !sample.Known || sample.Available != available {
			t.Fatalf("cached sample = %+v, %v", sample, err)
		}
	}
}

func TestSampleFilesystemRealDarwinRoot(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	for _, role := range []lifecycle.RootRole{
		lifecycle.StateRoot,
		lifecycle.RecoveryRoot,
		lifecycle.TemporarySourceRoot,
		lifecycle.StagingRoot,
		lifecycle.ManagedOutputRoot,
	} {
		sample, err := sampleOneFilesystem(value, context.Background(), role)
		if err != nil {
			t.Fatalf("SampleFilesystem(%s) error = %v", role, err)
		}
		if !sample.Valid() || !sample.Known {
			t.Fatalf("SampleFilesystem(%s) = %+v", role, sample)
		}
	}
}

func qualifiedCapacityStat(identity uint64, blockSize uint32, available uint64) unix.Statfs_t {
	stat := unix.Statfs_t{
		Bsize: blockSize, Bavail: available, Flags: unix.MNT_LOCAL,
		Fsid: unix.Fsid{Val: [2]int32{int32(uint32(identity)), int32(uint32(identity >> 32))}},
	}
	setStatfsText(stat.Fstypename[:], "apfs")
	return stat
}

func setStatfsText(destination []byte, value string) {
	clear(destination)
	copy(destination, value)
}

func packedTestFSID(first, second int32) uint64 {
	return uint64(uint32(first)) | uint64(uint32(second))<<32
}

func sampleOneFilesystem(value *Filesystem, ctx context.Context, role lifecycle.RootRole) (resource.FilesystemSample, error) {
	samples, err := value.SampleFilesystems(ctx, []lifecycle.RootRole{role})
	if err != nil {
		return resource.FilesystemSample{}, err
	}
	if len(samples) != 1 {
		return resource.FilesystemSample{}, errors.New("unexpected sample count")
	}
	return samples[0], nil
}
