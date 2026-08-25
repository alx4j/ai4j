//go:build darwin && arm64

package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/fault"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"golang.org/x/sys/unix"
)

type atomicOperations interface {
	Create(*os.File, string, fs.FileMode) (*os.File, error)
	Write(*os.File, []byte) error
	SyncFile(*os.File) error
	RenameExclusive(*os.File, string, string) error
	Swap(*os.File, string, string) error
	SyncDirectory(*os.File) error
	Remove(*os.File, string) error
	BeforeFinalValidation()
	AfterFinalValidation()
	AfterArtifactInspection()
	BeforeRemoveValidation(*os.File, string)
}

type realAtomicOperations struct{}

func (realAtomicOperations) Create(directory *os.File, name string, mode fs.FileMode) (*os.File, error) {
	return createLeafNoFollow(directory, name, mode)
}

func (realAtomicOperations) Write(file *os.File, content []byte) error {
	for len(content) > 0 {
		written, err := file.Write(content)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		content = content[written:]
	}
	return nil
}

func (realAtomicOperations) SyncFile(file *os.File) error { return file.Sync() }

func (realAtomicOperations) RenameExclusive(directory *os.File, old, new string) error {
	fd := int(directory.Fd())
	return unix.RenameatxNp(fd, old, fd, new, unix.RENAME_EXCL)
}

func (realAtomicOperations) Swap(directory *os.File, first, second string) error {
	fd := int(directory.Fd())
	return unix.RenameatxNp(fd, first, fd, second, unix.RENAME_SWAP)
}

func (realAtomicOperations) SyncDirectory(directory *os.File) error { return directory.Sync() }
func (realAtomicOperations) Remove(directory *os.File, name string) error {
	return removeLeaf(directory, name)
}
func (realAtomicOperations) BeforeFinalValidation()                  {}
func (realAtomicOperations) AfterFinalValidation()                   {}
func (realAtomicOperations) AfterArtifactInspection()                {}
func (realAtomicOperations) BeforeRemoveValidation(*os.File, string) {}

type mutationParent struct {
	directory     *os.File
	identity      lifecycle.ObjectIdentity
	root          lifecycle.RootRole
	path          string
	rootID        lifecycle.ObjectIdentity
	authority     *rootedDirectory
	destination   string
	expectation   lifecycle.FileExpectation
	operationID   domain.OperationID
	artifactToken domain.ArtifactToken
	artifacts     lifecycle.FileArtifactPlan
}

func (p *mutationParent) Close() error {
	return p.directory.Close()
}

func (p *mutationParent) artifact(name string, expected lifecycle.FileExpectation) lifecycle.CleanupArtifact {
	artifact := lifecycle.CleanupArtifact{
		OperationID: p.operationID, ArtifactToken: p.artifactToken, Artifacts: p.artifacts,
		Root: p.root, Path: p.relative(name), Expected: expected,
	}
	if !artifact.Valid() {
		return lifecycle.CleanupArtifact{}
	}
	return artifact
}

func (p *mutationParent) bindArtifacts(operation domain.OperationID, token domain.ArtifactToken, plan lifecycle.FileArtifactPlan) error {
	planned, ok := lifecycle.PlanFileArtifacts(operation, token)
	if !ok || plan != planned {
		return invalid("artifact_plan", fault.ReasonInvalidFormat)
	}
	p.operationID = operation
	p.artifactToken = token
	p.artifacts = plan
	return nil
}

func (p *mutationParent) quarantineName(source string) (string, error) {
	if source != p.artifacts.TemporaryName || p.artifacts.QuarantineName == "" {
		return "", invalid("cleanup_artifact", fault.ReasonInvalidFormat)
	}
	return p.artifacts.QuarantineName, nil
}

func (p *mutationParent) relative(name string) string {
	if p.path == "." {
		return name
	}
	return path.Join(p.path, name)
}

func (p *mutationParent) authorityDetached(name string) lifecycle.FileRecoveryConflict {
	return lifecycle.FileRecoveryConflict{
		Root: p.root, Path: p.relative(name), Reason: lifecycle.RecoveryAuthorityDetached, Kind: lifecycle.RecoveryUnknownObject,
	}
}

func (p *mutationParent) recoveryConflict(name string, reason lifecycle.FileRecoveryConflictReason) (lifecycle.FileRecoveryConflict, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(p.directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return lifecycle.FileRecoveryConflict{}, err
	}
	kind := lifecycle.RecoverySpecialObject
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		kind = lifecycle.RecoveryRegularObject
	case unix.S_IFDIR:
		kind = lifecycle.RecoveryDirectoryObject
	case unix.S_IFLNK:
		kind = lifecycle.RecoverySymlinkObject
	}
	conflict := lifecycle.FileRecoveryConflict{
		Root: p.root, Path: p.relative(name), Reason: reason, Kind: kind,
		Identity: lifecycle.ObjectIdentity{Filesystem: uint64(stat.Dev), Object: stat.Ino},
	}
	if !conflict.Valid() {
		return lifecycle.FileRecoveryConflict{}, errors.New("invalid recovery-conflict observation")
	}
	return conflict, nil
}

// ReplaceFile performs a checksum- and identity-gated same-directory atomic
// replacement. Darwin's exclusive rename protects expected absence. Existing
// files are swapped atomically. After visibility, failures preserve all names
// for journal reconciliation; this function never performs an unsafe rollback.
func (f *Filesystem) ReplaceFile(ctx context.Context, request lifecycle.FileMutation) (result lifecycle.FileMutationResult, resultErr error) {
	result.Cleanup = lifecycle.CleanupNotRequired
	result.Visibility = lifecycle.FileNotApplied
	result.Durability = lifecycle.NamespaceNotStarted
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !request.OperationID.Valid() {
		return result, invalid("operation_id", fault.ReasonInvalidFormat)
	}
	if !request.ArtifactToken.Valid() {
		return result, invalid("artifact_token", fault.ReasonInvalidFormat)
	}
	planned, ok := lifecycle.PlanFileArtifacts(request.OperationID, request.ArtifactToken)
	if !ok || request.Artifacts != planned {
		return result, invalid("artifact_plan", fault.ReasonInvalidFormat)
	}
	if !request.Expected.Valid() {
		return result, invalid("file_expectation", fault.ReasonInvalidFormat)
	}
	if !request.Valid() {
		return result, invalid("file_mutation", fault.ReasonInvalidFormat)
	}
	if int64(len(request.Content)) > f.maximumBytes {
		return result, invalid("content_size", fault.ReasonOutOfRange)
	}
	request.Content = append([]byte(nil), request.Content...)
	root, err := f.root(request.Root)
	if err != nil {
		return result, err
	}
	if err := validateRelativePath(request.Destination); err != nil {
		return result, err
	}
	finalMode, err := replacementMode(request, root.private)
	if err != nil {
		return result, err
	}
	digest, err := renderedDigest(request.Content)
	if err != nil {
		return result, err
	}
	result.Digest = digest
	if err := f.verifyExpectation(ctx, request); err != nil {
		return result, err
	}

	parent, err := f.openExpectedParent(request.Root, root, request.Destination, request.Expected)
	if err != nil {
		return result, err
	}
	defer func() {
		if closeErr := parent.Close(); resultErr == nil && closeErr != nil {
			resultErr = fmt.Errorf("close mutation parent: %w", closeErr)
		}
	}()
	if err := parent.bindArtifacts(request.OperationID, request.ArtifactToken, request.Artifacts); err != nil {
		return result, err
	}

	temporaryName := planned.TemporaryName
	if candidate, err := f.preflightArtifactCandidates(parent, planned); err != nil {
		result.Cleanup = lifecycle.CleanupRequired
		resultErr = recordRecoveryConflict(&result, parent, candidate, lifecycle.RecoveryPredicateMismatch, err)
		return result, resultErr
	}
	temporary, err := f.ops.Create(parent.directory, temporaryName, finalMode)
	if err != nil {
		return result, fmt.Errorf("create same-directory temporary file: %w", err)
	}
	result.Cleanup = lifecycle.CleanupComplete
	temporaryFacts, err := inspectOpenFile(temporary)
	if err != nil {
		_ = temporary.Close()
		result.Cleanup = lifecycle.CleanupRequired
		return f.cleanupTemporary(result, parent, temporaryName, lifecycle.ObjectIdentity{}, fmt.Errorf("inspect temporary file: %w", err))
	}
	requireExactMode := root.private || request.Expected.State == lifecycle.ExpectPresent
	modeMatches := temporaryFacts.mode == finalMode
	if !requireExactMode {
		modeMatches = temporaryFacts.mode.Perm()&^finalMode.Perm() == 0
	}
	if temporaryFacts.kind != lifecycle.RegularResource || !temporaryFacts.ownedBy(f.currentUID) ||
		temporaryFacts.links != 1 || !modeMatches ||
		temporaryFacts.identity.Filesystem != parent.identity.Filesystem {
		_ = temporary.Close()
		return f.cleanupTemporary(result, parent, temporaryName, temporaryFacts.identity, conflict("temporary", "unsafe_object", nil))
	}
	prepared := lifecycle.FileExpectation{
		State: lifecycle.ExpectPresent, Digest: digest, RootIdentity: root.identity, ParentIdentity: parent.identity,
		Identity: temporaryFacts.identity, Mode: temporaryFacts.mode, Size: int64(len(request.Content)), OwnedByCurrentUser: true,
	}
	if err := f.ops.Write(temporary, request.Content); err != nil {
		_ = temporary.Close()
		return f.cleanupTemporary(result, parent, temporaryName, temporaryFacts.identity, fmt.Errorf("write temporary file: %w", err))
	}
	if err := f.ops.SyncFile(temporary); err != nil {
		_ = temporary.Close()
		return f.cleanupTemporary(result, parent, temporaryName, temporaryFacts.identity, fmt.Errorf("sync temporary file: %w", err))
	}
	if err := temporary.Close(); err != nil {
		return f.cleanupTemporary(result, parent, temporaryName, temporaryFacts.identity, fmt.Errorf("close temporary file: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return f.cleanupTemporary(result, parent, temporaryName, temporaryFacts.identity, err)
	}
	f.ops.BeforeFinalValidation()
	validationErr := f.verifyExpectation(ctx, request)
	f.ops.AfterFinalValidation()
	if validationErr != nil {
		return f.cleanupTemporary(result, parent, temporaryName, temporaryFacts.identity, validationErr)
	}
	if err := f.verifyAnchoredExpectation(parent, temporaryName, prepared); err != nil {
		return f.cleanupTemporary(result, parent, temporaryName, temporaryFacts.identity, err)
	}

	destinationName := path.Base(request.Destination)
	switch request.Expected.State {
	case lifecycle.ExpectAbsent:
		if err := f.ops.RenameExclusive(parent.directory, temporaryName, destinationName); err != nil {
			return f.cleanupTemporary(result, parent, temporaryName, temporaryFacts.identity, fmt.Errorf("exclusive rename temporary file: %w", err))
		}
		result.Visibility = lifecycle.FileIndeterminate
		result.Durability = lifecycle.NamespacePending
		result.VisibleExpectation = prepared
		result.Cleanup = lifecycle.CleanupNotRequired
		if err := f.verifyCanonicalParent(root, request.Destination, request.Expected); err != nil {
			result.Cleanup = lifecycle.CleanupRequired
			result.RecoveryConflict = parent.authorityDetached(destinationName)
			return result, err
		}
		if err := f.verifyPreparedFile(parent, destinationName, prepared); err != nil {
			result.Cleanup = lifecycle.CleanupRequired
			resultErr = recordRecoveryConflict(&result, parent, destinationName, lifecycle.RecoveryPredicateMismatch, err)
			return result, resultErr
		}
		if err := f.ops.SyncDirectory(parent.directory); err != nil {
			return result, fmt.Errorf("sync containing directory after exclusive rename: %w", err)
		}
		result.Durability = lifecycle.NamespaceDurable
		if err := f.verifyCanonicalParent(root, request.Destination, request.Expected); err != nil {
			result.Cleanup = lifecycle.CleanupRequired
			result.RecoveryConflict = parent.authorityDetached(destinationName)
			return result, err
		}
		if err := f.verifyPreparedFile(parent, destinationName, prepared); err != nil {
			result.Cleanup = lifecycle.CleanupRequired
			resultErr = recordRecoveryConflict(&result, parent, destinationName, lifecycle.RecoveryPredicateMismatch, err)
			return result, resultErr
		}
		result.Cleanup = lifecycle.CleanupNotRequired
		result.Visibility = lifecycle.FileAppliedVerified
	case lifecycle.ExpectPresent:
		if err := f.ops.Swap(parent.directory, temporaryName, destinationName); err != nil {
			return f.cleanupTemporary(result, parent, temporaryName, temporaryFacts.identity, fmt.Errorf("swap temporary file: %w", err))
		}
		result.Visibility = lifecycle.FileIndeterminate
		result.Durability = lifecycle.NamespacePending
		result.VisibleExpectation = prepared
		result.Cleanup = lifecycle.CleanupRequired
		if err := f.verifyAnchoredExpectation(parent, temporaryName, request.Expected); err != nil {
			resultErr = recordRecoveryConflict(&result, parent, temporaryName, lifecycle.RecoveryPredicateMismatch, err)
			return result, resultErr
		}
		result.CleanupArtifact = parent.artifact(temporaryName, request.Expected)
		if err := f.verifyPreparedFile(parent, destinationName, prepared); err != nil {
			return result, err
		}
		if err := f.verifyCanonicalParent(root, request.Destination, request.Expected); err != nil {
			result.CleanupArtifact = lifecycle.CleanupArtifact{}
			result.RecoveryConflict = parent.authorityDetached(temporaryName)
			return result, err
		}
		// Persist the swap while both complete files still exist. The displaced
		// old object is retained for structural recovery if this sync fails.
		if err := f.ops.SyncDirectory(parent.directory); err != nil {
			result.Cleanup = lifecycle.CleanupRequired
			result.CleanupArtifact = parent.artifact(temporaryName, request.Expected)
			return result, fmt.Errorf("sync containing directory after swap: %w", err)
		}
		result.Durability = lifecycle.NamespaceDurable
		// Revalidate both complete objects at the cleanup transition. If either
		// changed after the first namespace sync, retain the old recovery object.
		if err := f.verifyPreparedFile(parent, destinationName, prepared); err != nil {
			result.Cleanup = lifecycle.CleanupRequired
			result.CleanupArtifact = parent.artifact(temporaryName, request.Expected)
			return result, err
		}
		if err := f.verifyAnchoredExpectation(parent, temporaryName, request.Expected); err != nil {
			result.Cleanup = lifecycle.CleanupRequired
			resultErr = recordRecoveryConflict(&result, parent, temporaryName, lifecycle.RecoveryPredicateMismatch, err)
			return result, resultErr
		}
		cleanup, err := f.removeVerifiedDisplaced(parent, temporaryName, request.Expected, func() error {
			if err := f.verifyCanonicalParent(root, request.Destination, request.Expected); err != nil {
				return err
			}
			return f.verifyPreparedFile(parent, destinationName, prepared)
		})
		result.CleanupArtifact = cleanup.artifact
		result.RecoveryConflict = cleanup.conflict
		if err != nil {
			result.Cleanup = lifecycle.CleanupRequired
			if canonicalErr := f.verifyCanonicalParent(root, request.Destination, request.Expected); canonicalErr != nil {
				result.CleanupArtifact = lifecycle.CleanupArtifact{}
				result.RecoveryConflict = parent.authorityDetached(request.Artifacts.QuarantineName)
			}
			return result, fmt.Errorf("remove displaced owned file: %w", err)
		}
		// removeVerifiedDisplaced includes the second directory sync, after the
		// displaced object is durably removed from its quarantine name.
		result.Cleanup = lifecycle.CleanupNotRequired
		result.CleanupArtifact = lifecycle.CleanupArtifact{}
		if err := f.verifyCanonicalParent(root, request.Destination, request.Expected); err != nil {
			result.Cleanup = lifecycle.CleanupRequired
			result.RecoveryConflict = parent.authorityDetached(destinationName)
			return result, err
		}
		if err := f.verifyPreparedFile(parent, destinationName, prepared); err != nil {
			result.Cleanup = lifecycle.CleanupRequired
			resultErr = recordRecoveryConflict(&result, parent, destinationName, lifecycle.RecoveryPredicateMismatch, err)
			return result, resultErr
		}
		result.Visibility = lifecycle.FileAppliedVerified
	default:
		panic("validated file expectation has unknown state")
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func recordRecoveryConflict(result *lifecycle.FileMutationResult, parent *mutationParent, name string, reason lifecycle.FileRecoveryConflictReason, primary error) error {
	observed, err := parent.recoveryConflict(name, reason)
	if err != nil {
		result.CleanupArtifact = lifecycle.CleanupArtifact{}
		result.RecoveryConflict = lifecycle.FileRecoveryConflict{
			Root: parent.root, Path: parent.relative(name), Reason: lifecycle.RecoveryObservationFailed, Kind: lifecycle.RecoveryUnknownObject,
		}
		return errors.Join(primary, fmt.Errorf("observe do-not-delete recovery conflict: %w", err))
	}
	result.CleanupArtifact = lifecycle.CleanupArtifact{}
	result.RecoveryConflict = observed
	return primary
}

func (f *Filesystem) preflightArtifactCandidates(parent *mutationParent, plan lifecycle.FileArtifactPlan) (string, error) {
	for _, name := range []string{plan.TemporaryName, plan.QuarantineName} {
		_, err := classifyLeafNoFollow(parent.directory, name)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return name, fmt.Errorf("inspect deterministic artifact candidate: %w", err)
		}
		return name, conflict("artifact_candidate", "recovery_required", nil)
	}
	return "", nil
}

// CleanupFile completes a journal-directed cleanup using only the rooted full
// predicate returned or preplanned before mutation. Missing is safe: the
// parent is still synced so an earlier unlink becomes durable.
func (f *Filesystem) CleanupFile(ctx context.Context, artifact lifecycle.CleanupArtifact) (result lifecycle.FileCleanupResult, resultErr error) {
	result.Cleanup = lifecycle.CleanupRequired
	result.Artifact = artifact
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !artifact.Valid() {
		return result, invalid("cleanup_artifact", fault.ReasonInvalidFormat)
	}
	root, err := f.root(artifact.Root)
	if err != nil {
		return result, err
	}
	if root.identity != artifact.Expected.RootIdentity {
		return cleanupAuthorityDetached(result, artifact), conflict("cleanup_root", "identity_changed", nil)
	}
	if err := validateRelativePath(artifact.Path); err != nil {
		return result, err
	}
	parent, err := f.openExpectedParent(artifact.Root, root, artifact.Path, artifact.Expected)
	if err != nil {
		return cleanupAuthorityDetached(result, artifact), err
	}
	defer func() {
		if closeErr := parent.Close(); resultErr == nil && closeErr != nil {
			resultErr = fmt.Errorf("close cleanup parent: %w", closeErr)
		}
	}()
	if err := parent.bindArtifacts(artifact.OperationID, artifact.ArtifactToken, artifact.Artifacts); err != nil {
		return result, err
	}
	name := path.Base(artifact.Path)
	authorityArtifact := artifact
	listed, err := classifyLeafNoFollow(parent.directory, name)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
		if name == artifact.Artifacts.TemporaryName {
			name = artifact.Artifacts.QuarantineName
		} else {
			name = artifact.Artifacts.TemporaryName
		}
		authorityArtifact.Path = parent.relative(name)
		result.Artifact = parent.artifact(name, artifact.Expected)
		listed, err = classifyLeafNoFollow(parent.directory, name)
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
		if err := f.verifyCanonicalParent(root, authorityArtifact.Path, artifact.Expected); err != nil {
			return cleanupAuthorityDetached(result, authorityArtifact), err
		}
		syncErr := f.ops.SyncDirectory(parent.directory)
		if authorityErr := f.verifyCanonicalParent(root, authorityArtifact.Path, artifact.Expected); authorityErr != nil {
			return cleanupAuthorityDetached(result, authorityArtifact), errors.Join(syncErr, authorityErr)
		}
		if syncErr != nil {
			return result, fmt.Errorf("sync absent cleanup artifact: %w", syncErr)
		}
		result.Cleanup = lifecycle.CleanupComplete
		result.Artifact = lifecycle.CleanupArtifact{}
		return result, nil
	}
	if err != nil {
		resultErr = recordCleanupConflict(&result, parent, name, lifecycle.RecoveryUnsafeObject, fmt.Errorf("classify cleanup artifact: %w", err))
		return result, resultErr
	}
	if listed.kind != lifecycle.RegularResource {
		resultErr = recordCleanupConflict(&result, parent, name, lifecycle.RecoveryUnsafeObject, conflict("cleanup_artifact", "unsafe_type", nil))
		return result, resultErr
	}
	if name == artifact.Artifacts.QuarantineName {
		attempt, cleanupErr := f.removeRecoveredQuarantine(parent, name, artifact.Expected)
		result.Artifact = attempt.artifact
		result.RecoveryConflict = attempt.conflict
		if authorityErr := f.verifyCanonicalParent(root, authorityArtifact.Path, artifact.Expected); authorityErr != nil {
			return cleanupAuthorityDetached(result, authorityArtifact), errors.Join(cleanupErr, authorityErr)
		}
		if cleanupErr != nil {
			return result, cleanupErr
		}
		result.Cleanup = lifecycle.CleanupComplete
		result.Artifact = lifecycle.CleanupArtifact{}
		result.RecoveryConflict = lifecycle.FileRecoveryConflict{}
		return result, nil
	}
	attempt, err := f.quarantineAndRemove(parent, name, artifact.Expected)
	result.Artifact = attempt.artifact
	result.RecoveryConflict = attempt.conflict
	if authorityErr := f.verifyCanonicalParent(root, authorityArtifact.Path, artifact.Expected); authorityErr != nil {
		return cleanupAuthorityDetached(result, authorityArtifact), errors.Join(err, authorityErr)
	}
	if err != nil {
		return result, err
	}
	result.Cleanup = lifecycle.CleanupComplete
	result.Artifact = lifecycle.CleanupArtifact{}
	result.RecoveryConflict = lifecycle.FileRecoveryConflict{}
	return result, nil
}

func recordCleanupConflict(result *lifecycle.FileCleanupResult, parent *mutationParent, name string, reason lifecycle.FileRecoveryConflictReason, primary error) error {
	observed, err := parent.recoveryConflict(name, reason)
	if err != nil {
		result.Artifact = lifecycle.CleanupArtifact{}
		result.RecoveryConflict = lifecycle.FileRecoveryConflict{
			Root: parent.root, Path: parent.relative(name), Reason: lifecycle.RecoveryObservationFailed, Kind: lifecycle.RecoveryUnknownObject,
		}
		return errors.Join(primary, fmt.Errorf("observe do-not-delete cleanup conflict: %w", err))
	}
	result.Artifact = lifecycle.CleanupArtifact{}
	result.RecoveryConflict = observed
	return primary
}

func cleanupAuthorityDetached(result lifecycle.FileCleanupResult, artifact lifecycle.CleanupArtifact) lifecycle.FileCleanupResult {
	result.Cleanup = lifecycle.CleanupRequired
	result.Artifact = lifecycle.CleanupArtifact{}
	result.RecoveryConflict = lifecycle.FileRecoveryConflict{
		Root: artifact.Root, Path: artifact.Path, Reason: lifecycle.RecoveryAuthorityDetached, Kind: lifecycle.RecoveryUnknownObject,
	}
	return result
}

// InspectFileArtifacts classifies the destination together with at most the two
// deterministic operation names. Only an object matching the journaled
// preimage or desired/prepared predicate in a coherent state becomes deletable;
// every mismatch is returned as a do-not-delete recovery conflict.
func (f *Filesystem) InspectFileArtifacts(ctx context.Context, request lifecycle.FileArtifactInspectionRequest) (result lifecycle.FileArtifactInspectionResult, resultErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !request.Valid() {
		return result, invalid("artifact_inspection", fault.ReasonInvalidFormat)
	}
	if err := validateRelativePath(request.Destination); err != nil {
		return result, err
	}
	root, err := f.root(request.Root)
	if err != nil {
		return result, err
	}
	if root.identity != request.RootIdentity {
		result.Conflicts = []lifecycle.FileRecoveryConflict{{
			Root: request.Root, Path: request.Destination, Reason: lifecycle.RecoveryAuthorityDetached, Kind: lifecycle.RecoveryUnknownObject,
		}}
		return result, conflict("artifact_root", "identity_changed", nil)
	}
	parent, err := f.openExpectedParent(request.Root, root, request.Destination, request.Preimage)
	if err != nil {
		result.Conflicts = []lifecycle.FileRecoveryConflict{{
			Root: request.Root, Path: request.Destination, Reason: lifecycle.RecoveryAuthorityDetached, Kind: lifecycle.RecoveryUnknownObject,
		}}
		return result, err
	}
	defer func() {
		if closeErr := parent.Close(); resultErr == nil && closeErr != nil {
			resultErr = fmt.Errorf("close artifact-inspection parent: %w", closeErr)
		}
	}()
	defer func() {
		f.ops.AfterArtifactInspection()
		if authorityErr := f.verifyCanonicalParent(root, request.Destination, request.Preimage); authorityErr != nil {
			result.Artifacts = nil
			result.Conflicts = []lifecycle.FileRecoveryConflict{{
				Root: request.Root, Path: request.Destination, Reason: lifecycle.RecoveryAuthorityDetached, Kind: lifecycle.RecoveryUnknownObject,
			}}
			resultErr = errors.Join(resultErr, authorityErr)
		}
	}()
	if err := parent.bindArtifacts(request.OperationID, request.ArtifactToken, request.Artifacts); err != nil {
		return result, err
	}
	result.Artifacts = make([]lifecycle.CleanupArtifact, 0, 2)
	result.Conflicts = make([]lifecycle.FileRecoveryConflict, 0, 3)

	destinationName := path.Base(request.Destination)
	destination, destinationExists, destinationErr := f.observeRecoveryExpectation(parent, destinationName)
	destinationState := recoveryDestinationConflict
	if destinationErr != nil {
		if err := appendRecoveryConflict(&result, parent, destinationName, lifecycle.RecoveryUnsafeObject); err != nil {
			return result, err
		}
	} else if recoveryMatchesPreimage(destination, destinationExists, request.Preimage) {
		destinationState = recoveryDestinationPreimage
	} else if destinationExists && recoveryMatchesDesired(destination, request) {
		destinationState = recoveryDestinationDesired
	} else {
		if err := appendRecoveryConflict(&result, parent, destinationName, lifecycle.RecoveryPredicateMismatch); err != nil {
			return result, err
		}
	}

	for _, name := range []string{request.Artifacts.TemporaryName, request.Artifacts.QuarantineName} {
		if err := ctx.Err(); err != nil {
			return lifecycle.FileArtifactInspectionResult{}, err
		}
		observed, exists, err := f.observeRecoveryExpectation(parent, name)
		if !exists && err == nil {
			continue
		}
		if err != nil {
			if conflictErr := appendRecoveryConflict(&result, parent, name, lifecycle.RecoveryUnsafeObject); conflictErr != nil {
				return result, conflictErr
			}
			continue
		}
		owned := destinationState == recoveryDestinationDesired && request.Preimage.State == lifecycle.ExpectPresent && observed == request.Preimage
		owned = owned || destinationState == recoveryDestinationPreimage && recoveryMatchesPrepared(observed, request)
		if !owned {
			if err := appendRecoveryConflict(&result, parent, name, lifecycle.RecoveryPredicateMismatch); err != nil {
				return result, err
			}
			continue
		}
		artifact := parent.artifact(name, observed)
		if !artifact.Valid() {
			return lifecycle.FileArtifactInspectionResult{}, conflict("planned_artifact", "invalid_observation", nil)
		}
		result.Artifacts = append(result.Artifacts, artifact)
	}
	return result, nil
}

type recoveryDestinationState uint8

const (
	recoveryDestinationConflict recoveryDestinationState = iota
	recoveryDestinationPreimage
	recoveryDestinationDesired
)

func (f *Filesystem) observeRecoveryExpectation(parent *mutationParent, name string) (lifecycle.FileExpectation, bool, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(parent.directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
			return lifecycle.FileExpectation{}, false, nil
		}
		return lifecycle.FileExpectation{}, true, err
	}
	facts := fileFactsFromUnixStat(&stat)
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || !facts.ownedBy(f.currentUID) || facts.links != 1 ||
		facts.identity.Filesystem != parent.identity.Filesystem || facts.privilegeBearing() || facts.size < 0 || facts.size > f.maximumBytes {
		return lifecycle.FileExpectation{}, true, conflict("recovery_object", "unsafe_object", nil)
	}
	expected, err := f.observeAnchoredExpectation(parent, name, facts.identity)
	if err != nil {
		return lifecycle.FileExpectation{}, true, err
	}
	return expected, true, nil
}

func recoveryMatchesPreimage(observed lifecycle.FileExpectation, exists bool, preimage lifecycle.FileExpectation) bool {
	if preimage.State == lifecycle.ExpectAbsent {
		return !exists
	}
	return exists && observed == preimage
}

func recoveryMatchesDesired(observed lifecycle.FileExpectation, request lifecycle.FileArtifactInspectionRequest) bool {
	if !request.Prepared.Empty() {
		return observed == request.Prepared
	}
	if request.Preimage.State == lifecycle.ExpectPresent && observed.Identity == request.Preimage.Identity {
		return false
	}
	return request.Desired.Matches(observed)
}

func recoveryMatchesPrepared(observed lifecycle.FileExpectation, request lifecycle.FileArtifactInspectionRequest) bool {
	if !request.Prepared.Empty() {
		return observed == request.Prepared
	}
	if request.Preimage.State == lifecycle.ExpectPresent && observed.Identity == request.Preimage.Identity {
		return false
	}
	return request.Desired.Matches(observed)
}

func appendRecoveryConflict(result *lifecycle.FileArtifactInspectionResult, parent *mutationParent, name string, reason lifecycle.FileRecoveryConflictReason) error {
	observed, err := parent.recoveryConflict(name, reason)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
		observed = lifecycle.FileRecoveryConflict{
			Root: parent.root, Path: parent.relative(name), Reason: lifecycle.RecoveryPredicateMismatch, Kind: lifecycle.RecoveryMissingObject,
		}
		err = nil
	}
	if err != nil {
		result.Conflicts = append(result.Conflicts, lifecycle.FileRecoveryConflict{
			Root: parent.root, Path: parent.relative(name), Reason: lifecycle.RecoveryObservationFailed, Kind: lifecycle.RecoveryUnknownObject,
		})
		return err
	}
	result.Conflicts = append(result.Conflicts, observed)
	return nil
}

func (f *Filesystem) removeRecoveredQuarantine(parent *mutationParent, name string, expected lifecycle.FileExpectation) (cleanupAttempt, error) {
	attempt := cleanupAttempt{artifact: parent.artifact(name, expected)}
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			f.ops.BeforeRemoveValidation(parent.directory, name)
		}
		file, facts, err := openAnchoredFile(parent, name)
		if err == nil {
			err = f.verifyOpenExpectation(parent, file, facts, expected)
		}
		if file != nil {
			_ = file.Close()
		}
		if err != nil {
			return recoveryConflictAttempt(parent, name, err)
		}
	}
	if err := f.ops.Remove(parent.directory, name); err != nil {
		return attempt, err
	}
	if err := f.ops.SyncDirectory(parent.directory); err != nil {
		return attempt, err
	}
	attempt.artifact = lifecycle.CleanupArtifact{}
	return attempt, nil
}

func replacementMode(request lifecycle.FileMutation, private bool) (fs.FileMode, error) {
	if private {
		switch request.Expected.State {
		case lifecycle.ExpectAbsent:
			if request.Mode != 0o600 {
				return 0, invalid("mode", fault.ReasonOutOfRange)
			}
			return 0o600, nil
		case lifecycle.ExpectPresent:
			if request.Expected.Mode != 0o600 || request.Mode != 0 && request.Mode != request.Expected.Mode {
				return 0, conflict("destination", "unsafe_private_mode", nil)
			}
			return request.Expected.Mode, nil
		default:
			return 0, invalid("file_expectation", fault.ReasonInvalidFormat)
		}
	}
	if request.Expected.State == lifecycle.ExpectPresent {
		if request.Expected.Mode == 0 || request.Expected.Mode.Perm() != request.Expected.Mode ||
			request.Expected.Mode&0o111 != 0 || request.Expected.Mode&^maximumOwnedFileMode != 0 {
			return 0, conflict("destination", "unsafe_mode", nil)
		}
		if request.Mode != 0 && request.Mode != request.Expected.Mode {
			return 0, conflict("destination", "mode_change", nil)
		}
		return request.Expected.Mode, nil
	}
	mode := request.Mode
	if mode == 0 || mode.Perm() != mode || mode&0o111 != 0 || mode&^maximumOwnedFileMode != 0 {
		return 0, invalid("mode", fault.ReasonOutOfRange)
	}
	return mode, nil
}

func (f *Filesystem) openExpectedParent(role lifecycle.RootRole, root *rootedDirectory, destination string, expectation lifecycle.FileExpectation) (*mutationParent, error) {
	if err := f.verifyCanonicalParent(root, destination, expectation); err != nil {
		return nil, err
	}
	directory, identity, err := openParentNoFollow(root, destination, f.currentUID, nil)
	if err != nil {
		return nil, fmt.Errorf("open anchored mutation parent: %w", err)
	}
	if identity != expectation.ParentIdentity {
		_ = directory.Close()
		return nil, conflict("parent", "identity_changed", nil)
	}
	if err := f.verifyCanonicalParent(root, destination, expectation); err != nil {
		_ = directory.Close()
		return nil, err
	}
	return &mutationParent{
		directory: directory, identity: identity, root: role, path: path.Dir(destination), rootID: root.identity,
		authority: root, destination: destination, expectation: expectation,
	}, nil
}

func (f *Filesystem) verifyCanonicalParent(root *rootedDirectory, destination string, expectation lifecycle.FileExpectation) error {
	if root.identity != expectation.RootIdentity {
		return conflict("root", "identity_changed", nil)
	}
	if err := revalidateRoot(root, f.currentUID); err != nil {
		return err
	}
	identity, err := verifyParent(root, destination, f.currentUID)
	if err != nil {
		return err
	}
	if identity != expectation.ParentIdentity {
		return conflict("parent", "identity_changed", nil)
	}
	return nil
}

func (f *Filesystem) verifyExpectation(ctx context.Context, request lifecycle.FileMutation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resource := lifecycle.ResourceRequest{
		Root: request.Root, Path: request.Destination, Kind: lifecycle.RegularResource,
		RequireCurrentOwner: true, RejectMultipleLinks: true,
	}
	if request.Expected.State == lifecycle.ExpectAbsent {
		observation, err := f.CheckResource(ctx, resource)
		if err != nil {
			return err
		}
		if observation.RootIdentity != request.Expected.RootIdentity || observation.ParentIdentity != request.Expected.ParentIdentity {
			return conflict("destination", "parent_changed", nil)
		}
		if observation.Exists {
			return conflict("destination", "unexpected_object", nil)
		}
		return nil
	}
	read, err := f.ReadResource(ctx, lifecycle.ResourceReadRequest{Resource: resource, MaxBytes: f.maximumBytes})
	if err != nil {
		return err
	}
	observation := read.Observation
	if !observation.Exists || !observation.OwnedByCurrentUser || observation.LinkCount != 1 ||
		observation.RootIdentity != request.Expected.RootIdentity ||
		observation.ParentIdentity != request.Expected.ParentIdentity ||
		observation.Identity != request.Expected.Identity || observation.Mode != request.Expected.Mode || observation.Size != request.Expected.Size {
		return conflict("destination", "precondition_changed", nil)
	}
	digest, err := renderedDigest(read.Content)
	if err != nil {
		return err
	}
	if digest != request.Expected.Digest {
		return conflict("destination", "checksum_changed", nil)
	}
	return nil
}

func (f *Filesystem) verifyAnchoredExpectation(parent *mutationParent, name string, expectation lifecycle.FileExpectation) error {
	file, facts, err := openAnchoredFile(parent, name)
	if err != nil {
		return err
	}
	defer file.Close()
	return f.verifyOpenExpectation(parent, file, facts, expectation)
}

func (f *Filesystem) verifyOpenExpectation(parent *mutationParent, file *os.File, facts fileFacts, expectation lifecycle.FileExpectation) error {
	if !expectation.Valid() || expectation.State != lifecycle.ExpectPresent || !facts.ownedBy(f.currentUID) || facts.links != 1 ||
		facts.identity != expectation.Identity || facts.mode != expectation.Mode || facts.size != expectation.Size ||
		expectation.RootIdentity != parent.rootID || expectation.ParentIdentity != parent.identity ||
		facts.privilegeBearing() || facts.size < 0 || facts.size > f.maximumBytes {
		return conflict("destination", "concurrent_replacement", nil)
	}
	content, overLimit, err := readBoundedFile(file, f.maximumBytes)
	if err != nil || overLimit {
		return conflict("destination", "concurrent_replacement", err)
	}
	digest, err := renderedDigest(content)
	if err != nil || digest != expectation.Digest {
		return conflict("destination", "concurrent_replacement", err)
	}
	return nil
}

func (f *Filesystem) observeAnchoredExpectation(parent *mutationParent, name string, identity lifecycle.ObjectIdentity) (lifecycle.FileExpectation, error) {
	file, facts, err := openAnchoredFile(parent, name)
	if err != nil {
		return lifecycle.FileExpectation{}, err
	}
	defer file.Close()
	if facts.identity != identity || !facts.ownedBy(f.currentUID) || facts.links != 1 || facts.kind != lifecycle.RegularResource ||
		facts.privilegeBearing() || facts.size < 0 || facts.size > f.maximumBytes {
		return lifecycle.FileExpectation{}, conflict("cleanup", "identity_changed", nil)
	}
	content, overLimit, err := readBoundedFile(file, f.maximumBytes)
	if err != nil || overLimit {
		return lifecycle.FileExpectation{}, conflict("cleanup", "content_unbounded", err)
	}
	digest, err := renderedDigest(content)
	if err != nil {
		return lifecycle.FileExpectation{}, err
	}
	return lifecycle.FileExpectation{
		State: lifecycle.ExpectPresent, Digest: digest, RootIdentity: parent.rootID, ParentIdentity: parent.identity,
		Identity: facts.identity, Mode: facts.mode, Size: facts.size, OwnedByCurrentUser: true,
	}, nil
}

func (f *Filesystem) verifyPreparedFile(parent *mutationParent, name string, prepared lifecycle.FileExpectation) error {
	return f.verifyAnchoredExpectation(parent, name, prepared)
}

func openAnchoredFile(parent *mutationParent, name string) (*os.File, fileFacts, error) {
	file, opened, err := openLeafNoFollow(parent.directory, name, lifecycle.RegularResource, nil)
	if err != nil {
		return nil, fileFacts{}, err
	}
	if opened.identity.Filesystem != parent.identity.Filesystem {
		_ = file.Close()
		return nil, fileFacts{}, conflict("anchored_file", "identity_changed", nil)
	}
	return file, opened, nil
}

type cleanupAttempt struct {
	artifact lifecycle.CleanupArtifact
	conflict lifecycle.FileRecoveryConflict
}

func (f *Filesystem) removeVerifiedDisplaced(parent *mutationParent, name string, expected lifecycle.FileExpectation, beforeRemove func() error) (cleanupAttempt, error) {
	return f.quarantineAndRemoveGuarded(parent, name, expected, beforeRemove)
}

// quarantineAndRemove is the sole identity-gated deletion primitive. It fully
// verifies the visible object, moves it to the pre-journaled exclusive
// operation name, verifies it again, and only then unlinks it. A final-window
// concurrent object is preserved at the quarantine name and reported.
func (f *Filesystem) quarantineAndRemove(parent *mutationParent, name string, expected lifecycle.FileExpectation) (cleanupAttempt, error) {
	return f.quarantineAndRemoveGuarded(parent, name, expected, nil)
}

func (f *Filesystem) quarantineAndRemoveGuarded(parent *mutationParent, name string, expected lifecycle.FileExpectation, beforeRemove func() error) (cleanupAttempt, error) {
	quarantine, err := parent.quarantineName(name)
	if err != nil {
		return cleanupAttempt{}, err
	}
	file, facts, err := openAnchoredFile(parent, name)
	if err == nil {
		err = f.verifyOpenExpectation(parent, file, facts, expected)
	}
	if file != nil {
		_ = file.Close()
	}
	if err != nil {
		return recoveryConflictAttempt(parent, name, conflict("cleanup", "pre_quarantine_changed", err))
	}
	// Move the verified entry to its high-entropy, pre-journaled exclusive name.
	// A replacement in the final rename window is identified in quarantine and
	// preserved rather than deleted. Unix has no identity-CAS unlink; the
	// quarantine name is the remaining same-user trust boundary for the final
	// classify/unlink pair.
	if err := f.ops.RenameExclusive(parent.directory, name, quarantine); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return recoveryConflictAttempt(parent, quarantine, conflict("cleanup", "quarantine_collision", err))
		}
		if verifyErr := f.verifyAnchoredExpectation(parent, name, expected); verifyErr == nil {
			return cleanupAttempt{artifact: parent.artifact(name, expected)}, err
		}
		return recoveryConflictAttempt(parent, name, err)
	}
	attempt := cleanupAttempt{artifact: parent.artifact(quarantine, expected)}
	file, facts, err = openAnchoredFile(parent, quarantine)
	if err == nil {
		err = f.verifyOpenExpectation(parent, file, facts, expected)
	}
	if file != nil {
		_ = file.Close()
	}
	if err != nil {
		syncErr := f.ops.SyncDirectory(parent.directory)
		conflictAttempt, observeErr := recoveryConflictAttempt(parent, quarantine, conflict("cleanup", "identity_changed", err))
		return conflictAttempt, errors.Join(observeErr, syncErr)
	}
	// Make the operation-scoped quarantine name recoverable before unlinking.
	// A crash before this sync can still discover the original temp prefix.
	if err := f.ops.SyncDirectory(parent.directory); err != nil {
		return attempt, fmt.Errorf("sync quarantined-file rename: %w", err)
	}
	f.ops.BeforeRemoveValidation(parent.directory, quarantine)
	file, facts, err = openAnchoredFile(parent, quarantine)
	if err == nil {
		err = f.verifyOpenExpectation(parent, file, facts, expected)
	}
	if file != nil {
		_ = file.Close()
	}
	if err != nil {
		return recoveryConflictAttempt(parent, quarantine, conflict("cleanup", "pre_remove_changed", err))
	}
	if beforeRemove != nil {
		if err := beforeRemove(); err != nil {
			return attempt, err
		}
	}
	// Darwin exposes no identity-CAS unlink. The remaining check-to-unlink gap
	// assumes no actively adversarial same-UID process can guess and target the
	// high-entropy operation-scoped quarantine name. Ordinary substitutions are
	// detected by the immediately preceding full-state revalidation.
	if err := f.ops.Remove(parent.directory, quarantine); err != nil {
		return attempt, err
	}
	if err := f.ops.SyncDirectory(parent.directory); err != nil {
		return attempt, fmt.Errorf("sync quarantined-file removal: %w", err)
	}
	attempt.artifact = lifecycle.CleanupArtifact{}
	return attempt, nil
}

func recoveryConflictAttempt(parent *mutationParent, name string, primary error) (cleanupAttempt, error) {
	observed, err := parent.recoveryConflict(name, lifecycle.RecoveryPredicateMismatch)
	if err != nil {
		observed = lifecycle.FileRecoveryConflict{
			Root: parent.root, Path: parent.relative(name), Reason: lifecycle.RecoveryObservationFailed, Kind: lifecycle.RecoveryUnknownObject,
		}
		return cleanupAttempt{conflict: observed}, errors.Join(primary, fmt.Errorf("observe do-not-delete cleanup conflict: %w", err))
	}
	return cleanupAttempt{conflict: observed}, primary
}

func (f *Filesystem) cleanupTemporary(initial lifecycle.FileMutationResult, parent *mutationParent, name string, identity lifecycle.ObjectIdentity, primary error) (result lifecycle.FileMutationResult, resultErr error) {
	result = initial
	defer func() {
		if authorityErr := f.verifyCanonicalParent(parent.authority, parent.destination, parent.expectation); authorityErr != nil {
			conflictName := name
			if result.CleanupArtifact.Valid() {
				conflictName = path.Base(result.CleanupArtifact.Path)
			} else if result.RecoveryConflict.Valid() {
				conflictName = path.Base(result.RecoveryConflict.Path)
			}
			result.Cleanup = lifecycle.CleanupRequired
			result.CleanupArtifact = lifecycle.CleanupArtifact{}
			result.RecoveryConflict = parent.authorityDetached(conflictName)
			resultErr = errors.Join(resultErr, authorityErr)
		}
	}()
	if !identity.Valid() {
		result.Cleanup = lifecycle.CleanupRequired
		resultErr = recordRecoveryConflict(&result, parent, name, lifecycle.RecoveryObservationFailed, primary)
		return result, resultErr
	}
	facts, err := classifyLeafNoFollow(parent.directory, name)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
		if syncErr := f.ops.SyncDirectory(parent.directory); syncErr != nil {
			result.Cleanup = lifecycle.CleanupRequired
			result.CleanupArtifact = lifecycle.CleanupArtifact{}
			result.RecoveryConflict = lifecycle.FileRecoveryConflict{
				Root: parent.root, Path: parent.relative(name), Reason: lifecycle.RecoveryObservationFailed, Kind: lifecycle.RecoveryUnknownObject,
			}
			return result, errors.Join(primary, fmt.Errorf("sync absent temporary cleanup: %w", syncErr))
		}
		result.Cleanup = lifecycle.CleanupComplete
		return result, primary
	}
	if err != nil {
		result.Cleanup = lifecycle.CleanupRequired
		failure := errors.Join(primary, fmt.Errorf("inspect temporary cleanup: %w", err))
		resultErr = recordRecoveryConflict(&result, parent, name, lifecycle.RecoveryUnsafeObject, failure)
		return result, resultErr
	}
	if facts.identity != identity || !facts.ownedBy(f.currentUID) || facts.links != 1 || facts.kind != lifecycle.RegularResource {
		result.Cleanup = lifecycle.CleanupRequired
		failure := errors.Join(primary, conflict("cleanup", "identity_changed", nil))
		resultErr = recordRecoveryConflict(&result, parent, name, lifecycle.RecoveryPredicateMismatch, failure)
		return result, resultErr
	}
	expected, err := f.observeAnchoredExpectation(parent, name, identity)
	if err != nil {
		result.Cleanup = lifecycle.CleanupRequired
		resultErr = recordRecoveryConflict(&result, parent, name, lifecycle.RecoveryPredicateMismatch, errors.Join(primary, err))
		return result, resultErr
	}
	result.CleanupArtifact = parent.artifact(name, expected)
	attempt, err := f.quarantineAndRemove(parent, name, expected)
	result.CleanupArtifact = attempt.artifact
	result.RecoveryConflict = attempt.conflict
	if err != nil {
		result.Cleanup = lifecycle.CleanupRequired
		return result, errors.Join(primary, fmt.Errorf("remove temporary cleanup: %w", err))
	}
	result.Cleanup = lifecycle.CleanupComplete
	return result, primary
}

func renderedDigest(content []byte) (domain.RenderedDigest, error) {
	sum := sha256.Sum256(content)
	return domain.NewRenderedDigest(hex.EncodeToString(sum[:]))
}
