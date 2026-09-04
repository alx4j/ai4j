package git

import (
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

// SelectedObjectProof binds the exact selected OID to its observed Git object
// type. Branch/default/full-commit selections must name a commit; a tag may be
// either a lightweight commit or an annotated tag object.
type SelectedObjectProof struct {
	resolution ReferenceResolution
	objectType SelectedObjectType
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
	proof := SelectedObjectProof{resolution: resolution, objectType: objectType}
	if !proof.Valid() {
		return SelectedObjectProof{}, NewExecutorError(OperationObjectType, FailurePolicyRejected)
	}
	return proof, nil
}

func (p SelectedObjectProof) Resolution() ReferenceResolution { return p.resolution }
func (p SelectedObjectProof) Object() GitObjectOID            { return p.resolution.SelectedObject() }
func (p SelectedObjectProof) Type() SelectedObjectType        { return p.objectType }
func (p SelectedObjectProof) Valid() bool {
	return p.resolution.Valid() && p.objectType.Valid() &&
		(p.objectType == SelectedCommitObject || p.resolution.Resolved().Kind() == ResolvedTag)
}

type provenCommitMode string

const (
	provenCommitDirect provenCommitMode = "direct"
	provenCommitPeeled provenCommitMode = "peeled"
)

// ProvenCommit binds either a directly selected commit or the peeled commit of
// an exact selected annotated-tag object. It cannot be assembled from a free
// commit identifier.
type ProvenCommit struct {
	selected SelectedObjectProof
	commit   domain.CommitOID
	mode     provenCommitMode
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
	proof := ProvenCommit{selected: selected, commit: commit, mode: mode}
	if !proof.Valid() {
		return ProvenCommit{}, ErrExecutorContract
	}
	return proof, nil
}

func (p ProvenCommit) SelectedObject() SelectedObjectProof { return p.selected }
func (p ProvenCommit) Resolution() ReferenceResolution     { return p.selected.Resolution() }
func (p ProvenCommit) Commit() domain.CommitOID            { return p.commit }
func (p ProvenCommit) Valid() bool {
	if !p.selected.Valid() || !p.commit.Valid() {
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
