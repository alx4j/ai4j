// Package github parses and normalizes the closed GitHub source forms accepted
// by AI4J. It never performs authentication or repository I/O.
package github

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/domain"
)

const maxRepositoryInputBytes = 256

var (
	ownerPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$`)
	repositoryPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)
)

// ErrorCode is a bounded public-safe classification. The rejected input is
// intentionally never retained in an error.
type ErrorCode string

const (
	ErrorInvalidRepository ErrorCode = "invalid_repository"
	ErrorInvalidReference  ErrorCode = "invalid_reference"
	ErrorInvalidSelection  ErrorCode = "invalid_source_selection"
	ErrorAccessFailed      ErrorCode = "source_access_failed"
)

// SelectionError reports a source-selection failure without echoing raw input,
// URLs, helper output, credentials, or native errors.
type SelectionError struct{ code ErrorCode }

func (e SelectionError) Error() string {
	switch e.code {
	case ErrorInvalidRepository:
		return "GitHub repository is not in a supported canonical form"
	case ErrorInvalidReference:
		return "Git reference is not a safe explicit value"
	case ErrorInvalidSelection:
		return "source selection is internally inconsistent"
	case ErrorAccessFailed:
		return "GitHub source access failed"
	default:
		return "source selection failed"
	}
}

func (e SelectionError) Code() ErrorCode { return e.code }

// Is supports errors.Is without exposing rejected data.
func (e SelectionError) Is(target error) bool {
	other, ok := target.(SelectionError)
	return ok && e.code == other.code
}

func newError(code ErrorCode) error { return SelectionError{code: code} }

// ParsedRepository is a canonical repository identity paired with the
// credential-free transport preference expressed by the accepted spelling.
type ParsedRepository struct {
	identity  domain.RepositoryIdentity
	transport domain.GitTransport
}

func (r ParsedRepository) Identity() domain.RepositoryIdentity { return r.identity }
func (r ParsedRepository) Transport() domain.GitTransport      { return r.transport }
func (r ParsedRepository) valid() bool {
	return r.identity.Valid() && r.transport.Valid()
}

// ParseRepository accepts exactly owner/repository, the canonical GitHub HTTPS
// URL, or the canonical GitHub SSH form. HTTPS and SSH URLs require the .git
// suffix; shorthand must not contain it.
func ParseRepository(value string) (ParsedRepository, error) {
	if !safeInput(value, maxRepositoryInputBytes) || strings.HasPrefix(value, "-") {
		return ParsedRepository{}, newError(ErrorInvalidRepository)
	}

	var owner, repository string
	transport := domain.HTTPSGitTransport()
	switch {
	case strings.HasPrefix(value, "https://github.com/"):
		path := strings.TrimPrefix(value, "https://github.com/")
		owner, repository = splitRepositoryPath(path, true)
	case strings.HasPrefix(value, "git@github.com:"):
		path := strings.TrimPrefix(value, "git@github.com:")
		owner, repository = splitRepositoryPath(path, true)
		transport = domain.SSHGitTransport()
	default:
		owner, repository = splitRepositoryPath(value, false)
	}
	if owner == "" || repository == "" {
		return ParsedRepository{}, newError(ErrorInvalidRepository)
	}

	owner = strings.ToLower(owner)
	repository = strings.ToLower(repository)
	if !ownerPattern.MatchString(owner) || strings.Contains(owner, "--") || !repositoryPattern.MatchString(repository) || strings.HasSuffix(repository, ".git") {
		return ParsedRepository{}, newError(ErrorInvalidRepository)
	}
	identity, err := domain.NewRepositoryIdentity("github.com/" + owner + "/" + repository)
	if err != nil {
		return ParsedRepository{}, newError(ErrorInvalidRepository)
	}
	parsed := ParsedRepository{identity: identity, transport: transport}
	if !parsed.valid() {
		return ParsedRepository{}, newError(ErrorInvalidRepository)
	}
	return parsed, nil
}

func splitRepositoryPath(value string, requireGitSuffix bool) (string, string) {
	if strings.Count(value, "/") != 1 || strings.ContainsAny(value, `\:@?#`) {
		return "", ""
	}
	parts := strings.SplitN(value, "/", 2)
	if requireGitSuffix {
		if !strings.HasSuffix(parts[1], ".git") {
			return "", ""
		}
		parts[1] = strings.TrimSuffix(parts[1], ".git")
	} else if strings.HasSuffix(parts[1], ".git") {
		return "", ""
	}
	return parts[0], parts[1]
}

func safeInput(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validateReference(value string) error {
	if !safeInput(value, 1024) || strings.HasPrefix(value, "-") {
		return newError(ErrorInvalidReference)
	}
	return nil
}

func validateTransport(value domain.GitTransport) error {
	if value != domain.HTTPSGitTransport() && value != domain.SSHGitTransport() {
		return fmt.Errorf("unsupported Git transport")
	}
	return nil
}
