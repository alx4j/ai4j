package filesystem

import (
	"bytes"
	"io/fs"
	"testing"

	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/pathsafe"
)

type ledgerAuthority struct{ closes int }

func (a *ledgerAuthority) Close() error {
	a.closes++
	return nil
}

func TestIssuedProofLedgerRequiresExactHomeAndLeafValues(t *testing.T) {
	t.Parallel()

	home, leaf := ledgerProofs(t, 0x21, 3, ".claude")
	otherHome, _ := ledgerProofs(t, 0x22, 13, ".claude")
	var ledger issuedProofLedger
	if ledger.containsHome(home) || ledger.issueHome(lifecycle.UserHomeProof{}) == nil {
		t.Fatal("zero or unissued home accepted")
	}
	if err := ledger.issueHome(home); err != nil || !ledger.containsHome(home) || ledger.containsHome(otherHome) {
		t.Fatalf("issue home: %v", err)
	}
	if err := ledger.issueHome(otherHome); err != errIssuedProofChanged {
		t.Fatalf("changed home error = %v", err)
	}
	firstAuthority := &ledgerAuthority{}
	got, existing, err := ledger.issueLeaf(leaf, firstAuthority)
	if err != nil || existing || got != firstAuthority {
		t.Fatalf("first leaf = %v, %v, %v", got, existing, err)
	}
	duplicateAuthority := &ledgerAuthority{}
	got, existing, err = ledger.issueLeaf(leaf, duplicateAuthority)
	if err != nil || !existing || got != firstAuthority || duplicateAuthority.closes != 0 {
		t.Fatalf("duplicate leaf = %v, %v, %v", got, existing, err)
	}
	_, changedLeaf := ledgerProofsForHome(t, home, 0x21, 99, ".claude")
	if _, _, err := ledger.issueLeaf(changedLeaf, &ledgerAuthority{}); err != errIssuedProofChanged {
		t.Fatalf("changed exact leaf error = %v", err)
	}
	if err := ledger.close(); err != nil || firstAuthority.closes != 1 || ledger.containsHome(home) {
		t.Fatalf("close = %v, closes=%d", err, firstAuthority.closes)
	}
}

func TestIssuedProofLedgerHasFixedLeafBound(t *testing.T) {
	t.Parallel()

	home, _ := ledgerProofs(t, 0x31, 23, ".claude")
	var ledger issuedProofLedger
	if err := ledger.issueHome(home); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maximumIssuedDirectoryLeafProofs; index++ {
		_, leaf := ledgerProofsForHome(t, home, 0x31, uint64(30+index), "directory-"+string(rune('a'+index)))
		if _, _, err := ledger.issueLeaf(leaf, &ledgerAuthority{}); err != nil {
			t.Fatalf("leaf %d: %v", index, err)
		}
	}
	_, overflow := ledgerProofsForHome(t, home, 0x31, 100, "overflow")
	if _, _, err := ledger.issueLeaf(overflow, &ledgerAuthority{}); err != errIssuedProofLimit {
		t.Fatalf("overflow error = %v", err)
	}
	if err := ledger.close(); err != nil {
		t.Fatal(err)
	}
}

func ledgerProofs(t *testing.T, sealByte byte, homeObject uint64, relative string) (lifecycle.UserHomeProof, lifecycle.DirectoryLeafProof) {
	t.Helper()
	seal, err := lifecycle.NewProofIssuerSeal(bytes.Repeat([]byte{sealByte}, 32))
	if err != nil {
		t.Fatal(err)
	}
	locator, _ := lifecycle.NewHostDirectoryLocator("/Users/ledger")
	parent := ledgerObject(t, 1, 2, lifecycle.SystemOwner, 0o755)
	homeObjectProof := ledgerObject(t, 1, homeObject, lifecycle.CurrentUserOwner, 0o700)
	home, err := lifecycle.NewUserHomeProof(seal, locator, parent, homeObjectProof)
	if err != nil {
		t.Fatal(err)
	}
	return ledgerProofsForHome(t, home, sealByte, homeObject+1, relative)
}

func ledgerProofsForHome(
	t *testing.T,
	home lifecycle.UserHomeProof,
	sealByte byte,
	leafObject uint64,
	relative string,
) (lifecycle.UserHomeProof, lifecycle.DirectoryLeafProof) {
	t.Helper()
	seal, _ := lifecycle.NewProofIssuerSeal(bytes.Repeat([]byte{sealByte}, 32))
	relativePath, err := pathsafe.NewRelativePath(relative)
	if err != nil {
		t.Fatal(err)
	}
	locator, _ := lifecycle.NewHostDirectoryLocator(home.Locator().Value() + "/" + relative)
	leaf, err := lifecycle.NewDirectoryLeafProof(
		seal, home, relativePath, locator, lifecycle.PresentDirectoryLeaf(),
		home.Home(), home.Home(), ledgerObject(t, 1, leafObject, lifecycle.CurrentUserOwner, 0o700),
	)
	if err != nil {
		t.Fatal(err)
	}
	return home, leaf
}

func ledgerObject(
	t *testing.T,
	filesystem uint64,
	object uint64,
	owner lifecycle.OwnerClass,
	mode fs.FileMode,
) lifecycle.DirectoryObjectProof {
	t.Helper()
	proof, err := lifecycle.NewDirectoryObjectProof(
		lifecycle.ObjectIdentity{Filesystem: filesystem, Object: object}, owner, mode,
	)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}
