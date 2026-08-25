package resource

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

type samplerFunc func(context.Context, []lifecycle.RootRole) ([]FilesystemSample, error)

func (f samplerFunc) SampleFilesystems(ctx context.Context, roles []lifecycle.RootRole) ([]FilesystemSample, error) {
	return f(ctx, roles)
}

func TestPreflightGroupsOpenedFilesystemIdentitiesAndUsesConservativeCapacity(t *testing.T) {
	t.Parallel()

	samples := map[lifecycle.RootRole]FilesystemSample{
		lifecycle.ManagedOutputRoot:   {Root: lifecycle.ObjectIdentity{Filesystem: 20, Object: 20}, Filesystem: 2, Available: 1_000, Known: true},
		lifecycle.StateRoot:           {Root: lifecycle.ObjectIdentity{Filesystem: 10, Object: 10}, Filesystem: 1, Available: 900, Known: true},
		lifecycle.RecoveryRoot:        {Root: lifecycle.ObjectIdentity{Filesystem: 20, Object: 21}, Filesystem: 2, Available: 800, Known: true},
		lifecycle.TemporarySourceRoot: {Root: lifecycle.ObjectIdentity{Filesystem: 10, Object: 11}, Filesystem: 1, Available: 700, Known: true},
	}
	calls := 0
	preflighter := testPreflighter(t, samplerFunc(func(_ context.Context, roles []lifecycle.RootRole) ([]FilesystemSample, error) {
		calls++
		return samplesForRoles(roles, samples), nil
	}), testPolicy(t))
	result, err := preflighter.PreflightDisk(context.Background(), lifecycle.DiskPreflightRequest{
		TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.ManagedOutputRoot, Bytes: 10},
		StagedOutput:    lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 20},
		Journal:         lifecycle.DiskAllocation{Root: lifecycle.RecoveryRoot, Bytes: 30},
		Recovery:        lifecycle.DiskAllocation{Root: lifecycle.TemporarySourceRoot, Bytes: 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []lifecycle.FilesystemCapacity{
		{Identity: 1, Required: 66, Available: 700, Known: true},
		{Identity: 2, Required: 44, Available: 800, Known: true},
	}
	if !result.Sufficient || !result.Coherent() || !reflect.DeepEqual(result.Filesystems, want) {
		t.Fatalf("result = %+v, want %+v", result, want)
	}
	if calls != 1 {
		t.Fatalf("filesystem sampler calls = %d, want 1", calls)
	}
}

func TestPreflightKeepsBothFilesystemIdentityWords(t *testing.T) {
	t.Parallel()

	first := packedTestFilesystemID(7, 1)
	second := packedTestFilesystemID(7, 2)
	negative := packedTestFilesystemID(-7, -2)
	samples := map[lifecycle.RootRole]FilesystemSample{
		lifecycle.StateRoot: {
			Root: lifecycle.ObjectIdentity{Filesystem: 50, Object: 1}, Filesystem: first, Available: 100, Known: true,
		},
		lifecycle.RecoveryRoot: {
			Root: lifecycle.ObjectIdentity{Filesystem: 50, Object: 2}, Filesystem: second, Available: 100, Known: true,
		},
		lifecycle.StagingRoot: {
			Root: lifecycle.ObjectIdentity{Filesystem: 60, Object: 3}, Filesystem: negative, Available: 100, Known: true,
		},
	}
	preflighter := testPreflighter(t, samplerFunc(func(_ context.Context, roles []lifecycle.RootRole) ([]FilesystemSample, error) {
		return samplesForRoles(roles, samples), nil
	}), testPolicy(t))
	result, err := preflighter.PreflightDisk(context.Background(), lifecycle.DiskPreflightRequest{
		TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 1},
		StagedOutput:    lifecycle.DiskAllocation{Root: lifecycle.RecoveryRoot, Bytes: 1},
		Journal:         lifecycle.DiskAllocation{Root: lifecycle.StagingRoot, Bytes: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Filesystems) != 3 {
		t.Fatalf("same first FSID word collapsed distinct filesystems: %+v", result)
	}
	identities := map[uint64]bool{}
	for _, capacity := range result.Filesystems {
		identities[capacity.Identity] = true
	}
	if !identities[first] || !identities[second] || !identities[negative] {
		t.Fatalf("packed filesystem identities = %+v", result.Filesystems)
	}
}

func TestPreflightMakesUnknownAndInsufficientCapacityUnambiguous(t *testing.T) {
	t.Parallel()

	for name, sample := range map[string]FilesystemSample{
		"unknown":      {Root: lifecycle.ObjectIdentity{Filesystem: 10, Object: 1}, Filesystem: 1},
		"insufficient": {Root: lifecycle.ObjectIdentity{Filesystem: 10, Object: 1}, Filesystem: 1, Available: 10, Known: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			preflighter := testPreflighter(t, samplerFunc(func(_ context.Context, roles []lifecycle.RootRole) ([]FilesystemSample, error) {
				sample.Role = roles[0]
				return []FilesystemSample{sample}, nil
			}), testPolicy(t))
			result, err := preflighter.PreflightDisk(context.Background(), lifecycle.DiskPreflightRequest{
				TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 10},
			})
			if err != nil || result.Sufficient || !result.Coherent() || len(result.Filesystems) != 1 {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
			if name == "unknown" && (result.Filesystems[0].Known || result.Filesystems[0].Available != 0) {
				t.Fatalf("unknown capacity became numeric: %+v", result.Filesystems[0])
			}
		})
	}

	preflighter := testPreflighter(t, samplerFunc(func(_ context.Context, roles []lifecycle.RootRole) ([]FilesystemSample, error) {
		return []FilesystemSample{
			{Role: roles[0], Root: lifecycle.ObjectIdentity{Filesystem: 10, Object: 1}, Filesystem: 1, Available: 100, Known: true},
			{Role: roles[1], Root: lifecycle.ObjectIdentity{Filesystem: 10, Object: 2}, Filesystem: 1},
		}, nil
	}), testPolicy(t))
	result, err := preflighter.PreflightDisk(context.Background(), lifecycle.DiskPreflightRequest{
		TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 1},
		StagedOutput:    lifecycle.DiskAllocation{Root: lifecycle.RecoveryRoot, Bytes: 1},
	})
	if err != nil || result.Sufficient || result.Filesystems[0].Known || result.Filesystems[0].Available != 0 {
		t.Fatalf("mixed known/unknown aliases = %+v, %v", result, err)
	}
}

func TestPreflightRejectsBeforeSamplingAndReturnsNoUsableErrorResult(t *testing.T) {
	t.Parallel()

	calls := 0
	injected := errors.New("sample authority changed")
	preflighter := testPreflighter(t, samplerFunc(func(context.Context, []lifecycle.RootRole) ([]FilesystemSample, error) {
		calls++
		return nil, injected
	}), testPolicy(t))
	for name, request := range map[string]lifecycle.DiskPreflightRequest{
		"invalid": {},
		"over policy": {
			TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 101},
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := preflighter.PreflightDisk(context.Background(), request)
			if err == nil || result.Coherent() || len(result.Filesystems) != 0 {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("rejected requests sampled filesystem %d times", calls)
	}
	result, err := preflighter.PreflightDisk(context.Background(), lifecycle.DiskPreflightRequest{
		TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 1},
	})
	if !errors.Is(err, injected) || result.Coherent() || len(result.Filesystems) != 0 || calls != 1 {
		t.Fatalf("sample failure = %+v, %v, calls=%d", result, err, calls)
	}

	invalidSample := testPreflighter(t, samplerFunc(func(_ context.Context, roles []lifecycle.RootRole) ([]FilesystemSample, error) {
		return []FilesystemSample{{
			Role: roles[0], Root: lifecycle.ObjectIdentity{Filesystem: 10, Object: 1}, Filesystem: 1, Available: 1,
		}}, nil
	}), testPolicy(t))
	if result, err := invalidSample.PreflightDisk(context.Background(), lifecycle.DiskPreflightRequest{
		TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 1},
	}); !errors.Is(err, errInvalidCapacitySample) || result.Coherent() {
		t.Fatalf("invalid sample result = %+v, %v", result, err)
	}

	undefinedFilesystem := testPreflighter(t, samplerFunc(func(_ context.Context, roles []lifecycle.RootRole) ([]FilesystemSample, error) {
		return []FilesystemSample{{
			Role: roles[0], Root: lifecycle.ObjectIdentity{Filesystem: 10, Object: 1}, Filesystem: math.MaxUint64,
		}}, nil
	}), testPolicy(t))
	if result, err := undefinedFilesystem.PreflightDisk(context.Background(), lifecycle.DiskPreflightRequest{
		TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 1},
	}); !errors.Is(err, errInvalidCapacitySample) || result.Coherent() {
		t.Fatalf("undefined filesystem result = %+v, %v", result, err)
	}
}

func TestPreflightCallerAndConfiguredDeadlinesReleaseSampler(t *testing.T) {
	t.Parallel()

	for name, setup := range map[string]func(*policyConfig) (context.Context, context.CancelFunc){
		"caller deadline": func(config *policyConfig) (context.Context, context.CancelFunc) {
			config.FilesystemTimeout = time.Minute
			return context.WithTimeout(context.Background(), 20*time.Millisecond)
		},
		"configured deadline": func(config *policyConfig) (context.Context, context.CancelFunc) {
			config.FilesystemTimeout = 20 * time.Millisecond
			return context.WithCancel(context.Background())
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := testPolicyConfig()
			ctx, cancel := setup(&config)
			defer cancel()
			policy, err := newPolicy(config)
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			acquired := 0
			preflighter := testPreflighter(t, samplerFunc(func(ctx context.Context, _ []lifecycle.RootRole) ([]FilesystemSample, error) {
				mu.Lock()
				acquired++
				mu.Unlock()
				defer func() {
					mu.Lock()
					acquired--
					mu.Unlock()
				}()
				<-ctx.Done()
				return nil, ctx.Err()
			}), policy)
			result, err := preflighter.PreflightDisk(ctx, lifecycle.DiskPreflightRequest{
				TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 1},
			})
			if !errors.Is(err, context.DeadlineExceeded) || result.Coherent() {
				t.Fatalf("deadline result = %+v, %v", result, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if acquired != 0 {
				t.Fatalf("sampler retained %d acquired resources", acquired)
			}
		})
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	preflighter := testPreflighter(t, samplerFunc(func(context.Context, []lifecycle.RootRole) ([]FilesystemSample, error) {
		calls++
		return nil, nil
	}), testPolicy(t))
	if result, err := preflighter.PreflightDisk(cancelled, lifecycle.DiskPreflightRequest{
		TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 1},
	}); !errors.Is(err, context.Canceled) || result.Coherent() || calls != 0 {
		t.Fatalf("cancelled result = %+v, %v, calls=%d", result, err, calls)
	}
}

func TestPreflighterRejectsNilAuthorities(t *testing.T) {
	t.Parallel()

	if value, err := NewPreflighter(nil, testPolicy(t)); !errors.Is(err, errInvalidPolicy) || value != nil {
		t.Fatalf("nil sampler = %#v, %v", value, err)
	}
	var typedNil *typedNilSampler
	if value, err := NewPreflighter(typedNil, testPolicy(t)); !errors.Is(err, errInvalidPolicy) || value != nil {
		t.Fatalf("typed nil sampler = %#v, %v", value, err)
	}
}

type typedNilSampler struct{}

func (*typedNilSampler) SampleFilesystems(context.Context, []lifecycle.RootRole) ([]FilesystemSample, error) {
	return nil, nil
}

func testPreflighter(t *testing.T, sampler FilesystemSampler, policy Policy) *Preflighter {
	t.Helper()
	value, err := NewPreflighter(sampler, policy)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func packedTestFilesystemID(first, second int32) uint64 {
	return uint64(uint32(first)) | uint64(uint32(second))<<32
}

func samplesForRoles(roles []lifecycle.RootRole, samples map[lifecycle.RootRole]FilesystemSample) []FilesystemSample {
	result := make([]FilesystemSample, len(roles))
	for index, role := range roles {
		result[index] = samples[role]
		result[index].Role = role
	}
	return result
}
