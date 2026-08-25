package pathsafe_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/pathsafe"
)

func FuzzRelativePathContracts(f *testing.F) {
	for _, seed := range []string{
		"a", "skills/Agent.md", "skills/Caf\u00e9.md", "skills/Cafe\u0301.md",
		"cherokee/Ꮟ.md", "cherokee/ꮟ.md", "../escape", "a\\b", "NUL",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, spelling string) {
		path, err := pathsafe.NewRelativePath(spelling)
		if err != nil {
			return
		}
		if !path.Valid() || path.String() != spelling || strings.Join(path.Components(), "/") != spelling {
			t.Fatalf("accepted path lost its exact spelling: %q", spelling)
		}
		key, keyErr := pathsafe.NewCollisionKey(path)
		if keyErr != nil || !key.Valid() || !utf8.ValidString(key.Canonical()) {
			t.Fatalf("accepted path has invalid key: %q / %v", spelling, keyErr)
		}
		repeated, repeatedErr := pathsafe.NewCollisionKey(path)
		if repeatedErr != nil || repeated != key {
			t.Fatalf("collision key is nondeterministic: %#v / %#v", key, repeated)
		}
	})
}

func FuzzFilesystemIdentifierIsInjective(f *testing.F) {
	f.Add("ai4j", "AI4J")
	f.Add("\u00e9", "e\u0301")
	f.Add("safe", "NUL")
	f.Fuzz(func(t *testing.T, left, right string) {
		leftID, leftErr := pathsafe.NewFilesystemIdentifier(left)
		rightID, rightErr := pathsafe.NewFilesystemIdentifier(right)
		for _, accepted := range []struct {
			identifier pathsafe.FilesystemIdentifier
			err        error
		}{{identifier: leftID, err: leftErr}, {identifier: rightID, err: rightErr}} {
			if accepted.err != nil {
				continue
			}
			if !accepted.identifier.Valid() || len(accepted.identifier.Component()) > pathsafe.MaximumPathComponentBytes {
				t.Fatalf("invalid accepted identifier: %q", accepted.identifier)
			}
			if path, err := pathsafe.NewRelativePath(accepted.identifier.Component()); err != nil || !path.Valid() {
				t.Fatalf("encoding is not path-safe: %q", accepted.identifier)
			}
		}
		if leftErr == nil && rightErr == nil && left != right && leftID == rightID {
			t.Fatalf("distinct inputs encoded identically: %q / %q", left, right)
		}
	})
}

func FuzzPathCollisionCoversNormalizedLeafNamespaces(f *testing.F) {
	f.Add("A", "a/b")
	f.Add("Caf\u00e9", "Cafe\u0301/x")
	f.Add("same/path", "SAME/PATH")
	f.Add("a", "a-foo")
	f.Fuzz(func(t *testing.T, leftSpelling, rightSpelling string) {
		left, leftErr := pathsafe.NewRelativePath(leftSpelling)
		right, rightErr := pathsafe.NewRelativePath(rightSpelling)
		if leftErr != nil || rightErr != nil {
			return
		}
		leftKey, leftKeyErr := pathsafe.NewCollisionKey(left)
		rightKey, rightKeyErr := pathsafe.NewCollisionKey(right)
		if leftKeyErr != nil || rightKeyErr != nil {
			t.Fatalf("validated paths lacked collision keys: %v / %v", leftKeyErr, rightKeyErr)
		}
		equivalent := leftKey == rightKey
		ancestor := strings.HasPrefix(leftKey.Canonical(), rightKey.Canonical()+"/") ||
			strings.HasPrefix(rightKey.Canonical(), leftKey.Canonical()+"/")
		collision, found, err := pathsafe.FindPathCollision([]pathsafe.RelativePath{left, right})
		if err != nil || found != (equivalent || ancestor) {
			t.Fatalf("collision result = %#v, %t, %v; equivalent=%t ancestor=%t",
				collision, found, err, equivalent, ancestor)
		}
		if !found {
			return
		}
		if !collision.Valid() || equivalent != (collision.Kind() == pathsafe.EquivalentLeafCollision()) ||
			ancestor != (collision.Kind() == pathsafe.AncestorLeafCollision()) {
			t.Fatalf("invalid collision classification: %#v", collision)
		}
		reversed, reversedFound, reversedErr := pathsafe.FindPathCollision([]pathsafe.RelativePath{right, left})
		if reversedErr != nil || !reversedFound || reversed != collision {
			t.Fatalf("collision changed with input order: %#v / %#v / %v", collision, reversed, reversedErr)
		}
	})
}

func FuzzPathCollisionOrderingWithInterveningSibling(f *testing.F) {
	f.Add("A", "a/b", "a-foo")
	f.Add("Caf\u00e9", "Cafe\u0301/x", "caf\u00e9-branch")
	f.Add("a/b", "a/c", "a-foo")
	f.Fuzz(func(t *testing.T, firstSpelling, secondSpelling, thirdSpelling string) {
		spellings := []string{firstSpelling, secondSpelling, thirdSpelling}
		paths := make([]pathsafe.RelativePath, len(spellings))
		keys := make([]pathsafe.CollisionKey, len(spellings))
		for index, spelling := range spellings {
			path, err := pathsafe.NewRelativePath(spelling)
			if err != nil {
				return
			}
			key, keyErr := pathsafe.NewCollisionKey(path)
			if keyErr != nil {
				t.Fatalf("validated path lacked a collision key: %v", keyErr)
			}
			paths[index], keys[index] = path, key
		}
		wantCollision := false
		for left := 0; left < len(keys); left++ {
			for right := left + 1; right < len(keys); right++ {
				wantCollision = wantCollision || collisionKeysConflict(keys[left], keys[right])
			}
		}
		collision, found, err := pathsafe.FindPathCollision(paths)
		if err != nil || found != wantCollision || (found && !collision.Valid()) {
			t.Fatalf("three-leaf collision = %#v, %t, %v; want %t", collision, found, err, wantCollision)
		}
		reversed := []pathsafe.RelativePath{paths[2], paths[1], paths[0]}
		reversedCollision, reversedFound, reversedErr := pathsafe.FindPathCollision(reversed)
		if reversedErr != nil || reversedFound != found || reversedCollision != collision {
			t.Fatalf("three-leaf collision changed with input order: %#v / %#v / %v",
				collision, reversedCollision, reversedErr)
		}
	})
}

func collisionKeysConflict(left, right pathsafe.CollisionKey) bool {
	return left == right || strings.HasPrefix(left.Canonical(), right.Canonical()+"/") ||
		strings.HasPrefix(right.Canonical(), left.Canonical()+"/")
}

func FuzzRootPlacementUsesExactPrefix(f *testing.F) {
	f.Add("plugins/ai4j", "plugins/ai4j/skills/a.md")
	f.Add("plugins/Caf\u00e9", "plugins/Cafe\u0301/file")
	f.Add("plugins/a", "plugins/ab/file")
	f.Fuzz(func(t *testing.T, rootSpelling, pathSpelling string) {
		rootPath, rootErr := pathsafe.NewRelativePath(rootSpelling)
		path, pathErr := pathsafe.NewRelativePath(pathSpelling)
		if rootErr != nil || pathErr != nil {
			return
		}
		root, err := pathsafe.NewClosedRoot(rootPath)
		if err != nil {
			t.Fatal(err)
		}
		placement, placementErr := pathsafe.PlaceUnderRoot(root, path)
		inside := strings.HasPrefix(pathSpelling, rootSpelling+"/")
		if !inside {
			if !errors.Is(placementErr, pathsafe.ErrPathOutsideClosedRoot) || placement.Valid() {
				t.Fatalf("non-descendant placement succeeded: %q / %q", rootSpelling, pathSpelling)
			}
			return
		}
		if placementErr != nil || !placement.Valid() ||
			placement.Relative().String() != strings.TrimPrefix(pathSpelling, rootSpelling+"/") {
			t.Fatalf("invalid exact-prefix placement: %q / %q", rootSpelling, pathSpelling)
		}
	})
}
