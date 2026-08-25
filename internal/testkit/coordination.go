package testkit

import (
	"context"
	"sync"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

type Lock struct {
	mu      sync.Mutex
	gate    <-chan struct{}
	results *script[lifecycle.LockHandle]
	calls   []lifecycle.LockRequest
}

func NewLock(gate <-chan struct{}, results []Result[lifecycle.LockHandle]) *Lock {
	return &Lock{gate: gate, results: newScript(results)}
}

func (f *Lock) AcquireLock(ctx context.Context, request lifecycle.LockRequest) (lifecycle.LockHandle, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, request)
	f.mu.Unlock()
	return f.results.nextResult()
}
func (f *Lock) Calls() []lifecycle.LockRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.LockRequest(nil), f.calls...)
}

type LockHandle struct {
	mu       sync.Mutex
	releases int
	err      error
}

func NewLockHandle(err error) *LockHandle { return &LockHandle{err: err} }
func (h *LockHandle) Release() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.releases++
	return h.err
}
func (h *LockHandle) Releases() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.releases
}

type JournalRead struct {
	Record lifecycle.JournalRecord
	Found  bool
}

type Journal struct {
	mu          sync.Mutex
	gate        <-chan struct{}
	reads       *script[JournalRead]
	writes      *script[struct{}]
	deletes     *script[struct{}]
	readCalls   []domain.InstallationID
	writeCalls  []lifecycle.JournalRecord
	deleteCalls []domain.OperationID
}

func NewJournal(gate <-chan struct{}, reads []Result[JournalRead], writes, deletes []Result[struct{}]) *Journal {
	return &Journal{gate: gate, reads: newScript(reads), writes: newScript(writes), deletes: newScript(deletes)}
}
func (f *Journal) ReadJournal(ctx context.Context, installationID domain.InstallationID) (lifecycle.JournalRecord, bool, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.JournalRecord{}, false, err
	}
	f.mu.Lock()
	f.readCalls = append(f.readCalls, installationID)
	f.mu.Unlock()
	result, err := f.reads.nextResult()
	return result.Record, result.Found, err
}
func (f *Journal) WriteJournal(ctx context.Context, record lifecycle.JournalRecord) error {
	if err := waitForContext(ctx, f.gate); err != nil {
		return err
	}
	f.mu.Lock()
	f.writeCalls = append(f.writeCalls, record)
	f.mu.Unlock()
	_, err := f.writes.nextResult()
	return err
}
func (f *Journal) DeleteJournal(ctx context.Context, operationID domain.OperationID) error {
	if err := waitForContext(ctx, f.gate); err != nil {
		return err
	}
	f.mu.Lock()
	f.deleteCalls = append(f.deleteCalls, operationID)
	f.mu.Unlock()
	_, err := f.deletes.nextResult()
	return err
}
func (f *Journal) WriteCalls() []lifecycle.JournalRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.JournalRecord(nil), f.writeCalls...)
}

type Recovery struct {
	mu          sync.Mutex
	gate        <-chan struct{}
	reads       *script[[]lifecycle.RecoveryArtifact]
	writes      *script[struct{}]
	deletes     *script[struct{}]
	readCalls   []domain.OperationID
	writeCalls  []lifecycle.RecoveryArtifact
	deleteCalls []domain.OperationID
}

func NewRecovery(gate <-chan struct{}, reads []Result[[]lifecycle.RecoveryArtifact], writes, deletes []Result[struct{}]) *Recovery {
	readCopies := append([]Result[[]lifecycle.RecoveryArtifact](nil), reads...)
	for index := range readCopies {
		readCopies[index].Value = append([]lifecycle.RecoveryArtifact(nil), readCopies[index].Value...)
	}
	return &Recovery{gate: gate, reads: newScript(readCopies), writes: newScript(writes), deletes: newScript(deletes)}
}
func (f *Recovery) ReadRecovery(ctx context.Context, operationID domain.OperationID) ([]lifecycle.RecoveryArtifact, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.readCalls = append(f.readCalls, operationID)
	f.mu.Unlock()
	result, err := f.reads.nextResult()
	return append([]lifecycle.RecoveryArtifact(nil), result...), err
}
func (f *Recovery) WriteRecovery(ctx context.Context, artifact lifecycle.RecoveryArtifact) error {
	if err := waitForContext(ctx, f.gate); err != nil {
		return err
	}
	f.mu.Lock()
	f.writeCalls = append(f.writeCalls, artifact)
	f.mu.Unlock()
	_, err := f.writes.nextResult()
	return err
}
func (f *Recovery) DeleteRecovery(ctx context.Context, operationID domain.OperationID) error {
	if err := waitForContext(ctx, f.gate); err != nil {
		return err
	}
	f.mu.Lock()
	f.deleteCalls = append(f.deleteCalls, operationID)
	f.mu.Unlock()
	_, err := f.deletes.nextResult()
	return err
}
func (f *Recovery) WriteCalls() []lifecycle.RecoveryArtifact {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.RecoveryArtifact(nil), f.writeCalls...)
}

var _ lifecycle.LockAcquirer = (*Lock)(nil)
var _ lifecycle.LockHandle = (*LockHandle)(nil)
var _ lifecycle.JournalReader = (*Journal)(nil)
var _ lifecycle.JournalWriter = (*Journal)(nil)
var _ lifecycle.RecoveryReader = (*Recovery)(nil)
var _ lifecycle.RecoveryWriter = (*Recovery)(nil)
