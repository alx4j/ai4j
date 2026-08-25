package git_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
)

const commitOIDText = "0123456789abcdef0123456789abcdef01234567"

func TestRequestedReferencePreservesOmissionAndExactText(t *testing.T) {
	t.Parallel()

	omitted := gitsource.OmittedRequestedReference()
	if !omitted.Valid() {
		t.Fatal("omitted requested reference is invalid")
	}
	if value, provided := omitted.Value(); value != "" || provided {
		t.Fatalf("omitted Value() = %q, %t", value, provided)
	}

	for _, value := range []string{
		"main",
		"feature/typed-provenance",
		"refs/heads/main",
		"refs/tags/v1.2.3",
		commitOIDText,
		"release-β",
	} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			got, err := gitsource.NewRequestedReference(value)
			if err != nil {
				t.Fatal(err)
			}
			if observed, provided := got.Value(); observed != value || !provided || !got.Valid() {
				t.Fatalf("Value() = %q, %t; Valid() = %t", observed, provided, got.Valid())
			}
		})
	}
}

func TestRequestedReferenceRejectsUnsupportedGitFormsWithoutReflection(t *testing.T) {
	t.Parallel()

	invalidUTF8 := string([]byte{'r', 'e', 'f', 0xff})
	for _, value := range []string{
		"",
		"HEAD",
		"@",
		"-option",
		" refs/heads/main",
		"refs/heads/main ",
		"refs/heads/",
		"refs/tags/",
		"refs/other/name",
		"refs/heads/.hidden",
		"refs/heads/name.lock",
		"refs/heads/name..part",
		"refs/heads/name@{part",
		"refs/heads/name^part",
		"refs/heads/name\\part",
		"refs/heads/name//part",
		"refs/heads/name.",
		"refs/heads/name\x00part",
		invalidUTF8,
		strings.Repeat("a", 1025),
	} {
		if _, err := gitsource.NewRequestedReference(value); !errors.Is(err, gitsource.ErrInvalidRequestedReference) {
			t.Errorf("NewRequestedReference(%q) error = %v", value, err)
		}
	}
}

func TestResolvedReferenceUsesCanonicalNames(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		kind gitsource.ResolvedReferenceKind
		name string
	}{
		{kind: gitsource.ResolvedDefaultBranch, name: "main"},
		{kind: gitsource.ResolvedBranch, name: "feature/provenance"},
		{kind: gitsource.ResolvedTag, name: "v1.2.3"},
		{kind: gitsource.ResolvedCommit, name: commitOIDText},
	} {
		resolved, err := gitsource.NewResolvedReference(test.kind, test.name)
		if err != nil {
			t.Fatalf("NewResolvedReference(%q, %q): %v", test.kind, test.name, err)
		}
		if resolved.Kind() != test.kind || resolved.Name() != test.name || !resolved.Valid() {
			t.Fatalf("resolved = %q/%q, valid=%t", resolved.Kind(), resolved.Name(), resolved.Valid())
		}
	}

	for _, test := range []struct {
		kind gitsource.ResolvedReferenceKind
		name string
	}{
		{kind: "", name: "main"},
		{kind: gitsource.ResolvedBranch, name: ""},
		{kind: gitsource.ResolvedBranch, name: "refs/heads/main"},
		{kind: gitsource.ResolvedTag, name: "refs/tags/v1"},
		{kind: gitsource.ResolvedCommit, name: commitOIDText[:39]},
		{kind: gitsource.ResolvedCommit, name: strings.ToUpper(commitOIDText)},
		{kind: gitsource.ResolvedCommit, name: strings.Repeat("0", 40)},
	} {
		if _, err := gitsource.NewResolvedReference(test.kind, test.name); !errors.Is(err, gitsource.ErrInvalidResolvedReference) {
			t.Errorf("NewResolvedReference(%q, %q) error = %v", test.kind, test.name, err)
		}
	}
}

func TestReferenceAndIdentityTypesRemainDistinct(t *testing.T) {
	t.Parallel()

	commit, err := domain.NewCommitOID(commitOIDText)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := gitsource.NewResolvedReference(gitsource.ResolvedCommit, commit.String())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Name() != commit.String() {
		t.Fatalf("resolved commit = %q", resolved.Name())
	}
	if !gitsource.UpdateNoChange.Valid() || gitsource.UpdateDisposition("").Valid() || gitsource.UpdateDisposition("up_to_date").Valid() {
		t.Fatal("source update disposition is not a closed enum")
	}
}

func FuzzRequestedReferenceIsBounded(f *testing.F) {
	for _, seed := range []string{"main", "refs/heads/main", "refs/tags/v1", commitOIDText, "-option", string([]byte{0xff})} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		requested, err := gitsource.NewRequestedReference(value)
		if err != nil {
			if len(err.Error()) > 128 || strings.Contains(err.Error(), value) && len(value) > 128 {
				t.Fatalf("unsafe error: %q", err)
			}
			return
		}
		if !requested.Valid() {
			t.Fatal("constructor returned invalid reference")
		}
	})
}
