package pathsafe_test

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/pathsafe"
)

func TestFilesystemIdentifierEncodingIsVersionedInjectiveAndPathSafe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		canonical string
		component string
	}{
		{canonical: "ai4j", component: "id1-6169346a"},
		{canonical: "A", component: "id1-41"},
		{canonical: "a", component: "id1-61"},
		{canonical: "\u00e9", component: "id1-c3a9"},
		{canonical: "e\u0301", component: "id1-65cc81"},
		{canonical: "con", component: "id1-636f6e"},
		{canonical: "nul", component: "id1-6e756c"},
		{canonical: "NUL.txt", component: "id1-4e554c2e747874"},
		{canonical: "trailing.", component: "id1-747261696c696e672e"},
		{canonical: "a/b\\c.", component: "id1-612f625c632e"},
	}
	seen := make(map[string]string, len(tests))
	for _, test := range tests {
		identifier, err := pathsafe.NewFilesystemIdentifier(test.canonical)
		if err != nil {
			t.Fatal(err)
		}
		if !identifier.Valid() || identifier.Component() != test.component || identifier.String() != test.component {
			t.Fatalf("identifier %q = %q", test.canonical, identifier.Component())
		}
		if previous, duplicate := seen[identifier.Component()]; duplicate && previous != test.canonical {
			t.Fatalf("%q and %q encoded identically", previous, test.canonical)
		}
		seen[identifier.Component()] = test.canonical
		decoded, decodeErr := hex.DecodeString(strings.TrimPrefix(identifier.Component(), pathsafe.FilesystemIdentifierPrefix))
		if decodeErr != nil || string(decoded) != test.canonical {
			t.Fatalf("identifier %q did not round-trip: %q, %v", test.canonical, decoded, decodeErr)
		}
		path, pathErr := pathsafe.NewRelativePath(identifier.Component())
		if pathErr != nil || !path.Valid() {
			t.Fatalf("encoded component is not path-safe: %q, %v", identifier.Component(), pathErr)
		}
	}
	if seen["id1-41"] == seen["id1-61"] || seen["id1-c3a9"] == seen["id1-65cc81"] {
		t.Fatal("test inputs did not remain distinct")
	}
}

func TestFilesystemIdentifierHonorsExactEncodedComponentBound(t *testing.T) {
	t.Parallel()
	canonical := strings.Repeat("a", pathsafe.MaximumFilesystemIdentifierBytes)
	identifier, err := pathsafe.NewFilesystemIdentifier(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(identifier.Component()), len(pathsafe.FilesystemIdentifierPrefix)+2*pathsafe.MaximumFilesystemIdentifierBytes; got != want || got != 254 {
		t.Fatalf("encoded length = %d, want %d", got, want)
	}
	if identifier, err := pathsafe.NewFilesystemIdentifier(canonical + "a"); !errors.Is(err, pathsafe.ErrInvalidFilesystemIdentifier) || identifier.Valid() {
		t.Fatalf("over-limit identifier = %q, %v", identifier, err)
	}
}

func TestFilesystemIdentifierRejectsUnsafeCanonicalInputs(t *testing.T) {
	t.Parallel()
	invalidUTF8 := string([]byte{0xff})
	inputs := []string{
		"", "line\nbreak", "nul\x00byte", invalidUTF8,
	}
	for _, input := range inputs {
		identifier, err := pathsafe.NewFilesystemIdentifier(input)
		if !errors.Is(err, pathsafe.ErrInvalidFilesystemIdentifier) || identifier.Valid() {
			t.Fatalf("unsafe identifier %q accepted as %q", input, identifier)
		}
	}
	if (pathsafe.FilesystemIdentifier{}).Valid() {
		t.Fatal("zero filesystem identifier is valid")
	}
}
