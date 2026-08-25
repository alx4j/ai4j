package lifecycle_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestDiskAllocationClosedActivityAndValidity(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		allocation lifecycle.DiskAllocation
		active     bool
		valid      bool
	}{
		"inactive":   {valid: true},
		"active":     {allocation: lifecycle.DiskAllocation{Root: lifecycle.ManagedOutputRoot, Bytes: 1}, active: true, valid: true},
		"role only":  {allocation: lifecycle.DiskAllocation{Root: lifecycle.StateRoot}},
		"bytes only": {allocation: lifecycle.DiskAllocation{Bytes: 1}},
		"unknown role": {
			allocation: lifecycle.DiskAllocation{Root: lifecycle.RootRole("unknown"), Bytes: 1},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := test.allocation.Active(); got != test.active {
				t.Fatalf("Active() = %t, want %t", got, test.active)
			}
			if got := test.allocation.Valid(); got != test.valid {
				t.Fatalf("Valid() = %t, want %t", got, test.valid)
			}
		})
	}
}

func TestDiskPreflightRequestAcceptsExplicitCrossRolePlacement(t *testing.T) {
	t.Parallel()

	request := lifecycle.DiskPreflightRequest{
		TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.ManagedOutputRoot, Bytes: 11},
		StagedOutput:    lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 13},
		Journal:         lifecycle.DiskAllocation{Root: lifecycle.RecoveryRoot, Bytes: 17},
		Recovery:        lifecycle.DiskAllocation{Root: lifecycle.TemporarySourceRoot, Bytes: 19},
	}
	if !request.Valid() {
		t.Fatal("valid cross-role placement was rejected")
	}
	if total, ok := request.TotalBytes(); !ok || total != 60 {
		t.Fatalf("TotalBytes() = %d, %t, want 60, true", total, ok)
	}
}

func TestDiskPreflightRequestRejectsEmptyPartialAndOverflow(t *testing.T) {
	t.Parallel()

	for name, request := range map[string]lifecycle.DiskPreflightRequest{
		"empty": {},
		"temporary role only": {
			TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.TemporarySourceRoot},
		},
		"staging bytes only": {
			StagedOutput: lifecycle.DiskAllocation{Bytes: 1},
		},
		"journal unknown root": {
			Journal: lifecycle.DiskAllocation{Root: lifecycle.RootRole("unknown"), Bytes: 1},
		},
		"overflow": {
			TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.TemporarySourceRoot, Bytes: math.MaxUint64},
			Recovery:        lifecycle.DiskAllocation{Root: lifecycle.RecoveryRoot, Bytes: 1},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if request.Valid() {
				t.Fatal("invalid disk request was accepted")
			}
			if total, ok := request.TotalBytes(); ok || total != 0 {
				t.Fatalf("TotalBytes() = %d, %t, want 0, false", total, ok)
			}
		})
	}
}

func TestFilesystemCapacityFactsAreClosed(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		capacity lifecycle.FilesystemCapacity
		valid    bool
	}{
		"known zero available": {
			capacity: lifecycle.FilesystemCapacity{Identity: 1, Required: 1, Known: true}, valid: true,
		},
		"known sufficient": {
			capacity: lifecycle.FilesystemCapacity{Identity: 1, Required: 1, Available: 1, Known: true}, valid: true,
		},
		"unknown": {
			capacity: lifecycle.FilesystemCapacity{Identity: 1, Required: 1}, valid: true,
		},
		"unknown with available": {
			capacity: lifecycle.FilesystemCapacity{Identity: 1, Required: 1, Available: 1},
		},
		"missing identity": {capacity: lifecycle.FilesystemCapacity{Required: 1, Known: true}},
		"missing required": {capacity: lifecycle.FilesystemCapacity{Identity: 1, Known: true}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := test.capacity.Valid(); got != test.valid {
				t.Fatalf("Valid() = %t, want %t", got, test.valid)
			}
		})
	}
}

func TestNewDiskPreflightResultCopiesSortsAndDerivesSufficiency(t *testing.T) {
	t.Parallel()

	input := []lifecycle.FilesystemCapacity{
		{Identity: 3, Required: 5, Available: 5, Known: true},
		{Identity: 1, Required: 2, Available: 3, Known: true},
	}
	result, err := lifecycle.NewDiskPreflightResult(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []lifecycle.FilesystemCapacity{
		{Identity: 1, Required: 2, Available: 3, Known: true},
		{Identity: 3, Required: 5, Available: 5, Known: true},
	}
	if !result.Coherent() || !result.Sufficient || !reflect.DeepEqual(result.Filesystems, want) {
		t.Fatalf("result = %+v, want sufficient sorted facts %+v", result, want)
	}
	input[0] = lifecycle.FilesystemCapacity{}
	if !reflect.DeepEqual(result.Filesystems, want) {
		t.Fatal("constructor retained caller-owned capacity storage")
	}

	unknown, err := lifecycle.NewDiskPreflightResult([]lifecycle.FilesystemCapacity{{Identity: 1, Required: 1}})
	if err != nil || unknown.Sufficient || !unknown.Coherent() {
		t.Fatalf("unknown result = %+v, error = %v", unknown, err)
	}
	insufficient, err := lifecycle.NewDiskPreflightResult([]lifecycle.FilesystemCapacity{{
		Identity: 1, Required: 2, Available: 1, Known: true,
	}})
	if err != nil || insufficient.Sufficient || !insufficient.Coherent() {
		t.Fatalf("insufficient result = %+v, error = %v", insufficient, err)
	}
}

func TestDiskPreflightResultRejectsIncoherentAndUnboundedFacts(t *testing.T) {
	t.Parallel()

	valid := lifecycle.FilesystemCapacity{Identity: 1, Required: 1, Available: 1, Known: true}
	for name, filesystems := range map[string][]lifecycle.FilesystemCapacity{
		"empty": nil,
		"too many": {
			valid,
			{Identity: 2, Required: 1, Available: 1, Known: true},
			{Identity: 3, Required: 1, Available: 1, Known: true},
			{Identity: 4, Required: 1, Available: 1, Known: true},
			{Identity: 5, Required: 1, Available: 1, Known: true},
		},
		"duplicate": {valid, valid},
		"invalid":   {{Identity: 1, Required: 1, Available: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if result, err := lifecycle.NewDiskPreflightResult(filesystems); err == nil || result.Coherent() {
				t.Fatalf("NewDiskPreflightResult() = %+v, %v", result, err)
			}
		})
	}

	for name, result := range map[string]lifecycle.DiskPreflightResult{
		"zero": {},
		"unsorted": {
			Sufficient: true,
			Filesystems: []lifecycle.FilesystemCapacity{
				{Identity: 2, Required: 1, Available: 1, Known: true},
				valid,
			},
		},
		"duplicate": {Sufficient: true, Filesystems: []lifecycle.FilesystemCapacity{valid, valid}},
		"false sufficient": {
			Filesystems: []lifecycle.FilesystemCapacity{valid},
		},
		"true insufficient": {
			Sufficient:  true,
			Filesystems: []lifecycle.FilesystemCapacity{{Identity: 1, Required: 2, Available: 1, Known: true}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if result.Coherent() {
				t.Fatalf("incoherent result accepted: %+v", result)
			}
		})
	}
}
