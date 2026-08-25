//go:build darwin && arm64

// Package filesystem implements descriptor-rooted Darwin filesystem authority.
package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/fault"
	"github.com/alx4j/ai4j/internal/host/darwin/executableprofile"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"golang.org/x/sys/unix"
)

const (
	privateDirectoryMode       fs.FileMode = 0o700
	maximumOwnedFileMode       fs.FileMode = 0o644
	maximumPathBytes                       = 4096
	maximumConfiguredFileBytes int64       = 64 << 20
	maximumExecutableBytes     int64       = 512 << 20
)

// Config names every filesystem authority explicitly. Private role paths are
// single components beneath BaseRoot; ManagedOutputRoot is an existing,
// current-user-owned directory that AI4J may manage.
type Config struct {
	BaseRoot            string
	StatePath           string
	RecoveryPath        string
	TemporarySourcePath string
	StagingPath         string
	ManagedOutputRoot   string
	MaximumFileBytes    int64
}

type rootedDirectory struct {
	directory          *os.File
	absolute           string
	identity           lifecycle.ObjectIdentity
	authorityChain     []lifecycle.ObjectIdentity
	private            bool
	capacityFilesystem uint64
}

// Filesystem owns the open root descriptors until Close is called.
type Filesystem struct {
	roots        map[lifecycle.RootRole]*rootedDirectory
	maximumBytes int64
	currentUID   uint32
	ops          atomicOperations
	capacityOps  capacityOperations
	closeOnce    sync.Once
	closeErr     error
}

var _ lifecycle.AtomicFileWriter = (*Filesystem)(nil)

// New creates and verifies all private role roots while refusing activation
// when the caller's already-bounded construction context expires. Synchronous
// Darwin filesystem observations remain non-preemptible and are bracketed by
// context checks rather than wrapped in a leaking goroutine.
func New(ctx context.Context, config Config) (*Filesystem, error) {
	return newForUIDWithContext(ctx, config, uint32(os.Geteuid()))
}

func newForUID(config Config, uid uint32) (*Filesystem, error) {
	return newForUIDWithContext(context.Background(), config, uid)
}

func newForUIDWithContext(ctx context.Context, config Config, uid uint32) (*Filesystem, error) {
	return newForUIDWithContextAndCapacityOperations(ctx, config, uid, realCapacityOperations{})
}

func newForUIDWithCapacityOperations(config Config, uid uint32, capacityOps capacityOperations) (*Filesystem, error) {
	return newForUIDWithContextAndCapacityOperations(context.Background(), config, uid, capacityOps)
}

func newForUIDWithContextAndCapacityOperations(
	ctx context.Context,
	config Config,
	uid uint32,
	capacityOps capacityOperations,
) (_ *Filesystem, resultErr error) {
	if ctx == nil {
		return nil, invalid("construction_context", fault.ReasonEmpty)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nil, unsupportedHost()
	}
	if capacityOps == nil {
		return nil, invalid("capacity_operations", fault.ReasonEmpty)
	}
	if config.MaximumFileBytes <= 0 || config.MaximumFileBytes > maximumConfiguredFileBytes {
		return nil, invalid("maximum_file_bytes", fault.ReasonOutOfRange)
	}
	basePath, err := validateAbsoluteDirectory(config.BaseRoot)
	if err != nil {
		return nil, fmt.Errorf("validate base root: %w", err)
	}
	managedPath, err := validateAbsoluteDirectory(config.ManagedOutputRoot)
	if err != nil {
		return nil, fmt.Errorf("validate managed output root: %w", err)
	}
	if authoritiesOverlap(basePath, managedPath) {
		return nil, invalid("configured_roots", fault.ReasonOutOfRange)
	}
	base, err := openAbsoluteDirectoryDescriptor(basePath, lifecycle.ObjectIdentity{})
	if err != nil {
		return nil, fmt.Errorf("open base root: %w", err)
	}
	defer func() {
		if resultErr != nil && base != nil {
			_ = base.Close()
		}
	}()
	openedBase, err := inspectOpenFile(base)
	if err != nil {
		return nil, fmt.Errorf("verify opened base root: %w", err)
	}
	if !safeWritableDirectory(openedBase, uid) {
		return nil, conflict("base_root", "unsafe_directory", nil)
	}

	privatePaths := []struct {
		role lifecycle.RootRole
		name string
	}{
		{role: lifecycle.StateRoot, name: config.StatePath},
		{role: lifecycle.RecoveryRoot, name: config.RecoveryPath},
		{role: lifecycle.TemporarySourceRoot, name: config.TemporarySourcePath},
		{role: lifecycle.StagingRoot, name: config.StagingPath},
	}
	roots := make(map[lifecycle.RootRole]*rootedDirectory, len(privatePaths)+1)
	defer func() {
		if resultErr == nil {
			return
		}
		for _, root := range roots {
			_ = root.directory.Close()
		}
	}()
	managed, err := openExistingRoot(managedPath, uid, false)
	if err != nil {
		return nil, fmt.Errorf("open managed output root: %w", err)
	}
	roots[lifecycle.ManagedOutputRoot] = managed
	baseChain, err := absoluteDirectoryIdentityChain(basePath, uid)
	if err != nil {
		return nil, fmt.Errorf("inspect base-root identity chain: %w", err)
	}
	managedChain := managed.authorityChain
	if baseChain[len(baseChain)-1] != openedBase.identity || managedChain[len(managedChain)-1] != managed.identity {
		return nil, conflict("configured_roots", "identity_changed", nil)
	}
	if authorityIdentityChainsOverlap(baseChain, managedChain) {
		return nil, invalid("configured_roots", fault.ReasonOutOfRange)
	}
	baseAuthority := &rootedDirectory{
		directory: base, absolute: basePath, identity: openedBase.identity, authorityChain: baseChain,
	}
	baseCapacityFilesystem, err := qualifyCapacityRoot(ctx, baseAuthority, uid, capacityOps)
	if err != nil {
		return nil, fmt.Errorf("qualify base-root capacity: %w", err)
	}
	managedCapacityFilesystem, err := qualifyCapacityRoot(ctx, managed, uid, capacityOps)
	if err != nil {
		return nil, fmt.Errorf("qualify managed-root capacity: %w", err)
	}
	managed.capacityFilesystem = managedCapacityFilesystem
	seen := make(map[string]struct{}, len(privatePaths))
	for _, candidate := range privatePaths {
		if err := validateSingleComponent(candidate.name); err != nil {
			return nil, fmt.Errorf("validate %s root path: %w", candidate.role, err)
		}
		if _, duplicate := seen[candidate.name]; duplicate {
			return nil, invalid("private_root_path", fault.ReasonInvalidFormat)
		}
		seen[candidate.name] = struct{}{}
	}
	for _, candidate := range privatePaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := preflightPrivateRoot(base, openedBase.identity, candidate.name, uid); err != nil {
			return nil, fmt.Errorf("preflight %s root: %w", candidate.role, err)
		}
	}
	seenIdentities := map[lifecycle.ObjectIdentity]lifecycle.RootRole{managed.identity: lifecycle.ManagedOutputRoot}
	for _, candidate := range privatePaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		root, err := createPrivateRoot(base, openedBase.identity, basePath, candidate.name, uid, nil)
		if err != nil {
			return nil, fmt.Errorf("create %s root: %w", candidate.role, err)
		}
		if previous, duplicate := seenIdentities[root.identity]; duplicate {
			_ = root.directory.Close()
			return nil, conflict("private_root", "duplicates_"+string(previous), nil)
		}
		root.authorityChain, err = absoluteDirectoryIdentityChain(root.absolute, uid)
		if err != nil || len(root.authorityChain) == 0 || root.authorityChain[len(root.authorityChain)-1] != root.identity {
			_ = root.directory.Close()
			return nil, fmt.Errorf("verify %s root authority chain: %w", candidate.role, errors.Join(err, conflict("private_root", "identity_changed", nil)))
		}
		root.capacityFilesystem = baseCapacityFilesystem
		seenIdentities[root.identity] = candidate.role
		roots[candidate.role] = root
	}
	if err := base.Close(); err != nil {
		return nil, fmt.Errorf("close base root: %w", err)
	}
	base = nil

	value := &Filesystem{
		roots: roots, maximumBytes: config.MaximumFileBytes, currentUID: uid, capacityOps: capacityOps,
	}
	value.ops = realAtomicOperations{}
	return value, nil
}

func preflightPrivateRoot(base *os.File, baseIdentity lifecycle.ObjectIdentity, name string, uid uint32) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(base.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	facts := fileFactsFromUnixStat(&stat)
	if stat.Mode&unix.S_IFMT == unix.S_IFLNK || !safePrivateDirectory(facts, uid) || facts.identity.Filesystem != baseIdentity.Filesystem {
		return conflict("private_root", "unsafe_mode_type_owner_or_mount", nil)
	}
	return nil
}

// Close releases all root descriptors. It is safe to call more than once.
func (f *Filesystem) Close() error {
	if f == nil {
		return nil
	}
	f.closeOnce.Do(func() {
		for _, root := range f.roots {
			f.closeErr = errors.Join(f.closeErr, root.directory.Close())
		}
	})
	return f.closeErr
}

// CheckResource performs no-follow parent traversal and derives all facts from
// the object held open during inspection.
func (f *Filesystem) CheckResource(ctx context.Context, request lifecycle.ResourceRequest) (lifecycle.ResourceObservation, error) {
	file, observation, err := f.openResource(ctx, request)
	if file != nil {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close inspected resource: %w", closeErr)
		}
	}
	return observation, err
}

// ReadResource returns bounded bytes from the same opened object used to
// produce Observation; it never reopens by path after validation.
func (f *Filesystem) ReadResource(ctx context.Context, request lifecycle.ResourceReadRequest) (lifecycle.ResourceReadResult, error) {
	if request.MaxBytes <= 0 || request.MaxBytes > f.maximumBytes {
		return lifecycle.ResourceReadResult{}, invalid("max_bytes", fault.ReasonOutOfRange)
	}
	file, observation, err := f.openResource(ctx, request.Resource)
	if err != nil {
		if file != nil {
			_ = file.Close()
		}
		return lifecycle.ResourceReadResult{}, err
	}
	if file == nil || !observation.Exists {
		return lifecycle.ResourceReadResult{}, conflict("resource", "missing", nil)
	}
	defer file.Close()
	if observation.Kind != lifecycle.RegularResource && observation.Kind != lifecycle.ExecutableResource {
		return lifecycle.ResourceReadResult{}, conflict("resource", "not_regular", nil)
	}
	if observation.Size < 0 || observation.Size > request.MaxBytes {
		return lifecycle.ResourceReadResult{}, invalid("resource_size", fault.ReasonOutOfRange)
	}
	content, overLimit, err := readBoundedFile(file, request.MaxBytes)
	if err != nil {
		return lifecycle.ResourceReadResult{}, fmt.Errorf("read rooted resource: %w", err)
	}
	if overLimit {
		return lifecycle.ResourceReadResult{}, invalid("resource_size", fault.ReasonOutOfRange)
	}
	if err := ctx.Err(); err != nil {
		return lifecycle.ResourceReadResult{}, err
	}
	return lifecycle.ResourceReadResult{Observation: observation, Content: content}, nil
}

func readBoundedFile(file *os.File, maximum int64) ([]byte, bool, error) {
	content, err := io.ReadAll(io.LimitReader(file, maximum))
	if err != nil {
		return nil, false, err
	}
	var probe [1]byte
	count, probeErr := file.Read(probe[:])
	if probeErr != nil && !errors.Is(probeErr, io.EOF) {
		return nil, false, probeErr
	}
	return content, count > 0, nil
}

func (f *Filesystem) openResource(ctx context.Context, request lifecycle.ResourceRequest) (*os.File, lifecycle.ResourceObservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, lifecycle.ResourceObservation{}, err
	}
	if !request.Kind.Valid() {
		return nil, lifecycle.ResourceObservation{}, invalid("resource_kind", fault.ReasonUnknownValue)
	}
	root, err := f.root(request.Root)
	if err != nil {
		return nil, lifecycle.ResourceObservation{}, err
	}
	if err := validateRelativePath(request.Path); err != nil {
		return nil, lifecycle.ResourceObservation{}, err
	}
	if err := revalidateRoot(root, f.currentUID); err != nil {
		return nil, lifecycle.ResourceObservation{}, err
	}
	parentIdentity, err := verifyParent(root, request.Path, f.currentUID)
	if err != nil {
		return nil, lifecycle.ResourceObservation{}, err
	}
	parent, err := f.openExpectedParent(request.Root, root, request.Path, lifecycle.FileExpectation{
		RootIdentity: root.identity, ParentIdentity: parentIdentity,
	})
	if err != nil {
		return nil, lifecycle.ResourceObservation{}, err
	}
	defer parent.Close()
	baseName := path.Base(request.Path)
	listed, err := classifyLeafNoFollow(parent.directory, baseName)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
		if verifyErr := f.verifyCanonicalParent(root, request.Path, lifecycle.FileExpectation{
			RootIdentity: root.identity, ParentIdentity: parentIdentity,
		}); verifyErr != nil {
			return nil, lifecycle.ResourceObservation{}, verifyErr
		}
		return nil, lifecycle.ResourceObservation{
			Exists: false, Kind: request.Kind, RootIdentity: root.identity, ParentIdentity: parentIdentity,
		}, nil
	}
	if err != nil {
		return nil, lifecycle.ResourceObservation{}, fmt.Errorf("lstat rooted resource: %w", err)
	}
	if err != nil {
		return nil, lifecycle.ResourceObservation{}, err
	}
	// Reject special files and policy conflicts before any potentially
	// blocking or side-effecting leaf open.
	if !kindMatches(request.Kind, listed) {
		return nil, lifecycle.ResourceObservation{}, conflict("resource", "wrong_type", nil)
	}
	if request.RequireCurrentOwner && !listed.ownedBy(f.currentUID) {
		return nil, lifecycle.ResourceObservation{}, conflict("resource", "wrong_owner", nil)
	}
	if request.RejectMultipleLinks && listed.links != 1 {
		return nil, lifecycle.ResourceObservation{}, conflict("resource", "multiple_links", nil)
	}
	if listed.identity.Filesystem != root.identity.Filesystem {
		return nil, lifecycle.ResourceObservation{}, conflict("resource", "mount_changed", nil)
	}
	file, opened, err := openLeafNoFollow(parent.directory, baseName, request.Kind, nil)
	if err != nil {
		return nil, lifecycle.ResourceObservation{}, fmt.Errorf("open rooted resource: %w", err)
	}
	if listed.identity != opened.identity {
		_ = file.Close()
		return nil, lifecycle.ResourceObservation{}, conflict("resource", "identity_changed", nil)
	}
	if opened.identity.Filesystem != root.identity.Filesystem {
		_ = file.Close()
		return nil, lifecycle.ResourceObservation{}, conflict("resource", "mount_changed", nil)
	}
	if !kindMatches(request.Kind, opened) || request.RequireCurrentOwner && !opened.ownedBy(f.currentUID) {
		_ = file.Close()
		return nil, lifecycle.ResourceObservation{}, conflict("resource", "wrong_owner", nil)
	}
	if request.RejectMultipleLinks && opened.links != 1 {
		_ = file.Close()
		return nil, lifecycle.ResourceObservation{}, conflict("resource", "multiple_links", nil)
	}
	if err := f.verifyCanonicalParent(root, request.Path, lifecycle.FileExpectation{
		RootIdentity: root.identity, ParentIdentity: parentIdentity,
	}); err != nil {
		_ = file.Close()
		return nil, lifecycle.ResourceObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return nil, lifecycle.ResourceObservation{}, err
	}
	return file, opened.observation(root.identity, parentIdentity, f.currentUID), nil
}

// CheckExecutable permits symlink aliases only while resolving a candidate to
// its canonical target. The target is then inspected with descriptor-relative
// no-follow traversal and must satisfy the executable trust policy.
func (f *Filesystem) CheckExecutable(ctx context.Context, request lifecycle.ExecutableRequest) (lifecycle.ExecutableObservation, error) {
	return f.checkExecutable(ctx, request, nil)
}

// InspectDeniedExecutableDigest qualifies one fixed canonical system
// executable for the runner's content-deny policy. Unlike CheckExecutable it
// may observe set-ID launchers such as sudo, but it never returns a launchable
// expectation and requires system ownership plus non-writable trust facts.
// The package function is read-only and does not open or create configured
// filesystem roots, so Bundle can prove its supported-host baseline before
// activating private authorities.
func InspectDeniedExecutableDigest(
	ctx context.Context,
	absolute string,
) (domain.ExecutableDigest, error) {
	return inspectDeniedExecutableDigest(ctx, absolute, uint32(os.Geteuid()), deniedExecutableInspectionHooks{})
}

func (f *Filesystem) InspectDeniedExecutableDigest(
	ctx context.Context,
	absolute string,
) (domain.ExecutableDigest, error) {
	if f == nil {
		return domain.ExecutableDigest{}, invalid("denied_executable", fault.ReasonEmpty)
	}
	return inspectDeniedExecutableDigest(ctx, absolute, f.currentUID, deniedExecutableInspectionHooks{})
}

type deniedExecutableInspectionHooks struct {
	afterAliasClassify func(string)
	afterOpen          func(*os.File)
}

const maximumDeniedExecutableAliasDepth = 16

type deniedExecutableAliasRelation struct {
	linkPath        string
	target          string
	suffix          string
	linkFacts       fileFacts
	parentAuthority []executableAuthorityAncestor
}

type deniedExecutableResolution struct {
	resolved  string
	relations []deniedExecutableAliasRelation
}

type deniedExecutableAliasStep func(context.Context, string) (deniedExecutableAliasRelation, bool, error)

func resolveDeniedExecutable(
	ctx context.Context,
	absolute string,
	afterClassify func(string),
) (deniedExecutableResolution, error) {
	return resolveDeniedExecutableWithStep(ctx, absolute, func(ctx context.Context, absolute string) (deniedExecutableAliasRelation, bool, error) {
		return inspectDeniedAliasStep(ctx, absolute, afterClassify)
	})
}

func resolveDeniedExecutableWithStep(
	ctx context.Context,
	absolute string,
	step deniedExecutableAliasStep,
) (deniedExecutableResolution, error) {
	if ctx == nil {
		return deniedExecutableResolution{}, invalid("context", fault.ReasonEmpty)
	}
	if step == nil {
		return deniedExecutableResolution{}, invalid("denied_executable", fault.ReasonEmpty)
	}
	if absolute == "" || len(absolute) > maximumPathBytes || !filepath.IsAbs(absolute) ||
		filepath.Clean(absolute) != absolute || !utf8.ValidString(absolute) || containsControl(absolute) {
		return deniedExecutableResolution{}, invalid("denied_executable", fault.ReasonInvalidFormat)
	}
	next := absolute
	relations := make([]deniedExecutableAliasRelation, 0, 2)
	seen := make(map[lifecycle.ObjectIdentity]struct{}, maximumDeniedExecutableAliasDepth)
	for depth := 0; depth <= maximumDeniedExecutableAliasDepth; depth++ {
		if err := ctx.Err(); err != nil {
			return deniedExecutableResolution{}, err
		}
		relation, found, err := step(ctx, next)
		if err != nil {
			return deniedExecutableResolution{}, err
		}
		if err := ctx.Err(); err != nil {
			return deniedExecutableResolution{}, err
		}
		if !found {
			return deniedExecutableResolution{resolved: next, relations: relations}, nil
		}
		if depth == maximumDeniedExecutableAliasDepth {
			return deniedExecutableResolution{}, conflict("denied_executable", "unsafe_alias", nil)
		}
		if _, duplicate := seen[relation.linkFacts.identity]; duplicate {
			return deniedExecutableResolution{}, conflict("denied_executable", "unsafe_alias", nil)
		}
		seen[relation.linkFacts.identity] = struct{}{}
		relations = append(relations, relation)
		next, err = nextDeniedAliasPath(relation)
		if err != nil {
			return deniedExecutableResolution{}, err
		}
	}
	return deniedExecutableResolution{}, conflict("denied_executable", "unsafe_alias", nil)
}

func inspectDeniedAliasStep(
	ctx context.Context,
	absolute string,
	afterClassify func(string),
) (deniedExecutableAliasRelation, bool, error) {
	if err := ctx.Err(); err != nil {
		return deniedExecutableAliasRelation{}, false, err
	}
	clean := filepath.Clean(absolute)
	if clean != absolute || !filepath.IsAbs(clean) || len(clean) > maximumPathBytes ||
		!utf8.ValidString(clean) || containsControl(clean) {
		return deniedExecutableAliasRelation{}, false, invalid("denied_executable", fault.ReasonInvalidFormat)
	}
	rootFD, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return deniedExecutableAliasRelation{}, false, err
	}
	currentFD := rootFD
	defer func() {
		if currentFD != rootFD {
			_ = unix.Close(currentFD)
		}
		_ = unix.Close(rootFD)
	}()
	var rootStat unix.Stat_t
	if err := unix.Fstat(rootFD, &rootStat); err != nil {
		return deniedExecutableAliasRelation{}, false, err
	}
	rootFacts := fileFactsFromUnixStat(&rootStat)
	if !executableAuthorityAncestorSafe(rootFacts, 0, lifecycle.SystemOwnedChainAuthority) {
		return deniedExecutableAliasRelation{}, false, conflict("denied_executable", "unsafe_trust", nil)
	}
	components := strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator))
	currentPath := string(filepath.Separator)
	parentAuthority := []executableAuthorityAncestor{executableAncestorFact(rootFacts)}
	for index, component := range components {
		if err := ctx.Err(); err != nil {
			return deniedExecutableAliasRelation{}, false, err
		}
		var listedStat unix.Stat_t
		if err := unix.Fstatat(currentFD, component, &listedStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return deniedExecutableAliasRelation{}, false, err
		}
		listed := fileFactsFromUnixStat(&listedStat)
		componentPath := filepath.Join(currentPath, component)
		if afterClassify != nil {
			afterClassify(componentPath)
		}
		if err := ctx.Err(); err != nil {
			return deniedExecutableAliasRelation{}, false, err
		}
		if listedStat.Mode&unix.S_IFMT == unix.S_IFLNK {
			if listed.uid != 0 || !listed.identity.Valid() {
				return deniedExecutableAliasRelation{}, false, conflict("denied_executable", "unsafe_alias", nil)
			}
			target, err := readlinkatBounded(currentFD, component)
			if err != nil {
				return deniedExecutableAliasRelation{}, false, err
			}
			var recheckedStat unix.Stat_t
			if err := unix.Fstatat(currentFD, component, &recheckedStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return deniedExecutableAliasRelation{}, false, err
			}
			if recheckedStat.Mode&unix.S_IFMT != unix.S_IFLNK || fileFactsFromUnixStat(&recheckedStat) != listed {
				return deniedExecutableAliasRelation{}, false, conflict("denied_executable", "alias_changed", nil)
			}
			suffix := ""
			if index+1 < len(components) {
				suffix = filepath.Join(components[index+1:]...)
			}
			return deniedExecutableAliasRelation{
				linkPath: componentPath, target: target, suffix: suffix, linkFacts: listed,
				parentAuthority: append([]executableAuthorityAncestor(nil), parentAuthority...),
			}, true, nil
		}
		last := index == len(components)-1
		if last {
			return deniedExecutableAliasRelation{}, false, nil
		}
		if !executableAuthorityAncestorSafe(listed, 0, lifecycle.SystemOwnedChainAuthority) {
			return deniedExecutableAliasRelation{}, false, conflict("denied_executable", "unsafe_trust", nil)
		}
		openedFD, err := unix.Openat(currentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return deniedExecutableAliasRelation{}, false, err
		}
		var openedStat unix.Stat_t
		statErr := unix.Fstat(openedFD, &openedStat)
		if currentFD != rootFD {
			_ = unix.Close(currentFD)
		}
		currentFD = openedFD
		if statErr != nil {
			return deniedExecutableAliasRelation{}, false, statErr
		}
		opened := fileFactsFromUnixStat(&openedStat)
		if executableAncestorFact(opened) != executableAncestorFact(listed) ||
			!executableAuthorityAncestorSafe(opened, 0, lifecycle.SystemOwnedChainAuthority) {
			return deniedExecutableAliasRelation{}, false, conflict("denied_executable", "target_changed", nil)
		}
		parentAuthority = append(parentAuthority, executableAncestorFact(opened))
		currentPath = componentPath
	}
	return deniedExecutableAliasRelation{}, false, errors.New("denied alias traversal did not reach a leaf")
}

func readlinkatBounded(parentFD int, name string) (string, error) {
	buffer := make([]byte, maximumPathBytes+1)
	count, err := unix.Readlinkat(parentFD, name, buffer)
	if err != nil {
		return "", err
	}
	if count <= 0 || count == len(buffer) {
		return "", conflict("denied_executable", "unsafe_alias", nil)
	}
	target := string(buffer[:count])
	if len(target) > maximumPathBytes || !utf8.ValidString(target) || containsControl(target) {
		return "", conflict("denied_executable", "unsafe_alias", nil)
	}
	return target, nil
}

func nextDeniedAliasPath(relation deniedExecutableAliasRelation) (string, error) {
	target := relation.target
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(relation.linkPath), target)
	}
	if relation.suffix != "" {
		target = filepath.Join(target, relation.suffix)
	}
	clean := filepath.Clean(target)
	if clean == string(filepath.Separator) || len(clean) > maximumPathBytes || !filepath.IsAbs(clean) ||
		!utf8.ValidString(clean) || containsControl(clean) {
		return "", conflict("denied_executable", "unsafe_alias", nil)
	}
	return clean, nil
}

func sameDeniedExecutableResolution(first, second deniedExecutableResolution) bool {
	if first.resolved != second.resolved || len(first.relations) != len(second.relations) {
		return false
	}
	for index := range first.relations {
		left, right := first.relations[index], second.relations[index]
		if left.linkPath != right.linkPath || left.target != right.target || left.suffix != right.suffix || left.linkFacts != right.linkFacts ||
			!sameExecutableAuthorityChain(left.parentAuthority, right.parentAuthority) {
			return false
		}
	}
	return true
}

func inspectDeniedExecutableDigest(
	ctx context.Context,
	absolute string,
	currentUID uint32,
	hooks deniedExecutableInspectionHooks,
) (domain.ExecutableDigest, error) {
	if ctx == nil || absolute == "" || len(absolute) > maximumPathBytes || !filepath.IsAbs(absolute) ||
		filepath.Clean(absolute) != absolute || !utf8.ValidString(absolute) || containsControl(absolute) {
		return domain.ExecutableDigest{}, invalid("denied_executable", fault.ReasonInvalidFormat)
	}
	if err := ctx.Err(); err != nil {
		return domain.ExecutableDigest{}, err
	}
	resolutionBefore, err := resolveDeniedExecutable(ctx, absolute, hooks.afterAliasClassify)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return domain.ExecutableDigest{}, err
		}
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
			return domain.ExecutableDigest{}, lifecycle.ErrExecutableNotFound
		}
		if errors.Is(err, fault.ErrConflict) || errors.Is(err, fault.ErrInvalidInput) {
			return domain.ExecutableDigest{}, err
		}
		return domain.ExecutableDigest{}, conflict("denied_executable", "unsafe_alias", nil)
	}
	listed, err := inspectAbsoluteDeniedExecutable(resolutionBefore.resolved, currentUID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
			return domain.ExecutableDigest{}, lifecycle.ErrExecutableNotFound
		}
		return domain.ExecutableDigest{}, conflict("denied_executable", "inspection_failed", nil)
	}
	if err := ctx.Err(); err != nil {
		return domain.ExecutableDigest{}, err
	}
	if !trustedDeniedExecutableFacts(listed.facts, currentUID) || listed.facts.size < 0 || listed.facts.size > maximumExecutableBytes {
		return domain.ExecutableDigest{}, conflict("denied_executable", "unsafe_trust", nil)
	}
	opened, err := openAbsoluteDeniedExecutable(resolutionBefore.resolved, currentUID)
	if err != nil {
		return domain.ExecutableDigest{}, conflict("denied_executable", "open_failed", nil)
	}
	file := opened.file
	if hooks.afterOpen != nil {
		hooks.afterOpen(file)
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return domain.ExecutableDigest{}, err
	}
	if !sameExecutableAuthorityChain(listed.authorityChain, opened.authorityChain) ||
		!sameExecutableFacts(listed.facts, opened.facts) || !trustedDeniedExecutableFacts(opened.facts, currentUID) {
		_ = file.Close()
		return domain.ExecutableDigest{}, conflict("denied_executable", "target_changed", nil)
	}
	proofBefore, beforeErr := (executableprofile.Prover{}).Prove(ctx, file, opened.facts.size, maximumExecutableBytes)
	if beforeErr != nil {
		_ = file.Close()
		if err := ctx.Err(); err != nil {
			return domain.ExecutableDigest{}, err
		}
		return domain.ExecutableDigest{}, conflict("denied_executable", "proof_failed", nil)
	}
	proofAfter, afterErr := (executableprofile.Prover{}).Prove(ctx, file, opened.facts.size, maximumExecutableBytes)
	post, postErr := inspectOpenFile(file)
	if afterErr != nil || postErr != nil {
		_ = file.Close()
		if err := ctx.Err(); err != nil {
			return domain.ExecutableDigest{}, err
		}
		return domain.ExecutableDigest{}, conflict("denied_executable", "proof_failed", nil)
	}
	if err := ctx.Err(); err != nil {
		closeErr := file.Close()
		return domain.ExecutableDigest{}, errors.Join(err, closeErr)
	}
	resolutionAfter, resolutionErr := resolveDeniedExecutable(ctx, absolute, hooks.afterAliasClassify)
	if resolutionErr != nil {
		_ = file.Close()
		if errors.Is(resolutionErr, context.Canceled) || errors.Is(resolutionErr, context.DeadlineExceeded) {
			return domain.ExecutableDigest{}, resolutionErr
		}
		return domain.ExecutableDigest{}, conflict("denied_executable", "target_changed", nil)
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return domain.ExecutableDigest{}, err
	}
	postPath, postPathErr := inspectAbsoluteDeniedExecutable(resolutionBefore.resolved, currentUID)
	closeErr := file.Close()
	if err := ctx.Err(); err != nil {
		return domain.ExecutableDigest{}, err
	}
	if postPathErr != nil || closeErr != nil {
		return domain.ExecutableDigest{}, conflict("denied_executable", "target_changed", nil)
	}
	if proofBefore != proofAfter || !sameDeniedExecutableResolution(resolutionBefore, resolutionAfter) ||
		!sameExecutableFacts(opened.facts, post) || !sameExecutableFacts(opened.facts, postPath.facts) ||
		!sameExecutableAuthorityChain(opened.authorityChain, postPath.authorityChain) ||
		!trustedDeniedExecutableFacts(post, currentUID) || !trustedDeniedExecutableFacts(postPath.facts, currentUID) {
		return domain.ExecutableDigest{}, conflict("denied_executable", "target_changed", nil)
	}
	if err := ctx.Err(); err != nil {
		return domain.ExecutableDigest{}, err
	}
	return proofAfter.Digest, nil
}

func trustedDeniedExecutableFacts(facts fileFacts, currentUID uint32) bool {
	return facts.kind == lifecycle.RegularResource && executableByCurrentUser(facts, currentUID) &&
		classifyOwner(facts.uid, currentUID) == lifecycle.SystemOwner && !facts.writableByUntrusted() &&
		facts.mode&fs.ModeSticky == 0
}

func (f *Filesystem) checkExecutable(ctx context.Context, request lifecycle.ExecutableRequest, afterInspection func()) (lifecycle.ExecutableObservation, error) {
	return f.checkExecutableWithHooks(ctx, request, executableInspectionHooks{afterInspection: afterInspection})
}

type executableInspectionHooks struct {
	beforeCanonicalOpen func()
	afterFirstProfile   func()
	afterFirstDigest    func()
	afterSecondProfile  func()
	afterInspection     func()
}

func (f *Filesystem) checkExecutableWithHooks(ctx context.Context, request lifecycle.ExecutableRequest, hooks executableInspectionHooks) (lifecycle.ExecutableObservation, error) {
	if ctx == nil {
		return lifecycle.ExecutableObservation{}, invalid("context", fault.ReasonEmpty)
	}
	if f == nil {
		return lifecycle.ExecutableObservation{}, invalid("filesystem", fault.ReasonEmpty)
	}
	if err := ctx.Err(); err != nil {
		return lifecycle.ExecutableObservation{}, err
	}
	if !request.Valid() {
		return lifecycle.ExecutableObservation{}, invalid("executable", fault.ReasonInvalidFormat)
	}
	alias, err := exec.LookPath(request.Candidate)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return lifecycle.ExecutableObservation{}, lifecycle.ErrExecutableNotFound
		}
		return lifecycle.ExecutableObservation{}, conflict("executable", "lookup_failed", nil)
	}
	alias, err = filepath.Abs(alias)
	if err != nil {
		return lifecycle.ExecutableObservation{}, invalid("executable", fault.ReasonInvalidFormat)
	}
	alias = filepath.Clean(alias)
	resolved, err := resolveExecutableAlias(alias)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
			return lifecycle.ExecutableObservation{}, lifecycle.ErrExecutableNotFound
		}
		return lifecycle.ExecutableObservation{}, conflict("executable", "unsafe_alias", nil)
	}
	// The strict system-owned authority intentionally rejects aliases in this
	// checkpoint. Qualifying only their canonical target would let a
	// user-controlled namespace (for example /tmp/x -> /usr/bin/tool) claim
	// system authority. trusted/current-user authority retains legacy aliases.
	if request.Authority == lifecycle.SystemOwnedChainAuthority && resolved != alias {
		return lifecycle.ExecutableObservation{}, conflict("executable", "unsafe_alias", nil)
	}
	initial, err := inspectAbsoluteExecutable(resolved, f.currentUID, request.Authority, nil)
	if err != nil {
		return lifecycle.ExecutableObservation{}, err
	}
	info := initial.facts
	if info.kind != lifecycle.RegularResource || !executableByCurrentUser(info, f.currentUID) {
		return lifecycle.ExecutableObservation{}, conflict("executable", "not_executable", nil)
	}
	owner, trusted := trustedExecutableFacts(info, f.currentUID, request.Authority)
	if !trusted {
		return lifecycle.ExecutableObservation{}, conflict("executable", "unsafe_trust", nil)
	}
	if hooks.beforeCanonicalOpen != nil {
		hooks.beforeCanonicalOpen()
	}
	openedWalk, err := openAbsoluteExecutable(resolved, f.currentUID, request.Authority)
	if err != nil {
		return lifecycle.ExecutableObservation{}, fmt.Errorf("open canonical executable: %w", err)
	}
	file, opened := openedWalk.file, openedWalk.facts
	if !sameExecutableAuthorityChain(initial.authorityChain, openedWalk.authorityChain) || !sameExecutableFacts(info, opened) {
		_ = file.Close()
		return lifecycle.ExecutableObservation{}, conflict("executable", "target_changed", nil)
	}
	if opened.size < 0 || opened.size > maximumExecutableBytes {
		_ = file.Close()
		return lifecycle.ExecutableObservation{}, invalid("executable_size", fault.ReasonOutOfRange)
	}
	proofBefore, proofBeforeErr := (executableprofile.Prover{BeforeContentPass: hooks.afterFirstProfile}).Prove(ctx, file, opened.size, maximumExecutableBytes)
	if proofBeforeErr != nil {
		closeErr := file.Close()
		if errors.Is(proofBeforeErr, executableprofile.ErrUnstableEvidence) {
			return lifecycle.ExecutableObservation{}, conflict("executable", "target_changed", errors.Join(proofBeforeErr, closeErr))
		}
		return lifecycle.ExecutableObservation{}, fmt.Errorf("prove executable content: %w", errors.Join(proofBeforeErr, closeErr))
	}
	if hooks.afterFirstDigest != nil {
		hooks.afterFirstDigest()
	}
	proofAfter, proofAfterErr := (executableprofile.Prover{BeforeContentPass: hooks.afterSecondProfile}).Prove(ctx, file, opened.size, maximumExecutableBytes)
	postHash, postHashErr := inspectOpenFile(file)
	if hooks.afterInspection != nil {
		hooks.afterInspection()
	}
	if err := ctx.Err(); err != nil {
		closeErr := file.Close()
		return lifecycle.ExecutableObservation{}, errors.Join(err, closeErr)
	}
	// Re-resolve the selection alias before the final full authority walk. No
	// mutation hook or path-dependent operation follows that walk; as with any
	// pathname proof, namespace mutation after its final syscall is outside the
	// returned observation and is closed again by OpenExecutable before launch.
	revalidated, aliasErr := resolveExecutableAlias(alias)
	if aliasErr != nil || revalidated != resolved || request.Authority == lifecycle.SystemOwnedChainAuthority && revalidated != alias {
		closeErr := file.Close()
		return lifecycle.ExecutableObservation{}, conflict("executable", "alias_changed", errors.Join(aliasErr, closeErr))
	}
	postPath, postPathErr := inspectAbsoluteExecutable(resolved, f.currentUID, request.Authority, nil)
	closeErr := file.Close()
	if errors.Is(proofAfterErr, executableprofile.ErrUnstableEvidence) {
		return lifecycle.ExecutableObservation{}, conflict("executable", "target_changed", errors.Join(proofAfterErr, postHashErr, postPathErr, closeErr))
	}
	if postPathErr != nil {
		return lifecycle.ExecutableObservation{}, conflict("executable", "target_changed", errors.Join(
			proofAfterErr, postHashErr, postPathErr, closeErr,
		))
	}
	if proofAfterErr != nil || postHashErr != nil || closeErr != nil {
		return lifecycle.ExecutableObservation{}, fmt.Errorf("prove executable content: %w", errors.Join(
			proofAfterErr, postHashErr, postPathErr, closeErr,
		))
	}
	openedOwner, openedTrusted := trustedExecutableFacts(opened, f.currentUID, request.Authority)
	postOwner, postTrusted := trustedExecutableFacts(postHash, f.currentUID, request.Authority)
	if !sameExecutableFacts(info, opened) || !sameExecutableFacts(opened, postHash) || !openedTrusted || !postTrusted || openedOwner != owner || postOwner != owner ||
		!sameExecutableFacts(opened, postPath.facts) || !sameExecutableAuthorityChain(openedWalk.authorityChain, postPath.authorityChain) || proofBefore != proofAfter {
		return lifecycle.ExecutableObservation{}, conflict("executable", "target_changed", nil)
	}
	if err := ctx.Err(); err != nil {
		return lifecycle.ExecutableObservation{}, err
	}
	observation := opened.observation(openedWalk.rootIdentity, openedWalk.parentIdentity, f.currentUID)
	observation.Kind = lifecycle.ExecutableResource
	observation.ExecutableDigest = proofAfter.Digest
	result := lifecycle.ExecutableObservation{
		ResolvedPath: resolved, Authority: request.Authority, Resource: observation, Profile: proofAfter.Profile,
	}
	if !result.Valid() {
		return lifecycle.ExecutableObservation{}, fmt.Errorf("invalid executable observation")
	}
	return result, nil
}

func sameExecutableFacts(left, right fileFacts) bool { return left == right }

type executableAuthorityAncestor struct {
	kind     lifecycle.ResourceKind
	mode     fs.FileMode
	uid      uint32
	identity lifecycle.ObjectIdentity
}

func executableAncestorFact(facts fileFacts) executableAuthorityAncestor {
	return executableAuthorityAncestor{kind: facts.kind, mode: facts.mode, uid: facts.uid, identity: facts.identity}
}

func sameExecutableAuthorityChain(first, second []executableAuthorityAncestor) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func trustedExecutableFacts(
	facts fileFacts,
	currentUID uint32,
	authority lifecycle.ExecutableAuthorityClass,
) (lifecycle.OwnerClass, bool) {
	owner := classifyOwner(facts.uid, currentUID)
	trusted := facts.kind == lifecycle.RegularResource && executableByCurrentUser(facts, currentUID) &&
		owner.TrustedExecutableOwner() && !facts.privilegeBearing() && !facts.writableByUntrusted()
	switch authority {
	case lifecycle.TrustedUserOrSystemAuthority:
	case lifecycle.CurrentUserAuthority:
		trusted = trusted && owner == lifecycle.CurrentUserOwner
	case lifecycle.SystemOwnedChainAuthority:
		trusted = trusted && owner == lifecycle.SystemOwner
	default:
		trusted = false
	}
	return owner, trusted
}

func executableByCurrentUser(facts fileFacts, currentUID uint32) bool {
	if facts.uid == currentUID {
		return facts.mode.Perm()&0o100 != 0
	}
	// Group membership is intentionally not part of executable identity. A
	// non-owned executable must therefore grant execute permission to others or
	// it is rejected rather than inferred executable through ambient groups.
	return facts.mode.Perm()&0o001 != 0
}

func resolveExecutableAlias(alias string) (string, error) {
	if alias == "" || len(alias) > maximumPathBytes || !utf8.ValidString(alias) || containsControl(alias) || !filepath.IsAbs(alias) {
		return "", invalid("executable_alias", fault.ReasonInvalidFormat)
	}
	resolved, err := filepath.EvalSymlinks(alias)
	if err != nil {
		return "", err
	}
	resolved = filepath.Clean(resolved)
	if len(resolved) > maximumPathBytes || !utf8.ValidString(resolved) || containsControl(resolved) || !filepath.IsAbs(resolved) {
		return "", invalid("executable_target", fault.ReasonInvalidFormat)
	}
	return resolved, nil
}

// OpenDirectory revalidates a previously observed rooted directory and returns
// an owned descriptor. The caller must close it. The descriptor name carries
// the qualified absolute path used for Darwin's direct process launch.
func (f *Filesystem) OpenDirectory(ctx context.Context, expectation lifecycle.DirectoryExpectation) (*os.File, error) {
	if ctx == nil || !expectation.Valid() {
		return nil, invalid("working_directory", fault.ReasonInvalidFormat)
	}
	if expectation.Path == "." {
		return f.openRootDirectory(ctx, expectation)
	}
	file, observation, err := f.openResource(ctx, lifecycle.ResourceRequest{
		Root: expectation.Root, Path: expectation.Path, Kind: lifecycle.DirectoryResource,
		RequireCurrentOwner: true,
	})
	if err != nil {
		return nil, err
	}
	if !observation.Exists || observation.RootIdentity != expectation.RootIdentity ||
		observation.ParentIdentity != expectation.ParentIdentity || observation.Identity != expectation.Identity {
		_ = file.Close()
		return nil, conflict("working_directory", "identity_changed", nil)
	}
	return file, nil
}

// RootDirectoryExpectation returns an immutable proof for one configured
// private root. It exposes identities only; no path or descriptor authority is
// transferred to the caller.
func (f *Filesystem) RootDirectoryExpectation(
	ctx context.Context,
	role lifecycle.RootRole,
) (lifecycle.DirectoryExpectation, error) {
	if ctx == nil {
		return lifecycle.DirectoryExpectation{}, invalid("context", fault.ReasonEmpty)
	}
	if err := ctx.Err(); err != nil {
		return lifecycle.DirectoryExpectation{}, err
	}
	root, err := f.root(role)
	if err != nil {
		return lifecycle.DirectoryExpectation{}, err
	}
	if !root.private || len(root.authorityChain) < 2 {
		return lifecycle.DirectoryExpectation{}, invalid("working_directory", fault.ReasonOutOfRange)
	}
	if err := revalidateRoot(root, f.currentUID); err != nil {
		return lifecycle.DirectoryExpectation{}, err
	}
	expectation := lifecycle.DirectoryExpectation{
		Root:           role,
		Path:           ".",
		RootIdentity:   root.identity,
		ParentIdentity: root.authorityChain[len(root.authorityChain)-2],
		Identity:       root.identity,
	}
	if err := ctx.Err(); err != nil {
		return lifecycle.DirectoryExpectation{}, err
	}
	if !expectation.Valid() {
		return lifecycle.DirectoryExpectation{}, errors.New("invalid private root expectation")
	}
	return expectation, nil
}

func (f *Filesystem) openRootDirectory(
	ctx context.Context,
	expectation lifecycle.DirectoryExpectation,
) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := f.root(expectation.Root)
	if err != nil {
		return nil, err
	}
	if !root.private || len(root.authorityChain) < 2 || expectation.RootIdentity != root.identity ||
		expectation.Identity != root.identity || expectation.ParentIdentity != root.authorityChain[len(root.authorityChain)-2] {
		return nil, conflict("working_directory", "identity_changed", nil)
	}
	if err := revalidateRoot(root, f.currentUID); err != nil {
		return nil, err
	}
	duplicate, err := unix.FcntlInt(root.directory.Fd(), unix.F_DUPFD_CLOEXEC, minimumAuthorityDescriptor)
	if err != nil {
		return nil, fmt.Errorf("duplicate private root descriptor: %w", err)
	}
	file := os.NewFile(uintptr(duplicate), root.absolute)
	if file == nil {
		_ = unix.Close(duplicate)
		return nil, errors.New("create private root descriptor")
	}
	opened, err := inspectOpenFile(file)
	if err != nil || opened.identity != root.identity || !safePrivateDirectory(opened, f.currentUID) {
		_ = file.Close()
		return nil, conflict("working_directory", "identity_changed", nil)
	}
	if err := revalidateRoot(root, f.currentUID); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// OpenExecutable reopens a canonical qualified executable with descriptor-
// relative no-follow traversal and returns the still-open CLOEXEC descriptor.
// Content proof is deliberately repeated by the process runner on this exact
// descriptor immediately before descriptor-bound execution.
func (f *Filesystem) OpenExecutable(ctx context.Context, absolute string, expectation lifecycle.ExecutableExpectation) (*os.File, error) {
	return f.openExecutableWithHooks(ctx, absolute, expectation, openExecutableHooks{})
}

type openExecutableHooks struct {
	afterOpen      func(*os.File)
	beforePostWalk func()
}

func (f *Filesystem) openExecutableWithHooks(
	ctx context.Context,
	absolute string,
	expectation lifecycle.ExecutableExpectation,
	hooks openExecutableHooks,
) (*os.File, error) {
	if ctx == nil {
		return nil, invalid("context", fault.ReasonEmpty)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f == nil || !expectation.Valid() || absolute == "" || len(absolute) > maximumPathBytes || !filepath.IsAbs(absolute) ||
		filepath.Clean(absolute) != absolute || !utf8.ValidString(absolute) || containsControl(absolute) {
		return nil, invalid("executable", fault.ReasonInvalidFormat)
	}
	listed, err := inspectAbsoluteExecutable(absolute, f.currentUID, expectation.Authority, nil)
	if err != nil {
		return nil, err
	}
	if !matchesExecutableExpectation(listed.facts, expectation, f.currentUID) || listed.facts.size < 0 || listed.facts.size > maximumExecutableBytes {
		return nil, conflict("executable", "expectation_changed", nil)
	}
	opened, err := openAbsoluteExecutable(absolute, f.currentUID, expectation.Authority)
	if err != nil {
		return nil, fmt.Errorf("open qualified executable: %w", err)
	}
	file := opened.file
	if !sameExecutableAuthorityChain(listed.authorityChain, opened.authorityChain) || !sameExecutableFacts(listed.facts, opened.facts) ||
		!matchesExecutableExpectation(opened.facts, expectation, f.currentUID) {
		_ = opened.file.Close()
		return nil, conflict("executable", "target_changed", nil)
	}
	if hooks.afterOpen != nil {
		hooks.afterOpen(file)
	}
	if hooks.beforePostWalk != nil {
		hooks.beforePostWalk()
	}
	rechecked, err := inspectAbsoluteExecutable(absolute, f.currentUID, expectation.Authority, nil)
	if err != nil || !sameExecutableAuthorityChain(opened.authorityChain, rechecked.authorityChain) ||
		!sameExecutableFacts(opened.facts, rechecked.facts) || !matchesExecutableExpectation(rechecked.facts, expectation, f.currentUID) {
		_ = file.Close()
		return nil, conflict("executable", "target_changed", err)
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func matchesExecutableExpectation(facts fileFacts, expectation lifecycle.ExecutableExpectation, currentUID uint32) bool {
	owner, trusted := trustedExecutableFacts(facts, currentUID, expectation.Authority)
	return expectation.Valid() && trusted && facts.identity == expectation.Identity && owner == expectation.OwnerClass && facts.mode == expectation.Mode &&
		facts.privilegeBearing() == expectation.PrivilegeBearing && facts.writableByUntrusted() == expectation.WritableByUntrusted
}

func (f *Filesystem) root(role lifecycle.RootRole) (*rootedDirectory, error) {
	if f == nil || !role.Valid() {
		return nil, invalid("root_role", fault.ReasonUnknownValue)
	}
	root, ok := f.roots[role]
	if !ok || root == nil {
		return nil, invalid("root_role", fault.ReasonUnknownValue)
	}
	return root, nil
}

func createPrivateRoot(base *os.File, baseIdentity lifecycle.ObjectIdentity, basePath, name string, uid uint32, afterClassify func()) (*rootedDirectory, error) {
	err := unix.Mkdirat(int(base.Fd()), name, uint32(privateDirectoryMode.Perm()))
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return nil, fmt.Errorf("mkdir private root: %w", err)
	}
	var listedStat unix.Stat_t
	if err := unix.Fstatat(int(base.Fd()), name, &listedStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, fmt.Errorf("classify private root: %w", err)
	}
	listed := fileFactsFromUnixStat(&listedStat)
	if listedStat.Mode&unix.S_IFMT == unix.S_IFLNK || listed.kind != lifecycle.DirectoryResource || listed.mode.Perm() != privateDirectoryMode {
		return nil, conflict("private_root", "unsafe_mode_or_type", nil)
	}
	if !listed.ownedBy(uid) || listed.identity.Filesystem != baseIdentity.Filesystem {
		return nil, conflict("private_root", "wrong_owner", nil)
	}
	if afterClassify != nil {
		afterClassify()
	}
	openedFD, err := unix.Openat(int(base.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open private root: %w", err)
	}
	opened := os.NewFile(uintptr(openedFD), filepath.Join(basePath, name))
	openedFacts, err := inspectOpenFile(opened)
	if err != nil {
		_ = opened.Close()
		return nil, err
	}
	if openedFacts.identity != listed.identity || !safePrivateDirectory(openedFacts, uid) ||
		openedFacts.identity.Filesystem != baseIdentity.Filesystem {
		_ = opened.Close()
		return nil, conflict("private_root", "identity_changed", nil)
	}
	if err := base.Sync(); err != nil {
		_ = opened.Close()
		return nil, fmt.Errorf("sync private-root parent: %w", err)
	}
	return &rootedDirectory{
		directory: opened, absolute: filepath.Join(basePath, name), identity: openedFacts.identity, private: true,
	}, nil
}

func openExistingRoot(absolute string, uid uint32, private bool) (*rootedDirectory, error) {
	directory, err := openAbsoluteDirectoryDescriptor(absolute, lifecycle.ObjectIdentity{})
	if err != nil {
		return nil, err
	}
	opened, err := inspectOpenFile(directory)
	if err != nil {
		_ = directory.Close()
		return nil, err
	}
	if !safeWritableDirectory(opened, uid) {
		_ = directory.Close()
		return nil, conflict("configured_root", "unsafe_directory", nil)
	}
	chain, err := absoluteDirectoryIdentityChain(absolute, uid)
	if err != nil || len(chain) == 0 || chain[len(chain)-1] != opened.identity {
		_ = directory.Close()
		return nil, errors.Join(err, conflict("configured_root", "unsafe_or_changed_ancestor", nil))
	}
	return &rootedDirectory{directory: directory, absolute: absolute, identity: opened.identity, authorityChain: chain, private: private}, nil
}

type fileFacts struct {
	kind     lifecycle.ResourceKind
	mode     fs.FileMode
	size     int64
	uid      uint32
	links    uint64
	identity lifecycle.ObjectIdentity
}

func (f fileFacts) ownedBy(uid uint32) bool { return f.uid == uid }

func (f fileFacts) observation(root, parent lifecycle.ObjectIdentity, currentUID uint32) lifecycle.ResourceObservation {
	owner := classifyOwner(f.uid, currentUID)
	return lifecycle.ResourceObservation{
		Exists: true, Kind: f.kind, OwnedByCurrentUser: owner == lifecycle.CurrentUserOwner,
		OwnerClass: owner, PrivilegeBearing: f.privilegeBearing(),
		WritableByUntrusted: f.writableByUntrusted(), Mode: f.mode, Size: f.size, LinkCount: f.links,
		RootIdentity: root, ParentIdentity: parent, Identity: f.identity,
	}
}

func classifyOwner(uid, currentUID uint32) lifecycle.OwnerClass {
	if uid == 0 {
		return lifecycle.SystemOwner
	}
	if uid == currentUID {
		return lifecycle.CurrentUserOwner
	}
	return lifecycle.OtherOwner
}

func (f fileFacts) privilegeBearing() bool {
	return f.mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0
}

func (f fileFacts) writableByUntrusted() bool { return f.mode.Perm()&0o022 != 0 }

func safePrivateDirectory(facts fileFacts, uid uint32) bool {
	return facts.kind == lifecycle.DirectoryResource && facts.ownedBy(uid) && facts.mode == privateDirectoryMode
}

func safeWritableDirectory(facts fileFacts, uid uint32) bool {
	return facts.kind == lifecycle.DirectoryResource && facts.ownedBy(uid) && facts.mode.Perm()&0o700 == 0o700 &&
		!facts.writableByUntrusted() && !facts.privilegeBearing()
}

func safeAuthorityAncestor(facts fileFacts, uid uint32) bool {
	if facts.kind != lifecycle.DirectoryResource || facts.uid != 0 && facts.uid != uid {
		return false
	}
	special := facts.mode & (fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
	if !facts.writableByUntrusted() {
		return special == 0
	}
	// A root-owned sticky world-writable directory (for example /tmp) does
	// not let another UID rename or remove an entry owned by AI4J's user.
	return facts.uid == 0 && facts.mode.Perm()&0o002 != 0 && special == fs.ModeSticky
}

func inspectOpenFile(file *os.File) (fileFacts, error) {
	info, err := file.Stat()
	if err != nil {
		return fileFacts{}, err
	}
	return fileFactsFromInfo(info)
}

func fileFactsFromInfo(info fs.FileInfo) (fileFacts, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return fileFacts{}, errors.New("darwin file identity unavailable")
	}
	kind := lifecycle.RegularResource
	if info.IsDir() {
		kind = lifecycle.DirectoryResource
	} else if !info.Mode().IsRegular() {
		kind = "special"
	}
	mode := info.Mode().Perm() | info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)
	return fileFacts{
		kind: kind, mode: mode, size: info.Size(), uid: stat.Uid,
		links: uint64(stat.Nlink), identity: lifecycle.ObjectIdentity{Filesystem: uint64(stat.Dev), Object: stat.Ino},
	}, nil
}

func verifyParent(root *rootedDirectory, name string, uid uint32) (lifecycle.ObjectIdentity, error) {
	directory, identity, err := openParentNoFollow(root, name, uid, nil)
	if directory != nil {
		_ = directory.Close()
	}
	return identity, err
}

func kindMatches(want lifecycle.ResourceKind, got fileFacts) bool {
	switch want {
	case lifecycle.RegularResource:
		return got.kind == lifecycle.RegularResource
	case lifecycle.DirectoryResource:
		return got.kind == lifecycle.DirectoryResource
	case lifecycle.ExecutableResource:
		return got.kind == lifecycle.RegularResource && got.mode.Perm()&0o111 != 0
	default:
		return false
	}
}

func validateAbsoluteDirectory(value string) (string, error) {
	if value == "" || len(value) > maximumPathBytes || containsControl(value) || !filepath.IsAbs(value) {
		return "", invalid("root_path", fault.ReasonInvalidFormat)
	}
	clean := filepath.Clean(value)
	if clean == string(filepath.Separator) {
		return "", invalid("root_path", fault.ReasonInvalidFormat)
	}
	return clean, nil
}

func validateSingleComponent(value string) error {
	if value == "" || len(value) > 64 {
		return invalid("root_path", fault.ReasonInvalidFormat)
	}
	for index, character := range []byte(value) {
		alpha := character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		if !alpha && !digit && character != '-' && character != '_' || index == 0 && !alpha && !digit {
			return invalid("root_path", fault.ReasonInvalidFormat)
		}
	}
	return nil
}

func validateRelativePath(value string) error {
	if value == "" || len(value) > maximumPathBytes || !utf8.ValidString(value) || containsControl(value) ||
		filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return invalid("path", fault.ReasonInvalidFormat)
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return invalid("path", fault.ReasonInvalidFormat)
		}
	}
	return nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func revalidateRoot(root *rootedDirectory, uid uint32) error {
	chain, err := absoluteDirectoryIdentityChain(root.absolute, uid)
	if err != nil {
		return conflict("configured_root", "authority_detached", nil)
	}
	if !sameIdentityChain(chain, root.authorityChain) {
		return conflict("configured_root", "ancestor_identity_changed", nil)
	}
	listed, err := inspectAbsoluteDirectory(root.absolute, uid)
	if err != nil {
		return fmt.Errorf("revalidate configured root: %w", err)
	}
	if listed.identity != root.identity {
		return conflict("configured_root", "identity_changed", nil)
	}
	opened, err := inspectOpenFile(root.directory)
	if err != nil {
		return fmt.Errorf("revalidate opened root: %w", err)
	}
	if opened.identity != root.identity {
		return conflict("configured_root", "descriptor_changed", nil)
	}
	if (root.private && !safePrivateDirectory(opened, uid)) || (!root.private && !safeWritableDirectory(opened, uid)) {
		return conflict("configured_root", "unsafe_mode_or_owner", nil)
	}
	return nil
}

func sameIdentityChain(first, second []lifecycle.ObjectIdentity) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func authoritiesOverlap(first, second string) bool {
	canonicalFirst, firstErr := filepath.EvalSymlinks(first)
	canonicalSecond, secondErr := filepath.EvalSymlinks(second)
	if firstErr == nil {
		first = canonicalFirst
	}
	if secondErr == nil {
		second = canonicalSecond
	}
	contains := func(parent, child string) bool {
		relative, err := filepath.Rel(parent, child)
		return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
	}
	return contains(first, second) || contains(second, first)
}

func authorityIdentityChainsOverlap(first, second []lifecycle.ObjectIdentity) bool {
	if len(first) == 0 || len(second) == 0 {
		return true
	}
	firstTarget := first[len(first)-1]
	secondTarget := second[len(second)-1]
	contains := func(chain []lifecycle.ObjectIdentity, target lifecycle.ObjectIdentity) bool {
		for _, identity := range chain {
			if identity == target {
				return true
			}
		}
		return false
	}
	return contains(first, secondTarget) || contains(second, firstTarget)
}

func inspectAbsoluteDirectory(absolute string, uid uint32) (fileFacts, error) {
	info, _, _, err := inspectAbsoluteFile(absolute, uid)
	if err != nil {
		return fileFacts{}, err
	}
	if info.kind != lifecycle.DirectoryResource || !info.ownedBy(uid) {
		return fileFacts{}, conflict("configured_root", "unsafe_directory", nil)
	}
	return info, nil
}

func inspectAbsoluteFile(absolute string, uid uint32) (fileFacts, lifecycle.ObjectIdentity, lifecycle.ObjectIdentity, error) {
	return inspectAbsoluteFileWithHook(absolute, uid, nil)
}

// inspectAbsoluteFileWithHook exists so Darwin hostile-path tests can force a
// substitution at the exact classification/open boundary. Production always
// passes nil.
func inspectAbsoluteFileWithHook(absolute string, uid uint32, afterClassify func(string)) (fileFacts, lifecycle.ObjectIdentity, lifecycle.ObjectIdentity, error) {
	result, err := walkAbsoluteFile(absolute, uid, lifecycle.ExecutableAuthorityClass(""), false, false, afterClassify)
	return result.facts, result.rootIdentity, result.parentIdentity, err
}

type absoluteFileWalk struct {
	file           *os.File
	facts          fileFacts
	rootIdentity   lifecycle.ObjectIdentity
	parentIdentity lifecycle.ObjectIdentity
	authorityChain []executableAuthorityAncestor
}

func inspectAbsoluteExecutable(
	absolute string,
	uid uint32,
	authority lifecycle.ExecutableAuthorityClass,
	afterClassify func(string),
) (absoluteFileWalk, error) {
	if !authority.Valid() {
		return absoluteFileWalk{}, invalid("executable_authority", fault.ReasonUnknownValue)
	}
	return walkAbsoluteFile(absolute, uid, authority, false, false, afterClassify)
}

func openAbsoluteExecutable(
	absolute string,
	uid uint32,
	authority lifecycle.ExecutableAuthorityClass,
) (absoluteFileWalk, error) {
	if !authority.Valid() {
		return absoluteFileWalk{}, invalid("executable_authority", fault.ReasonUnknownValue)
	}
	return walkAbsoluteFile(absolute, uid, authority, false, true, nil)
}

func inspectAbsoluteDeniedExecutable(absolute string, uid uint32) (absoluteFileWalk, error) {
	return walkAbsoluteFile(absolute, uid, lifecycle.SystemOwnedChainAuthority, true, false, nil)
}

func openAbsoluteDeniedExecutable(absolute string, uid uint32) (absoluteFileWalk, error) {
	return walkAbsoluteFile(absolute, uid, lifecycle.SystemOwnedChainAuthority, true, true, nil)
}

// walkAbsoluteFile is the single descriptor-relative, no-follow absolute walk
// used by executable qualification and final reopening. A zero authority keeps
// the legacy general-file inspection behavior. system_owned_chain_v1 applies
// the stricter predicate to the root, every listed and opened ancestor, and the
// leaf; unlike configured private-root traversal, it has no sticky-directory
// exception.
func walkAbsoluteFile(
	absolute string,
	uid uint32,
	authority lifecycle.ExecutableAuthorityClass,
	denyOnly bool,
	retainLeaf bool,
	afterClassify func(string),
) (absoluteFileWalk, error) {
	if authority != "" && !authority.Valid() || denyOnly && authority != lifecycle.SystemOwnedChainAuthority {
		return absoluteFileWalk{}, invalid("executable_authority", fault.ReasonUnknownValue)
	}
	clean := filepath.Clean(absolute)
	if !filepath.IsAbs(clean) || len(clean) > maximumPathBytes || !utf8.ValidString(clean) || containsControl(clean) {
		return absoluteFileWalk{}, invalid("absolute_path", fault.ReasonInvalidFormat)
	}
	volume := filepath.VolumeName(clean)
	rootPath := volume + string(filepath.Separator)
	rootFD, err := unix.Open(rootPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return absoluteFileWalk{}, err
	}
	defer func() { _ = unix.Close(rootFD) }()
	var rootStat unix.Stat_t
	if err := unix.Fstat(rootFD, &rootStat); err != nil {
		return absoluteFileWalk{}, err
	}
	rootFacts := fileFactsFromUnixStat(&rootStat)
	if rootFacts.kind != lifecycle.DirectoryResource {
		return absoluteFileWalk{}, conflict("absolute_path", "root_not_directory", nil)
	}
	if authority.Valid() && !executableAuthorityAncestorSafe(rootFacts, uid, authority) {
		return absoluteFileWalk{}, conflict("executable", "unsafe_trust", nil)
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(clean, volume), string(filepath.Separator))
	if relative == "" {
		if authority.Valid() && !executableAuthorityLeafSafe(rootFacts, uid, authority, denyOnly) {
			return absoluteFileWalk{}, conflict("executable", "unsafe_trust", nil)
		}
		return absoluteFileWalk{
			facts: rootFacts, rootIdentity: rootFacts.identity, parentIdentity: rootFacts.identity,
			authorityChain: []executableAuthorityAncestor{executableAncestorFact(rootFacts)},
		}, nil
	}
	components := strings.Split(relative, string(filepath.Separator))
	currentFD := rootFD
	defer func() {
		if currentFD != rootFD {
			_ = unix.Close(currentFD)
		}
	}()
	currentPath := rootPath
	parentIdentity := rootFacts.identity
	authorityChain := []executableAuthorityAncestor{executableAncestorFact(rootFacts)}
	for index, component := range components {
		var listedStat unix.Stat_t
		if err := unix.Fstatat(currentFD, component, &listedStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return absoluteFileWalk{}, err
		}
		listed := fileFactsFromUnixStat(&listedStat)
		if listedStat.Mode&unix.S_IFMT == unix.S_IFLNK {
			return absoluteFileWalk{}, conflict("absolute_path", "symlink", nil)
		}
		if listed.kind != lifecycle.RegularResource && listed.kind != lifecycle.DirectoryResource {
			return absoluteFileWalk{}, conflict("absolute_path", "unsafe_type", nil)
		}
		last := index == len(components)-1
		if !last && listed.kind != lifecycle.DirectoryResource {
			return absoluteFileWalk{}, conflict("absolute_path", "parent_not_directory", nil)
		}
		if authority.Valid() {
			safe := executableAuthorityAncestorSafe(listed, uid, authority)
			if last {
				safe = executableAuthorityLeafSafe(listed, uid, authority, denyOnly)
			}
			if !safe {
				return absoluteFileWalk{}, conflict("executable", "unsafe_trust", nil)
			}
		}
		currentPath = filepath.Join(currentPath, component)
		if afterClassify != nil {
			afterClassify(currentPath)
		}
		flags := unix.O_CLOEXEC | unix.O_NOFOLLOW
		if last {
			if retainLeaf {
				flags |= unix.O_RDONLY | unix.O_NONBLOCK
			} else {
				// O_EVTONLY obtains a metadata descriptor without opening a FIFO for
				// I/O or activating a device data path.
				flags |= unix.O_EVTONLY
			}
		} else {
			flags |= unix.O_RDONLY | unix.O_DIRECTORY
		}
		openedFD, err := unix.Openat(currentFD, component, flags, 0)
		if err != nil {
			return absoluteFileWalk{}, err
		}
		var openedStat unix.Stat_t
		statErr := unix.Fstat(openedFD, &openedStat)
		if currentFD != rootFD {
			_ = unix.Close(currentFD)
		}
		currentFD = openedFD
		if statErr != nil {
			return absoluteFileWalk{}, statErr
		}
		opened := fileFactsFromUnixStat(&openedStat)
		if opened.identity != listed.identity {
			return absoluteFileWalk{}, conflict("absolute_path", "identity_changed", nil)
		}
		if authority.Valid() {
			safe := executableAuthorityAncestorSafe(opened, uid, authority)
			if last {
				safe = executableAuthorityLeafSafe(opened, uid, authority, denyOnly)
			}
			if !safe {
				return absoluteFileWalk{}, conflict("executable", "unsafe_trust", nil)
			}
		}
		if !last {
			authorityChain = append(authorityChain, executableAncestorFact(opened))
			parentIdentity = opened.identity
			continue
		}
		result := absoluteFileWalk{
			facts: opened, rootIdentity: rootFacts.identity, parentIdentity: parentIdentity,
			authorityChain: authorityChain,
		}
		if retainLeaf {
			result.file = os.NewFile(uintptr(openedFD), clean)
			if result.file == nil {
				_ = unix.Close(openedFD)
				currentFD = rootFD
				return absoluteFileWalk{}, errors.New("create executable descriptor")
			}
		} else {
			_ = unix.Close(openedFD)
		}
		currentFD = rootFD
		return result, nil
	}
	return absoluteFileWalk{}, errors.New("absolute traversal did not reach a leaf")
}

func executableAuthorityAncestorSafe(
	facts fileFacts,
	currentUID uint32,
	authority lifecycle.ExecutableAuthorityClass,
) bool {
	if authority == lifecycle.SystemOwnedChainAuthority {
		return facts.kind == lifecycle.DirectoryResource && facts.uid == 0 &&
			!facts.writableByUntrusted() && !facts.privilegeBearing()
	}
	return safeAuthorityAncestor(facts, currentUID)
}

func executableAuthorityLeafSafe(
	facts fileFacts,
	currentUID uint32,
	authority lifecycle.ExecutableAuthorityClass,
	denyOnly bool,
) bool {
	if denyOnly {
		return trustedDeniedExecutableFacts(facts, currentUID)
	}
	owner, trusted := trustedExecutableFacts(facts, currentUID, authority)
	return trusted && owner != lifecycle.OtherOwner
}

func fileFactsFromUnixStat(stat *unix.Stat_t) fileFacts {
	kind := lifecycle.ResourceKind("special")
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		kind = lifecycle.RegularResource
	case unix.S_IFDIR:
		kind = lifecycle.DirectoryResource
	}
	mode := fs.FileMode(stat.Mode & 0o777)
	if stat.Mode&unix.S_ISUID != 0 {
		mode |= fs.ModeSetuid
	}
	if stat.Mode&unix.S_ISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if stat.Mode&unix.S_ISVTX != 0 {
		mode |= fs.ModeSticky
	}
	return fileFacts{
		kind: kind, mode: mode, size: stat.Size, uid: stat.Uid,
		links: uint64(stat.Nlink), identity: lifecycle.ObjectIdentity{Filesystem: uint64(stat.Dev), Object: stat.Ino},
	}
}

func invalid(field string, reason fault.InvalidReason) error {
	detail, _ := fault.NewInvalidDetail(field, reason)
	return fault.MustNew(fault.InvalidInput, detail, nil)
}

func conflict(resource, identity string, cause error) error {
	detail, _ := fault.NewConflictDetail(resource, identity)
	return fault.MustNew(fault.Conflict, detail, cause)
}

func unsupportedHost() error {
	detail, _ := fault.NewUnsupportedDetail("host", runtime.GOOS+"_"+runtime.GOARCH, "darwin_arm64")
	return fault.MustNew(fault.UnsupportedCapability, detail, nil)
}
