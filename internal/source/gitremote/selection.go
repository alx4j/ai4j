package gitremote

import (
	"context"
	"errors"
	"strings"

	"github.com/alx4j/ai4j/internal/domain"
)

const builtInRepository = "alx4j/ai4j"

// SelectionInput preserves option presence independently from its value. This
// keeps omission distinct from an explicitly empty value rejected by the CLI.
type SelectionInput struct {
	repository         string
	repositoryProvided bool
	reference          string
	referenceProvided  bool
}

func NewSelectionInput(repository string, repositoryProvided bool, reference string, referenceProvided bool) (SelectionInput, error) {
	if repositoryProvided != (repository != "") || referenceProvided != (reference != "") {
		return SelectionInput{}, newError(ErrorInvalidSelection)
	}
	if referenceProvided {
		if err := validateReference(reference); err != nil {
			return SelectionInput{}, err
		}
	}
	input := SelectionInput{
		repository:         repository,
		repositoryProvided: repositoryProvided,
		reference:          reference,
		referenceProvided:  referenceProvided,
	}
	return input, nil
}

func (i SelectionInput) Repository() (string, bool) { return i.repository, i.repositoryProvided }
func (i SelectionInput) Reference() (string, bool)  { return i.reference, i.referenceProvided }
func (i SelectionInput) valid() bool {
	if i.repositoryProvided != (i.repository != "") || i.referenceProvided != (i.reference != "") {
		return false
	}
	if i.referenceProvided && validateReference(i.reference) != nil {
		return false
	}
	return true
}

// Remote is an operation-local sanitized endpoint. It intentionally has no
// text or JSON marshaler so it cannot be mistaken for persisted identity.
type Remote struct {
	endpoint  string
	identity  domain.RepositoryIdentity
	transport domain.GitTransport
}

func (r Remote) Endpoint() string                    { return r.endpoint }
func (r Remote) Identity() domain.RepositoryIdentity { return r.identity }
func (r Remote) Transport() domain.GitTransport      { return r.transport }
func (r Remote) valid() bool {
	parsed, err := ParseRepository(r.endpoint)
	return err == nil && parsed.valid() && parsed.Identity() == r.identity && parsed.Transport() == r.transport
}

// ReconstructRemote creates one sanitized endpoint from typed identity and
// transport. It never accepts a raw URL or credential-bearing value.
func ReconstructRemote(repository domain.RepositoryIdentity, transport domain.GitTransport) (Remote, error) {
	if !repository.Valid() || validateTransport(transport) != nil {
		return Remote{}, newError(ErrorInvalidSelection)
	}
	host, path, ok := strings.Cut(repository.String(), "/")
	if !ok || host == "" || path == "" {
		return Remote{}, newError(ErrorInvalidSelection)
	}
	endpoint := "https://" + host + "/" + path + ".git"
	if transport == domain.SSHGitTransport() {
		endpoint = "git@" + host + ":" + path + ".git"
	}
	remote := Remote{endpoint: endpoint, identity: repository, transport: transport}
	parsed, err := ParseRepository(remote.endpoint)
	if err != nil || parsed.Identity() != repository || parsed.Transport() != transport {
		return Remote{}, newError(ErrorInvalidSelection)
	}
	return remote, nil
}

// EffectiveSource is the immutable, credential-free handoff to the Git source
// adapter. Repository identity remains transport-independent.
type EffectiveSource struct {
	selection          domain.SourceSelection
	repository         domain.RepositoryIdentity
	transport          domain.GitTransport
	remote             Remote
	requestedReference string
	hasReference       bool
}

func (s EffectiveSource) Selection() domain.SourceSelection     { return s.selection }
func (s EffectiveSource) Repository() domain.RepositoryIdentity { return s.repository }
func (s EffectiveSource) Transport() domain.GitTransport        { return s.transport }
func (s EffectiveSource) Remote() Remote                        { return s.remote }
func (s EffectiveSource) RequestedReference() (string, bool) {
	return s.requestedReference, s.hasReference
}
func (s EffectiveSource) valid() bool {
	if (s.selection != domain.BuiltInDefaultSource() && s.selection != domain.ExplicitSource()) || !s.repository.Valid() || !s.transport.Valid() || !s.remote.valid() || s.hasReference != (s.requestedReference != "") {
		return false
	}
	if s.hasReference && validateReference(s.requestedReference) != nil {
		return false
	}
	reconstructed, err := ReconstructRemote(s.repository, s.transport)
	return err == nil && reconstructed.endpoint == s.remote.endpoint
}

// Resolve expands omission to the built-in first-party repository before any
// caller performs authentication, state access, locking, or acquisition.
func Resolve(input SelectionInput) (EffectiveSource, error) {
	if !input.valid() {
		return EffectiveSource{}, newError(ErrorInvalidSelection)
	}
	selection := domain.BuiltInDefaultSource()
	repositoryInput := builtInRepository
	if input.repositoryProvided {
		selection = domain.ExplicitSource()
		repositoryInput = input.repository
	}
	parsed, err := ParseRepository(repositoryInput)
	if err != nil {
		return EffectiveSource{}, err
	}
	remote, err := ReconstructRemote(parsed.Identity(), parsed.Transport())
	if err != nil {
		return EffectiveSource{}, err
	}
	effective := EffectiveSource{
		selection:          selection,
		repository:         parsed.Identity(),
		transport:          parsed.Transport(),
		remote:             remote,
		requestedReference: input.reference,
		hasReference:       input.referenceProvided,
	}
	if !effective.valid() {
		return EffectiveSource{}, newError(ErrorInvalidSelection)
	}
	return effective, nil
}

// AccessProbe is implemented by the later Git adapter. Authentication remains
// wholly inside system Git/SSH configuration; no credential callback exists.
type AccessProbe interface {
	Probe(context.Context, EffectiveSource) error
}

// Qualify resolves once, then hands the sanitized request to system-Git access.
// An explicit failure is never retried against the built-in repository.
func Qualify(ctx context.Context, input SelectionInput, probe AccessProbe) (EffectiveSource, error) {
	if ctx == nil || probe == nil {
		return EffectiveSource{}, newError(ErrorInvalidSelection)
	}
	effective, err := Resolve(input)
	if err != nil {
		return EffectiveSource{}, err
	}
	if err := ctx.Err(); err != nil {
		return EffectiveSource{}, err
	}
	if err := probe.Probe(ctx, effective); err != nil {
		if errors.Is(err, context.Canceled) {
			return EffectiveSource{}, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return EffectiveSource{}, context.DeadlineExceeded
		}
		return EffectiveSource{}, newError(ErrorAccessFailed)
	}
	return effective, nil
}
