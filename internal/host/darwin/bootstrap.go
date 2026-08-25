package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/alx4j/ai4j/internal/host/darwin/resource"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/pathsafe"
)

var (
	errInvalidBootstrap = errors.New("invalid Darwin read-only bootstrap")
	errBootstrapClosed  = errors.New("Darwin read-only bootstrap closed")
	errBootstrapClose   = errors.New("close Darwin read-only bootstrap")
)

type userDirectoryProofAuthority interface {
	InspectUserHome(context.Context) (lifecycle.UserHomeProof, error)
	QualifyUserDirectory(context.Context, lifecycle.UserHomeProof, pathsafe.RelativePath) (lifecycle.DirectoryLeafProof, error)
	Close() error
}

// Bootstrap owns only the read-only user-directory authority in this staged
// checkpoint. It exposes no generic filesystem or mutation facet and does not
// expose the separately returned host resource policy.
type Bootstrap struct {
	state            *bootstrapState
	directories      userDirectoryProofAuthority
	filesystemPolicy resource.Policy
}

type bootstrapState struct {
	mu        sync.Mutex
	active    int
	closing   bool
	closed    bool
	drained   chan struct{}
	closeDone chan struct{}
	closeErr  error
}

func newBootstrap(directories userDirectoryProofAuthority) (*Bootstrap, error) {
	if nilInterface(directories) {
		return nil, errInvalidBootstrap
	}
	return &Bootstrap{
		state:            &bootstrapState{closeDone: make(chan struct{})},
		directories:      directories,
		filesystemPolicy: resource.MVPPolicy(),
	}, nil
}

func (Bootstrap) String() string   { return "<darwin-read-only-bootstrap:redacted>" }
func (Bootstrap) GoString() string { return "<darwin-read-only-bootstrap:redacted>" }
func (b Bootstrap) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, b.String())
}
func (b Bootstrap) MarshalText() ([]byte, error) { return []byte(b.String()), nil }
func (Bootstrap) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"bootstrap": "redacted"})
}

func (b *Bootstrap) InspectUserHome(ctx context.Context) (lifecycle.UserHomeProof, error) {
	bounded, cancel, err := b.acquire(ctx)
	if err != nil {
		return lifecycle.UserHomeProof{}, err
	}
	defer b.release()
	defer cancel()
	proof, err := b.directories.InspectUserHome(bounded)
	if err := bounded.Err(); err != nil {
		return lifecycle.UserHomeProof{}, err
	}
	if err != nil {
		return lifecycle.UserHomeProof{}, err
	}
	return proof, nil
}

func (b *Bootstrap) QualifyUserDirectory(
	ctx context.Context,
	home lifecycle.UserHomeProof,
	relative pathsafe.RelativePath,
) (lifecycle.DirectoryLeafProof, error) {
	bounded, cancel, err := b.acquire(ctx)
	if err != nil {
		return lifecycle.DirectoryLeafProof{}, err
	}
	defer b.release()
	defer cancel()
	proof, err := b.directories.QualifyUserDirectory(bounded, home, relative)
	if err := bounded.Err(); err != nil {
		return lifecycle.DirectoryLeafProof{}, err
	}
	if err != nil {
		return lifecycle.DirectoryLeafProof{}, err
	}
	return proof, nil
}

func (b *Bootstrap) acquire(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if b == nil || b.state == nil || b.state.closeDone == nil || nilInterface(b.directories) || ctx == nil ||
		b.filesystemPolicy != resource.MVPPolicy() {
		return nil, nil, errInvalidBootstrap
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	bounded, cancel, err := boundedFilesystemContext(ctx, b.filesystemPolicy)
	if err != nil {
		return nil, nil, err
	}
	b.state.mu.Lock()
	if err := bounded.Err(); err != nil {
		b.state.mu.Unlock()
		cancel()
		return nil, nil, err
	}
	if b.state.closing || b.state.closed {
		b.state.mu.Unlock()
		cancel()
		return nil, nil, errBootstrapClosed
	}
	b.state.active++
	b.state.mu.Unlock()
	return bounded, cancel, nil
}

func (b *Bootstrap) release() {
	state := b.state
	state.mu.Lock()
	state.active--
	if state.closing && state.active == 0 && state.drained != nil {
		close(state.drained)
		state.drained = nil
	}
	state.mu.Unlock()
}

// boundedFilesystemContext creates a new budget for one observation. It never
// stores the caller context, and context.WithTimeout preserves an earlier
// caller deadline or cancellation through the parent chain.
func boundedFilesystemContext(
	parent context.Context,
	policy resource.Policy,
) (context.Context, context.CancelFunc, error) {
	if parent == nil || policy != resource.MVPPolicy() {
		return nil, nil, errInvalidBootstrap
	}
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	bounded, cancel, err := policy.WithBudget(parent, resource.FilesystemBudget)
	if err != nil {
		return nil, nil, errInvalidBootstrap
	}
	return bounded, cancel, nil
}

// constructUserDirectoryProofAuthority gives construction its own filesystem
// budget. A constructor that returns after its context has expired is treated
// as failed and its descriptor authority is closed before returning.
func constructUserDirectoryProofAuthority(
	ctx context.Context,
	construct func(context.Context) (userDirectoryProofAuthority, error),
) (userDirectoryProofAuthority, error) {
	if construct == nil {
		return nil, errInvalidBootstrap
	}
	bounded, cancel, err := boundedFilesystemContext(ctx, resource.MVPPolicy())
	if err != nil {
		return nil, err
	}
	defer cancel()
	directories, err := construct(bounded)
	if contextErr := bounded.Err(); contextErr != nil {
		if !nilInterface(directories) {
			_ = directories.Close()
		}
		return nil, contextErr
	}
	if err != nil {
		if !nilInterface(directories) {
			_ = directories.Close()
		}
		return nil, err
	}
	if nilInterface(directories) {
		return nil, errInvalidBootstrap
	}
	return directories, nil
}

// Close waits for active read-only observations, closes descriptor authority
// exactly once, and returns only a fixed non-data-bearing close result.
func (b *Bootstrap) Close() error {
	if b == nil || b.state == nil || b.state.closeDone == nil || nilInterface(b.directories) {
		return errInvalidBootstrap
	}
	state := b.state
	state.mu.Lock()
	if state.closed {
		result := state.closeErr
		state.mu.Unlock()
		return result
	}
	if state.closing {
		done := state.closeDone
		state.mu.Unlock()
		<-done
		state.mu.Lock()
		result := state.closeErr
		state.mu.Unlock()
		return result
	}
	state.closing = true
	drained := make(chan struct{})
	state.drained = drained
	if state.active == 0 {
		close(drained)
		state.drained = nil
	}
	state.mu.Unlock()

	<-drained
	closeErr := b.directories.Close()
	state.mu.Lock()
	if closeErr != nil {
		state.closeErr = errBootstrapClose
	}
	state.closed = true
	state.closing = false
	close(state.closeDone)
	result := state.closeErr
	state.mu.Unlock()
	return result
}
