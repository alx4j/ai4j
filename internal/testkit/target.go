package testkit

import (
	"context"
	"sync"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

type Target struct {
	mu            sync.Mutex
	gate          <-chan struct{}
	observations  *script[lifecycle.TargetObservation]
	mutations     *script[lifecycle.TargetMutationResult]
	observeCalls  []lifecycle.TargetObservationRequest
	mutationCalls []lifecycle.TargetMutationRequest
}

func NewTarget(
	gate <-chan struct{},
	observations []Result[lifecycle.TargetObservation],
	mutations []Result[lifecycle.TargetMutationResult],
) *Target {
	return &Target{gate: gate, observations: newScript(observations), mutations: newScript(mutations)}
}

func (f *Target) ObserveTarget(ctx context.Context, request lifecycle.TargetObservationRequest) (lifecycle.TargetObservation, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.TargetObservation{}, err
	}
	f.mu.Lock()
	f.observeCalls = append(f.observeCalls, request)
	f.mu.Unlock()
	return f.observations.nextResult()
}

func (f *Target) MutateTarget(ctx context.Context, request lifecycle.TargetMutationRequest) (lifecycle.TargetMutationResult, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.TargetMutationResult{}, err
	}
	f.mu.Lock()
	f.mutationCalls = append(f.mutationCalls, request)
	f.mu.Unlock()
	return f.mutations.nextResult()
}

func (f *Target) ObserveCalls() []lifecycle.TargetObservationRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.TargetObservationRequest(nil), f.observeCalls...)
}

func (f *Target) MutationCalls() []lifecycle.TargetMutationRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.TargetMutationRequest(nil), f.mutationCalls...)
}

var _ lifecycle.TargetObserver = (*Target)(nil)
var _ lifecycle.TargetMutator = (*Target)(nil)
