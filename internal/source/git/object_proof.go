package git

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/source/git/protocol"
)

// SelectedObjectType is the closed object-type result accepted at the exact
// OID retained by a ReferenceResolution.
type SelectedObjectType string

const (
	SelectedCommitObject SelectedObjectType = "commit"
	SelectedTagObject    SelectedObjectType = "tag"
)

func (t SelectedObjectType) Valid() bool {
	return t == SelectedCommitObject || t == SelectedTagObject
}

type selectedObjectProofSeal struct{ marker byte }

var issuedSelectedObjectProofSeal = &selectedObjectProofSeal{marker: 1}

// SelectedObjectProof binds the exact selected OID to its observed Git object
// type. Branch/default/full-commit selections must name a commit; a tag may be
// either a lightweight commit or an annotated tag object.
type SelectedObjectProof struct {
	resolution ReferenceResolution
	objectType SelectedObjectType
	binding    [sha256.Size]byte
	seal       *selectedObjectProofSeal
}

func NewSelectedObjectProof(resolution ReferenceResolution, data []byte) (SelectedObjectProof, error) {
	if !resolution.Valid() {
		return SelectedObjectProof{}, NewExecutorError(OperationObjectType, FailureInvalidOperation)
	}
	value, err := protocol.ParseSingleLine(data)
	if err != nil {
		return SelectedObjectProof{}, NewExecutorError(OperationObjectType, FailureMalformedProtocol)
	}
	objectType := SelectedObjectType(value)
	if !objectType.Valid() || objectType == SelectedTagObject && resolution.Resolved().Kind() != ResolvedTag {
		return SelectedObjectProof{}, NewExecutorError(OperationObjectType, FailurePolicyRejected)
	}
	proof := SelectedObjectProof{
		resolution: resolution, objectType: objectType, seal: issuedSelectedObjectProofSeal,
	}
	proof.binding = selectedObjectProofBinding(resolution, objectType)
	if !proof.Valid() {
		return SelectedObjectProof{}, NewExecutorError(OperationObjectType, FailurePolicyRejected)
	}
	return proof, nil
}

func (p SelectedObjectProof) Resolution() ReferenceResolution { return p.resolution }
func (p SelectedObjectProof) Object() GitObjectOID            { return p.resolution.SelectedObject() }
func (p SelectedObjectProof) Type() SelectedObjectType        { return p.objectType }
func (p SelectedObjectProof) Valid() bool {
	return p.seal == issuedSelectedObjectProofSeal && p.resolution.Valid() && p.objectType.Valid() &&
		(p.objectType == SelectedCommitObject || p.resolution.Resolved().Kind() == ResolvedTag) &&
		p.binding == selectedObjectProofBinding(p.resolution, p.objectType)
}

func selectedObjectProofBinding(
	resolution ReferenceResolution,
	objectType SelectedObjectType,
) [sha256.Size]byte {
	value := make([]byte, 0, len(resolution.binding)+1+len(objectType))
	value = append(value, resolution.binding[:]...)
	value = append(value, 0)
	value = append(value, objectType...)
	return sha256.Sum256(value)
}

type provenCommitMode string

const (
	provenCommitDirect provenCommitMode = "direct"
	provenCommitPeeled provenCommitMode = "peeled"
)

type provenCommitSeal struct{ marker byte }

var issuedProvenCommitSeal = &provenCommitSeal{marker: 1}

// ProvenCommit binds either a directly selected commit or the peeled commit of
// an exact selected annotated-tag object. It cannot be assembled from a free
// commit identifier.
type ProvenCommit struct {
	selected SelectedObjectProof
	commit   domain.CommitOID
	mode     provenCommitMode
	binding  [sha256.Size]byte
	seal     *provenCommitSeal
}

func NewDirectProvenCommit(selected SelectedObjectProof) (ProvenCommit, error) {
	if !selected.Valid() || selected.Type() != SelectedCommitObject {
		return ProvenCommit{}, NewExecutorError(OperationObjectType, FailureInvalidOperation)
	}
	commit, err := domain.NewCommitOID(selected.Object().String())
	if err != nil {
		return ProvenCommit{}, NewExecutorError(OperationObjectType, FailureMalformedProtocol)
	}
	return issueProvenCommit(selected, commit, provenCommitDirect)
}

func NewPeeledProvenCommit(selected SelectedObjectProof, data []byte) (ProvenCommit, error) {
	if !selected.Valid() || selected.Type() != SelectedTagObject {
		return ProvenCommit{}, NewExecutorError(OperationPeelCommit, FailureInvalidOperation)
	}
	value, err := protocol.ParseSingleLine(data)
	if err != nil {
		return ProvenCommit{}, NewExecutorError(OperationPeelCommit, FailureMalformedProtocol)
	}
	commit, err := domain.NewCommitOID(value)
	if err != nil {
		return ProvenCommit{}, NewExecutorError(OperationPeelCommit, FailureMalformedProtocol)
	}
	return issueProvenCommit(selected, commit, provenCommitPeeled)
}

func issueProvenCommit(selected SelectedObjectProof, commit domain.CommitOID, mode provenCommitMode) (ProvenCommit, error) {
	proof := ProvenCommit{selected: selected, commit: commit, mode: mode, seal: issuedProvenCommitSeal}
	proof.binding = provenCommitBinding(selected, commit, mode)
	if !proof.Valid() {
		return ProvenCommit{}, ErrExecutorContract
	}
	return proof, nil
}

func (p ProvenCommit) SelectedObject() SelectedObjectProof { return p.selected }
func (p ProvenCommit) Resolution() ReferenceResolution     { return p.selected.Resolution() }
func (p ProvenCommit) Commit() domain.CommitOID            { return p.commit }
func (p ProvenCommit) Valid() bool {
	if p.seal != issuedProvenCommitSeal || !p.selected.Valid() || !p.commit.Valid() ||
		p.binding != provenCommitBinding(p.selected, p.commit, p.mode) {
		return false
	}
	switch p.mode {
	case provenCommitDirect:
		return p.selected.Type() == SelectedCommitObject && p.commit.String() == p.selected.Object().String()
	case provenCommitPeeled:
		return p.selected.Type() == SelectedTagObject && p.selected.Resolution().Resolved().Kind() == ResolvedTag
	default:
		return false
	}
}

func provenCommitBinding(
	selected SelectedObjectProof,
	commit domain.CommitOID,
	mode provenCommitMode,
) [sha256.Size]byte {
	value := make([]byte, 0, len(selected.binding)+len(commit.String())+len(mode)+2)
	value = append(value, selected.binding[:]...)
	value = append(value, 0)
	value = append(value, commit.String()...)
	value = append(value, 0)
	value = append(value, mode...)
	return sha256.Sum256(value)
}

func (SelectedObjectProof) String() string   { return "<git-selected-object-proof:redacted>" }
func (SelectedObjectProof) GoString() string { return "<git-selected-object-proof:redacted>" }
func (p SelectedObjectProof) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, p.String())
}
func (p SelectedObjectProof) MarshalText() ([]byte, error) { return []byte(p.String()), nil }
func (SelectedObjectProof) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_selected_object_proof": "redacted"})
}

func (ProvenCommit) String() string   { return "<git-proven-commit:redacted>" }
func (ProvenCommit) GoString() string { return "<git-proven-commit:redacted>" }
func (p ProvenCommit) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, p.String())
}
func (p ProvenCommit) MarshalText() ([]byte, error) { return []byte(p.String()), nil }
func (ProvenCommit) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_proven_commit": "redacted"})
}
