//go:build darwin && arm64

package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/pathsafe"
)

type fakeEffectiveAccountOperations struct {
	realUID      int
	effectiveUID int
	realGID      int
	effectiveGID int
	account      *user.User
	lookupErr    error
	randomByte   byte
	randomReads  int
	lookups      []string
}

func (o *fakeEffectiveAccountOperations) RealUID() int      { return o.realUID }
func (o *fakeEffectiveAccountOperations) EffectiveUID() int { return o.effectiveUID }
func (o *fakeEffectiveAccountOperations) RealGID() int      { return o.realGID }
func (o *fakeEffectiveAccountOperations) EffectiveGID() int { return o.effectiveGID }
func (o *fakeEffectiveAccountOperations) LookupUserID(value string) (*user.User, error) {
	o.lookups = append(o.lookups, value)
	return o.account, o.lookupErr
}
func (o *fakeEffectiveAccountOperations) ReadRandom(value []byte) (int, error) {
	o.randomReads++
	for index := range value {
		value[index] = o.randomByte
	}
	return len(value), nil
}

func TestResolveEffectiveAccountUsesExactEffectiveUIDAndCanonicalDatabaseHome(t *testing.T) {
	t.Parallel()

	operations := &fakeEffectiveAccountOperations{
		realUID: 501, effectiveUID: 501, realGID: 20, effectiveGID: 20,
		account: &user.User{Uid: "501", Gid: "20", HomeDir: "/Users/account-home-canary"},
	}
	uid, gid, home, issue, err := resolveEffectiveAccount(t.Context(), operations)
	if err != nil || issue.Valid() || uid != 501 || gid != 20 || home != "/Users/account-home-canary" ||
		len(operations.lookups) != 1 || operations.lookups[0] != "501" {
		t.Fatalf("resolve = uid:%d gid:%d home:%q issue:%v err:%v lookups:%v", uid, gid, home, issue, err, operations.lookups)
	}

	tests := []struct {
		name       string
		operations *fakeEffectiveAccountOperations
		want       lifecycle.DirectoryQualificationIssue
	}{
		{name: "real effective uid mismatch", operations: &fakeEffectiveAccountOperations{realUID: 500, effectiveUID: 501, realGID: 20, effectiveGID: 20}, want: lifecycle.TrustedAccountUnavailableIssue()},
		{name: "real effective gid mismatch", operations: &fakeEffectiveAccountOperations{realUID: 501, effectiveUID: 501, realGID: 20, effectiveGID: 80}, want: lifecycle.TrustedAccountUnavailableIssue()},
		{name: "lookup failure", operations: &fakeEffectiveAccountOperations{realUID: 501, effectiveUID: 501, realGID: 20, effectiveGID: 20, lookupErr: errors.New("canary")}, want: lifecycle.TrustedAccountUnavailableIssue()},
		{name: "lookup uid mismatch", operations: &fakeEffectiveAccountOperations{realUID: 501, effectiveUID: 501, realGID: 20, effectiveGID: 20, account: &user.User{Uid: "502", Gid: "20", HomeDir: "/Users/a"}}, want: lifecycle.TrustedAccountUnavailableIssue()},
		{name: "lookup gid mismatch", operations: &fakeEffectiveAccountOperations{realUID: 501, effectiveUID: 501, realGID: 20, effectiveGID: 20, account: &user.User{Uid: "501", Gid: "80", HomeDir: "/Users/a"}}, want: lifecycle.TrustedAccountUnavailableIssue()},
		{name: "relative home", operations: &fakeEffectiveAccountOperations{realUID: 501, effectiveUID: 501, realGID: 20, effectiveGID: 20, account: &user.User{Uid: "501", Gid: "20", HomeDir: "relative"}}, want: lifecycle.InvalidDirectoryLocatorIssue()},
		{name: "noncanonical home", operations: &fakeEffectiveAccountOperations{realUID: 501, effectiveUID: 501, realGID: 20, effectiveGID: 20, account: &user.User{Uid: "501", Gid: "20", HomeDir: "/Users/a/.."}}, want: lifecycle.InvalidDirectoryLocatorIssue()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, _, got, resolveErr := resolveEffectiveAccount(t.Context(), test.operations)
			if resolveErr != nil || got != test.want {
				t.Fatalf("resolve issue = %v, %v", got, resolveErr)
			}
		})
	}
}

func TestGenerateIssuerSealIsPerInstanceAndRejectsZeroEntropy(t *testing.T) {
	t.Parallel()

	firstOperations := &fakeEffectiveAccountOperations{randomByte: 0x41}
	secondOperations := &fakeEffectiveAccountOperations{randomByte: 0x42}
	first, err := generateIssuerSeal(firstOperations)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateIssuerSeal(secondOperations)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Valid() || !second.Valid() || first == second || firstOperations.randomReads != 1 || secondOperations.randomReads != 1 {
		t.Fatal("issuer seals are not distinct exact per-instance values")
	}
	zeroOperations := &fakeEffectiveAccountOperations{}
	if seal, err := generateIssuerSeal(zeroOperations); err == nil || seal.Valid() || zeroOperations.randomReads != maximumIssuerGenerationAttempts {
		t.Fatalf("zero entropy = %v, %v, reads=%d", seal, err, zeroOperations.randomReads)
	}
}

func TestUserDirectoryAuthorityRejectsEffectiveGIDDriftAfterStartup(t *testing.T) {
	t.Parallel()

	home := canonicalDarwinTempDirectory(t)
	uid := os.Geteuid()
	operations := fixtureAccountOperations(uid, home, 0x50)
	authority, err := newUserDirectoryAuthority(
		t.Context(),
		filepath.Join(home, ".ai4j"),
		operations,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	if _, err := authority.InspectUserHome(t.Context()); err != nil {
		t.Fatal(err)
	}
	changedGID := operations.effectiveGID + 1
	operations.realGID = changedGID
	operations.effectiveGID = changedGID
	operations.account.Gid = strconv.Itoa(changedGID)
	if _, err := authority.InspectUserHome(t.Context()); err != lifecycle.DirectoryIdentityChangedIssue() {
		t.Fatalf("effective GID drift error = %v", err)
	}
}

func TestUserDirectoryAuthorityQualifiesPresentAndFinalAbsentWithoutWriting(t *testing.T) {
	t.Parallel()

	home := canonicalDarwinTempDirectory(t)
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	uid, gid := os.Geteuid(), os.Getegid()
	operations := &fakeEffectiveAccountOperations{
		realUID: uid, effectiveUID: uid, realGID: gid, effectiveGID: gid, randomByte: 0x51,
		account: &user.User{Uid: strconv.Itoa(uid), Gid: strconv.Itoa(gid), HomeDir: home},
	}
	authority, err := newUserDirectoryAuthority(t.Context(), filepath.Join(home, ".ai4j"), operations)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	homeProof, err := authority.InspectUserHome(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := authority.QualifyUserDirectory(t.Context(), homeProof, mustDarwinRelative(t, ".claude"))
	if err != nil || configuration.Presence() != lifecycle.PresentDirectoryLeaf() {
		t.Fatalf("configuration proof = %v, %v", configuration, err)
	}
	rules, err := authority.QualifyUserDirectory(t.Context(), homeProof, mustDarwinRelative(t, ".claude/rules"))
	if err != nil || rules.Presence() != lifecycle.AbsentDirectoryLeaf() {
		t.Fatalf("rules proof = %v, %v", rules, err)
	}
	configurationLeaf, ok := configuration.Leaf()
	if !ok || rules.Parent() != configurationLeaf {
		t.Fatal("rules parent is not the exact configuration leaf")
	}
	if _, err := os.Lstat(filepath.Join(home, ".ai4j")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("protected root was created: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "rules")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absent rules leaf was created: %v", err)
	}
}

func TestUserDirectoryAuthorityRejectsProtectedOverlapAndProofDrift(t *testing.T) {
	t.Parallel()

	t.Run("protected lexical descendant", func(t *testing.T) {
		home := canonicalDarwinTempDirectory(t)
		uid := os.Geteuid()
		operations := fixtureAccountOperations(uid, home, 0x61)
		authority, err := newUserDirectoryAuthority(t.Context(), filepath.Join(home, ".claude", "ai4j"), operations)
		if err != nil {
			t.Fatal(err)
		}
		defer authority.Close()
		homeProof, err := authority.InspectUserHome(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_, err = authority.QualifyUserDirectory(t.Context(), homeProof, mustDarwinRelative(t, ".claude"))
		if !errors.Is(err, lifecycle.ErrDirectoryQualification) || err != lifecycle.ProtectedRootOverlapIssue() {
			t.Fatalf("overlap error = %v", err)
		}
	})

	t.Run("absent leaf appearance invalidates exact ledger", func(t *testing.T) {
		home := canonicalDarwinTempDirectory(t)
		uid := os.Geteuid()
		authority, err := newUserDirectoryAuthority(t.Context(), filepath.Join(home, ".ai4j"), fixtureAccountOperations(uid, home, 0x62))
		if err != nil {
			t.Fatal(err)
		}
		defer authority.Close()
		homeProof, err := authority.InspectUserHome(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := authority.QualifyUserDirectory(t.Context(), homeProof, mustDarwinRelative(t, ".claude")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := authority.QualifyUserDirectory(t.Context(), homeProof, mustDarwinRelative(t, ".claude")); err != lifecycle.DirectoryIdentityChangedIssue() {
			t.Fatalf("changed leaf error = %v", err)
		}
	})
}

func TestUserDirectoryAuthorityFinalRevalidationRejectsProtectedRootSubstitution(t *testing.T) {
	t.Parallel()

	home := canonicalDarwinTempDirectory(t)
	protected := filepath.Join(home, ".ai4j")
	if err := os.Mkdir(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	uid := os.Geteuid()
	authority, err := newUserDirectoryAuthority(t.Context(), protected, fixtureAccountOperations(uid, home, 0x66))
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	homeProof, err := authority.InspectUserHome(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	authority.state.afterCandidateObservation = func() {
		moved := protected + ".moved"
		if renameErr := os.Rename(protected, moved); renameErr != nil {
			t.Errorf("rename protected root: %v", renameErr)
			return
		}
		if mkdirErr := os.Mkdir(protected, 0o700); mkdirErr != nil {
			t.Errorf("replace protected root: %v", mkdirErr)
		}
	}
	_, err = authority.QualifyUserDirectory(t.Context(), homeProof, mustDarwinRelative(t, ".claude"))
	if err != lifecycle.DirectoryIdentityChangedIssue() {
		t.Fatalf("protected substitution error = %v", err)
	}
}

func TestUserDirectoryAuthorityFinalRevalidationRejectsIntermediateSubstitution(t *testing.T) {
	t.Parallel()

	home := canonicalDarwinTempDirectory(t)
	configuration := filepath.Join(home, ".claude")
	if err := os.Mkdir(configuration, 0o700); err != nil {
		t.Fatal(err)
	}
	uid := os.Geteuid()
	authority, err := newUserDirectoryAuthority(
		t.Context(),
		filepath.Join(home, ".ai4j"),
		fixtureAccountOperations(uid, home, 0x67),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	homeProof, err := authority.InspectUserHome(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	authority.state.afterCandidateObservation = func() {
		moved := configuration + ".moved"
		if renameErr := os.Rename(configuration, moved); renameErr != nil {
			t.Errorf("rename configuration root: %v", renameErr)
			return
		}
		if mkdirErr := os.Mkdir(configuration, 0o700); mkdirErr != nil {
			t.Errorf("replace configuration root: %v", mkdirErr)
		}
	}
	_, err = authority.QualifyUserDirectory(
		t.Context(),
		homeProof,
		mustDarwinRelative(t, ".claude/rules"),
	)
	if err != lifecycle.DirectoryIdentityChangedIssue() {
		t.Fatalf("intermediate substitution error = %v", err)
	}
}

func TestUserDirectoryAuthorityCancellationDuringFinalRevalidationConsumesNoLedgerSlot(t *testing.T) {
	t.Parallel()

	home := canonicalDarwinTempDirectory(t)
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	uid := os.Geteuid()
	authority, err := newUserDirectoryAuthority(
		t.Context(),
		filepath.Join(home, ".ai4j"),
		fixtureAccountOperations(uid, home, 0x68),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	homeProof, err := authority.InspectUserHome(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancelContext := context.WithCancel(context.Background())
	defer cancelContext()
	var retained *retainedLeafAuthority
	authority.state.beforeLeafIssue = func(value *retainedLeafAuthority) {
		retained = value
		cancelContext()
	}
	_, err = authority.QualifyUserDirectory(ctx, homeProof, mustDarwinRelative(t, ".claude"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("qualification error = %v", err)
	}
	if got := len(authority.state.ledger.leaves); got != 0 {
		t.Fatalf("cancelled qualification retained %d leaf proofs", got)
	}
	if retained == nil || retained.root != nil || retained.parent != nil || retained.leaf != nil {
		t.Fatal("cancelled qualification did not close every retained descriptor")
	}
}

func TestUserDirectoryAuthorityRejectsUnsafeAndSymlinkedTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*testing.T, string)
		want  lifecycle.DirectoryQualificationIssue
	}{
		{
			name: "unsafe mode",
			setup: func(t *testing.T, home string) {
				t.Helper()
				path := filepath.Join(home, ".claude")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o777); err != nil {
					t.Fatal(err)
				}
			},
			want: lifecycle.UnsafeDirectoryModeIssue(),
		},
		{
			name: "symlink",
			setup: func(t *testing.T, home string) {
				t.Helper()
				if err := os.Symlink(home, filepath.Join(home, ".claude")); err != nil {
					t.Fatal(err)
				}
			},
			want: lifecycle.SymlinkedDirectoryIssue(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := canonicalDarwinTempDirectory(t)
			test.setup(t, home)
			uid := os.Geteuid()
			authority, err := newUserDirectoryAuthority(t.Context(), filepath.Join(home, ".ai4j"), fixtureAccountOperations(uid, home, 0x71))
			if err != nil {
				t.Fatal(err)
			}
			defer authority.Close()
			homeProof, err := authority.InspectUserHome(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			_, err = authority.QualifyUserDirectory(t.Context(), homeProof, mustDarwinRelative(t, ".claude"))
			if err != test.want {
				t.Fatalf("qualification error = %v", err)
			}
		})
	}
}

func TestUserDirectoryAuthorityFormattingRedactsEveryLocatorAndSeal(t *testing.T) {
	t.Parallel()

	home := canonicalDarwinTempDirectory(t)
	uid := os.Geteuid()
	authority, err := newUserDirectoryAuthority(t.Context(), filepath.Join(home, ".ai4j-secret-canary"), fixtureAccountOperations(uid, home, 0x7a))
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	formatted := fmt.Sprintf("%v|%+v|%#v|%q|%s", authority, authority, authority, authority, authority)
	for _, canary := range []string{home, ".ai4j-secret-canary", "7a"} {
		if strings.Contains(formatted, canary) {
			t.Fatalf("authority disclosed %q", canary)
		}
	}
}

func TestUserDirectoryAuthorityCopiesShareOneClosedState(t *testing.T) {
	t.Parallel()

	home := canonicalDarwinTempDirectory(t)
	uid := os.Geteuid()
	authority, err := newUserDirectoryAuthority(
		t.Context(),
		filepath.Join(home, ".ai4j"),
		fixtureAccountOperations(uid, home, 0x7b),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.InspectUserHome(t.Context()); err != nil {
		t.Fatal(err)
	}
	copyValue := *authority
	if err := copyValue.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.InspectUserHome(t.Context()); err != lifecycle.DirectoryObservationFailedIssue() {
		t.Fatalf("original use after copied close = %v", err)
	}
	if err := authority.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUserDirectoryAuthorityQueuedCopyCallHonorsDeadlineAndCloseWaits(t *testing.T) {
	t.Parallel()

	home := canonicalDarwinTempDirectory(t)
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	uid := os.Geteuid()
	authority, err := newUserDirectoryAuthority(
		t.Context(),
		filepath.Join(home, ".ai4j"),
		fixtureAccountOperations(uid, home, 0x7c),
	)
	if err != nil {
		t.Fatal(err)
	}
	homeProof, err := authority.InspectUserHome(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	copyValue := *authority
	entered := make(chan struct{})
	release := make(chan struct{})
	authority.state.afterCandidateObservation = func() {
		close(entered)
		<-release
	}
	configuration := mustDarwinRelative(t, ".claude")
	activeDone := make(chan error, 1)
	go func() {
		_, qualifyErr := authority.QualifyUserDirectory(
			context.Background(),
			homeProof,
			configuration,
		)
		activeDone <- qualifyErr
	}()
	<-entered
	caller, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	started := time.Now()
	_, queuedErr := copyValue.InspectUserHome(caller)
	cancel()
	if !errors.Is(queuedErr, context.DeadlineExceeded) {
		t.Fatalf("queued copied call error = %v", queuedErr)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("queued copied call exceeded its budget by %v", elapsed)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- copyValue.Close() }()
	select {
	case closeErr := <-closeDone:
		t.Fatalf("Close returned while copied authority call was active: %v", closeErr)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := <-activeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if _, err := authority.InspectUserHome(t.Context()); err != lifecycle.DirectoryObservationFailedIssue() {
		t.Fatalf("original use after concurrent copied close = %v", err)
	}
}

func fixtureAccountOperations(uid int, home string, randomByte byte) *fakeEffectiveAccountOperations {
	gid := os.Getegid()
	return &fakeEffectiveAccountOperations{
		realUID: uid, effectiveUID: uid, realGID: gid, effectiveGID: gid, randomByte: randomByte,
		account: &user.User{Uid: strconv.Itoa(uid), Gid: strconv.Itoa(gid), HomeDir: home},
	}
}

func canonicalDarwinTempDirectory(t *testing.T) string {
	t.Helper()
	value, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(value, 0o700); err != nil {
		t.Fatal(err)
	}
	return value
}

func mustDarwinRelative(t *testing.T, value string) pathsafe.RelativePath {
	t.Helper()
	result, err := pathsafe.NewRelativePath(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
