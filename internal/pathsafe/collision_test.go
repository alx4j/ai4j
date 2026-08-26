package pathsafe_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/alx4j/ai4j/internal/pathsafe"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func TestCollisionKeyPinsUnicodeAndGoldenCanonicalSpelling(t *testing.T) {
	t.Parallel()
	if cases.UnicodeVersion != pathsafe.CollisionUnicodeVersion || norm.Version != pathsafe.CollisionUnicodeVersion {
		t.Fatalf("Unicode tables = cases %s / norm %s, want %s", cases.UnicodeVersion, norm.Version, pathsafe.CollisionUnicodeVersion)
	}
	path := mustRelativePath(t, "Plugins/Cafe\u0301/STRA\u1e9eE.md")
	key, err := pathsafe.NewCollisionKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !key.Valid() || key.Canonical() != "plugins/caf\u00e9/strasse.md" {
		t.Fatalf("key = %q", key.Canonical())
	}
	if (pathsafe.CollisionKey{}).Valid() {
		t.Fatal("zero collision key is valid")
	}
	if !pathsafe.EquivalentLeafCollision().Valid() || !pathsafe.AncestorLeafCollision().Valid() ||
		pathsafe.EquivalentLeafCollision().String() != "equivalent_leaf" ||
		pathsafe.AncestorLeafCollision().String() != "ancestor_leaf" ||
		(pathsafe.PathCollisionKind{}).Valid() {
		t.Fatal("path collision kinds are not closed and stable")
	}
}

func TestCollisionKeyUsesCanonicalEquivalenceAndFullDefaultCaseFold(t *testing.T) {
	t.Parallel()
	pairs := [][2]string{
		{"Skills/Agent.md", "skills/agent.md"},
		{"skills/Caf\u00e9.md", "SKILLS/Cafe\u0301.MD"},
		{"rules/Stra\u00dfe.md", "RULES/STRASSE.MD"},
		{"greek/\u03a3.md", "GREEK/\u03c2.MD"},
		{"compat/\ufb03.md", "COMPAT/FFI.MD"},
		{"locale/I.md", "LOCALE/i.MD"},
		{"cherokee/\u13cf.md", "CHEROKEE/\uab9f.MD"},
	}
	for _, pair := range pairs {
		left := mustCollisionKey(t, pair[0])
		right := mustCollisionKey(t, pair[1])
		if left != right || left.Compare(right) != 0 {
			t.Fatalf("keys for %q and %q differ: %q / %q", pair[0], pair[1], left.Canonical(), right.Canonical())
		}
	}

	// Canonicalization uses NFC, not compatibility NFKC. Fullwidth Latin remains
	// distinct from ASCII even though both are case-folded.
	fullwidth := mustCollisionKey(t, "width/\uff21.md")
	ascii := mustCollisionKey(t, "width/A.md")
	if fullwidth == ascii || fullwidth.Compare(ascii) == 0 {
		t.Fatal("compatibility-only spellings collided")
	}

	// Default case folding is locale-independent: dotted capital I folds to
	// i plus combining dot, not to plain ASCII i.
	dottedI := mustCollisionKey(t, "locale/\u0130.md")
	plainI := mustCollisionKey(t, "locale/i.md")
	if dottedI == plainI || dottedI.Compare(plainI) == 0 {
		t.Fatal("locale-specific Turkish-I comparison changed the default fold")
	}
	if got := mustCollisionKey(t, "cherokee/\uab9f.md").Canonical(); got != "cherokee/\u13cf.md" {
		t.Fatalf("Cherokee default fold = %q, want historic uppercase representative", got)
	}
}

func TestFindPathCollisionIsDeterministicAndKeepsDisplaySpellings(t *testing.T) {
	t.Parallel()
	spellings := []string{
		"z/Cafe\u0301.md", "A/Stra\u00dfe.md", "Z/Caf\u00e9.MD", "a/STRASSE.MD", "unique/path.md",
	}
	paths := make([]pathsafe.RelativePath, len(spellings))
	for index, spelling := range spellings {
		paths[index] = mustRelativePath(t, spelling)
	}
	permutations := [][]pathsafe.RelativePath{
		paths,
		slices.Clone(paths),
		slices.Clone(paths),
	}
	slices.Reverse(permutations[1])
	permutations[2] = append(permutations[2][2:], permutations[2][:2]...)
	for _, permutation := range permutations {
		collision, found, err := pathsafe.FindPathCollision(permutation)
		if err != nil || !found || !collision.Valid() {
			t.Fatalf("collision = %#v, %t, %v", collision, found, err)
		}
		if collision.First().String() != "A/Stra\u00dfe.md" || collision.Second().String() != "a/STRASSE.MD" ||
			collision.Key().Canonical() != "a/strasse.md" || collision.Kind() != pathsafe.EquivalentLeafCollision() {
			t.Fatalf("unexpected deterministic collision: %q / %q / %q",
				collision.First(), collision.Second(), collision.Key().Canonical())
		}
	}

	duplicate := mustRelativePath(t, "same/path")
	if collision, found, err := pathsafe.FindPathCollision([]pathsafe.RelativePath{duplicate, duplicate}); err != nil || !found ||
		!collision.Valid() || collision.Kind() != pathsafe.EquivalentLeafCollision() {
		t.Fatalf("exact duplicate collision = %#v, %t, %v", collision, found, err)
	}
	if collision, found, err := pathsafe.FindPathCollision([]pathsafe.RelativePath{mustRelativePath(t, "a"), mustRelativePath(t, "b")}); err != nil || found || collision.Valid() {
		t.Fatalf("non-collision = %#v, %t, %v", collision, found, err)
	}
	if collision, found, err := pathsafe.FindPathCollision([]pathsafe.RelativePath{{}}); !errors.Is(err, pathsafe.ErrInvalidRelativePath) || found || collision.Valid() {
		t.Fatalf("invalid collision input = %#v, %t, %v", collision, found, err)
	}
}

func TestFindPathCollisionRejectsCanonicalAncestorLeafNamespaces(t *testing.T) {
	t.Parallel()
	fixtures := []struct {
		name       string
		spellings  []string
		wantFirst  string
		wantSecond string
		wantKey    string
	}{
		{
			name: "case with intervening sibling",
			spellings: []string{
				"a/b", "a-foo", "unrelated", "A",
			},
			wantFirst: "A", wantSecond: "a/b", wantKey: "a",
		},
		{
			name: "canonical Unicode with intervening sibling",
			spellings: []string{
				"Cafe\u0301/x", "caf\u00e9-branch", "other", "Caf\u00e9",
			},
			wantFirst: "Caf\u00e9", wantSecond: "Cafe\u0301/x", wantKey: "caf\u00e9",
		},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			paths := make([]pathsafe.RelativePath, len(fixture.spellings))
			for index, spelling := range fixture.spellings {
				paths[index] = mustRelativePath(t, spelling)
			}
			permutations := [][]pathsafe.RelativePath{
				slices.Clone(paths), slices.Clone(paths), slices.Clone(paths),
			}
			slices.Reverse(permutations[1])
			permutations[2] = append(permutations[2][1:], permutations[2][0])
			for _, permutation := range permutations {
				collision, found, err := pathsafe.FindPathCollision(permutation)
				if err != nil || !found || !collision.Valid() ||
					collision.Kind() != pathsafe.AncestorLeafCollision() {
					t.Fatalf("ancestor collision = %#v, %t, %v", collision, found, err)
				}
				if collision.First().String() != fixture.wantFirst ||
					collision.Second().String() != fixture.wantSecond ||
					collision.Key().Canonical() != fixture.wantKey {
					t.Fatalf("unexpected ancestor collision: %q / %q / %q",
						collision.First(), collision.Second(), collision.Key().Canonical())
				}
			}
		})
	}
}

func mustCollisionKey(t *testing.T, spelling string) pathsafe.CollisionKey {
	t.Helper()
	key, err := pathsafe.NewCollisionKey(mustRelativePath(t, spelling))
	if err != nil {
		t.Fatal(err)
	}
	return key
}
