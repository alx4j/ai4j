package resource

import (
	"context"
	"errors"
	"math"
	"reflect"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

// FilesystemSample is a pre/post-revalidated observation made through one
// already-opened configured-root descriptor. Unknown capacity is represented
// by Known=false and Available=0.
type FilesystemSample struct {
	Role       lifecycle.RootRole
	Root       lifecycle.ObjectIdentity
	Filesystem uint64
	Available  uint64
	Known      bool
}

func (s FilesystemSample) Valid() bool {
	return s.Role.Valid() && s.Root.Valid() && s.Filesystem != 0 && s.Filesystem != math.MaxUint64 &&
		(s.Known || s.Available == 0)
}

// FilesystemSampler is owned by this consumer package. The Darwin filesystem
// provider implements it without exposing descriptors or configured paths.
type FilesystemSampler interface {
	SampleFilesystems(context.Context, []lifecycle.RootRole) ([]FilesystemSample, error)
}

type Preflighter struct {
	filesystems FilesystemSampler
	policy      Policy
}

var _ lifecycle.DiskPreflighter = (*Preflighter)(nil)

func NewPreflighter(filesystems FilesystemSampler, policy Policy) (*Preflighter, error) {
	if nilFilesystemSampler(filesystems) || !policy.Valid() {
		return nil, errInvalidPolicy
	}
	return &Preflighter{filesystems: filesystems, policy: policy}, nil
}

func (p *Preflighter) PreflightDisk(parent context.Context, request lifecycle.DiskPreflightRequest) (lifecycle.DiskPreflightResult, error) {
	if p == nil || nilFilesystemSampler(p.filesystems) || !p.policy.Valid() || parent == nil {
		return lifecycle.DiskPreflightResult{}, errInvalidPolicy
	}
	planned, count, err := p.policy.plan(request)
	if err != nil {
		return lifecycle.DiskPreflightResult{}, err
	}
	ctx, cancel, err := p.policy.WithBudget(parent, FilesystemBudget)
	if err != nil {
		return lifecycle.DiskPreflightResult{}, err
	}
	defer cancel()
	if err := ctx.Err(); err != nil {
		return lifecycle.DiskPreflightResult{}, err
	}

	grouped := make(map[uint64]lifecycle.FilesystemCapacity, count)
	roles := make([]lifecycle.RootRole, 0, count)
	requestedRoles := make(map[lifecycle.RootRole]struct{}, count)
	for _, allocation := range planned[:count] {
		if _, duplicate := requestedRoles[allocation.root]; duplicate {
			continue
		}
		requestedRoles[allocation.root] = struct{}{}
		roles = append(roles, allocation.root)
	}
	samples, sampleErr := p.filesystems.SampleFilesystems(ctx, roles)
	if err := ctx.Err(); err != nil {
		return lifecycle.DiskPreflightResult{}, err
	}
	if sampleErr != nil {
		return lifecycle.DiskPreflightResult{}, sampleErr
	}
	if len(samples) != len(roles) {
		return lifecycle.DiskPreflightResult{}, errInvalidCapacitySample
	}
	byRole := make(map[lifecycle.RootRole]FilesystemSample, len(samples))
	for _, sample := range samples {
		_, requested := requestedRoles[sample.Role]
		if !sample.Valid() || !requested {
			return lifecycle.DiskPreflightResult{}, errInvalidCapacitySample
		}
		if _, duplicate := byRole[sample.Role]; duplicate {
			return lifecycle.DiskPreflightResult{}, errInvalidCapacitySample
		}
		byRole[sample.Role] = sample
	}
	for _, allocation := range planned[:count] {
		if err := ctx.Err(); err != nil {
			return lifecycle.DiskPreflightResult{}, err
		}
		sample, ok := byRole[allocation.root]
		if !ok {
			return lifecycle.DiskPreflightResult{}, errInvalidCapacitySample
		}
		identity := sample.Filesystem
		capacity, exists := grouped[identity]
		if !exists {
			grouped[identity] = lifecycle.FilesystemCapacity{
				Identity: identity, Required: allocation.required,
				Available: sample.Available, Known: sample.Known,
			}
			continue
		}
		required, ok := checkedAdd(capacity.Required, allocation.required)
		if !ok {
			return lifecycle.DiskPreflightResult{}, errDiskArithmeticOverflow
		}
		capacity.Required = required
		if !capacity.Known || !sample.Known {
			capacity.Known = false
			capacity.Available = 0
		} else if sample.Available < capacity.Available {
			capacity.Available = sample.Available
		}
		grouped[identity] = capacity
	}

	capacities := make([]lifecycle.FilesystemCapacity, 0, len(grouped))
	for _, capacity := range grouped {
		capacities = append(capacities, capacity)
	}
	result, err := lifecycle.NewDiskPreflightResult(capacities)
	if err != nil {
		return lifecycle.DiskPreflightResult{}, errors.Join(errInvalidCapacitySample, err)
	}
	return result, nil
}

func nilFilesystemSampler(value FilesystemSampler) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
