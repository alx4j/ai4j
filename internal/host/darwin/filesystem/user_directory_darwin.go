//go:build darwin && arm64

package filesystem

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/user"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/pathsafe"
	"golang.org/x/sys/unix"
)

const maximumIssuerGenerationAttempts = 4

var errUserDirectoryAuthorityClose = errors.New("close Darwin user-directory authority")

type effectiveAccountOperations interface {
	RealUID() int
	EffectiveUID() int
	RealGID() int
	EffectiveGID() int
	LookupUserID(string) (*user.User, error)
	ReadRandom([]byte) (int, error)
}

type realEffectiveAccountOperations struct{}

func (realEffectiveAccountOperations) RealUID() int      { return os.Getuid() }
func (realEffectiveAccountOperations) EffectiveUID() int { return os.Geteuid() }
func (realEffectiveAccountOperations) RealGID() int      { return os.Getgid() }
func (realEffectiveAccountOperations) EffectiveGID() int { return os.Getegid() }
func (realEffectiveAccountOperations) LookupUserID(value string) (*user.User, error) {
	return user.LookupId(value)
}
func (realEffectiveAccountOperations) ReadRandom(value []byte) (int, error) {
	return io.ReadFull(rand.Reader, value)
}

type absoluteDirectoryObservation struct {
	absolute     string
	objects      []lifecycle.DirectoryObjectProof
	present      bool
	missingIndex int
	parent       *os.File
	leaf         *os.File
}

func (o absoluteDirectoryObservation) identities() []lifecycle.ObjectIdentity {
	result := make([]lifecycle.ObjectIdentity, 0, len(o.objects))
	for _, object := range o.objects {
		result = append(result, object.Identity())
	}
	return result
}

func (o *absoluteDirectoryObservation) close() error {
	if o == nil {
		return nil
	}
	var result error
	if o.leaf != nil {
		result = errors.Join(result, o.leaf.Close())
		o.leaf = nil
	}
	if o.parent != nil {
		result = errors.Join(result, o.parent.Close())
		o.parent = nil
	}
	return result
}

func (o absoluteDirectoryObservation) sameFacts(other absoluteDirectoryObservation) bool {
	if o.absolute != other.absolute || o.present != other.present || o.missingIndex != other.missingIndex ||
		len(o.objects) != len(other.objects) {
		return false
	}
	for index := range o.objects {
		if o.objects[index] != other.objects[index] {
			return false
		}
	}
	return true
}

type retainedLeafAuthority struct {
	uid            uint32
	root           *os.File
	parent         *os.File
	leaf           *os.File
	components     []string
	chainExpected  []lifecycle.DirectoryObjectProof
	leafName       string
	presence       lifecycle.DirectoryLeafPresence
	parentExpected lifecycle.DirectoryObjectProof
	leafExpected   lifecycle.DirectoryObjectProof
	closeOnce      sync.Once
	closeErr       error
}

func (a *retainedLeafAuthority) revalidate(ctx context.Context) error {
	if a == nil || ctx == nil || a.root == nil || a.parent == nil || !a.parentExpected.Valid() ||
		!a.presence.Valid() || len(a.components) == 0 || len(a.chainExpected) == 0 {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	if err := a.revalidatePath(ctx); err != nil {
		return err
	}
	parent, err := directoryObjectFromOpenFile(a.parent, a.uid)
	if err != nil {
		return lifecycle.DirectoryObservationFailedIssue()
	}
	if parent != a.parentExpected {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	var listed unix.Stat_t
	err = unix.Fstatat(int(a.parent.Fd()), a.leafName, &listed, unix.AT_SYMLINK_NOFOLLOW)
	if a.presence == lifecycle.AbsentDirectoryLeaf() {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
			return nil
		}
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	if err != nil || listed.Mode&unix.S_IFMT == unix.S_IFLNK || a.leaf == nil || !a.leafExpected.Valid() {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	listedObject, objectErr := directoryObjectFromUnixStat(&listed, a.uid)
	if objectErr != nil || listedObject != a.leafExpected {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	openedObject, objectErr := directoryObjectFromOpenFile(a.leaf, a.uid)
	if objectErr != nil || openedObject != a.leafExpected {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	return nil
}

// revalidatePath binds the retained final parent and leaf back to the exact
// home-relative descriptor chain. Rechecking only the retained final objects
// would accept a renamed-away intermediate whose old descriptor stayed valid.
func (a *retainedLeafAuthority) revalidatePath(ctx context.Context) (_ error) {
	expectedCount := len(a.components) + 1
	if a.presence == lifecycle.AbsentDirectoryLeaf() {
		expectedCount--
	}
	if len(a.chainExpected) != expectedCount || !a.chainExpected[0].Valid() {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	duplicate, err := unix.FcntlInt(a.root.Fd(), unix.F_DUPFD_CLOEXEC, minimumAuthorityDescriptor)
	if err != nil {
		return lifecycle.DirectoryObservationFailedIssue()
	}
	current := os.NewFile(uintptr(duplicate), "<relative-proof-root>")
	if current == nil {
		_ = unix.Close(duplicate)
		return lifecycle.DirectoryObservationFailedIssue()
	}
	defer func() { _ = current.Close() }()
	rootObject, err := directoryObjectFromOpenFile(current, a.uid)
	if err != nil {
		return lifecycle.DirectoryObservationFailedIssue()
	}
	if rootObject != a.chainExpected[0] {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	for index, component := range a.components {
		if err := ctx.Err(); err != nil {
			return err
		}
		last := index == len(a.components)-1
		var listed unix.Stat_t
		statErr := unix.Fstatat(int(current.Fd()), component, &listed, unix.AT_SYMLINK_NOFOLLOW)
		if last && a.presence == lifecycle.AbsentDirectoryLeaf() {
			if errors.Is(statErr, fs.ErrNotExist) || errors.Is(statErr, unix.ENOENT) {
				if a.chainExpected[index] != a.parentExpected {
					return lifecycle.DirectoryIdentityChangedIssue()
				}
				return nil
			}
			return lifecycle.DirectoryIdentityChangedIssue()
		}
		if statErr != nil || listed.Mode&unix.S_IFMT != unix.S_IFDIR {
			return lifecycle.DirectoryIdentityChangedIssue()
		}
		listedObject, objectErr := directoryObjectFromUnixStat(&listed, a.uid)
		if objectErr != nil || listedObject != a.chainExpected[index+1] {
			return lifecycle.DirectoryIdentityChangedIssue()
		}
		openedFD, openErr := unix.Openat(
			int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0,
		)
		if openErr != nil {
			return lifecycle.DirectoryIdentityChangedIssue()
		}
		opened := os.NewFile(uintptr(openedFD), "<relative-proof-component>")
		if opened == nil {
			_ = unix.Close(openedFD)
			return lifecycle.DirectoryObservationFailedIssue()
		}
		openedObject, objectErr := directoryObjectFromOpenFile(opened, a.uid)
		if objectErr != nil || openedObject != listedObject {
			_ = opened.Close()
			return lifecycle.DirectoryIdentityChangedIssue()
		}
		if err := current.Close(); err != nil {
			_ = opened.Close()
			return lifecycle.DirectoryObservationFailedIssue()
		}
		current = opened
	}
	if a.presence != lifecycle.PresentDirectoryLeaf() ||
		a.chainExpected[len(a.chainExpected)-2] != a.parentExpected ||
		a.chainExpected[len(a.chainExpected)-1] != a.leafExpected {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	return nil
}

func (a *retainedLeafAuthority) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		if a.root != nil {
			a.closeErr = errors.Join(a.closeErr, a.root.Close())
			a.root = nil
		}
		if a.leaf != nil {
			a.closeErr = errors.Join(a.closeErr, a.leaf.Close())
			a.leaf = nil
		}
		if a.parent != nil {
			a.closeErr = errors.Join(a.closeErr, a.parent.Close())
			a.parent = nil
		}
	})
	return a.closeErr
}

// UserDirectoryAuthority owns descriptor-derived read-only observations for
// one effective user and one protected AI4J base-root candidate.
type UserDirectoryAuthority struct {
	state *userDirectoryAuthorityState
}

// userDirectoryAuthorityState is the only owner of mutable lifetime and proof
// state. Copies of the exported wrapper therefore remain one authority rather
// than sharing a lock while accidentally copying descriptors or closed flags.
type userDirectoryAuthorityState struct {
	lifetimeMu    sync.Mutex
	callGate      chan struct{}
	closeDone     chan struct{}
	closing       bool
	closed        bool
	closeErr      error
	operations    effectiveAccountOperations
	uid           uint32
	gid           uint32
	homeLocator   string
	protectedRoot string
	seal          lifecycle.ProofIssuerSeal
	home          absoluteDirectoryObservation
	protected     absoluteDirectoryObservation
	homeProof     lifecycle.UserHomeProof
	initialIssue  lifecycle.DirectoryQualificationIssue
	ledger        issuedProofLedger
	// afterCandidateObservation is a hostile-race test seam. Production leaves
	// it nil and performs no callback.
	afterCandidateObservation func()
	// beforeLeafIssue is a cancellation test seam. Production leaves it nil;
	// the proof and retained descriptors are never exposed outside this package.
	beforeLeafIssue func(*retainedLeafAuthority)
}

func (UserDirectoryAuthority) String() string {
	return "<darwin-user-directory-authority:redacted>"
}

func (UserDirectoryAuthority) GoString() string {
	return "<darwin-user-directory-authority:redacted>"
}

func (a UserDirectoryAuthority) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, a.String())
}

func (a UserDirectoryAuthority) MarshalText() ([]byte, error) {
	return []byte(a.String()), nil
}

func (UserDirectoryAuthority) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"user_directory_authority": "redacted"})
}

// NewUserDirectoryAuthority performs read-only startup qualification. A
// trusted-account or host-directory issue remains observable through
// InspectUserHome; malformed protected-root configuration fails construction.
func NewUserDirectoryAuthority(ctx context.Context, protectedBaseRoot string) (*UserDirectoryAuthority, error) {
	return newUserDirectoryAuthority(ctx, protectedBaseRoot, realEffectiveAccountOperations{})
}

func newUserDirectoryAuthority(
	ctx context.Context,
	protectedBaseRoot string,
	operations effectiveAccountOperations,
) (_ *UserDirectoryAuthority, resultErr error) {
	if ctx == nil || operations == nil {
		return nil, lifecycle.DirectoryObservationFailedIssue()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	protectedBaseRoot, valid := canonicalDarwinDirectoryLocator(protectedBaseRoot)
	if !valid {
		return nil, lifecycle.InvalidDirectoryLocatorIssue()
	}
	seal, err := generateIssuerSeal(operations)
	if err != nil {
		return nil, lifecycle.DirectoryObservationFailedIssue()
	}
	state := &userDirectoryAuthorityState{
		callGate: make(chan struct{}, 1), closeDone: make(chan struct{}),
		operations: operations, protectedRoot: protectedBaseRoot, seal: seal,
	}
	state.callGate <- struct{}{}
	authority := &UserDirectoryAuthority{state: state}
	defer func() {
		if resultErr != nil {
			_ = state.closeDescriptors()
		}
	}()

	uid, gid, home, issue, err := resolveEffectiveAccount(ctx, operations)
	if err != nil {
		return nil, err
	}
	state.uid = uid
	state.gid = gid
	state.homeLocator = home
	if issue.Valid() {
		state.initialIssue = issue
		return authority, nil
	}
	state.home, err = observeAbsoluteDirectory(ctx, home, uid, false, true)
	if err != nil {
		if issue := directoryIssue(err); issue.Valid() {
			state.initialIssue = issue
			return authority, nil
		}
		return nil, err
	}
	state.protected, err = observeAbsoluteDirectory(ctx, protectedBaseRoot, uid, true, true)
	if err != nil {
		if issue := directoryIssue(err); issue.Valid() {
			state.initialIssue = issue
			return authority, nil
		}
		return nil, err
	}
	state.homeProof, err = newUserHomeProof(seal, state.home)
	if err != nil {
		state.initialIssue = lifecycle.WrongDirectoryOwnerIssue()
		return authority, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return authority, nil
}

func generateIssuerSeal(operations effectiveAccountOperations) (lifecycle.ProofIssuerSeal, error) {
	for range maximumIssuerGenerationAttempts {
		value := make([]byte, 32)
		read, err := operations.ReadRandom(value)
		if err != nil || read != len(value) {
			return lifecycle.ProofIssuerSeal{}, errors.New("issuer randomness unavailable")
		}
		seal, err := lifecycle.NewProofIssuerSeal(value)
		if err == nil {
			return seal, nil
		}
	}
	return lifecycle.ProofIssuerSeal{}, errors.New("issuer randomness invalid")
}

func resolveEffectiveAccount(
	ctx context.Context,
	operations effectiveAccountOperations,
) (uint32, uint32, string, lifecycle.DirectoryQualificationIssue, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, "", lifecycle.DirectoryQualificationIssue{}, err
	}
	realUID, effectiveUID := operations.RealUID(), operations.EffectiveUID()
	realGID, effectiveGID := operations.RealGID(), operations.EffectiveGID()
	if realUID < 0 || effectiveUID < 0 || realUID != effectiveUID || uint64(effectiveUID) > math.MaxUint32 ||
		realGID < 0 || effectiveGID < 0 || realGID != effectiveGID || uint64(effectiveGID) > math.MaxUint32 {
		return 0, 0, "", lifecycle.TrustedAccountUnavailableIssue(), nil
	}
	account, err := operations.LookupUserID(strconv.Itoa(effectiveUID))
	if err != nil || account == nil {
		return 0, 0, "", lifecycle.TrustedAccountUnavailableIssue(), nil
	}
	parsedUID, err := strconv.ParseUint(account.Uid, 10, 32)
	parsedGID, gidErr := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil || gidErr != nil || parsedUID != uint64(effectiveUID) || parsedGID != uint64(effectiveGID) {
		return 0, 0, "", lifecycle.TrustedAccountUnavailableIssue(), nil
	}
	home, valid := canonicalDarwinDirectoryLocator(account.HomeDir)
	if !valid {
		return 0, 0, "", lifecycle.InvalidDirectoryLocatorIssue(), nil
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, "", lifecycle.DirectoryQualificationIssue{}, err
	}
	return uint32(effectiveUID), uint32(effectiveGID), home, lifecycle.DirectoryQualificationIssue{}, nil
}

func newUserHomeProof(
	seal lifecycle.ProofIssuerSeal,
	home absoluteDirectoryObservation,
) (lifecycle.UserHomeProof, error) {
	if !home.present || len(home.objects) < 2 {
		return lifecycle.UserHomeProof{}, errors.New("invalid home observation")
	}
	locator, err := lifecycle.NewHostDirectoryLocator(home.absolute)
	if err != nil {
		return lifecycle.UserHomeProof{}, err
	}
	return lifecycle.NewUserHomeProof(
		seal, locator, home.objects[len(home.objects)-2], home.objects[len(home.objects)-1],
	)
}

func (a *UserDirectoryAuthority) InspectUserHome(ctx context.Context) (lifecycle.UserHomeProof, error) {
	if a == nil || a.state == nil || ctx == nil {
		return lifecycle.UserHomeProof{}, lifecycle.DirectoryObservationFailedIssue()
	}
	state := a.state
	if err := state.acquireCall(ctx); err != nil {
		return lifecycle.UserHomeProof{}, err
	}
	defer state.releaseCall()
	if err := state.ready(ctx); err != nil {
		return lifecycle.UserHomeProof{}, err
	}
	if err := state.revalidate(ctx); err != nil {
		return lifecycle.UserHomeProof{}, err
	}
	if err := ctx.Err(); err != nil {
		return lifecycle.UserHomeProof{}, err
	}
	if err := state.ledger.issueHome(state.homeProof); err != nil {
		return lifecycle.UserHomeProof{}, lifecycle.DirectoryIdentityChangedIssue()
	}
	return state.homeProof, nil
}

func (a *UserDirectoryAuthority) QualifyUserDirectory(
	ctx context.Context,
	home lifecycle.UserHomeProof,
	relative pathsafe.RelativePath,
) (lifecycle.DirectoryLeafProof, error) {
	if a == nil || a.state == nil || ctx == nil || !home.Valid() || !relative.Valid() {
		return lifecycle.DirectoryLeafProof{}, lifecycle.InvalidDirectoryLocatorIssue()
	}
	state := a.state
	if err := state.acquireCall(ctx); err != nil {
		return lifecycle.DirectoryLeafProof{}, err
	}
	defer state.releaseCall()
	if err := state.ready(ctx); err != nil {
		return lifecycle.DirectoryLeafProof{}, err
	}
	if !state.ledger.containsHome(home) {
		return lifecycle.DirectoryLeafProof{}, lifecycle.DirectoryIdentityChangedIssue()
	}
	if err := state.revalidate(ctx); err != nil {
		return lifecycle.DirectoryLeafProof{}, err
	}
	candidateLocator := state.homeLocator + "/" + relative.String()
	if darwinDirectoryLocatorsOverlap(candidateLocator, state.protectedRoot) {
		return lifecycle.DirectoryLeafProof{}, lifecycle.ProtectedRootOverlapIssue()
	}
	proof, chain, retained, err := state.observeRelative(ctx, home, relative, candidateLocator)
	if err != nil {
		return lifecycle.DirectoryLeafProof{}, err
	}
	if state.afterCandidateObservation != nil {
		after := state.afterCandidateObservation
		state.afterCandidateObservation = nil
		after()
	}
	if err := state.revalidate(ctx); err != nil {
		_ = retained.Close()
		return lifecycle.DirectoryLeafProof{}, err
	}
	if err := retained.revalidate(ctx); err != nil {
		_ = retained.Close()
		return lifecycle.DirectoryLeafProof{}, err
	}
	if directoryIdentityOverlap(chain, proof.Presence() == lifecycle.PresentDirectoryLeaf(),
		state.protected.identities(), state.protected.present) {
		_ = retained.Close()
		return lifecycle.DirectoryLeafProof{}, lifecycle.ProtectedRootOverlapIssue()
	}
	if state.beforeLeafIssue != nil {
		before := state.beforeLeafIssue
		state.beforeLeafIssue = nil
		before(retained)
	}
	if err := ctx.Err(); err != nil {
		_ = retained.Close()
		return lifecycle.DirectoryLeafProof{}, err
	}
	stored, existing, err := state.ledger.issueLeaf(proof, retained)
	if err != nil {
		_ = retained.Close()
		switch err {
		case errIssuedProofChanged:
			return lifecycle.DirectoryLeafProof{}, lifecycle.DirectoryIdentityChangedIssue()
		default:
			return lifecycle.DirectoryLeafProof{}, lifecycle.DirectoryObservationFailedIssue()
		}
	}
	if existing {
		_ = retained.Close()
		existingAuthority, ok := stored.(*retainedLeafAuthority)
		if !ok {
			return lifecycle.DirectoryLeafProof{}, lifecycle.DirectoryObservationFailedIssue()
		}
		if err := existingAuthority.revalidate(ctx); err != nil {
			return lifecycle.DirectoryLeafProof{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return lifecycle.DirectoryLeafProof{}, err
	}
	return proof, nil
}

func (a *userDirectoryAuthorityState) acquireCall(ctx context.Context) error {
	if a == nil || ctx == nil || a.callGate == nil || a.closeDone == nil {
		return lifecycle.DirectoryObservationFailedIssue()
	}
	a.lifetimeMu.Lock()
	unavailable := a.closing || a.closed
	a.lifetimeMu.Unlock()
	if unavailable {
		return lifecycle.DirectoryObservationFailedIssue()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.callGate:
	}
	a.lifetimeMu.Lock()
	unavailable = a.closing || a.closed
	a.lifetimeMu.Unlock()
	if unavailable {
		a.releaseCall()
		return lifecycle.DirectoryObservationFailedIssue()
	}
	return nil
}

func (a *userDirectoryAuthorityState) releaseCall() {
	if a != nil && a.callGate != nil {
		a.callGate <- struct{}{}
	}
}

func (a *userDirectoryAuthorityState) ready(ctx context.Context) error {
	if a == nil || a.operations == nil {
		return lifecycle.DirectoryObservationFailedIssue()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.initialIssue.Valid() {
		return a.initialIssue
	}
	if !a.homeProof.Valid() || !a.seal.IssuedUserHome(a.homeProof) {
		return lifecycle.DirectoryObservationFailedIssue()
	}
	return nil
}

func (a *userDirectoryAuthorityState) revalidate(ctx context.Context) error {
	uid, gid, home, issue, err := resolveEffectiveAccount(ctx, a.operations)
	if err != nil {
		return err
	}
	if issue.Valid() {
		return issue
	}
	if uid != a.uid || gid != a.gid || home != a.homeLocator {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	if err := revalidateAbsoluteObservation(ctx, a.home, a.uid, false, true); err != nil {
		return err
	}
	if err := revalidateAbsoluteObservation(ctx, a.protected, a.uid, true, true); err != nil {
		return err
	}
	return nil
}

func (a *userDirectoryAuthorityState) observeRelative(
	ctx context.Context,
	home lifecycle.UserHomeProof,
	relative pathsafe.RelativePath,
	locatorValue string,
) (_ lifecycle.DirectoryLeafProof, _ []lifecycle.ObjectIdentity, _ *retainedLeafAuthority, resultErr error) {
	rootDuplicate, err := unix.FcntlInt(a.home.leaf.Fd(), unix.F_DUPFD_CLOEXEC, minimumAuthorityDescriptor)
	if err != nil {
		return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.DirectoryObservationFailedIssue()
	}
	retainedRoot := os.NewFile(uintptr(rootDuplicate), "<relative-proof-root>")
	if retainedRoot == nil {
		_ = unix.Close(rootDuplicate)
		return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.DirectoryObservationFailedIssue()
	}
	defer func() {
		if resultErr != nil && retainedRoot != nil {
			_ = retainedRoot.Close()
		}
	}()
	duplicate, err := unix.FcntlInt(a.home.leaf.Fd(), unix.F_DUPFD_CLOEXEC, minimumAuthorityDescriptor)
	if err != nil {
		return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.DirectoryObservationFailedIssue()
	}
	current := os.NewFile(uintptr(duplicate), a.homeLocator)
	if current == nil {
		_ = unix.Close(duplicate)
		return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.DirectoryObservationFailedIssue()
	}
	defer func() {
		if resultErr != nil && current != nil {
			_ = current.Close()
		}
	}()
	currentObject, err := directoryObjectFromOpenFile(current, a.uid)
	if err != nil || currentObject != home.Home() {
		return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.DirectoryIdentityChangedIssue()
	}
	chain := []lifecycle.ObjectIdentity{currentObject.Identity()}
	chainObjects := []lifecycle.DirectoryObjectProof{currentObject}
	components := relative.Components()
	for index, component := range components {
		if err := ctx.Err(); err != nil {
			return lifecycle.DirectoryLeafProof{}, nil, nil, err
		}
		last := index == len(components)-1
		var listed unix.Stat_t
		statErr := unix.Fstatat(int(current.Fd()), component, &listed, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(statErr, fs.ErrNotExist) || errors.Is(statErr, unix.ENOENT) {
			if !last {
				return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.MissingIntermediateIssue()
			}
			locator, locatorErr := lifecycle.NewHostDirectoryLocator(locatorValue)
			if locatorErr != nil {
				return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.InvalidDirectoryLocatorIssue()
			}
			proof, proofErr := lifecycle.NewDirectoryLeafProof(
				a.seal, home, relative, locator, lifecycle.AbsentDirectoryLeaf(),
				home.Home(), currentObject, lifecycle.DirectoryObjectProof{},
			)
			if proofErr != nil {
				return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.DirectoryObservationFailedIssue()
			}
			retained := &retainedLeafAuthority{
				uid: a.uid, root: retainedRoot, parent: current,
				components: append([]string(nil), components...), chainExpected: append([]lifecycle.DirectoryObjectProof(nil), chainObjects...),
				leafName: component, presence: lifecycle.AbsentDirectoryLeaf(), parentExpected: currentObject,
			}
			retainedRoot = nil
			current = nil
			return proof, chain, retained, nil
		}
		if statErr != nil {
			return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.DirectoryObservationFailedIssue()
		}
		if listed.Mode&unix.S_IFMT == unix.S_IFLNK {
			return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.SymlinkedDirectoryIssue()
		}
		listedFacts := fileFactsFromUnixStat(&listed)
		if listedFacts.kind != lifecycle.DirectoryResource {
			return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.WrongDirectoryTypeIssue()
		}
		listedObject, listedErr := directoryObjectFromFacts(listedFacts, a.uid)
		if listedErr != nil {
			return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.DirectoryObservationFailedIssue()
		}
		if issue := relativeDirectoryObjectIssue(listedObject, home.Home()); issue.Valid() {
			return lifecycle.DirectoryLeafProof{}, nil, nil, issue
		}
		openedFD, openErr := unix.Openat(
			int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0,
		)
		if openErr != nil {
			return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.DirectoryObservationFailedIssue()
		}
		opened := os.NewFile(uintptr(openedFD), component)
		openedObject, objectErr := directoryObjectFromOpenFile(opened, a.uid)
		if objectErr != nil || openedObject != listedObject {
			_ = opened.Close()
			return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.DirectoryIdentityChangedIssue()
		}
		chain = append(chain, openedObject.Identity())
		chainObjects = append(chainObjects, openedObject)
		if !last {
			_ = current.Close()
			current = opened
			currentObject = openedObject
			continue
		}
		locator, locatorErr := lifecycle.NewHostDirectoryLocator(locatorValue)
		if locatorErr != nil {
			_ = opened.Close()
			return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.InvalidDirectoryLocatorIssue()
		}
		proof, proofErr := lifecycle.NewDirectoryLeafProof(
			a.seal, home, relative, locator, lifecycle.PresentDirectoryLeaf(),
			home.Home(), currentObject, openedObject,
		)
		if proofErr != nil {
			_ = opened.Close()
			return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.DirectoryObservationFailedIssue()
		}
		retained := &retainedLeafAuthority{
			uid: a.uid, root: retainedRoot, parent: current, leaf: opened,
			components: append([]string(nil), components...), chainExpected: append([]lifecycle.DirectoryObjectProof(nil), chainObjects...),
			leafName: component, presence: lifecycle.PresentDirectoryLeaf(), parentExpected: currentObject, leafExpected: openedObject,
		}
		retainedRoot = nil
		current = nil
		return proof, chain, retained, nil
	}
	return lifecycle.DirectoryLeafProof{}, nil, nil, lifecycle.InvalidDirectoryLocatorIssue()
}

func (a *UserDirectoryAuthority) Close() error {
	if a == nil || a.state == nil {
		return nil
	}
	return a.state.close()
}

func (a *userDirectoryAuthorityState) close() error {
	if a == nil {
		return nil
	}
	a.lifetimeMu.Lock()
	if a.closed {
		result := a.closeErr
		a.lifetimeMu.Unlock()
		return result
	}
	if a.closing {
		done := a.closeDone
		a.lifetimeMu.Unlock()
		<-done
		a.lifetimeMu.Lock()
		result := a.closeErr
		a.lifetimeMu.Unlock()
		return result
	}
	a.closing = true
	done := a.closeDone
	a.lifetimeMu.Unlock()

	<-a.callGate
	if err := a.closeDescriptors(); err != nil {
		a.closeErr = errUserDirectoryAuthorityClose
	}
	a.lifetimeMu.Lock()
	a.closed = true
	a.closing = false
	close(done)
	result := a.closeErr
	a.lifetimeMu.Unlock()
	a.callGate <- struct{}{}
	return result
}

func (a *userDirectoryAuthorityState) closeDescriptors() error {
	if a == nil {
		return nil
	}
	return errors.Join(a.ledger.close(), a.home.close(), a.protected.close())
}

func observeAbsoluteDirectory(
	ctx context.Context,
	absolute string,
	uid uint32,
	allowMissing bool,
	requireCurrentUserFinal bool,
) (_ absoluteDirectoryObservation, resultErr error) {
	absolute, valid := canonicalDarwinDirectoryLocator(absolute)
	if ctx == nil || !valid {
		return absoluteDirectoryObservation{}, lifecycle.InvalidDirectoryLocatorIssue()
	}
	rootFD, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return absoluteDirectoryObservation{}, lifecycle.DirectoryObservationFailedIssue()
	}
	current := os.NewFile(uintptr(rootFD), "/")
	if current == nil {
		_ = unix.Close(rootFD)
		return absoluteDirectoryObservation{}, lifecycle.DirectoryObservationFailedIssue()
	}
	defer func() {
		if resultErr != nil && current != nil {
			_ = current.Close()
		}
	}()
	rootFacts, err := inspectOpenFile(current)
	if err != nil || !safeAuthorityAncestor(rootFacts, uid) {
		return absoluteDirectoryObservation{}, lifecycle.DirectoryObservationFailedIssue()
	}
	rootObject, err := directoryObjectFromFacts(rootFacts, uid)
	if err != nil {
		return absoluteDirectoryObservation{}, lifecycle.DirectoryObservationFailedIssue()
	}
	objects := []lifecycle.DirectoryObjectProof{rootObject}
	components := strings.Split(strings.TrimPrefix(absolute, "/"), "/")
	for index, component := range components {
		if err := ctx.Err(); err != nil {
			return absoluteDirectoryObservation{}, err
		}
		last := index == len(components)-1
		var listed unix.Stat_t
		statErr := unix.Fstatat(int(current.Fd()), component, &listed, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(statErr, fs.ErrNotExist) || errors.Is(statErr, unix.ENOENT) {
			if !allowMissing {
				return absoluteDirectoryObservation{}, lifecycle.MissingIntermediateIssue()
			}
			if err := ctx.Err(); err != nil {
				return absoluteDirectoryObservation{}, err
			}
			result := absoluteDirectoryObservation{
				absolute: absolute, objects: objects, missingIndex: index, parent: current,
			}
			current = nil
			return result, nil
		}
		if statErr != nil {
			return absoluteDirectoryObservation{}, lifecycle.DirectoryObservationFailedIssue()
		}
		if listed.Mode&unix.S_IFMT == unix.S_IFLNK {
			return absoluteDirectoryObservation{}, lifecycle.SymlinkedDirectoryIssue()
		}
		listedFacts := fileFactsFromUnixStat(&listed)
		if listedFacts.kind != lifecycle.DirectoryResource {
			return absoluteDirectoryObservation{}, lifecycle.WrongDirectoryTypeIssue()
		}
		if last && requireCurrentUserFinal {
			if listedFacts.uid != uid {
				return absoluteDirectoryObservation{}, lifecycle.WrongDirectoryOwnerIssue()
			}
			if !safeWritableDirectory(listedFacts, uid) {
				return absoluteDirectoryObservation{}, lifecycle.UnsafeDirectoryModeIssue()
			}
		} else if !safeAuthorityAncestor(listedFacts, uid) {
			if listedFacts.uid != 0 && listedFacts.uid != uid {
				return absoluteDirectoryObservation{}, lifecycle.WrongDirectoryOwnerIssue()
			}
			return absoluteDirectoryObservation{}, lifecycle.UnsafeDirectoryModeIssue()
		}
		openedFD, openErr := unix.Openat(
			int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0,
		)
		if openErr != nil {
			return absoluteDirectoryObservation{}, lifecycle.DirectoryObservationFailedIssue()
		}
		opened := os.NewFile(uintptr(openedFD), path.Join("/", strings.Join(components[:index+1], "/")))
		openedFacts, inspectErr := inspectOpenFile(opened)
		if inspectErr != nil || openedFacts.identity != listedFacts.identity || openedFacts.mode != listedFacts.mode ||
			openedFacts.uid != listedFacts.uid || openedFacts.kind != listedFacts.kind {
			_ = opened.Close()
			return absoluteDirectoryObservation{}, lifecycle.DirectoryIdentityChangedIssue()
		}
		openedObject, objectErr := directoryObjectFromFacts(openedFacts, uid)
		if objectErr != nil {
			_ = opened.Close()
			return absoluteDirectoryObservation{}, lifecycle.DirectoryObservationFailedIssue()
		}
		objects = append(objects, openedObject)
		if err := ctx.Err(); err != nil {
			_ = opened.Close()
			return absoluteDirectoryObservation{}, err
		}
		if !last {
			_ = current.Close()
			current = opened
			continue
		}
		result := absoluteDirectoryObservation{
			absolute: absolute, objects: objects, present: true, missingIndex: -1,
			parent: current, leaf: opened,
		}
		current = nil
		return result, nil
	}
	return absoluteDirectoryObservation{}, lifecycle.DirectoryObservationFailedIssue()
}

func revalidateAbsoluteObservation(
	ctx context.Context,
	expected absoluteDirectoryObservation,
	uid uint32,
	allowMissing bool,
	requireCurrentUserFinal bool,
) error {
	if err := revalidateRetainedAbsolute(expected, uid); err != nil {
		return err
	}
	fresh, err := observeAbsoluteDirectory(ctx, expected.absolute, uid, allowMissing, requireCurrentUserFinal)
	if err != nil {
		return err
	}
	defer fresh.close()
	if !expected.sameFacts(fresh) {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func revalidateRetainedAbsolute(expected absoluteDirectoryObservation, uid uint32) error {
	if expected.parent == nil || len(expected.objects) == 0 {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	parentObject, err := directoryObjectFromOpenFile(expected.parent, uid)
	if err != nil {
		return lifecycle.DirectoryObservationFailedIssue()
	}
	parentIndex := len(expected.objects) - 1
	if expected.present {
		parentIndex--
	}
	if parentIndex < 0 || parentObject != expected.objects[parentIndex] {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	components := strings.Split(strings.TrimPrefix(expected.absolute, "/"), "/")
	leafIndex := len(components) - 1
	if !expected.present {
		leafIndex = expected.missingIndex
	}
	if leafIndex < 0 || leafIndex >= len(components) {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	leafName := components[leafIndex]
	var listed unix.Stat_t
	err = unix.Fstatat(int(expected.parent.Fd()), leafName, &listed, unix.AT_SYMLINK_NOFOLLOW)
	if !expected.present {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOENT) {
			return nil
		}
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	if err != nil || listed.Mode&unix.S_IFMT == unix.S_IFLNK || expected.leaf == nil {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	listedObject, err := directoryObjectFromUnixStat(&listed, uid)
	if err != nil || listedObject != expected.objects[len(expected.objects)-1] {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	leafObject, err := directoryObjectFromOpenFile(expected.leaf, uid)
	if err != nil || leafObject != expected.objects[len(expected.objects)-1] {
		return lifecycle.DirectoryIdentityChangedIssue()
	}
	return nil
}

func directoryObjectFromOpenFile(file *os.File, currentUID uint32) (lifecycle.DirectoryObjectProof, error) {
	facts, err := inspectOpenFile(file)
	if err != nil {
		return lifecycle.DirectoryObjectProof{}, err
	}
	return directoryObjectFromFacts(facts, currentUID)
}

func directoryObjectFromUnixStat(stat *unix.Stat_t, currentUID uint32) (lifecycle.DirectoryObjectProof, error) {
	return directoryObjectFromFacts(fileFactsFromUnixStat(stat), currentUID)
}

func directoryObjectFromFacts(facts fileFacts, currentUID uint32) (lifecycle.DirectoryObjectProof, error) {
	return lifecycle.NewDirectoryObjectProof(facts.identity, classifyOwner(facts.uid, currentUID), facts.mode)
}

func directoryIssue(err error) lifecycle.DirectoryQualificationIssue {
	var issue lifecycle.DirectoryQualificationIssue
	if errors.As(err, &issue) && issue.Valid() {
		return issue
	}
	return lifecycle.DirectoryQualificationIssue{}
}
