package testkit

import (
	"context"
	"sync"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

type StateRead struct {
	Record lifecycle.InstallationRecord
	Found  bool
}

type State struct {
	mu          sync.Mutex
	gate        <-chan struct{}
	reads       *script[StateRead]
	writes      *script[struct{}]
	deletes     *script[struct{}]
	readCalls   []lifecycle.InstallationKey
	writeCalls  []lifecycle.InstallationRecord
	deleteCalls []lifecycle.InstallationKey
}

func NewState(gate <-chan struct{}, reads []Result[StateRead], writes, deletes []Result[struct{}]) *State {
	return &State{gate: gate, reads: newScript(reads), writes: newScript(writes), deletes: newScript(deletes)}
}

func (f *State) ReadInstallation(ctx context.Context, key lifecycle.InstallationKey) (lifecycle.InstallationRecord, bool, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.InstallationRecord{}, false, err
	}
	f.mu.Lock()
	f.readCalls = append(f.readCalls, key)
	f.mu.Unlock()
	result, err := f.reads.nextResult()
	return result.Record, result.Found, err
}

func (f *State) WriteInstallation(ctx context.Context, record lifecycle.InstallationRecord) error {
	if err := waitForContext(ctx, f.gate); err != nil {
		return err
	}
	f.mu.Lock()
	f.writeCalls = append(f.writeCalls, record)
	f.mu.Unlock()
	_, err := f.writes.nextResult()
	return err
}

func (f *State) DeleteInstallation(ctx context.Context, key lifecycle.InstallationKey) error {
	if err := waitForContext(ctx, f.gate); err != nil {
		return err
	}
	f.mu.Lock()
	f.deleteCalls = append(f.deleteCalls, key)
	f.mu.Unlock()
	_, err := f.deletes.nextResult()
	return err
}

func (f *State) WriteCalls() []lifecycle.InstallationRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.InstallationRecord(nil), f.writeCalls...)
}

func (f *State) ReadCalls() []lifecycle.InstallationKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.InstallationKey(nil), f.readCalls...)
}

func (f *State) DeleteCalls() []lifecycle.InstallationKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.InstallationKey(nil), f.deleteCalls...)
}

var _ lifecycle.InstallationStateReader = (*State)(nil)
var _ lifecycle.InstallationStateWriter = (*State)(nil)
