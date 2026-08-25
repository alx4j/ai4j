package pathsafe_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/pathsafe"
)

func TestRelativePathPreservesValidatedSpellingAndComponents(t *testing.T) {
	t.Parallel()
	spellings := []string{
		"toolkit.json",
		"plugins/ai4j-default/.claude-plugin/plugin.json",
		"skills/Caf\u00e9/reference file.md",
		"skills/Cafe\u0301/reference file.md",
		strings.Repeat("a", pathsafe.MaximumPathComponentBytes),
		exactMaximumPath(),
	}
	for _, spelling := range spellings {
		spelling := spelling
		t.Run(spelling[:min(len(spelling), 48)], func(t *testing.T) {
			t.Parallel()
			path, err := pathsafe.NewRelativePath(spelling)
			if err != nil {
				t.Fatal(err)
			}
			if got := path.String(); got != spelling {
				t.Fatalf("String() = %q, want %q", got, spelling)
			}
			if !path.Valid() || strings.Join(path.Components(), "/") != spelling {
				t.Fatalf("invalid reconstructed path: %#v", path.Components())
			}
			components := path.Components()
			components[0] = "mutated"
			if path.String() != spelling {
				t.Fatal("component result mutated the relative path")
			}
		})
	}
	if (pathsafe.RelativePath{}).Valid() || (pathsafe.RelativePath{}).Components() != nil {
		t.Fatal("zero relative path is valid")
	}
}

func TestRelativePathRejectsHostileAndNonportableSpellings(t *testing.T) {
	t.Parallel()
	invalidUTF8 := string([]byte{'a', 0xff})
	paths := []string{
		"", ".", "..", "/absolute", "//server/share", "C:/absolute", "c:relative",
		"a/", "a//b", "a/./b", "a/../b", "a\\b", "a/b\\c",
		"a<b", "a>b", "a:b", "a\"b", "a|b", "a?b", "a*b",
		"a\x00b", "a\nb", "a\u007fb", invalidUTF8,
		"trailing.", "trailing ", "a/trailing.", "a/trailing ",
		strings.Repeat("a", pathsafe.MaximumPathComponentBytes+1),
		exactMaximumPath() + "a",
		strings.TrimSuffix(strings.Repeat("a/", pathsafe.MaximumRelativePathComponents+1), "/"),
	}
	for _, spelling := range paths {
		if path, err := pathsafe.NewRelativePath(spelling); !errors.Is(err, pathsafe.ErrInvalidRelativePath) || path.Valid() {
			t.Fatalf("NewRelativePath(%q) = %q, %v", spelling, path.String(), err)
		}
	}
	maximumComponents := make([]string, pathsafe.MaximumRelativePathComponents)
	for index := range maximumComponents {
		maximumComponents[index] = "a"
	}
	if path, err := pathsafe.NewRelativePath(strings.Join(maximumComponents, "/")); err != nil || !path.Valid() {
		t.Fatalf("maximum component count rejected: %v", err)
	}
}

func TestRelativePathRejectsPlatformDeviceComponents(t *testing.T) {
	t.Parallel()
	devices := []string{
		"CON", "con.txt", "PRN", "AUX.log", "NUL", "nul .txt", "CLOCK$", "clock$.json",
		"CONIN$", "conout$.txt", "COM1", "com9.log", "LPT1", "lpt9.txt",
		"COM¹", "com².txt", "LPT³", "lpt¹.log", "NUL:", "a/NUL.txt/b",
	}
	for _, spelling := range devices {
		if path, err := pathsafe.NewRelativePath(spelling); !errors.Is(err, pathsafe.ErrInvalidRelativePath) || path.Valid() {
			t.Fatalf("device path %q accepted", spelling)
		}
	}
	for _, spelling := range []string{"console", "nulled", "COM0", "COM10", "LPT0", ".NUL", "CLOCK"} {
		if path, err := pathsafe.NewRelativePath(spelling); err != nil || !path.Valid() {
			t.Fatalf("ordinary path %q rejected: %v", spelling, err)
		}
	}
}

func TestRootPlacementUsesExactComponentPrefix(t *testing.T) {
	t.Parallel()
	rootPath := mustRelativePath(t, "plugins/Caf\u00e9")
	root, err := pathsafe.NewClosedRoot(rootPath)
	if err != nil || !root.Valid() || root.Path() != rootPath {
		t.Fatalf("closed root = %#v, %v", root, err)
	}
	member := mustRelativePath(t, "plugins/Caf\u00e9/skills/Agent.md")
	placement, err := pathsafe.PlaceUnderRoot(root, member)
	if err != nil {
		t.Fatal(err)
	}
	if !placement.Valid() || placement.Root() != root || placement.Path() != member ||
		placement.Relative().String() != "skills/Agent.md" {
		t.Fatalf("placement = %#v", placement)
	}

	outside := []RelativePathCase{
		{name: "root itself", path: "plugins/Caf\u00e9"},
		{name: "sibling prefix", path: "plugins/Caf\u00e9-extra/file"},
		{name: "case collision", path: "plugins/caf\u00e9/file"},
		{name: "canonical Unicode collision", path: "plugins/Cafe\u0301/file"},
		{name: "ancestor", path: "plugins/file"},
	}
	for _, test := range outside {
		t.Run(test.name, func(t *testing.T) {
			candidate := mustRelativePath(t, test.path)
			got, placementErr := pathsafe.PlaceUnderRoot(root, candidate)
			if !errors.Is(placementErr, pathsafe.ErrPathOutsideClosedRoot) || got.Valid() {
				t.Fatalf("PlaceUnderRoot(%q) = %#v, %v", test.path, got, placementErr)
			}
		})
	}

	if closed, closedErr := pathsafe.NewClosedRoot(pathsafe.RelativePath{}); !errors.Is(closedErr, pathsafe.ErrInvalidClosedRoot) || closed.Valid() {
		t.Fatalf("zero closed root = %#v, %v", closed, closedErr)
	}
	if placement, placementErr := pathsafe.PlaceUnderRoot(root, pathsafe.RelativePath{}); !errors.Is(placementErr, pathsafe.ErrInvalidRelativePath) || placement.Valid() {
		t.Fatalf("zero member placement = %#v, %v", placement, placementErr)
	}
	if (pathsafe.RootPlacement{}).Valid() {
		t.Fatal("zero root placement is valid")
	}
}

type RelativePathCase struct {
	name string
	path string
}

func exactMaximumPath() string {
	components := make([]string, 17)
	for index := range components {
		components[index] = strings.Repeat("a", 240)
	}
	return strings.Join(components, "/")
}

func mustRelativePath(t *testing.T, spelling string) pathsafe.RelativePath {
	t.Helper()
	path, err := pathsafe.NewRelativePath(spelling)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
