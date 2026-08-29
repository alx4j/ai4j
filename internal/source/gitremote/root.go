package gitremote

import (
	"regexp"
	"strings"

	"github.com/alx4j/ai4j/internal/domain"
)

const rootValidationRepository = "zz"

var rootRepositoryNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// Root is a canonical, credential-free Git namespace from which repositories
// can be derived. Its fields are private so a value returned by ParseRoot
// cannot be mutated into an unvalidated endpoint.
type Root struct {
	host      string
	path      string
	transport domain.GitTransport
}

// ParseRoot accepts a canonical HTTPS namespace root or an SCP-style SSH
// namespace root. A root identifies a namespace, not a repository, so it must
// not end in .git.
func ParseRoot(value string) (Root, error) {
	if !safeInput(value, maxRepositoryInputBytes) || strings.HasPrefix(value, "-") || strings.HasSuffix(strings.ToLower(value), ".git") {
		return Root{}, newError(ErrorInvalidRoot)
	}

	parsed, err := ParseRepository(value + "/" + rootValidationRepository + ".git")
	if err != nil {
		return Root{}, newError(ErrorInvalidRoot)
	}
	identity := parsed.Identity().String()
	suffix := "/" + rootValidationRepository
	if !strings.HasSuffix(identity, suffix) {
		return Root{}, newError(ErrorInvalidRoot)
	}
	rootIdentity := strings.TrimSuffix(identity, suffix)
	if _, err := domain.NewRepositoryIdentity(rootIdentity); err != nil {
		return Root{}, newError(ErrorInvalidRoot)
	}
	host, path, ok := strings.Cut(rootIdentity, "/")
	if !ok || host == "" || path == "" {
		return Root{}, newError(ErrorInvalidRoot)
	}
	return Root{host: host, path: path, transport: parsed.Transport()}, nil
}

// String returns the canonical credential-free spelling of the root.
func (r Root) String() string {
	if r.host == "" || r.path == "" || !r.transport.Valid() {
		return ""
	}
	if r.transport == domain.SSHGitTransport() {
		return "git@" + r.host + ":" + r.path
	}
	return "https://" + r.host + "/" + r.path
}

func (r Root) Transport() domain.GitTransport { return r.transport }

// Repository derives a credential-free remote beneath the root. Names use
// the same lowercase, hyphenated identifier form as toolkit and bundle IDs.
func (r Root) Repository(name string) (Remote, error) {
	if r.String() == "" || !rootRepositoryNamePattern.MatchString(name) {
		return Remote{}, newError(ErrorInvalidRepository)
	}
	identity, err := domain.NewRepositoryIdentity(r.host + "/" + r.path + "/" + name)
	if err != nil {
		return Remote{}, newError(ErrorInvalidRepository)
	}
	return ReconstructRemote(identity, r.transport)
}
