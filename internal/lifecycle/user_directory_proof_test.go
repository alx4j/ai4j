package lifecycle_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/pathsafe"
)

const proofPathCanary = "/Users/proof-secret-canary"

func TestProofIssuerSealCopiesAndCorrelatesWithoutDisclosure(t *testing.T) {
	t.Parallel()

	bytesOne := bytes.Repeat([]byte{0x31}, 32)
	sealOne, err := lifecycle.NewProofIssuerSeal(bytesOne)
	if err != nil {
		t.Fatal(err)
	}
	bytesOne[0] = 0x41
	sealOneAgain, err := lifecycle.NewProofIssuerSeal(bytes.Repeat([]byte{0x31}, 32))
	if err != nil || sealOne != sealOneAgain {
		t.Fatalf("copied seal changed: %v, %v", sealOne == sealOneAgain, err)
	}
	sealTwo := mustSeal(t, 0x32)
	home := mustHomeProof(t, sealOne)
	leaf := mustLeafProof(t, sealOne, home, lifecycle.PresentDirectoryLeaf(), mustObject(t, 4, lifecycle.CurrentUserOwner, 0o700))
	if !sealOne.IssuedUserHome(home) || !sealOne.IssuedDirectoryLeaf(leaf) ||
		sealTwo.IssuedUserHome(home) || sealTwo.IssuedDirectoryLeaf(leaf) {
		t.Fatal("issuer correlation mismatch")
	}

	for _, invalid := range [][]byte{nil, make([]byte, 31), make([]byte, 32), make([]byte, 33)} {
		if got, sealErr := lifecycle.NewProofIssuerSeal(invalid); sealErr == nil || got.Valid() {
			t.Fatalf("invalid issuer accepted: length=%d", len(invalid))
		}
	}
	assertRedacted(t, sealOne, "31", proofPathCanary)
	if _, err := json.Marshal(lifecycle.ProofIssuerSeal{}); err == nil {
		t.Fatal("zero issuer serialized")
	}
}

func TestHostDirectoryLocatorIsOpaqueBoundedAndRedacted(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		proofPathCanary,
		`C:\Users\proof-secret-canary`,
		"relative-but-host-opaque",
	} {
		locator, err := lifecycle.NewHostDirectoryLocator(value)
		if err != nil || !locator.Valid() || locator.Value() != value {
			t.Fatalf("opaque locator %q = %v, %v", value, locator, err)
		}
		assertRedacted(t, locator, value)
	}

	for _, value := range []string{
		"",
		"contains\x00nul",
		"contains\ncontrol",
		string([]byte{0xff}),
		strings.Repeat("x", 4097),
	} {
		if locator, err := lifecycle.NewHostDirectoryLocator(value); err == nil || locator.Valid() {
			t.Fatalf("invalid locator accepted: length=%d", len(value))
		}
	}
	if _, err := json.Marshal(lifecycle.HostDirectoryLocator{}); err == nil {
		t.Fatal("zero locator serialized")
	}
}

func TestDirectoryObjectProofRejectsMalformedFacts(t *testing.T) {
	t.Parallel()

	valid := mustObject(t, 7, lifecycle.CurrentUserOwner, 0o700)
	if valid.Identity() != (lifecycle.ObjectIdentity{Filesystem: 1, Object: 7}) ||
		valid.OwnerClass() != lifecycle.CurrentUserOwner || valid.Mode() != 0o700 {
		t.Fatal("directory fact accessors changed")
	}
	assertRedacted(t, valid, "7", proofPathCanary)

	tests := []struct {
		identity lifecycle.ObjectIdentity
		owner    lifecycle.OwnerClass
		mode     fs.FileMode
	}{
		{owner: lifecycle.CurrentUserOwner, mode: 0o700},
		{identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 1}, mode: 0o700},
		{identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 1}, owner: lifecycle.OwnerClass("unknown"), mode: 0o700},
		{identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 1}, owner: lifecycle.CurrentUserOwner},
		{identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 1}, owner: lifecycle.CurrentUserOwner, mode: fs.ModeDir | 0o700},
	}
	for _, test := range tests {
		if proof, err := lifecycle.NewDirectoryObjectProof(test.identity, test.owner, test.mode); err == nil || proof.Valid() {
			t.Fatalf("invalid object proof accepted: %+v", test)
		}
	}
}

func TestUserHomeProofRequiresTrustedParentAndSafeCurrentUserHome(t *testing.T) {
	t.Parallel()

	seal := mustSeal(t, 0x51)
	valid := mustHomeProof(t, seal)
	if valid.Locator().Value() != proofPathCanary || valid.Parent().Identity().Object != 2 || valid.Home().Identity().Object != 3 {
		t.Fatal("home proof accessors changed")
	}
	assertRedacted(t, valid, proofPathCanary, "51", "2", "3")

	locator := mustLocator(t, proofPathCanary)
	trustedParent := mustObject(t, 2, lifecycle.SystemOwner, 0o755)
	safeHome := mustObject(t, 3, lifecycle.CurrentUserOwner, 0o700)
	tests := []struct {
		name   string
		parent lifecycle.DirectoryObjectProof
		home   lifecycle.DirectoryObjectProof
	}{
		{name: "other parent", parent: mustObject(t, 2, lifecycle.OtherOwner, 0o755), home: safeHome},
		{name: "writable parent", parent: mustObject(t, 2, lifecycle.SystemOwner, 0o777), home: safeHome},
		{name: "system home", parent: trustedParent, home: mustObject(t, 3, lifecycle.SystemOwner, 0o700)},
		{name: "unwritable home", parent: trustedParent, home: mustObject(t, 3, lifecycle.CurrentUserOwner, 0o500)},
		{name: "group writable home", parent: trustedParent, home: mustObject(t, 3, lifecycle.CurrentUserOwner, 0o720)},
		{name: "privileged home", parent: trustedParent, home: mustObject(t, 3, lifecycle.CurrentUserOwner, fs.ModeSetgid|0o700)},
		{name: "same parent and home", parent: safeHome, home: safeHome},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if proof, err := lifecycle.NewUserHomeProof(seal, locator, test.parent, test.home); err == nil || proof.Valid() {
				t.Fatal("unsafe home proof accepted")
			}
		})
	}
}

func TestDirectoryLeafProofEnforcesPresentAndAbsentOwnershipFacts(t *testing.T) {
	t.Parallel()

	seal := mustSeal(t, 0x61)
	home := mustHomeProof(t, seal)
	presentLeaf := mustObject(t, 4, lifecycle.CurrentUserOwner, 0o700)
	present := mustLeafProof(t, seal, home, lifecycle.PresentDirectoryLeaf(), presentLeaf)
	absent := mustLeafProof(t, seal, home, lifecycle.AbsentDirectoryLeaf(), lifecycle.DirectoryObjectProof{})
	if leaf, ok := present.Leaf(); !ok || leaf != presentLeaf {
		t.Fatal("present leaf fact missing")
	}
	if leaf, ok := absent.Leaf(); ok || leaf != (lifecycle.DirectoryObjectProof{}) {
		t.Fatal("absent leaf acquired ownership facts")
	}
	for _, proof := range []lifecycle.DirectoryLeafProof{present, absent} {
		if proof.HomeProof() != home || proof.Root() != home.Home() || proof.Parent().Identity().Object != 3 ||
			proof.RelativePath().String() != ".claude" || proof.Locator().Value() != proofPathCanary+"/.claude" {
			t.Fatal("leaf proof relationship changed")
		}
		assertRedacted(t, proof, proofPathCanary, "61")
	}

	relative := mustRelative(t, ".claude")
	locator := mustLocator(t, proofPathCanary+"/.claude")
	root := home.Home()
	parent := home.Home()
	otherSeal := mustSeal(t, 0x62)
	tests := []struct {
		name     string
		issuer   lifecycle.ProofIssuerSeal
		home     lifecycle.UserHomeProof
		presence lifecycle.DirectoryLeafPresence
		root     lifecycle.DirectoryObjectProof
		parent   lifecycle.DirectoryObjectProof
		leaf     lifecycle.DirectoryObjectProof
	}{
		{name: "wrong issuer", issuer: otherSeal, home: home, presence: lifecycle.PresentDirectoryLeaf(), root: root, parent: parent, leaf: presentLeaf},
		{name: "zero presence", issuer: seal, home: home, root: root, parent: parent, leaf: presentLeaf},
		{name: "wrong root", issuer: seal, home: home, presence: lifecycle.PresentDirectoryLeaf(), root: presentLeaf, parent: parent, leaf: presentLeaf},
		{name: "cross-filesystem parent", issuer: seal, home: home, presence: lifecycle.PresentDirectoryLeaf(), root: root, parent: mustObjectOnFilesystem(t, 2, 3, lifecycle.CurrentUserOwner, 0o700), leaf: presentLeaf},
		{name: "unsafe parent", issuer: seal, home: home, presence: lifecycle.PresentDirectoryLeaf(), root: root, parent: mustObject(t, 8, lifecycle.CurrentUserOwner, 0o777), leaf: presentLeaf},
		{name: "present zero leaf", issuer: seal, home: home, presence: lifecycle.PresentDirectoryLeaf(), root: root, parent: parent},
		{name: "present leaf is parent", issuer: seal, home: home, presence: lifecycle.PresentDirectoryLeaf(), root: root, parent: parent, leaf: parent},
		{name: "present cross-filesystem leaf", issuer: seal, home: home, presence: lifecycle.PresentDirectoryLeaf(), root: root, parent: parent, leaf: mustObjectOnFilesystem(t, 2, 4, lifecycle.CurrentUserOwner, 0o700)},
		{name: "present wrong owner", issuer: seal, home: home, presence: lifecycle.PresentDirectoryLeaf(), root: root, parent: parent, leaf: mustObject(t, 4, lifecycle.OtherOwner, 0o700)},
		{name: "absent nonzero leaf", issuer: seal, home: home, presence: lifecycle.AbsentDirectoryLeaf(), root: root, parent: parent, leaf: presentLeaf},
		{name: "absent cross-filesystem parent", issuer: seal, home: home, presence: lifecycle.AbsentDirectoryLeaf(), root: root, parent: mustObjectOnFilesystem(t, 2, 3, lifecycle.CurrentUserOwner, 0o700)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			proof, err := lifecycle.NewDirectoryLeafProof(
				test.issuer, test.home, relative, locator, test.presence, test.root, test.parent, test.leaf,
			)
			if err == nil || proof.Valid() {
				t.Fatal("invalid leaf proof accepted")
			}
		})
	}
}

func TestDirectoryLeafProofBindsParentDepth(t *testing.T) {
	t.Parallel()

	seal := mustSeal(t, 0x63)
	home := mustHomeProof(t, seal)
	configuration := mustObject(t, 4, lifecycle.CurrentUserOwner, 0o700)
	rulesPath := mustRelative(t, ".claude/rules")
	rulesLocator := mustLocator(t, proofPathCanary+"/.claude/rules")
	if proof, err := lifecycle.NewDirectoryLeafProof(
		seal, home, rulesPath, rulesLocator, lifecycle.AbsentDirectoryLeaf(),
		home.Home(), configuration, lifecycle.DirectoryObjectProof{},
	); err != nil || !proof.Valid() {
		t.Fatalf("nested absent proof = %v, %v", proof, err)
	}
	if proof, err := lifecycle.NewDirectoryLeafProof(
		seal, home, rulesPath, rulesLocator, lifecycle.AbsentDirectoryLeaf(),
		home.Home(), home.Home(), lifecycle.DirectoryObjectProof{},
	); err == nil || proof.Valid() {
		t.Fatal("nested proof accepted the home root as its final parent")
	}
	if proof, err := lifecycle.NewDirectoryLeafProof(
		seal, home, mustRelative(t, ".claude"), mustLocator(t, proofPathCanary+"/.claude"),
		lifecycle.AbsentDirectoryLeaf(), home.Home(), configuration, lifecycle.DirectoryObjectProof{},
	); err == nil || proof.Valid() {
		t.Fatal("direct-child proof accepted a non-root final parent")
	}
}

func TestDirectoryLeafPresenceIsClosed(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"present", "absent"} {
		presence, err := lifecycle.NewDirectoryLeafPresence(value)
		if err != nil || !presence.Valid() || presence.String() != value {
			t.Fatalf("presence %q = %v, %v", value, presence, err)
		}
	}
	for _, value := range []string{"", "unknown", "PRESENT"} {
		if presence, err := lifecycle.NewDirectoryLeafPresence(value); err == nil || presence.Valid() {
			t.Fatalf("invalid presence accepted: %q", value)
		}
	}
}

func TestDirectoryQualificationIssuesAreClosedSafeErrors(t *testing.T) {
	t.Parallel()

	issues := []lifecycle.DirectoryQualificationIssue{
		lifecycle.TrustedAccountUnavailableIssue(),
		lifecycle.InvalidDirectoryLocatorIssue(),
		lifecycle.MissingIntermediateIssue(),
		lifecycle.SymlinkedDirectoryIssue(),
		lifecycle.WrongDirectoryTypeIssue(),
		lifecycle.WrongDirectoryOwnerIssue(),
		lifecycle.UnsafeDirectoryModeIssue(),
		lifecycle.UnsupportedFilesystemIssue(),
		lifecycle.ProtectedRootOverlapIssue(),
		lifecycle.DirectoryIdentityChangedIssue(),
		lifecycle.DirectoryObservationFailedIssue(),
	}
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if !issue.Valid() || !errors.Is(issue, lifecycle.ErrDirectoryQualification) {
			t.Fatalf("invalid issue: %v", issue)
		}
		if _, duplicate := seen[issue.String()]; duplicate {
			t.Fatalf("duplicate issue: %s", issue.String())
		}
		seen[issue.String()] = struct{}{}
		parsed, err := lifecycle.NewDirectoryQualificationIssue(issue.String())
		if err != nil || parsed != issue {
			t.Fatalf("parse %s = %v, %v", issue.String(), parsed, err)
		}
		assertRedacted(t, issue, proofPathCanary)
	}
	for _, value := range []string{"", "unknown", "wrong-owner"} {
		if issue, err := lifecycle.NewDirectoryQualificationIssue(value); err == nil || issue.Valid() {
			t.Fatalf("invalid issue accepted: %q", value)
		}
	}
	if (lifecycle.DirectoryQualificationIssue{}).Valid() || errors.Is(lifecycle.DirectoryQualificationIssue{}, lifecycle.ErrDirectoryQualification) {
		t.Fatal("zero issue is valid")
	}
	if _, err := json.Marshal(lifecycle.DirectoryQualificationIssue{}); err == nil {
		t.Fatal("zero issue serialized")
	}
}

func mustSeal(t *testing.T, value byte) lifecycle.ProofIssuerSeal {
	t.Helper()
	seal, err := lifecycle.NewProofIssuerSeal(bytes.Repeat([]byte{value}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return seal
}

func mustLocator(t *testing.T, value string) lifecycle.HostDirectoryLocator {
	t.Helper()
	locator, err := lifecycle.NewHostDirectoryLocator(value)
	if err != nil {
		t.Fatal(err)
	}
	return locator
}

func mustObject(t *testing.T, object uint64, owner lifecycle.OwnerClass, mode fs.FileMode) lifecycle.DirectoryObjectProof {
	t.Helper()
	return mustObjectOnFilesystem(t, 1, object, owner, mode)
}

func mustObjectOnFilesystem(
	t *testing.T,
	filesystem uint64,
	object uint64,
	owner lifecycle.OwnerClass,
	mode fs.FileMode,
) lifecycle.DirectoryObjectProof {
	t.Helper()
	proof, err := lifecycle.NewDirectoryObjectProof(lifecycle.ObjectIdentity{Filesystem: filesystem, Object: object}, owner, mode)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func mustHomeProof(t *testing.T, seal lifecycle.ProofIssuerSeal) lifecycle.UserHomeProof {
	t.Helper()
	proof, err := lifecycle.NewUserHomeProof(
		seal,
		mustLocator(t, proofPathCanary),
		mustObject(t, 2, lifecycle.SystemOwner, 0o755),
		mustObject(t, 3, lifecycle.CurrentUserOwner, 0o700),
	)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func mustLeafProof(
	t *testing.T,
	seal lifecycle.ProofIssuerSeal,
	home lifecycle.UserHomeProof,
	presence lifecycle.DirectoryLeafPresence,
	leaf lifecycle.DirectoryObjectProof,
) lifecycle.DirectoryLeafProof {
	t.Helper()
	proof, err := lifecycle.NewDirectoryLeafProof(
		seal,
		home,
		mustRelative(t, ".claude"),
		mustLocator(t, proofPathCanary+"/.claude"),
		presence,
		home.Home(),
		home.Home(),
		leaf,
	)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func mustRelative(t *testing.T, value string) pathsafe.RelativePath {
	t.Helper()
	path, err := pathsafe.NewRelativePath(value)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func assertRedacted(t *testing.T, value any, forbidden ...string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	forms := []string{
		fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value),
		fmt.Sprintf("%q", value), fmt.Sprintf("%s", value), string(encoded),
	}
	for _, form := range forms {
		for _, canary := range forbidden {
			if canary != "" && strings.Contains(form, canary) {
				t.Fatalf("value disclosed %q in %q", canary, form)
			}
		}
	}
}
