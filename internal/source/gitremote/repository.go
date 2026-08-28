// Package gitremote parses and normalizes the credential-free Git remote forms
// accepted by AI4J. It never performs authentication or repository I/O.
package gitremote

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/domain"
)

const maxRepositoryInputBytes = 768

var (
	githubOwnerPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$`)
	githubRepositoryPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)
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
		return "Git repository is not in a supported canonical form"
	case ErrorInvalidReference:
		return "Git reference is not a safe explicit value"
	case ErrorInvalidSelection:
		return "source selection is internally inconsistent"
	case ErrorAccessFailed:
		return "Git source access failed"
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

// ParseRepository accepts GitHub owner/repository shorthand, a canonical HTTPS
// URL, or a canonical SCP-style SSH endpoint. Network endpoints require the
// .git suffix. Authentication material is deliberately outside this contract.
func ParseRepository(value string) (ParsedRepository, error) {
	if !safeInput(value, maxRepositoryInputBytes) || strings.HasPrefix(value, "-") {
		return ParsedRepository{}, newError(ErrorInvalidRepository)
	}

	identityValue, transport, ok := parseRepository(value)
	if !ok {
		return ParsedRepository{}, newError(ErrorInvalidRepository)
	}
	identity, err := domain.NewRepositoryIdentity(identityValue)
	if err != nil {
		return ParsedRepository{}, newError(ErrorInvalidRepository)
	}
	parsed := ParsedRepository{identity: identity, transport: transport}
	if !parsed.valid() {
		return ParsedRepository{}, newError(ErrorInvalidRepository)
	}
	return parsed, nil
}

func parseRepository(value string) (string, domain.GitTransport, bool) {
	var noTransport domain.GitTransport
	switch {
	case strings.HasPrefix(value, "https://"):
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Host == "" || parsed.Host != parsed.Hostname() || parsed.Port() != "" ||
			parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.ContainsAny(value, "%?#") ||
			!strings.HasSuffix(parsed.Path, ".git") || !strings.HasPrefix(parsed.Path, "/") {
			return "", noTransport, false
		}
		path := strings.TrimSuffix(strings.TrimPrefix(parsed.Path, "/"), ".git")
		return canonicalIdentity(parsed.Hostname(), path), domain.HTTPSGitTransport(), true
	case strings.HasPrefix(value, "git@"):
		remainder := strings.TrimPrefix(value, "git@")
		separator := strings.IndexByte(remainder, ':')
		if separator <= 0 || strings.Count(remainder, ":") != 1 || strings.ContainsAny(remainder, `@\?#`) || !strings.HasSuffix(remainder, ".git") {
			return "", noTransport, false
		}
		host := strings.ToLower(remainder[:separator])
		path := strings.TrimSuffix(remainder[separator+1:], ".git")
		return canonicalIdentity(host, path), domain.SSHGitTransport(), true
	default:
		if strings.Count(value, "/") != 1 || strings.ContainsAny(value, `\:@?#`) || strings.HasSuffix(value, ".git") {
			return "", noTransport, false
		}
		owner, repository, _ := strings.Cut(strings.ToLower(value), "/")
		if !githubOwnerPattern.MatchString(owner) || strings.Contains(owner, "--") || !githubRepositoryPattern.MatchString(repository) || strings.HasSuffix(repository, ".git") {
			return "", noTransport, false
		}
		return "github.com/" + owner + "/" + repository, domain.HTTPSGitTransport(), true
	}
}

func canonicalIdentity(host, path string) string {
	host = strings.ToLower(host)
	if host == "github.com" {
		path = strings.ToLower(path)
	}
	return host + "/" + path
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
