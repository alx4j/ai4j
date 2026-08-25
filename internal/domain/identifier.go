package domain

import (
	"encoding/hex"
	"fmt"
	"regexp"
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type OperationID struct{ value string }

func NewOperationID(value string) (OperationID, error) {
	if !identifierPattern.MatchString(value) {
		return OperationID{}, fmt.Errorf("operation ID %q is not canonical", value)
	}
	return OperationID{value: value}, nil
}
func (v OperationID) String() string { return v.value }
func (v OperationID) Valid() bool    { return identifierPattern.MatchString(v.value) }

type InstallationID struct{ value string }

func NewInstallationID(value string) (InstallationID, error) {
	if !identifierPattern.MatchString(value) {
		return InstallationID{}, fmt.Errorf("installation ID %q is not canonical", value)
	}
	return InstallationID{value: value}, nil
}
func (v InstallationID) String() string { return v.value }
func (v InstallationID) Valid() bool    { return identifierPattern.MatchString(v.value) }

// ArtifactToken is a caller-generated, pre-journaled 128-bit token used to
// derive unguessable, deterministic operation artifact names.
type ArtifactToken struct{ value [16]byte }

func NewArtifactToken(value string) (ArtifactToken, error) {
	if len(value) != 32 {
		return ArtifactToken{}, fmt.Errorf("artifact token must contain exactly 32 lowercase hexadecimal characters")
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return ArtifactToken{}, fmt.Errorf("artifact token must contain only lowercase hexadecimal characters")
		}
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return ArtifactToken{}, fmt.Errorf("decode artifact token: %w", err)
	}
	var token ArtifactToken
	copy(token.value[:], decoded)
	if !token.Valid() {
		return ArtifactToken{}, fmt.Errorf("artifact token must not be all zero")
	}
	return token, nil
}

func (v ArtifactToken) String() string { return hex.EncodeToString(v.value[:]) }
func (v ArtifactToken) Valid() bool    { return v != ArtifactToken{} }
