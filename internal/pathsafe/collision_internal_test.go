package pathsafe

import (
	"strings"
	"testing"
)

func TestCollisionKeyValidityEnforcesCanonicalSizeCeiling(t *testing.T) {
	t.Parallel()
	key := CollisionKey{version: collisionKeyVersionV1, canonical: strings.Repeat("a", MaximumCollisionKeyBytes)}
	if !key.Valid() {
		t.Fatal("exact collision-key ceiling rejected")
	}
	key.canonical += "a"
	if key.Valid() {
		t.Fatal("over-limit collision key accepted")
	}
}

func TestCollisionCanonicalizationIsIdempotentForCherokee(t *testing.T) {
	t.Parallel()
	first := canonicalCollisionSpelling("ꮟ")
	second := canonicalCollisionSpelling(first)
	if first != "Ꮟ" || second != first {
		t.Fatalf("Cherokee fold was not canonical: first %q (%U), second %q (%U)", first, []rune(first), second, []rune(second))
	}
}
