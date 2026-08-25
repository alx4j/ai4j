//go:build darwin && arm64

package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/fault"
	"github.com/alx4j/ai4j/internal/host/darwin/filesystem"
	"github.com/alx4j/ai4j/internal/host/darwin/process"
	"github.com/alx4j/ai4j/internal/host/darwin/resource"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

var (
	errBundleConstruction    = fault.MustNew(fault.Internal, mustOperationDetail("darwin_host_construction"), nil)
	errUnsupportedBundleHost = fault.MustNew(
		fault.UnsupportedCapability,
		mustUnsupportedDetail("host", "darwin_arm64", "secure_process_baseline"),
		nil,
	)
	errInvalidBundleConfiguration = fault.MustNew(
		fault.InvalidInput,
		mustInvalidDetail("darwin_host_config", fault.ReasonInvalidFormat),
		nil,
	)
	errBundleAuthorityConflict = fault.MustNew(
		fault.Conflict,
		mustConflictDetail("darwin_host_authority", "conflict"),
		nil,
	)
	errBundleCancelled = fault.MustNew(
		fault.Cancelled,
		mustOperationDetail("darwin_host_construction"),
		context.Canceled,
	)
	errBundleTimedOut = fault.MustNew(
		fault.Timeout,
		mustOperationDetail("darwin_host_construction"),
		context.DeadlineExceeded,
	)
)

type bundleConstructorOperations struct {
	deniedCandidates func() []deniedExecutableCandidate
	inspectDenied    func(context.Context, string) (domain.ExecutableDigest, error)
	newFilesystem    func(context.Context, filesystem.Config) (*filesystem.Filesystem, error)
}

// Config names the one Darwin filesystem authority owned by Bundle. Generic
// formatting never discloses configured paths.
type Config struct{ Filesystem filesystem.Config }

func (Config) String() string   { return "<darwin-host-config:redacted>" }
func (Config) GoString() string { return "<darwin-host-config:redacted>" }
func (c Config) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, c.String())
}
func (c Config) MarshalText() ([]byte, error) { return []byte(c.String()), nil }
func (Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"config": "redacted"})
}

// New constructs one lifetime-owned Darwin host bundle. Filesystem authority
// is created once and shared by executable qualification, disk preflight,
// atomic files, safe cwd, and descriptor-bound process execution.
func New(ctx context.Context, config Config) (_ *Bundle, resultErr error) {
	return newWithConstructorOperations(ctx, config, bundleConstructorOperations{
		deniedCandidates: mvpDeniedExecutableCandidates,
		inspectDenied:    filesystem.InspectDeniedExecutableDigest,
		newFilesystem:    filesystem.New,
	})
}

func newWithConstructorOperations(
	ctx context.Context,
	config Config,
	operations bundleConstructorOperations,
) (_ *Bundle, resultErr error) {
	if ctx == nil {
		return nil, errInvalidBundle
	}
	if operations.deniedCandidates == nil || operations.inspectDenied == nil || operations.newFilesystem == nil {
		return nil, errBundleConstruction
	}
	if err := ctx.Err(); err != nil {
		return nil, stableContextError(err)
	}
	policy := resource.MVPPolicy()
	filesystemContext, cancelFilesystem, err := policy.WithBudget(ctx, resource.FilesystemBudget)
	if err != nil {
		return nil, errBundleConstruction
	}
	defer cancelFilesystem()
	deniedDigests, err := qualifyDeniedExecutables(
		filesystemContext,
		operations.deniedCandidates(),
		operations.inspectDenied,
	)
	if err != nil {
		return nil, err
	}
	if err := filesystemContext.Err(); err != nil {
		return nil, stableContextError(err)
	}
	files, err := operations.newFilesystem(filesystemContext, config.Filesystem)
	if err != nil {
		return nil, stableConstructionError(ctx, err)
	}
	defer func() {
		if resultErr == nil {
			return
		}
		if closeErr := files.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, errBundleClose)
		}
	}()

	preflighter, err := resource.NewPreflighter(files, policy)
	if err != nil {
		return nil, errBundleConstruction
	}
	safeCWD, err := files.RootDirectoryExpectation(ctx, lifecycle.TemporarySourceRoot)
	if err != nil {
		return nil, stableConstructionError(ctx, err)
	}
	runner, err := process.NewMVP(files, safeCWD, deniedDigests)
	if err != nil {
		return nil, errBundleConstruction
	}
	hostInspector, err := newInspector(systemHostOperations{})
	if err != nil {
		return nil, errBundleConstruction
	}
	hostPolicy, err := lifecyclePolicy(policy)
	if err != nil {
		return nil, errBundleConstruction
	}
	bundle, err := newBundle(bundleComponents{
		inspector: hostInspector, environment: hostInspector, resources: files, disk: preflighter,
		files: files, processes: runner, policy: hostPolicy, closer: files,
	})
	if err != nil {
		return nil, errBundleConstruction
	}
	return bundle, nil
}

func lifecyclePolicy(policy resource.Policy) (lifecycle.HostResourcePolicy, error) {
	if !policy.Valid() || policy.Version() != resource.MVPPolicyVersion {
		return lifecycle.HostResourcePolicy{}, errBundleConstruction
	}
	version, err := lifecycle.NewResourcePolicyVersion(string(policy.Version()))
	if err != nil {
		return lifecycle.HostResourcePolicy{}, errBundleConstruction
	}
	git, gitOK := policy.Timeout(resource.GitBudget)
	claude, claudeOK := policy.Timeout(resource.ClaudeBudget)
	if !gitOK || !claudeOK {
		return lifecycle.HostResourcePolicy{}, errBundleConstruction
	}
	result, err := lifecycle.NewHostResourcePolicy(version, git, claude)
	if err != nil || !result.Valid() {
		return lifecycle.HostResourcePolicy{}, errBundleConstruction
	}
	return result, nil
}

func qualifyDeniedExecutables(
	ctx context.Context,
	candidates []deniedExecutableCandidate,
	inspect func(context.Context, string) (domain.ExecutableDigest, error),
) ([]domain.ExecutableDigest, error) {
	if ctx == nil || inspect == nil || len(candidates) == 0 {
		return nil, errBundleConstruction
	}
	unique := make(map[string]domain.ExecutableDigest, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, stableContextError(err)
		}
		digest, inspectErr := inspect(ctx, candidate.path)
		if errors.Is(inspectErr, lifecycle.ErrExecutableNotFound) && !candidate.required {
			continue
		}
		if inspectErr != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, stableContextError(contextErr)
			}
			return nil, errUnsupportedBundleHost
		}
		if !digest.Valid() {
			return nil, errUnsupportedBundleHost
		}
		unique[digest.String()] = digest
	}
	if len(unique) == 0 {
		return nil, errUnsupportedBundleHost
	}
	result := make([]domain.ExecutableDigest, 0, len(unique))
	for _, digest := range unique {
		result = append(result, digest)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].String() < result[right].String() })
	return result, nil
}

func stableConstructionError(ctx context.Context, err error) error {
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return stableContextError(contextErr)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errBundleTimedOut
	}
	if errors.Is(err, context.Canceled) {
		return errBundleCancelled
	}
	if errors.Is(err, fault.ErrUnsupportedCapability) {
		return errUnsupportedBundleHost
	}
	if errors.Is(err, fault.ErrInvalidInput) {
		return errInvalidBundleConfiguration
	}
	if errors.Is(err, fault.ErrConflict) {
		return errBundleAuthorityConflict
	}
	return errBundleConstruction
}

func stableContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errBundleTimedOut
	}
	if errors.Is(err, context.Canceled) {
		return errBundleCancelled
	}
	return errBundleConstruction
}

func mustOperationDetail(operation string) fault.OperationDetail {
	result, err := fault.NewOperationDetail(operation)
	if err != nil {
		panic("invalid Darwin bundle operation detail")
	}
	return result
}

func mustUnsupportedDetail(kind, value, capability string) fault.UnsupportedDetail {
	result, err := fault.NewUnsupportedDetail(kind, value, capability)
	if err != nil {
		panic("invalid Darwin bundle unsupported detail")
	}
	return result
}

func mustInvalidDetail(field string, reason fault.InvalidReason) fault.InvalidDetail {
	result, err := fault.NewInvalidDetail(field, reason)
	if err != nil {
		panic("invalid Darwin bundle invalid-input detail")
	}
	return result
}

func mustConflictDetail(resource, identity string) fault.ConflictDetail {
	result, err := fault.NewConflictDetail(resource, identity)
	if err != nil {
		panic("invalid Darwin bundle conflict detail")
	}
	return result
}
