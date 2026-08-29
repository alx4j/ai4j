// Package domain contains target-neutral values used by the AI4J lifecycle core.
package domain

import (
	"fmt"
	"regexp"
)

var symbolPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func validateSymbol(kind, value string) error {
	if !symbolPattern.MatchString(value) {
		return fmt.Errorf("%s %q is not a canonical identifier", kind, value)
	}
	return nil
}

type SourceSelection struct{ value string }

func NewSourceSelection(value string) (SourceSelection, error) {
	if err := validateSymbol("source selection", value); err != nil {
		return SourceSelection{}, err
	}
	return SourceSelection{value: value}, nil
}
func (v SourceSelection) String() string { return v.value }
func (v SourceSelection) Valid() bool    { return symbolPattern.MatchString(v.value) }

// GitTransport is the credential-free access preference retained for a
// Git source. It is deliberately separate from RepositoryIdentity: two
// transports that reach the same repository must compare as one source.
type GitTransport struct{ value string }

func NewGitTransport(value string) (GitTransport, error) {
	if value != "https" && value != "ssh" {
		return GitTransport{}, fmt.Errorf("git transport is not supported")
	}
	return GitTransport{value: value}, nil
}
func (v GitTransport) String() string { return v.value }
func (v GitTransport) Valid() bool    { return v.value == "https" || v.value == "ssh" }
func (v GitTransport) MarshalText() ([]byte, error) {
	if !v.Valid() {
		return nil, fmt.Errorf("invalid git transport")
	}
	return []byte(v.value), nil
}

type ObjectFormat struct{ value string }

func NewObjectFormat(value string) (ObjectFormat, error) {
	if err := validateSymbol("object format", value); err != nil {
		return ObjectFormat{}, err
	}
	return ObjectFormat{value: value}, nil
}
func (v ObjectFormat) String() string { return v.value }
func (v ObjectFormat) Valid() bool    { return symbolPattern.MatchString(v.value) }

var (
	sourceSelectionBuiltInDefault = mustValue(NewSourceSelection("built_in_default"))
	sourceSelectionExplicit       = mustValue(NewSourceSelection("explicit"))
	gitTransportHTTPS             = mustValue(NewGitTransport("https"))
	gitTransportSSH               = mustValue(NewGitTransport("ssh"))
	objectFormatSHA1              = mustValue(NewObjectFormat("sha1"))
)

func BuiltInDefaultSource() SourceSelection { return sourceSelectionBuiltInDefault }
func ExplicitSource() SourceSelection       { return sourceSelectionExplicit }
func HTTPSGitTransport() GitTransport       { return gitTransportHTTPS }
func SSHGitTransport() GitTransport         { return gitTransportSSH }
func SHA1ObjectFormat() ObjectFormat        { return objectFormatSHA1 }

func mustValue[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}
