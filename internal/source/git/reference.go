// Package git models immutable Git resolution and provenance facts. Native Git
// execution and workspace ownership are introduced by later substories.
package git

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/domain"
)

const maxReferenceBytes = 1024

var (
	ErrInvalidRequestedReference = errors.New("requested Git reference is invalid")
	ErrInvalidResolvedReference  = errors.New("resolved Git reference is invalid")
)

// RequestedReference preserves whether the user omitted the reference option.
// Its zero value is the valid omitted form.
type RequestedReference struct {
	value    string
	provided bool
}

// OmittedRequestedReference returns the explicit omitted option value.
func OmittedRequestedReference() RequestedReference { return RequestedReference{} }

// NewRequestedReference records the exact supported text supplied by the user.
func NewRequestedReference(value string) (RequestedReference, error) {
	if !validRequestedReference(value) {
		return RequestedReference{}, ErrInvalidRequestedReference
	}
	return RequestedReference{value: value, provided: true}, nil
}

// Value returns the exact requested text and whether the option was provided.
func (r RequestedReference) Value() (string, bool) { return r.value, r.provided }

func (r RequestedReference) Valid() bool {
	if !r.provided {
		return r.value == ""
	}
	return validRequestedReference(r.value)
}

// ResolvedReferenceKind is the closed semantic kind proven by resolution.
type ResolvedReferenceKind string

const (
	ResolvedDefaultBranch ResolvedReferenceKind = "default_branch"
	ResolvedBranch        ResolvedReferenceKind = "branch"
	ResolvedTag           ResolvedReferenceKind = "tag"
	ResolvedCommit        ResolvedReferenceKind = "commit"
)

func (k ResolvedReferenceKind) String() string { return string(k) }
func (k ResolvedReferenceKind) Valid() bool {
	switch k {
	case ResolvedDefaultBranch, ResolvedBranch, ResolvedTag, ResolvedCommit:
		return true
	default:
		return false
	}
}

// ResolvedReference retains a canonical short branch/tag name. Commit names
// are canonical full lower-case SHA-1 OIDs.
type ResolvedReference struct {
	kind ResolvedReferenceKind
	name string
}

func NewResolvedReference(kind ResolvedReferenceKind, name string) (ResolvedReference, error) {
	if !kind.Valid() || !validResolvedName(kind, name) {
		return ResolvedReference{}, ErrInvalidResolvedReference
	}
	return ResolvedReference{kind: kind, name: name}, nil
}

func (r ResolvedReference) Kind() ResolvedReferenceKind { return r.kind }
func (r ResolvedReference) Name() string                { return r.name }
func (r ResolvedReference) Valid() bool {
	return r.kind.Valid() && validResolvedName(r.kind, r.name)
}

func validResolvedName(kind ResolvedReferenceKind, name string) bool {
	if kind == ResolvedCommit {
		_, err := domain.NewCommitOID(name)
		return err == nil
	}
	return validShortReference(name)
}

func validRequestedReference(value string) bool {
	if !safeReferenceText(value) || strings.HasPrefix(value, "-") || value == "HEAD" || value == "@" {
		return false
	}
	if _, err := domain.NewCommitOID(value); err == nil {
		return true
	}
	if strings.HasPrefix(value, "refs/heads/") {
		return validShortReference(strings.TrimPrefix(value, "refs/heads/"))
	}
	if strings.HasPrefix(value, "refs/tags/") {
		return validShortReference(strings.TrimPrefix(value, "refs/tags/"))
	}
	return !strings.HasPrefix(value, "refs/") && validShortReference(value)
}

func validShortReference(value string) bool {
	if !safeReferenceText(value) || strings.HasPrefix(value, "refs/") || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.Contains(value, "//") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, " ~^:?*[\\") || value == "HEAD" || value == "@" {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

func safeReferenceText(value string) bool {
	if value == "" || len(value) > maxReferenceBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
