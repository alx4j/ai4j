package pathsafe

import (
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	// MaximumFilesystemIdentifierBytes keeps the versioned lowercase-hex
	// component at 254 bytes or fewer.
	MaximumFilesystemIdentifierBytes = 125
	// FilesystemIdentifierPrefix versions the injective component encoding.
	FilesystemIdentifierPrefix = "id1-"
)

var ErrInvalidFilesystemIdentifier = errors.New("invalid filesystem identifier")

// FilesystemIdentifier is an injective path-component encoding of an already
// canonical identifier. It intentionally does not retain or expose the source
// spelling as filesystem authority.
type FilesystemIdentifier struct{ component string }

func NewFilesystemIdentifier(canonical string) (FilesystemIdentifier, error) {
	if canonical == "" || len(canonical) > MaximumFilesystemIdentifierBytes || !utf8.ValidString(canonical) ||
		containsControl(canonical) {
		return FilesystemIdentifier{}, ErrInvalidFilesystemIdentifier
	}
	component := FilesystemIdentifierPrefix + hex.EncodeToString([]byte(canonical))
	if !validPathComponent(component) {
		return FilesystemIdentifier{}, ErrInvalidFilesystemIdentifier
	}
	return FilesystemIdentifier{component: component}, nil
}

// Component returns only the safe versioned encoding.
func (i FilesystemIdentifier) Component() string { return i.component }
func (i FilesystemIdentifier) String() string    { return i.component }

func (i FilesystemIdentifier) Valid() bool {
	if !strings.HasPrefix(i.component, FilesystemIdentifierPrefix) {
		return false
	}
	encoded := strings.TrimPrefix(i.component, FilesystemIdentifierPrefix)
	if encoded == "" || len(encoded)%2 != 0 {
		return false
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) == 0 || len(decoded) > MaximumFilesystemIdentifierBytes ||
		!utf8.Valid(decoded) || containsControl(string(decoded)) || !validPathComponent(i.component) {
		return false
	}
	validated, err := NewFilesystemIdentifier(string(decoded))
	return err == nil && validated == i
}
