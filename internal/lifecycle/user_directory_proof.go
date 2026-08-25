package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/alx4j/ai4j/internal/pathsafe"
)

const proofIssuerSealBytes = 32

var (
	errInvalidProofIssuerSeal        = errors.New("invalid directory proof issuer seal")
	errInvalidHostDirectoryLocator   = errors.New("invalid host directory locator")
	errInvalidDirectoryObjectProof   = errors.New("invalid directory object proof")
	errInvalidUserHomeProof          = errors.New("invalid user home proof")
	errInvalidDirectoryLeafPresence  = errors.New("invalid directory leaf presence")
	errInvalidDirectoryLeafProof     = errors.New("invalid directory leaf proof")
	errInvalidDirectoryQualification = errors.New("invalid directory qualification issue")
	ErrDirectoryQualification        = errors.New("directory qualification issue")
)

// ProofIssuerSeal binds observations to one live host issuer. It is a
// correlation secret, not filesystem authority. Constructors validate only
// structure; the issuer must retain descriptors and revalidate every proof.
type ProofIssuerSeal struct{ value [proofIssuerSealBytes]byte }

// NewProofIssuerSeal copies one fixed-size nonzero issuer secret.
func NewProofIssuerSeal(value []byte) (ProofIssuerSeal, error) {
	if len(value) != proofIssuerSealBytes {
		return ProofIssuerSeal{}, errInvalidProofIssuerSeal
	}
	var result ProofIssuerSeal
	copy(result.value[:], value)
	if !result.Valid() {
		return ProofIssuerSeal{}, errInvalidProofIssuerSeal
	}
	return result, nil
}

// Valid reports whether the seal contains the exact nonzero issuer width.
func (s ProofIssuerSeal) Valid() bool {
	var zero [proofIssuerSealBytes]byte
	return s.value != zero
}

// IssuedUserHome reports only issuer correlation. It establishes no current
// filesystem fact and must never replace descriptor revalidation.
func (s ProofIssuerSeal) IssuedUserHome(proof UserHomeProof) bool {
	return s.Valid() && proof.Valid() && proof.issuer == s.value
}

// IssuedDirectoryLeaf reports only issuer correlation. It establishes no
// current filesystem fact and must never replace descriptor revalidation.
func (s ProofIssuerSeal) IssuedDirectoryLeaf(proof DirectoryLeafProof) bool {
	return s.Valid() && proof.Valid() && proof.issuer == s.value
}

func (s ProofIssuerSeal) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<directory-proof-issuer:redacted>")
}

func (s ProofIssuerSeal) MarshalText() ([]byte, error) {
	if !s.Valid() {
		return nil, errInvalidProofIssuerSeal
	}
	return []byte("<directory-proof-issuer:redacted>"), nil
}

func (s ProofIssuerSeal) MarshalJSON() ([]byte, error) {
	if !s.Valid() {
		return nil, errInvalidProofIssuerSeal
	}
	return []byte(`{"issuer":"redacted"}`), nil
}

// HostDirectoryLocator preserves one bounded opaque host spelling. Neutral
// code deliberately does not decide whether it is absolute, canonical, POSIX,
// or Windows syntax; the selected host adapter owns those decisions.
type HostDirectoryLocator struct{ value string }

// NewHostDirectoryLocator copies one bounded opaque locator.
func NewHostDirectoryLocator(value string) (HostDirectoryLocator, error) {
	if !validHostLocator(value) {
		return HostDirectoryLocator{}, errInvalidHostDirectoryLocator
	}
	return HostDirectoryLocator{value: strings.Clone(value)}, nil
}

// Value returns a copy of the opaque spelling for an explicit host or target
// boundary. Generic formatting remains redacted.
func (l HostDirectoryLocator) Value() string { return strings.Clone(l.value) }

func (l HostDirectoryLocator) Valid() bool {
	validated, err := NewHostDirectoryLocator(l.value)
	return err == nil && validated == l
}

func (l HostDirectoryLocator) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<host-directory-locator:redacted>")
}

func (l HostDirectoryLocator) MarshalText() ([]byte, error) {
	if !l.Valid() {
		return nil, errInvalidHostDirectoryLocator
	}
	return []byte("<host-directory-locator:redacted>"), nil
}

func (l HostDirectoryLocator) MarshalJSON() ([]byte, error) {
	if !l.Valid() {
		return nil, errInvalidHostDirectoryLocator
	}
	return []byte(`{"locator":"redacted"}`), nil
}

// DirectoryObjectProof is an immutable observation of one opened directory.
// It carries no locator or descriptor and grants no authority by itself.
type DirectoryObjectProof struct {
	identity ObjectIdentity
	owner    OwnerClass
	mode     fs.FileMode
}

// NewDirectoryObjectProof constructs one normalized directory observation.
func NewDirectoryObjectProof(identity ObjectIdentity, owner OwnerClass, mode fs.FileMode) (DirectoryObjectProof, error) {
	result := DirectoryObjectProof{identity: identity, owner: owner, mode: mode}
	if !result.Valid() {
		return DirectoryObjectProof{}, errInvalidDirectoryObjectProof
	}
	return result, nil
}

func (p DirectoryObjectProof) Identity() ObjectIdentity { return p.identity }
func (p DirectoryObjectProof) OwnerClass() OwnerClass   { return p.owner }
func (p DirectoryObjectProof) Mode() fs.FileMode        { return p.mode }

func (p DirectoryObjectProof) Valid() bool {
	return p.identity.Valid() && validOwnerClass(p.owner) && normalizedDirectoryMode(p.mode)
}

func (p DirectoryObjectProof) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<directory-object-proof:redacted>")
}

func (p DirectoryObjectProof) MarshalText() ([]byte, error) {
	if !p.Valid() {
		return nil, errInvalidDirectoryObjectProof
	}
	return []byte("<directory-object-proof:redacted>"), nil
}

func (p DirectoryObjectProof) MarshalJSON() ([]byte, error) {
	if !p.Valid() {
		return nil, errInvalidDirectoryObjectProof
	}
	return []byte(`{"directory":"redacted"}`), nil
}

// UserHomeProof is a descriptor-derived observation issued by one Bootstrap.
// The complete ancestor chain and all open descriptors remain issuer-private.
type UserHomeProof struct {
	issuer  [proofIssuerSealBytes]byte
	locator HostDirectoryLocator
	parent  DirectoryObjectProof
	home    DirectoryObjectProof
}

// NewUserHomeProof binds an exact home observation to one issuer seal.
func NewUserHomeProof(
	issuer ProofIssuerSeal,
	locator HostDirectoryLocator,
	parent DirectoryObjectProof,
	home DirectoryObjectProof,
) (UserHomeProof, error) {
	result := UserHomeProof{issuer: issuer.value, locator: locator, parent: parent, home: home}
	if !issuer.Valid() || !result.Valid() {
		return UserHomeProof{}, errInvalidUserHomeProof
	}
	return result, nil
}

func (p UserHomeProof) Locator() HostDirectoryLocator { return p.locator }
func (p UserHomeProof) Parent() DirectoryObjectProof  { return p.parent }
func (p UserHomeProof) Home() DirectoryObjectProof    { return p.home }

func (p UserHomeProof) Valid() bool {
	var issuer ProofIssuerSeal
	issuer.value = p.issuer
	return issuer.Valid() && p.locator.Valid() && trustedAuthorityDirectory(p.parent) &&
		safeCurrentUserDirectory(p.home) && p.parent.identity != p.home.identity
}

func (p UserHomeProof) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<user-home-proof:redacted>")
}

func (p UserHomeProof) MarshalText() ([]byte, error) {
	if !p.Valid() {
		return nil, errInvalidUserHomeProof
	}
	return []byte("<user-home-proof:redacted>"), nil
}

func (p UserHomeProof) MarshalJSON() ([]byte, error) {
	if !p.Valid() {
		return nil, errInvalidUserHomeProof
	}
	return []byte(`{"user_home":"redacted"}`), nil
}

// DirectoryLeafPresence distinguishes a present opened directory from a
// safely observed absent final leaf. An absent intermediate is never a proof.
type DirectoryLeafPresence struct{ value uint8 }

var (
	presentDirectoryLeaf = DirectoryLeafPresence{value: 1}
	absentDirectoryLeaf  = DirectoryLeafPresence{value: 2}
)

func PresentDirectoryLeaf() DirectoryLeafPresence { return presentDirectoryLeaf }
func AbsentDirectoryLeaf() DirectoryLeafPresence  { return absentDirectoryLeaf }

func NewDirectoryLeafPresence(value string) (DirectoryLeafPresence, error) {
	switch value {
	case "present":
		return presentDirectoryLeaf, nil
	case "absent":
		return absentDirectoryLeaf, nil
	default:
		return DirectoryLeafPresence{}, errInvalidDirectoryLeafPresence
	}
}

func (p DirectoryLeafPresence) String() string {
	switch p {
	case presentDirectoryLeaf:
		return "present"
	case absentDirectoryLeaf:
		return "absent"
	default:
		return "invalid"
	}
}

func (p DirectoryLeafPresence) Valid() bool {
	return p == presentDirectoryLeaf || p == absentDirectoryLeaf
}

// DirectoryLeafProof binds one typed home-relative request to the issuing home
// proof. Present leaves retain exact opened facts; absent leaves retain only
// root and final-parent facts and must keep the leaf fact zero.
type DirectoryLeafProof struct {
	issuer   [proofIssuerSealBytes]byte
	home     UserHomeProof
	relative pathsafe.RelativePath
	locator  HostDirectoryLocator
	presence DirectoryLeafPresence
	root     DirectoryObjectProof
	parent   DirectoryObjectProof
	leaf     DirectoryObjectProof
}

// NewDirectoryLeafProof constructs one structurally coherent proof issued by
// the same seal as home. It does not establish current filesystem authority.
func NewDirectoryLeafProof(
	issuer ProofIssuerSeal,
	home UserHomeProof,
	relative pathsafe.RelativePath,
	locator HostDirectoryLocator,
	presence DirectoryLeafPresence,
	root DirectoryObjectProof,
	parent DirectoryObjectProof,
	leaf DirectoryObjectProof,
) (DirectoryLeafProof, error) {
	result := DirectoryLeafProof{
		issuer: issuer.value, home: home, relative: relative, locator: locator,
		presence: presence, root: root, parent: parent, leaf: leaf,
	}
	if !issuer.IssuedUserHome(home) || !result.Valid() {
		return DirectoryLeafProof{}, errInvalidDirectoryLeafProof
	}
	return result, nil
}

func (p DirectoryLeafProof) HomeProof() UserHomeProof            { return p.home }
func (p DirectoryLeafProof) RelativePath() pathsafe.RelativePath { return p.relative }
func (p DirectoryLeafProof) Locator() HostDirectoryLocator       { return p.locator }
func (p DirectoryLeafProof) Presence() DirectoryLeafPresence     { return p.presence }
func (p DirectoryLeafProof) Root() DirectoryObjectProof          { return p.root }
func (p DirectoryLeafProof) Parent() DirectoryObjectProof        { return p.parent }

// Leaf returns the present leaf fact. An absent leaf returns false and a zero
// fact so callers cannot accidentally assign ownership to a missing object.
func (p DirectoryLeafProof) Leaf() (DirectoryObjectProof, bool) {
	if p.presence != presentDirectoryLeaf {
		return DirectoryObjectProof{}, false
	}
	return p.leaf, p.leaf.Valid()
}

func (p DirectoryLeafProof) Valid() bool {
	if !p.home.Valid() || p.issuer != p.home.issuer || !p.relative.Valid() || !p.locator.Valid() ||
		!p.presence.Valid() || p.root != p.home.home || !safeCurrentUserDirectory(p.root) ||
		!safeCurrentUserDirectory(p.parent) || p.parent.identity.Filesystem != p.root.identity.Filesystem ||
		!coherentDirectoryParent(p.relative, p.root, p.parent) {
		return false
	}
	switch p.presence {
	case presentDirectoryLeaf:
		return safeCurrentUserDirectory(p.leaf) && p.leaf.identity.Filesystem == p.root.identity.Filesystem &&
			p.leaf.identity != p.parent.identity && p.leaf.identity != p.root.identity
	case absentDirectoryLeaf:
		return p.leaf == (DirectoryObjectProof{})
	default:
		return false
	}
}

func coherentDirectoryParent(relative pathsafe.RelativePath, root, parent DirectoryObjectProof) bool {
	components := relative.Components()
	if len(components) == 1 {
		return parent.identity == root.identity
	}
	return len(components) > 1 && parent.identity != root.identity
}

func (p DirectoryLeafProof) Format(state fmt.State, _ rune) {
	presence := p.presence.String()
	_, _ = io.WriteString(state, "<directory-leaf-proof:"+presence+":redacted>")
}

func (p DirectoryLeafProof) MarshalText() ([]byte, error) {
	if !p.Valid() {
		return nil, errInvalidDirectoryLeafProof
	}
	return []byte(fmt.Sprintf("%v", p)), nil
}

func (p DirectoryLeafProof) MarshalJSON() ([]byte, error) {
	if !p.Valid() {
		return nil, errInvalidDirectoryLeafProof
	}
	return json.Marshal(struct {
		Presence string `json:"presence"`
		Proof    string `json:"proof"`
	}{Presence: p.presence.String(), Proof: "redacted"})
}

// DirectoryQualificationIssue is a closed, path-free host qualification
// result. Stable policy/safety issues are distinct from operational races and
// observation failures so target code can fail closed without misclassification.
type DirectoryQualificationIssue struct{ value uint8 }

var (
	trustedAccountUnavailableIssue  = DirectoryQualificationIssue{value: 1}
	invalidDirectoryLocatorIssue    = DirectoryQualificationIssue{value: 2}
	missingIntermediateIssue        = DirectoryQualificationIssue{value: 3}
	symlinkedDirectoryIssue         = DirectoryQualificationIssue{value: 4}
	wrongDirectoryTypeIssue         = DirectoryQualificationIssue{value: 5}
	wrongDirectoryOwnerIssue        = DirectoryQualificationIssue{value: 6}
	unsafeDirectoryModeIssue        = DirectoryQualificationIssue{value: 7}
	unsupportedFilesystemIssue      = DirectoryQualificationIssue{value: 8}
	protectedRootOverlapIssue       = DirectoryQualificationIssue{value: 9}
	directoryIdentityChangedIssue   = DirectoryQualificationIssue{value: 10}
	directoryObservationFailedIssue = DirectoryQualificationIssue{value: 11}
)

func TrustedAccountUnavailableIssue() DirectoryQualificationIssue {
	return trustedAccountUnavailableIssue
}
func InvalidDirectoryLocatorIssue() DirectoryQualificationIssue { return invalidDirectoryLocatorIssue }
func MissingIntermediateIssue() DirectoryQualificationIssue     { return missingIntermediateIssue }
func SymlinkedDirectoryIssue() DirectoryQualificationIssue      { return symlinkedDirectoryIssue }
func WrongDirectoryTypeIssue() DirectoryQualificationIssue      { return wrongDirectoryTypeIssue }
func WrongDirectoryOwnerIssue() DirectoryQualificationIssue     { return wrongDirectoryOwnerIssue }
func UnsafeDirectoryModeIssue() DirectoryQualificationIssue     { return unsafeDirectoryModeIssue }
func UnsupportedFilesystemIssue() DirectoryQualificationIssue   { return unsupportedFilesystemIssue }
func ProtectedRootOverlapIssue() DirectoryQualificationIssue    { return protectedRootOverlapIssue }
func DirectoryIdentityChangedIssue() DirectoryQualificationIssue {
	return directoryIdentityChangedIssue
}
func DirectoryObservationFailedIssue() DirectoryQualificationIssue {
	return directoryObservationFailedIssue
}

func NewDirectoryQualificationIssue(value string) (DirectoryQualificationIssue, error) {
	for _, issue := range allDirectoryQualificationIssues() {
		if issue.String() == value {
			return issue, nil
		}
	}
	return DirectoryQualificationIssue{}, errInvalidDirectoryQualification
}

func (i DirectoryQualificationIssue) String() string {
	switch i {
	case trustedAccountUnavailableIssue:
		return "trusted_account_unavailable"
	case invalidDirectoryLocatorIssue:
		return "invalid_locator"
	case missingIntermediateIssue:
		return "missing_intermediate"
	case symlinkedDirectoryIssue:
		return "symlinked"
	case wrongDirectoryTypeIssue:
		return "wrong_type"
	case wrongDirectoryOwnerIssue:
		return "wrong_owner"
	case unsafeDirectoryModeIssue:
		return "unsafe_mode"
	case unsupportedFilesystemIssue:
		return "unsupported_filesystem"
	case protectedRootOverlapIssue:
		return "protected_root_overlap"
	case directoryIdentityChangedIssue:
		return "identity_changed"
	case directoryObservationFailedIssue:
		return "observation_failed"
	default:
		return "invalid"
	}
}

func (i DirectoryQualificationIssue) Valid() bool {
	for _, issue := range allDirectoryQualificationIssues() {
		if i == issue {
			return true
		}
	}
	return false
}

func (i DirectoryQualificationIssue) Error() string {
	if !i.Valid() {
		return "invalid_directory_qualification_issue"
	}
	return "directory_qualification:" + i.String()
}

func (i DirectoryQualificationIssue) Is(target error) bool {
	return i.Valid() && target == ErrDirectoryQualification
}

func (i DirectoryQualificationIssue) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, i.Error())
}

func (i DirectoryQualificationIssue) MarshalText() ([]byte, error) {
	if !i.Valid() {
		return nil, errInvalidDirectoryQualification
	}
	return []byte(i.String()), nil
}

func (i DirectoryQualificationIssue) MarshalJSON() ([]byte, error) {
	if !i.Valid() {
		return nil, errInvalidDirectoryQualification
	}
	return json.Marshal(struct {
		Issue string `json:"issue"`
	}{Issue: i.String()})
}

func allDirectoryQualificationIssues() []DirectoryQualificationIssue {
	return []DirectoryQualificationIssue{
		trustedAccountUnavailableIssue, invalidDirectoryLocatorIssue, missingIntermediateIssue,
		symlinkedDirectoryIssue, wrongDirectoryTypeIssue, wrongDirectoryOwnerIssue,
		unsafeDirectoryModeIssue, unsupportedFilesystemIssue, protectedRootOverlapIssue,
		directoryIdentityChangedIssue, directoryObservationFailedIssue,
	}
}

func validOwnerClass(owner OwnerClass) bool {
	switch owner {
	case CurrentUserOwner, SystemOwner, OtherOwner:
		return true
	default:
		return false
	}
}

func normalizedDirectoryMode(mode fs.FileMode) bool {
	const special = fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky
	return mode != 0 && mode == mode.Perm()|mode&special
}

func safeCurrentUserDirectory(proof DirectoryObjectProof) bool {
	return proof.Valid() && proof.owner == CurrentUserOwner && proof.mode.Perm()&0o700 == 0o700 &&
		proof.mode.Perm()&0o022 == 0 && proof.mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) == 0
}

func trustedAuthorityDirectory(proof DirectoryObjectProof) bool {
	if !proof.Valid() || proof.owner != CurrentUserOwner && proof.owner != SystemOwner {
		return false
	}
	const special = fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky
	if proof.mode.Perm()&0o022 == 0 {
		return proof.mode&special == 0
	}
	return proof.owner == SystemOwner && proof.mode.Perm()&0o002 != 0 && proof.mode&special == fs.ModeSticky
}
