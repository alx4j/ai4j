package pathsafe

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	// CollisionUnicodeVersion pins the Unicode data used by collision-key v1.
	// Changing the tables or algorithm requires a new CollisionKeyVersion.
	CollisionUnicodeVersion = "15.0.0"
	collisionKeyV1Label     = "unicode15_nfc_casefold_v1"
	// MaximumCollisionKeyBytes bounds comparison identities even if a value is
	// constructed inside this package without using NewCollisionKey.
	MaximumCollisionKeyBytes = 64 << 10
)

var ErrInvalidCollisionKey = errors.New("invalid collision key")

// CollisionKeyVersion identifies a closed comparison algorithm.
type CollisionKeyVersion struct{ value uint8 }

var collisionKeyVersionV1 = CollisionKeyVersion{value: 1}

func CollisionKeyVersionV1() CollisionKeyVersion { return collisionKeyVersionV1 }

func (v CollisionKeyVersion) String() string {
	if v == collisionKeyVersionV1 {
		return collisionKeyV1Label
	}
	return "invalid"
}

func (v CollisionKeyVersion) Valid() bool { return v == collisionKeyVersionV1 }

// CollisionKey is a comparable, versioned identity used only for collision
// detection and deterministic ordering. Canonical must never be used to open or
// join a filesystem path.
type CollisionKey struct {
	version   CollisionKeyVersion
	canonical string
}

func NewCollisionKey(path RelativePath) (CollisionKey, error) {
	if !path.Valid() {
		return CollisionKey{}, ErrInvalidRelativePath
	}
	key := CollisionKey{
		version:   collisionKeyVersionV1,
		canonical: canonicalCollisionSpelling(path.spelling),
	}
	if !key.Valid() {
		return CollisionKey{}, ErrInvalidCollisionKey
	}
	return key, nil
}

func (k CollisionKey) Version() CollisionKeyVersion { return k.version }

// Canonical returns comparison text, not display text or filesystem authority.
func (k CollisionKey) Canonical() string { return k.canonical }

func (k CollisionKey) Valid() bool {
	return k.version.Valid() && k.canonical != "" && len(k.canonical) <= MaximumCollisionKeyBytes &&
		utf8.ValidString(k.canonical) && !strings.Contains(k.canonical, "\\") &&
		!containsControl(k.canonical) && canonicalCollisionSpelling(k.canonical) == k.canonical
}

// Compare returns -1, 0, or 1 using version followed by canonical UTF-8 bytes.
func (k CollisionKey) Compare(other CollisionKey) int {
	if comparison := strings.Compare(k.version.String(), other.version.String()); comparison != 0 {
		return comparison
	}
	return strings.Compare(k.canonical, other.canonical)
}

func canonicalCollisionSpelling(spelling string) string {
	canonical := norm.NFC.String(spelling)
	canonical = cases.Fold().String(canonical)
	canonical = strings.Map(canonicalCherokeeFoldV1, canonical)
	return norm.NFC.String(canonical)
}

func canonicalCherokeeFoldV1(character rune) rune {
	// Unicode 15 default folding uses the historic uppercase Cherokee
	// representatives. x/text v0.14 folds uppercase Cherokee in the opposite
	// direction, so collapse both lowercase ranges back to the pinned UCD v15
	// representatives after the full fold.
	switch {
	case character >= '\uAB70' && character <= '\uABBF':
		return character - ('\uAB70' - '\u13A0')
	case character >= '\u13F8' && character <= '\u13FD':
		return character - ('\u13F8' - '\u13F0')
	default:
		return character
	}
}

// PathCollision retains readable validated spellings separately from their
// canonical comparison identity.
type PathCollision struct {
	first  RelativePath
	second RelativePath
	key    CollisionKey
	kind   PathCollisionKind
}

func (c PathCollision) First() RelativePath  { return c.first }
func (c PathCollision) Second() RelativePath { return c.second }
func (c PathCollision) Key() CollisionKey    { return c.key }
func (c PathCollision) Kind() PathCollisionKind {
	return c.kind
}

// PathCollisionKind identifies how two output leaves conflict in the
// normalized destination namespace.
type PathCollisionKind struct{ value string }

var (
	equivalentLeafCollision = PathCollisionKind{value: "equivalent_leaf"}
	ancestorLeafCollision   = PathCollisionKind{value: "ancestor_leaf"}
)

func EquivalentLeafCollision() PathCollisionKind { return equivalentLeafCollision }
func AncestorLeafCollision() PathCollisionKind   { return ancestorLeafCollision }
func (k PathCollisionKind) String() string       { return k.value }
func (k PathCollisionKind) Valid() bool {
	return k == equivalentLeafCollision || k == ancestorLeafCollision
}

func (c PathCollision) Valid() bool {
	firstKey, firstErr := NewCollisionKey(c.first)
	secondKey, secondErr := NewCollisionKey(c.second)
	if firstErr != nil || secondErr != nil || !c.kind.Valid() || c.key != firstKey {
		return false
	}
	switch c.kind {
	case equivalentLeafCollision:
		return firstKey == secondKey && strings.Compare(c.first.spelling, c.second.spelling) <= 0
	case ancestorLeafCollision:
		return firstKey != secondKey && canonicalKeyIsStrictAncestor(firstKey, secondKey)
	default:
		return false
	}
}

// FindPathCollision treats every input as an output leaf and returns the
// deterministic lowest equivalent-leaf or ancestor-leaf namespace conflict.
// Declared roots may legitimately nest and must not use this leaf-set check.
// The function performs no filesystem operation.
func FindPathCollision(paths []RelativePath) (PathCollision, bool, error) {
	type keyedPath struct {
		path       RelativePath
		key        CollisionKey
		components []string
	}
	keyed := make([]keyedPath, len(paths))
	for index, path := range paths {
		key, err := NewCollisionKey(path)
		if err != nil {
			return PathCollision{}, false, err
		}
		keyed[index] = keyedPath{
			path: path, key: key, components: strings.Split(key.canonical, "/"),
		}
	}
	sort.Slice(keyed, func(left, right int) bool {
		if comparison := strings.Compare(keyed[left].key.version.String(), keyed[right].key.version.String()); comparison != 0 {
			return comparison < 0
		}
		if comparison := compareCanonicalComponents(keyed[left].components, keyed[right].components); comparison != 0 {
			return comparison < 0
		}
		return keyed[left].path.spelling < keyed[right].path.spelling
	})
	for index := 1; index < len(keyed); index++ {
		previous, current := keyed[index-1], keyed[index]
		switch {
		case previous.key == current.key:
			return PathCollision{
				first: previous.path, second: current.path, key: previous.key, kind: equivalentLeafCollision,
			}, true, nil
		case isStrictComponentPrefix(previous.components, current.components):
			return PathCollision{
				first: previous.path, second: current.path, key: previous.key, kind: ancestorLeafCollision,
			}, true, nil
		}
	}
	return PathCollision{}, false, nil
}

func compareCanonicalComponents(left, right []string) int {
	for index := 0; index < min(len(left), len(right)); index++ {
		if comparison := strings.Compare(left[index], right[index]); comparison != 0 {
			return comparison
		}
	}
	return len(left) - len(right)
}

func isStrictComponentPrefix(ancestor, descendant []string) bool {
	if len(ancestor) >= len(descendant) {
		return false
	}
	for index := range ancestor {
		if ancestor[index] != descendant[index] {
			return false
		}
	}
	return true
}

func canonicalKeyIsStrictAncestor(ancestor, descendant CollisionKey) bool {
	return ancestor.version == descendant.version &&
		strings.HasPrefix(descendant.canonical, ancestor.canonical+"/")
}
