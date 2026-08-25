package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

var (
	errInvalidBundle = errors.New("invalid Darwin host bundle")
	errBundleClosed  = errors.New("Darwin host bundle closed")
	errBundleClose   = errors.New("close Darwin host bundle")
)

type resourceInspector interface {
	CheckResource(context.Context, lifecycle.ResourceRequest) (lifecycle.ResourceObservation, error)
	ReadResource(context.Context, lifecycle.ResourceReadRequest) (lifecycle.ResourceReadResult, error)
	CheckExecutable(context.Context, lifecycle.ExecutableRequest) (lifecycle.ExecutableObservation, error)
}

type bundleComponents struct {
	inspector   lifecycle.HostInspector
	environment lifecycle.EnvironmentInspector
	resources   resourceInspector
	disk        lifecycle.DiskPreflighter
	files       lifecycle.AtomicFileWriter
	processes   lifecycle.ProcessRunner
	policy      lifecycle.HostResourcePolicy
	closer      io.Closer
}

// Bundle owns one Darwin host authority and projects its narrow lifecycle
// facets. The registry borrows Bundle as HostServices but never owns Close.
type Bundle struct {
	state *bundleState
	parts bundleComponents
}

type bundleState struct {
	mu        sync.RWMutex
	closeOnce sync.Once
	closed    bool
	closeErr  error
}

var _ lifecycle.HostServices = (*Bundle)(nil)

func newBundle(parts bundleComponents) (*Bundle, error) {
	if nilInterface(parts.inspector) || nilInterface(parts.environment) || nilInterface(parts.resources) ||
		nilInterface(parts.disk) || nilInterface(parts.files) || nilInterface(parts.processes) ||
		nilInterface(parts.closer) || !parts.policy.Valid() {
		return nil, errInvalidBundle
	}
	return &Bundle{state: &bundleState{}, parts: parts}, nil
}

func (Bundle) String() string   { return "<darwin-host-bundle:redacted>" }
func (Bundle) GoString() string { return "<darwin-host-bundle:redacted>" }
func (b Bundle) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, b.String())
}
func (b Bundle) MarshalText() ([]byte, error) { return []byte(b.String()), nil }
func (Bundle) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"bundle": "redacted"})
}

func (b *Bundle) ResourcePolicy() lifecycle.HostResourcePolicy {
	if b == nil {
		return lifecycle.HostResourcePolicy{}
	}
	return b.parts.policy
}

func (b *Bundle) InspectHost(
	ctx context.Context,
	request lifecycle.HostInspectionRequest,
) (lifecycle.HostObservation, error) {
	if err := b.acquire(ctx); err != nil {
		return lifecycle.HostObservation{}, err
	}
	defer b.state.mu.RUnlock()
	return b.parts.inspector.InspectHost(ctx, request)
}

func (b *Bundle) InspectEnvironment(
	ctx context.Context,
	request lifecycle.EnvironmentPresenceRequest,
) (lifecycle.EnvironmentPresenceResult, error) {
	if err := b.acquire(ctx); err != nil {
		return lifecycle.EnvironmentPresenceResult{}, err
	}
	defer b.state.mu.RUnlock()
	return b.parts.environment.InspectEnvironment(ctx, request)
}

func (b *Bundle) CheckResource(
	ctx context.Context,
	request lifecycle.ResourceRequest,
) (lifecycle.ResourceObservation, error) {
	if err := b.acquire(ctx); err != nil {
		return lifecycle.ResourceObservation{}, err
	}
	defer b.state.mu.RUnlock()
	return b.parts.resources.CheckResource(ctx, request)
}

func (b *Bundle) ReadResource(
	ctx context.Context,
	request lifecycle.ResourceReadRequest,
) (lifecycle.ResourceReadResult, error) {
	if err := b.acquire(ctx); err != nil {
		return lifecycle.ResourceReadResult{}, err
	}
	defer b.state.mu.RUnlock()
	return b.parts.resources.ReadResource(ctx, request)
}

func (b *Bundle) CheckExecutable(
	ctx context.Context,
	request lifecycle.ExecutableRequest,
) (lifecycle.ExecutableObservation, error) {
	if err := b.acquire(ctx); err != nil {
		return lifecycle.ExecutableObservation{}, err
	}
	defer b.state.mu.RUnlock()
	return b.parts.resources.CheckExecutable(ctx, request)
}

func (b *Bundle) PreflightDisk(
	ctx context.Context,
	request lifecycle.DiskPreflightRequest,
) (lifecycle.DiskPreflightResult, error) {
	if err := b.acquire(ctx); err != nil {
		return lifecycle.DiskPreflightResult{}, err
	}
	defer b.state.mu.RUnlock()
	return b.parts.disk.PreflightDisk(ctx, request)
}

func (b *Bundle) ReplaceFile(
	ctx context.Context,
	request lifecycle.FileMutation,
) (lifecycle.FileMutationResult, error) {
	if err := b.acquire(ctx); err != nil {
		return lifecycle.FileMutationResult{}, err
	}
	defer b.state.mu.RUnlock()
	return b.parts.files.ReplaceFile(ctx, request)
}

func (b *Bundle) CleanupFile(
	ctx context.Context,
	artifact lifecycle.CleanupArtifact,
) (lifecycle.FileCleanupResult, error) {
	if err := b.acquire(ctx); err != nil {
		return lifecycle.FileCleanupResult{}, err
	}
	defer b.state.mu.RUnlock()
	return b.parts.files.CleanupFile(ctx, artifact)
}

func (b *Bundle) InspectFileArtifacts(
	ctx context.Context,
	request lifecycle.FileArtifactInspectionRequest,
) (lifecycle.FileArtifactInspectionResult, error) {
	if err := b.acquire(ctx); err != nil {
		return lifecycle.FileArtifactInspectionResult{}, err
	}
	defer b.state.mu.RUnlock()
	return b.parts.files.InspectFileArtifacts(ctx, request)
}

func (b *Bundle) RunProcess(
	ctx context.Context,
	request lifecycle.ProcessRequest,
) (lifecycle.ProcessResult, error) {
	if err := b.acquire(ctx); err != nil {
		return lifecycle.ProcessResult{}, err
	}
	defer b.state.mu.RUnlock()
	return b.parts.processes.RunProcess(ctx, request)
}

func (b *Bundle) acquire(ctx context.Context) error {
	if b == nil || b.state == nil || ctx == nil {
		return errInvalidBundle
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.state.mu.RLock()
	if b.state.closed {
		b.state.mu.RUnlock()
		return errBundleClosed
	}
	return nil
}

// Close waits for active synchronous operations, closes the shared authority
// exactly once, and returns the same bounded result to every caller.
func (b *Bundle) Close() error {
	if b == nil || b.state == nil {
		return errInvalidBundle
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	b.state.closeOnce.Do(func() {
		b.state.closed = true
		if err := b.parts.closer.Close(); err != nil {
			b.state.closeErr = errBundleClose
		}
	})
	return b.state.closeErr
}

func nilInterface(value any) bool {
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
