package pathsafe

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaximumRelativePathBytes bounds one complete canonical relative-path
	// spelling before any filesystem operation.
	MaximumRelativePathBytes = 4096
	// MaximumPathComponentBytes is the portable byte bound for one component.
	MaximumPathComponentBytes = 255
	// MaximumRelativePathComponents bounds traversal and comparison work.
	MaximumRelativePathComponents = 128
)

var (
	ErrInvalidRelativePath   = errors.New("invalid relative path")
	ErrInvalidClosedRoot     = errors.New("invalid closed root")
	ErrPathOutsideClosedRoot = errors.New("path is outside closed root")
)

// RelativePath preserves one validated slash-separated spelling exactly. It
// is suitable for display and rooted requests, but it is not filesystem
// authority and it is not a normalized collision identity.
type RelativePath struct{ spelling string }

func NewRelativePath(spelling string) (RelativePath, error) {
	if !validRelativePathSpelling(spelling) {
		return RelativePath{}, ErrInvalidRelativePath
	}
	return RelativePath{spelling: spelling}, nil
}

func (p RelativePath) String() string { return p.spelling }

// Components returns the exact validated components in display order.
func (p RelativePath) Components() []string {
	if !p.Valid() {
		return nil
	}
	return strings.Split(p.spelling, "/")
}

func (p RelativePath) Valid() bool {
	validated, err := NewRelativePath(p.spelling)
	return err == nil && validated == p
}

// ClosedRoot identifies a declared repository-relative boundary. It carries
// no host handle and grants no authority by itself.
type ClosedRoot struct{ path RelativePath }

func NewClosedRoot(path RelativePath) (ClosedRoot, error) {
	if !path.Valid() {
		return ClosedRoot{}, ErrInvalidClosedRoot
	}
	return ClosedRoot{path: path}, nil
}

func (r ClosedRoot) Path() RelativePath { return r.path }

func (r ClosedRoot) Valid() bool {
	validated, err := NewClosedRoot(r.path)
	return err == nil && validated == r
}

// RootPlacement proves that Path is a strict descendant of Root by exact
// component spelling. Case-folded or Unicode-equivalent prefixes never grant
// containment.
type RootPlacement struct {
	root     ClosedRoot
	path     RelativePath
	relative RelativePath
}

// PlaceUnderRoot constructs an exact-prefix placement and returns the path
// relative to that closed root. A root is not considered a member of itself.
func PlaceUnderRoot(root ClosedRoot, path RelativePath) (RootPlacement, error) {
	if !root.Valid() {
		return RootPlacement{}, ErrInvalidClosedRoot
	}
	if !path.Valid() {
		return RootPlacement{}, ErrInvalidRelativePath
	}
	prefix := root.path.spelling + "/"
	if !strings.HasPrefix(path.spelling, prefix) {
		return RootPlacement{}, ErrPathOutsideClosedRoot
	}
	relative, err := NewRelativePath(strings.TrimPrefix(path.spelling, prefix))
	if err != nil {
		return RootPlacement{}, ErrPathOutsideClosedRoot
	}
	return RootPlacement{root: root, path: path, relative: relative}, nil
}

func (p RootPlacement) Root() ClosedRoot       { return p.root }
func (p RootPlacement) Path() RelativePath     { return p.path }
func (p RootPlacement) Relative() RelativePath { return p.relative }

func (p RootPlacement) Valid() bool {
	validated, err := PlaceUnderRoot(p.root, p.path)
	return err == nil && validated == p
}

func validRelativePathSpelling(spelling string) bool {
	if spelling == "" || len(spelling) > MaximumRelativePathBytes || !utf8.ValidString(spelling) ||
		strings.HasPrefix(spelling, "/") || strings.Contains(spelling, "\\") || hasWindowsVolumePrefix(spelling) ||
		containsControl(spelling) {
		return false
	}
	components := strings.Split(spelling, "/")
	if len(components) > MaximumRelativePathComponents {
		return false
	}
	for _, component := range components {
		if !validPathComponent(component) {
			return false
		}
	}
	return true
}

func validPathComponent(component string) bool {
	if component == "" || component == "." || component == ".." || len(component) > MaximumPathComponentBytes ||
		!utf8.ValidString(component) || strings.ContainsAny(component, "/\\<>:\"|?*") || hasWindowsVolumePrefix(component) ||
		containsControl(component) || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
		return false
	}
	return !platformDeviceComponent(component)
}

func hasWindowsVolumePrefix(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func platformDeviceComponent(component string) bool {
	trimmed := strings.TrimRight(component, " .")
	if separator := strings.IndexAny(trimmed, ".:"); separator >= 0 {
		trimmed = trimmed[:separator]
	}
	name := strings.ToUpper(strings.TrimRight(trimmed, " "))
	switch name {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$", "CONIN$", "CONOUT$":
		return true
	}
	for _, prefix := range []string{"COM", "LPT"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		switch strings.TrimPrefix(name, prefix) {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return true
		}
	}
	return false
}
