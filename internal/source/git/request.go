package git

import (
	"errors"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/source/gitremote"
)

var ErrInvalidResolutionRequest = errors.New("Git resolution request is invalid")

// ResolutionRequest is the credential-free handoff from canonical source
// selection to immutable Git resolution.
type ResolutionRequest struct {
	selection  domain.SourceSelection
	repository domain.RepositoryIdentity
	transport  domain.GitTransport
	requested  RequestedReference
}

func NewResolutionRequest(source gitremote.EffectiveSource) (ResolutionRequest, error) {
	selection := source.Selection()
	repository := source.Repository()
	transport := source.Transport()
	if !validSelection(selection) || !repository.Valid() || !transport.Valid() {
		return ResolutionRequest{}, ErrInvalidResolutionRequest
	}
	reconstructed, err := gitremote.ReconstructRemote(repository, transport)
	if err != nil || reconstructed.Endpoint() != source.Remote().Endpoint() {
		return ResolutionRequest{}, ErrInvalidResolutionRequest
	}
	requestedValue, requestedProvided := source.RequestedReference()
	requested := OmittedRequestedReference()
	if requestedProvided {
		requested, err = NewRequestedReference(requestedValue)
		if err != nil {
			return ResolutionRequest{}, ErrInvalidResolutionRequest
		}
	} else if requestedValue != "" {
		return ResolutionRequest{}, ErrInvalidResolutionRequest
	}
	request := ResolutionRequest{
		selection:  selection,
		repository: repository,
		transport:  transport,
		requested:  requested,
	}
	if !request.Valid() {
		return ResolutionRequest{}, ErrInvalidResolutionRequest
	}
	return request, nil
}

func (r ResolutionRequest) SourceSelection() domain.SourceSelection { return r.selection }
func (r ResolutionRequest) Repository() domain.RepositoryIdentity   { return r.repository }
func (r ResolutionRequest) Transport() domain.GitTransport          { return r.transport }
func (r ResolutionRequest) RequestedReference() RequestedReference  { return r.requested }
func (r ResolutionRequest) Valid() bool {
	return validSelection(r.selection) && r.repository.Valid() && r.transport.Valid() && r.requested.Valid()
}

func validSelection(selection domain.SourceSelection) bool {
	return selection == domain.BuiltInDefaultSource() || selection == domain.ExplicitSource()
}
