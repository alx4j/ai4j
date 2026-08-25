// Package resource implements bounded Darwin resource policy and disk preflight.
package resource

import (
	"context"
	"errors"
	"time"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

const (
	maximumDeclaredBytesLimit uint64        = 1 << 40
	maximumHeadroomBytesLimit uint64        = 64 << 30
	maximumConfiguredTimeout  time.Duration = time.Hour
)

var (
	errInvalidPolicy          = errors.New("invalid Darwin resource policy")
	errInvalidDiskRequest     = errors.New("invalid disk preflight request")
	errDeclaredBytesExceeded  = errors.New("declared disk bytes exceed policy")
	errDiskArithmeticOverflow = errors.New("disk byte arithmetic overflow")
	errInvalidCapacitySample  = errors.New("invalid filesystem capacity sample")
)

type Budget string

const (
	GitBudget        Budget = "git"
	ClaudeBudget     Budget = "claude"
	FilesystemBudget Budget = "filesystem"
	LockBudget       Budget = "lock"
)

func (b Budget) Valid() bool {
	switch b {
	case GitBudget, ClaudeBudget, FilesystemBudget, LockBudget:
		return true
	default:
		return false
	}
}

type PolicyVersion string

const MVPPolicyVersion PolicyVersion = "mvp_resource_v1"

const testPolicyVersion PolicyVersion = "test_resource_policy"

type AllocationPurpose string

const (
	TemporarySourceAllocation AllocationPurpose = "temporary_source"
	StagedOutputAllocation    AllocationPurpose = "staged_output"
	JournalAllocation         AllocationPurpose = "journal"
	RecoveryAllocation        AllocationPurpose = "recovery"
)

func (p AllocationPurpose) Valid() bool {
	switch p {
	case TemporarySourceAllocation, StagedOutputAllocation, JournalAllocation, RecoveryAllocation:
		return true
	default:
		return false
	}
}

type AllocationPolicy struct {
	MaximumDeclaredBytes uint64
	HeadroomBytes        uint64
}

func (p AllocationPolicy) Valid() bool {
	return p.MaximumDeclaredBytes > 0 && p.MaximumDeclaredBytes <= maximumDeclaredBytesLimit &&
		p.HeadroomBytes > 0 && p.HeadroomBytes <= maximumHeadroomBytesLimit
}

type policyConfig struct {
	TemporarySource   AllocationPolicy
	StagedOutput      AllocationPolicy
	Journal           AllocationPolicy
	Recovery          AllocationPolicy
	GitTimeout        time.Duration
	ClaudeTimeout     time.Duration
	FilesystemTimeout time.Duration
	LockTimeout       time.Duration
}

type Policy struct {
	version           PolicyVersion
	temporarySource   AllocationPolicy
	stagedOutput      AllocationPolicy
	journal           AllocationPolicy
	recovery          AllocationPolicy
	gitTimeout        time.Duration
	claudeTimeout     time.Duration
	filesystemTimeout time.Duration
	lockTimeout       time.Duration
}

// MVPPolicy returns the one versioned production policy. External composition
// cannot construct an alternate Policy value because its state is private.
func MVPPolicy() Policy {
	return Policy{
		version:           MVPPolicyVersion,
		temporarySource:   AllocationPolicy{MaximumDeclaredBytes: 1 << 30, HeadroomBytes: 256 << 20},
		stagedOutput:      AllocationPolicy{MaximumDeclaredBytes: 512 << 20, HeadroomBytes: 128 << 20},
		journal:           AllocationPolicy{MaximumDeclaredBytes: 64 << 20, HeadroomBytes: 64 << 20},
		recovery:          AllocationPolicy{MaximumDeclaredBytes: 512 << 20, HeadroomBytes: 128 << 20},
		gitTimeout:        5 * time.Minute,
		claudeTimeout:     2 * time.Minute,
		filesystemTimeout: 5 * time.Second,
		lockTimeout:       30 * time.Second,
	}
}

// newPolicy is retained only for deterministic package tests. Production code
// receives MVPPolicy, whose exact values are bound to MVPPolicyVersion.
func newPolicy(config policyConfig) (Policy, error) {
	if !config.TemporarySource.Valid() || !config.StagedOutput.Valid() ||
		!config.Journal.Valid() || !config.Recovery.Valid() ||
		!validTimeout(config.GitTimeout) || !validTimeout(config.ClaudeTimeout) ||
		!validTimeout(config.FilesystemTimeout) || !validTimeout(config.LockTimeout) {
		return Policy{}, errInvalidPolicy
	}
	return Policy{
		version:           testPolicyVersion,
		temporarySource:   config.TemporarySource,
		stagedOutput:      config.StagedOutput,
		journal:           config.Journal,
		recovery:          config.Recovery,
		gitTimeout:        config.GitTimeout,
		claudeTimeout:     config.ClaudeTimeout,
		filesystemTimeout: config.FilesystemTimeout,
		lockTimeout:       config.LockTimeout,
	}, nil
}

func (p Policy) Valid() bool {
	valuesValid := p.temporarySource.Valid() && p.stagedOutput.Valid() && p.journal.Valid() && p.recovery.Valid() &&
		validTimeout(p.gitTimeout) && validTimeout(p.claudeTimeout) &&
		validTimeout(p.filesystemTimeout) && validTimeout(p.lockTimeout)
	if !valuesValid {
		return false
	}
	switch p.version {
	case MVPPolicyVersion:
		return p == MVPPolicy()
	case testPolicyVersion:
		return true
	default:
		return false
	}
}

func (p Policy) Version() PolicyVersion {
	if !p.Valid() {
		return ""
	}
	return p.version
}

func (p Policy) Allocation(purpose AllocationPurpose) (AllocationPolicy, bool) {
	if !p.Valid() || !purpose.Valid() {
		return AllocationPolicy{}, false
	}
	switch purpose {
	case TemporarySourceAllocation:
		return p.temporarySource, true
	case StagedOutputAllocation:
		return p.stagedOutput, true
	case JournalAllocation:
		return p.journal, true
	case RecoveryAllocation:
		return p.recovery, true
	default:
		return AllocationPolicy{}, false
	}
}

func (p Policy) Timeout(budget Budget) (time.Duration, bool) {
	if !p.Valid() || !budget.Valid() {
		return 0, false
	}
	return p.timeout(budget), true
}

// WithBudget derives a request-scoped context without retaining the caller's
// context. An earlier caller deadline or cancellation always wins through the
// parent context chain.
func (p Policy) WithBudget(parent context.Context, budget Budget) (context.Context, context.CancelFunc, error) {
	if parent == nil || !p.Valid() || !budget.Valid() {
		return nil, nil, errInvalidPolicy
	}
	timeout := p.timeout(budget)
	bounded, cancel := context.WithTimeout(parent, timeout)
	return bounded, cancel, nil
}

func (p Policy) timeout(budget Budget) time.Duration {
	switch budget {
	case GitBudget:
		return p.gitTimeout
	case ClaudeBudget:
		return p.claudeTimeout
	case FilesystemBudget:
		return p.filesystemTimeout
	case LockBudget:
		return p.lockTimeout
	default:
		return 0
	}
}

type plannedAllocation struct {
	root     lifecycle.RootRole
	required uint64
}

func (p Policy) plan(request lifecycle.DiskPreflightRequest) ([4]plannedAllocation, int, error) {
	var result [4]plannedAllocation
	if !p.Valid() || !request.Valid() {
		return result, 0, errInvalidDiskRequest
	}
	inputs := [...]struct {
		allocation lifecycle.DiskAllocation
		policy     AllocationPolicy
	}{
		{allocation: request.TemporarySource, policy: p.temporarySource},
		{allocation: request.StagedOutput, policy: p.stagedOutput},
		{allocation: request.Journal, policy: p.journal},
		{allocation: request.Recovery, policy: p.recovery},
	}
	count := 0
	var aggregate uint64
	for _, input := range inputs {
		if !input.allocation.Active() {
			continue
		}
		if input.allocation.Bytes > input.policy.MaximumDeclaredBytes {
			return [4]plannedAllocation{}, 0, errDeclaredBytesExceeded
		}
		required, ok := checkedAdd(input.allocation.Bytes, input.policy.HeadroomBytes)
		if !ok {
			return [4]plannedAllocation{}, 0, errDiskArithmeticOverflow
		}
		aggregate, ok = checkedAdd(aggregate, required)
		if !ok {
			return [4]plannedAllocation{}, 0, errDiskArithmeticOverflow
		}
		result[count] = plannedAllocation{root: input.allocation.Root, required: required}
		count++
	}
	return result, count, nil
}

func validTimeout(value time.Duration) bool {
	return value > 0 && value <= maximumConfiguredTimeout
}

func checkedAdd(left, right uint64) (uint64, bool) {
	if left > ^uint64(0)-right {
		return 0, false
	}
	return left + right, true
}
