package testkit

import (
	"context"
	"sync"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

type Clock struct {
	mu     sync.Mutex
	values []time.Time
	next   int
}

func NewClock(values ...time.Time) *Clock { return &Clock{values: append([]time.Time(nil), values...)} }
func (f *Clock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.next >= len(f.values) {
		panic(ErrScriptExhausted)
	}
	value := f.values[f.next]
	f.next++
	return value
}

type Identifiers struct {
	gate          <-chan struct{}
	operations    *script[domain.OperationID]
	installations *script[domain.InstallationID]
	tokens        *script[domain.ArtifactToken]
}

func NewIdentifiers(gate <-chan struct{}, operations []Result[domain.OperationID], installations []Result[domain.InstallationID], tokens []Result[domain.ArtifactToken]) *Identifiers {
	return &Identifiers{gate: gate, operations: newScript(operations), installations: newScript(installations), tokens: newScript(tokens)}
}
func (f *Identifiers) NewArtifactToken(ctx context.Context) (domain.ArtifactToken, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return domain.ArtifactToken{}, err
	}
	return f.tokens.nextResult()
}
func (f *Identifiers) NewOperationID(ctx context.Context) (domain.OperationID, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return domain.OperationID{}, err
	}
	return f.operations.nextResult()
}
func (f *Identifiers) NewInstallationID(ctx context.Context) (domain.InstallationID, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return domain.InstallationID{}, err
	}
	return f.installations.nextResult()
}

var _ lifecycle.Clock = (*Clock)(nil)
var _ lifecycle.IdentifierGenerator = (*Identifiers)(nil)
