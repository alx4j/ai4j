// Package testkit provides deterministic, instance-local lifecycle fakes.
package testkit

import (
	"context"
	"errors"
	"sync"
)

var ErrScriptExhausted = errors.New("testkit script exhausted")

type Result[T any] struct {
	Value T
	Err   error
}

type script[T any] struct {
	mu      sync.Mutex
	results []Result[T]
	next    int
}

func newScript[T any](results []Result[T]) *script[T] {
	return &script[T]{results: append([]Result[T](nil), results...)}
}

func (s *script[T]) nextResult() (T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= len(s.results) {
		var zero T
		return zero, ErrScriptExhausted
	}
	result := s.results[s.next]
	s.next++
	return result.Value, result.Err
}

func waitForContext(ctx context.Context, gate <-chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if gate == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-gate:
		return nil
	}
}
