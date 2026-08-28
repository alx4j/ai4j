package git

import (
	"errors"

	"github.com/alx4j/ai4j/internal/domain"
)

var (
	ErrInvalidSourceProvenance   = errors.New("Git source provenance is invalid")
	ErrInvalidRenderedProvenance = errors.New("rendered source provenance is invalid")
)

// TrackingPolicy is the closed movement policy implied by a resolved ref.
type TrackingPolicy string

const (
	TrackFastForward TrackingPolicy = "track_fast_forward"
	TrackPinned      TrackingPolicy = "pinned"
)

func (p TrackingPolicy) String() string { return string(p) }
func (p TrackingPolicy) Valid() bool    { return p == TrackFastForward || p == TrackPinned }

// SourceProvenance is the immutable repository/ref/commit/tree proof. The
// credential-free transport is retained so exact source access can be
// reconstructed without retaining an endpoint or authentication material.
type SourceProvenance struct {
	proof      CommitTreeProof
	selection  domain.SourceSelection
	repository domain.RepositoryIdentity
	transport  domain.GitTransport
	requested  RequestedReference
	resolved   ResolvedReference
	commit     domain.CommitIdentity
	tree       domain.TreeOID
	tracking   TrackingPolicy
}

func NewSourceProvenance(proof CommitTreeProof) (SourceProvenance, error) {
	if !proof.Valid() {
		return SourceProvenance{}, ErrInvalidSourceProvenance
	}
	resolution := proof.CommitProof().Resolution()
	request := resolution.Request()
	tracking, ok := trackingFor(resolution.Resolved().Kind())
	if !ok {
		return SourceProvenance{}, ErrInvalidSourceProvenance
	}
	commit, err := domain.NewCommitIdentity(request.Repository(), domain.SHA1ObjectFormat(), proof.Commit())
	if err != nil {
		return SourceProvenance{}, ErrInvalidSourceProvenance
	}
	provenance := SourceProvenance{
		proof:      proof,
		selection:  request.SourceSelection(),
		repository: request.Repository(),
		transport:  request.Transport(),
		requested:  request.RequestedReference(),
		resolved:   resolution.Resolved(),
		commit:     commit,
		tree:       proof.Tree(),
		tracking:   tracking,
	}
	if !provenance.Valid() {
		return SourceProvenance{}, ErrInvalidSourceProvenance
	}
	return provenance, nil
}

func (p SourceProvenance) SourceSelection() domain.SourceSelection { return p.selection }
func (p SourceProvenance) Repository() domain.RepositoryIdentity   { return p.repository }
func (p SourceProvenance) Transport() domain.GitTransport          { return p.transport }
func (p SourceProvenance) RequestedReference() RequestedReference  { return p.requested }
func (p SourceProvenance) ResolvedReference() ResolvedReference    { return p.resolved }
func (p SourceProvenance) Commit() domain.CommitIdentity           { return p.commit }
func (p SourceProvenance) RootTree() domain.TreeOID                { return p.tree }
func (p SourceProvenance) TrackingPolicy() TrackingPolicy          { return p.tracking }

func (p SourceProvenance) Valid() bool {
	if !p.proof.Valid() || !validSelection(p.selection) || !p.repository.Valid() || !p.transport.Valid() || !p.requested.Valid() ||
		!p.resolved.Valid() || !p.commit.Valid() || p.commit.Repository() != p.repository ||
		p.commit.ObjectFormat() != domain.SHA1ObjectFormat() || !p.tree.Valid() || !p.tracking.Valid() {
		return false
	}
	resolution := p.proof.CommitProof().Resolution()
	request := resolution.Request()
	expectedTracking, ok := trackingFor(resolution.Resolved().Kind())
	if !ok || p.selection != request.SourceSelection() || p.repository != request.Repository() || p.transport != request.Transport() ||
		p.requested != request.RequestedReference() || p.resolved != resolution.Resolved() ||
		p.commit.OID() != p.proof.Commit() || p.tree != p.proof.Tree() || p.tracking != expectedTracking {
		return false
	}
	return true
}

// RenderedProvenance joins immutable source provenance with caller-proven
// package bytes and the independently typed CLI build identity.
type RenderedProvenance struct {
	source SourceProvenance
	digest domain.RenderedDigest
	build  domain.BuildCommit
}

func NewRenderedProvenance(source SourceProvenance, digest domain.RenderedDigest, build domain.BuildCommit) (RenderedProvenance, error) {
	provenance := RenderedProvenance{source: source, digest: digest, build: build}
	if !provenance.Valid() {
		return RenderedProvenance{}, ErrInvalidRenderedProvenance
	}
	return provenance, nil
}

func (p RenderedProvenance) Source() SourceProvenance              { return p.source }
func (p RenderedProvenance) RenderedDigest() domain.RenderedDigest { return p.digest }
func (p RenderedProvenance) BuildCommit() domain.BuildCommit       { return p.build }
func (p RenderedProvenance) Valid() bool {
	return p.source.Valid() && p.digest.Valid() && p.build.Valid()
}

func trackingFor(kind ResolvedReferenceKind) (TrackingPolicy, bool) {
	switch kind {
	case ResolvedDefaultBranch, ResolvedBranch:
		return TrackFastForward, true
	case ResolvedTag, ResolvedCommit:
		return TrackPinned, true
	default:
		return "", false
	}
}
