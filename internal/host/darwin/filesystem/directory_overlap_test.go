package filesystem

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestCanonicalDarwinDirectoryLocatorIsExactAndBounded(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"/Users/alice", "/Users/Alice Smith", "/Users/a/é"} {
		got, ok := canonicalDarwinDirectoryLocator(value)
		if !ok || got != value {
			t.Fatalf("canonicalDarwinDirectoryLocator(%q) = %q, %v", value, got, ok)
		}
	}
	for _, value := range []string{
		"", "/", "relative", "/Users/alice/", "/Users/../alice", "/Users//alice",
		"/Users/alice\x00x", "/Users/alice\nx", string([]byte{'/', 0xff}),
		"/" + strings.Repeat("x", maximumDarwinPathComponentBytes+1),
		"/" + strings.Repeat("x", maximumDarwinDirectoryLocatorBytes),
		"/" + strings.Repeat("a/", maximumDarwinPathComponents) + "a",
	} {
		if got, ok := canonicalDarwinDirectoryLocator(value); ok || got != "" {
			t.Fatalf("invalid locator accepted: length=%d", len(value))
		}
	}
}

func TestRelativeDirectoryObjectIssueRejectsFilesystemChangeDeterministically(t *testing.T) {
	t.Parallel()

	object := func(filesystemID uint64, owner lifecycle.OwnerClass, mode fs.FileMode) lifecycle.DirectoryObjectProof {
		proof, err := lifecycle.NewDirectoryObjectProof(
			lifecycle.ObjectIdentity{Filesystem: filesystemID, Object: filesystemID + 10}, owner, mode,
		)
		if err != nil {
			t.Fatal(err)
		}
		return proof
	}
	home := object(1, lifecycle.CurrentUserOwner, 0o700)
	for _, test := range []struct {
		name      string
		candidate lifecycle.DirectoryObjectProof
		want      lifecycle.DirectoryQualificationIssue
	}{
		{name: "same filesystem", candidate: object(1, lifecycle.CurrentUserOwner, 0o700)},
		{name: "different filesystem", candidate: object(2, lifecycle.CurrentUserOwner, 0o700), want: lifecycle.UnsupportedFilesystemIssue()},
		{name: "wrong owner precedes filesystem", candidate: object(2, lifecycle.OtherOwner, 0o700), want: lifecycle.WrongDirectoryOwnerIssue()},
		{name: "unsafe mode precedes filesystem", candidate: object(2, lifecycle.CurrentUserOwner, 0o777), want: lifecycle.UnsafeDirectoryModeIssue()},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := relativeDirectoryObjectIssue(test.candidate, home); got != test.want {
				t.Fatalf("issue = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDarwinDirectoryLocatorOverlapRejectsLexicalCaseAndUnicodeAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		first       string
		second      string
		wantOverlap bool
	}{
		{name: "same", first: "/Users/alice/.claude", second: "/Users/alice/.claude", wantOverlap: true},
		{name: "protected descendant", first: "/Users/alice/.claude", second: "/Users/alice/.claude/ai4j", wantOverlap: true},
		{name: "candidate descendant", first: "/Users/alice/.ai4j", second: "/Users/alice/.ai4j/config", wantOverlap: true},
		{name: "case alias", first: "/Users/alice/.AI4J", second: "/Users/alice/.ai4j/config", wantOverlap: true},
		{name: "unicode alias", first: "/Users/alice/caf\u00e9", second: "/Users/alice/cafe\u0301/rules", wantOverlap: true},
		{name: "pinned Cherokee alias", first: "/Users/alice/\u13cf", second: "/Users/alice/\uab9f/rules", wantOverlap: true},
		{name: "component boundary", first: "/Users/alice/.ai4j", second: "/Users/alice/.ai4j-other", wantOverlap: false},
		{name: "siblings", first: "/Users/alice/.ai4j", second: "/Users/alice/.claude", wantOverlap: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := darwinDirectoryLocatorsOverlap(test.first, test.second); got != test.wantOverlap {
				t.Fatalf("overlap(%q, %q) = %v", test.first, test.second, got)
			}
		})
	}
}

func TestDirectoryIdentityOverlapComparesOnlyObservableTargets(t *testing.T) {
	t.Parallel()

	id := func(object uint64) lifecycle.ObjectIdentity {
		return lifecycle.ObjectIdentity{Filesystem: 1, Object: object}
	}
	tests := []struct {
		name             string
		candidate        []lifecycle.ObjectIdentity
		candidatePresent bool
		protected        []lifecycle.ObjectIdentity
		protectedPresent bool
		want             bool
	}{
		{name: "siblings share ancestors", candidate: []lifecycle.ObjectIdentity{id(1), id(2), id(3)}, candidatePresent: true, protected: []lifecycle.ObjectIdentity{id(1), id(2), id(4)}, protectedPresent: true},
		{name: "candidate target in protected chain", candidate: []lifecycle.ObjectIdentity{id(2), id(3)}, candidatePresent: true, protected: []lifecycle.ObjectIdentity{id(1), id(2), id(3), id(4)}, protectedPresent: true, want: true},
		{name: "protected target in candidate chain", candidate: []lifecycle.ObjectIdentity{id(2), id(4), id(5)}, candidatePresent: true, protected: []lifecycle.ObjectIdentity{id(1), id(2), id(4)}, protectedPresent: true, want: true},
		{name: "absent candidate has no target", candidate: []lifecycle.ObjectIdentity{id(2), id(3)}, protected: []lifecycle.ObjectIdentity{id(1), id(2), id(4)}, protectedPresent: true},
		{name: "absent protected target cannot contain", candidate: []lifecycle.ObjectIdentity{id(2), id(4)}, candidatePresent: true, protected: []lifecycle.ObjectIdentity{id(1), id(2), id(3)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := directoryIdentityOverlap(test.candidate, test.candidatePresent, test.protected, test.protectedPresent); got != test.want {
				t.Fatalf("identity overlap = %v, want %v", got, test.want)
			}
		})
	}
}
