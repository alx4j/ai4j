package testkit

import (
	"context"
	"sync"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

type Source struct {
	mu      sync.Mutex
	gate    <-chan struct{}
	results *script[lifecycle.SourceSnapshot]
	calls   []lifecycle.SourceRequest
}

func NewSource(gate <-chan struct{}, results []Result[lifecycle.SourceSnapshot]) *Source {
	return &Source{gate: gate, results: newScript(results)}
}

func (f *Source) AcquireSource(ctx context.Context, request lifecycle.SourceRequest) (lifecycle.SourceSnapshot, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, request)
	f.mu.Unlock()
	return f.results.nextResult()
}

func (f *Source) Calls() []lifecycle.SourceRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.SourceRequest(nil), f.calls...)
}

type Snapshot struct {
	mu         sync.Mutex
	root       string
	commit     domain.CommitIdentity
	tree       domain.TreeOID
	closeCount int
	closeErr   error
}

func NewSnapshot(root string, commit domain.CommitIdentity, tree domain.TreeOID, closeErr error) *Snapshot {
	return &Snapshot{root: root, commit: commit, tree: tree, closeErr: closeErr}
}

func (s *Snapshot) Root() string                  { return s.root }
func (s *Snapshot) Commit() domain.CommitIdentity { return s.commit }
func (s *Snapshot) Tree() domain.TreeOID          { return s.tree }
func (s *Snapshot) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCount++
	return s.closeErr
}
func (s *Snapshot) CloseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCount
}

var _ lifecycle.SourceAcquirer = (*Source)(nil)
var _ lifecycle.SourceSnapshot = (*Snapshot)(nil)
