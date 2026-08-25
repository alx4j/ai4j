package resource

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestPolicyPlansPurposeHeadroomWithExplicitCrossRoleRoots(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t)
	request := lifecycle.DiskPreflightRequest{
		TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.ManagedOutputRoot, Bytes: 10},
		StagedOutput:    lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 20},
		Journal:         lifecycle.DiskAllocation{Root: lifecycle.RecoveryRoot, Bytes: 30},
		Recovery:        lifecycle.DiskAllocation{Root: lifecycle.TemporarySourceRoot, Bytes: 40},
	}
	planned, count, err := policy.plan(request)
	if err != nil {
		t.Fatal(err)
	}
	want := [4]plannedAllocation{
		{root: lifecycle.ManagedOutputRoot, required: 11},
		{root: lifecycle.StateRoot, required: 22},
		{root: lifecycle.RecoveryRoot, required: 33},
		{root: lifecycle.TemporarySourceRoot, required: 44},
	}
	if count != 4 || planned != want {
		t.Fatalf("plan = %+v, %d, want %+v, 4", planned, count, want)
	}
}

func TestMVPPolicyProfileHasExactVersionedProductionLimits(t *testing.T) {
	t.Parallel()

	policy := MVPPolicy()
	if !policy.Valid() || policy.Version() != MVPPolicyVersion {
		t.Fatalf("MVP policy version = %q, valid=%t", policy.Version(), policy.Valid())
	}
	wantAllocations := map[AllocationPurpose]AllocationPolicy{
		TemporarySourceAllocation: {MaximumDeclaredBytes: 1 << 30, HeadroomBytes: 256 << 20},
		StagedOutputAllocation:    {MaximumDeclaredBytes: 512 << 20, HeadroomBytes: 128 << 20},
		JournalAllocation:         {MaximumDeclaredBytes: 64 << 20, HeadroomBytes: 64 << 20},
		RecoveryAllocation:        {MaximumDeclaredBytes: 512 << 20, HeadroomBytes: 128 << 20},
	}
	for purpose, want := range wantAllocations {
		got, ok := policy.Allocation(purpose)
		if !ok || got != want {
			t.Errorf("Allocation(%s) = %+v, %t, want %+v, true", purpose, got, ok, want)
		}
	}
	wantTimeouts := map[Budget]time.Duration{
		GitBudget: 5 * time.Minute, ClaudeBudget: 2 * time.Minute,
		FilesystemBudget: 5 * time.Second, LockBudget: 30 * time.Second,
	}
	for budget, want := range wantTimeouts {
		got, ok := policy.Timeout(budget)
		if !ok || got != want {
			t.Errorf("Timeout(%s) = %s, %t, want %s, true", budget, got, ok, want)
		}
	}
	if _, ok := policy.Allocation(AllocationPurpose("unknown")); ok {
		t.Fatal("unknown allocation purpose produced a limit")
	}
	if _, ok := policy.Timeout(Budget("unknown")); ok {
		t.Fatal("unknown budget produced a timeout")
	}

	tampered := policy
	tampered.gitTimeout++
	if tampered.Valid() || tampered.Version() != "" {
		t.Fatal("MVP profile identity survived a value change")
	}
}

func TestPolicyRejectsInvalidLimitsRequestsAndArithmetic(t *testing.T) {
	t.Parallel()

	base := testPolicyConfig()
	for name, mutate := range map[string]func(*policyConfig){
		"zero declared maximum":      func(config *policyConfig) { config.TemporarySource.MaximumDeclaredBytes = 0 },
		"declared maximum ceiling":   func(config *policyConfig) { config.StagedOutput.MaximumDeclaredBytes = maximumDeclaredBytesLimit + 1 },
		"zero headroom":              func(config *policyConfig) { config.Journal.HeadroomBytes = 0 },
		"headroom ceiling":           func(config *policyConfig) { config.Recovery.HeadroomBytes = maximumHeadroomBytesLimit + 1 },
		"zero git timeout":           func(config *policyConfig) { config.GitTimeout = 0 },
		"negative Claude timeout":    func(config *policyConfig) { config.ClaudeTimeout = -time.Second },
		"filesystem timeout ceiling": func(config *policyConfig) { config.FilesystemTimeout = maximumConfiguredTimeout + 1 },
		"lock timeout ceiling":       func(config *policyConfig) { config.LockTimeout = maximumConfiguredTimeout + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := base
			mutate(&config)
			if value, err := newPolicy(config); !errors.Is(err, errInvalidPolicy) || value.Valid() {
				t.Fatalf("newPolicy() = %+v, %v", value, err)
			}
		})
	}

	policy := testPolicy(t)
	for name, request := range map[string]lifecycle.DiskPreflightRequest{
		"empty":   {},
		"partial": {TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot}},
		"over declared maximum": {
			TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 101},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := policy.plan(request); err == nil {
				t.Fatal("invalid request produced a plan")
			}
		})
	}
	if _, ok := checkedAdd(math.MaxUint64, 1); ok {
		t.Fatal("overflowing byte addition was accepted")
	}
	if sum, ok := checkedAdd(math.MaxUint64-1, 1); !ok || sum != math.MaxUint64 {
		t.Fatalf("boundary addition = %d, %t", sum, ok)
	}
}

func TestPolicyBudgetsPreserveCallerPrecedenceAndStoreNoContext(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t)
	parentDeadline := time.Now().Add(5 * time.Minute)
	parent, parentCancel := context.WithDeadline(context.Background(), parentDeadline)
	defer parentCancel()
	bounded, cancel, err := policy.WithBudget(parent, GitBudget)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	deadline, ok := bounded.Deadline()
	if !ok || !deadline.Equal(parentDeadline) {
		t.Fatalf("bounded deadline = %v, %t, want caller deadline %v", deadline, ok, parentDeadline)
	}

	before := time.Now()
	configured, configuredCancel, err := policy.WithBudget(context.Background(), FilesystemBudget)
	if err != nil {
		t.Fatal(err)
	}
	defer configuredCancel()
	configuredDeadline, ok := configured.Deadline()
	if !ok || configuredDeadline.Before(before.Add(29*time.Second)) || configuredDeadline.After(time.Now().Add(31*time.Second)) {
		t.Fatalf("configured deadline = %v", configuredDeadline)
	}

	cancelledParent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	cancelled, cancelledCleanup, err := policy.WithBudget(cancelledParent, LockBudget)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelledCleanup()
	if !errors.Is(cancelled.Err(), context.Canceled) {
		t.Fatalf("cancelled budget error = %v", cancelled.Err())
	}

	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	policyType := reflect.TypeOf(policy)
	for index := 0; index < policyType.NumField(); index++ {
		if policyType.Field(index).Type.Implements(contextType) {
			t.Fatalf("policy stores request context in field %q", policyType.Field(index).Name)
		}
	}
	if _, _, err := policy.WithBudget(nil, GitBudget); !errors.Is(err, errInvalidPolicy) {
		t.Fatalf("nil-parent budget error = %v", err)
	}
	if _, _, err := policy.WithBudget(context.Background(), Budget("unknown")); !errors.Is(err, errInvalidPolicy) {
		t.Fatalf("unknown budget error = %v", err)
	}
}

func testPolicy(t *testing.T) Policy {
	t.Helper()
	policy, err := newPolicy(testPolicyConfig())
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func testPolicyConfig() policyConfig {
	return policyConfig{
		TemporarySource:   AllocationPolicy{MaximumDeclaredBytes: 100, HeadroomBytes: 1},
		StagedOutput:      AllocationPolicy{MaximumDeclaredBytes: 100, HeadroomBytes: 2},
		Journal:           AllocationPolicy{MaximumDeclaredBytes: 100, HeadroomBytes: 3},
		Recovery:          AllocationPolicy{MaximumDeclaredBytes: 100, HeadroomBytes: 4},
		GitTimeout:        10 * time.Minute,
		ClaudeTimeout:     5 * time.Minute,
		FilesystemTimeout: 30 * time.Second,
		LockTimeout:       time.Minute,
	}
}
