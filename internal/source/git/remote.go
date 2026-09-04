package git

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/source/git/protocol"
)

// AdvertisedReferenceKind is the closed ref namespace accepted from Git.
type AdvertisedReferenceKind string

const (
	AdvertisedBranch AdvertisedReferenceKind = "branch"
	AdvertisedTag    AdvertisedReferenceKind = "tag"
)

func (k AdvertisedReferenceKind) Valid() bool {
	return k == AdvertisedBranch || k == AdvertisedTag
}

// AdvertisedReference combines one unique branch or tag with its exact object
// name. An annotated tag may additionally retain its unique peeled object.
type AdvertisedReference struct {
	kind      AdvertisedReferenceKind
	name      string
	oid       GitObjectOID
	peeled    GitObjectOID
	hasPeeled bool
}

func (r AdvertisedReference) Kind() AdvertisedReferenceKind { return r.kind }
func (r AdvertisedReference) Name() string                  { return r.name }
func (r AdvertisedReference) OID() GitObjectOID             { return r.oid }
func (r AdvertisedReference) PeeledOID() (GitObjectOID, bool) {
	return r.peeled, r.hasPeeled && r.peeled.Valid()
}

func (r AdvertisedReference) Valid() bool {
	if !r.kind.Valid() || !validShortReference(r.name) || !r.oid.Valid() || r.hasPeeled != r.peeled.Valid() {
		return false
	}
	return r.kind == AdvertisedTag || !r.hasPeeled
}

// RemoteAdvertisement is a validated, duplicate-free semantic projection of
// one bounded ls-remote response. It contains no endpoint or credential.
type RemoteAdvertisement struct {
	request       ResolutionRequest
	head          GitObjectOID
	hasHead       bool
	defaultBranch string
	references    []AdvertisedReference
}

// ReferenceResolution is the immutable, request-bound result of resolving one
// validated remote advertisement. The selected object is the exact advertised
// object for a branch or tag; a full commit request selects that exact
// requested object ID.
type ReferenceResolution struct {
	request  ResolutionRequest
	resolved ResolvedReference
	object   GitObjectOID
}

func (r ReferenceResolution) Request() ResolutionRequest   { return r.request }
func (r ReferenceResolution) Resolved() ResolvedReference  { return r.resolved }
func (r ReferenceResolution) SelectedObject() GitObjectOID { return r.object }
func (r ReferenceResolution) Valid() bool {
	return r.request.Valid() && r.resolved.Valid() && r.object.Valid() &&
		requestMatchesResolved(r.request.RequestedReference(), r.resolved, r.object)
}

func (ReferenceResolution) String() string   { return "<git-reference-resolution:redacted>" }
func (ReferenceResolution) GoString() string { return "<git-reference-resolution:redacted>" }
func (r ReferenceResolution) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, r.String())
}
func (r ReferenceResolution) MarshalText() ([]byte, error) { return []byte(r.String()), nil }
func (ReferenceResolution) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_reference_resolution": "redacted"})
}

func ParseRemoteAdvertisement(request ResolutionRequest, data []byte) (RemoteAdvertisement, error) {
	if !request.Valid() {
		return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureInvalidOperation)
	}
	records, err := protocol.ParseRemote(data)
	if err != nil {
		return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
	}
	type pendingReference struct {
		kind      AdvertisedReferenceKind
		name      string
		oid       GitObjectOID
		peeled    GitObjectOID
		hasOID    bool
		hasPeeled bool
	}
	pending := make(map[string]pendingReference, len(records))
	seen := make(map[string]struct{}, len(records))
	result := RemoteAdvertisement{request: request}
	for _, record := range records {
		kind := "oid"
		if record.SymrefTarget != "" {
			kind = "symref"
		}
		key := kind + "\x00" + record.Reference
		if _, duplicate := seen[key]; duplicate {
			return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
		}
		seen[key] = struct{}{}

		if record.Reference == "HEAD" {
			if record.SymrefTarget != "" {
				branch, ok := strings.CutPrefix(record.SymrefTarget, "refs/heads/")
				if !ok || !validShortReference(branch) || result.defaultBranch != "" {
					return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
				}
				result.defaultBranch = branch
				continue
			}
			oid, oidErr := NewGitObjectOID(record.OID)
			if oidErr != nil || result.hasHead {
				return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
			}
			result.head, result.hasHead = oid, true
			continue
		}
		if record.SymrefTarget != "" {
			return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
		}

		kindValue, name, peeled, ok := parseAdvertisedName(record.Reference)
		if !ok {
			return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
		}
		oid, oidErr := NewGitObjectOID(record.OID)
		if oidErr != nil {
			return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
		}
		mapKey := string(kindValue) + "\x00" + name
		entry := pending[mapKey]
		if entry.kind == "" {
			entry.kind, entry.name = kindValue, name
		}
		if peeled {
			if entry.hasPeeled {
				return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
			}
			entry.peeled, entry.hasPeeled = oid, true
		} else {
			if entry.hasOID {
				return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
			}
			entry.oid, entry.hasOID = oid, true
		}
		pending[mapKey] = entry
	}

	result.references = make([]AdvertisedReference, 0, len(pending))
	for _, entry := range pending {
		if !entry.hasOID || entry.hasPeeled && entry.kind != AdvertisedTag {
			return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
		}
		result.references = append(result.references, AdvertisedReference{
			kind: entry.kind, name: entry.name, oid: entry.oid, peeled: entry.peeled, hasPeeled: entry.hasPeeled,
		})
	}
	slices.SortFunc(result.references, compareAdvertisedReferences)
	if !result.Valid() {
		return RemoteAdvertisement{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
	}
	return result, nil
}

func (a RemoteAdvertisement) Head() (GitObjectOID, bool) {
	return a.head, a.hasHead && a.head.Valid()
}
func (a RemoteAdvertisement) DefaultBranch() (string, bool) {
	return a.defaultBranch, a.defaultBranch != ""
}
func (a RemoteAdvertisement) References() []AdvertisedReference {
	return append([]AdvertisedReference(nil), a.references...)
}

func (a RemoteAdvertisement) Valid() bool {
	if !a.request.Valid() || a.hasHead != a.head.Valid() ||
		a.defaultBranch != "" && !validShortReference(a.defaultBranch) ||
		len(a.references) > MaximumReferenceCount {
		return false
	}
	var defaultOID GitObjectOID
	defaultFound := false
	previous := AdvertisedReference{}
	for index, reference := range a.references {
		if !reference.Valid() || index > 0 && compareAdvertisedReferences(previous, reference) >= 0 {
			return false
		}
		if reference.kind == AdvertisedBranch && reference.name == a.defaultBranch {
			defaultOID, defaultFound = reference.oid, true
		}
		previous = reference
	}
	if a.defaultBranch != "" {
		return a.hasHead && defaultFound && defaultOID == a.head
	}
	return true
}

// ResolveReference resolves the typed request retained by one validated
// advertisement. A short spelling is accepted only when exactly one branch or
// tag has that name; ambiguity is never resolved by preference or ordering.
func ResolveReference(advertisement RemoteAdvertisement) (ReferenceResolution, error) {
	if !advertisement.Valid() {
		return ReferenceResolution{}, NewExecutorError(OperationEnumerateRefs, FailureInvalidOperation)
	}
	request := advertisement.request
	requested, provided := request.RequestedReference().Value()
	if !provided {
		name, ok := advertisement.DefaultBranch()
		if !ok {
			return ReferenceResolution{}, NewExecutorError(OperationEnumerateRefs, FailureDefaultBranchUnavailable)
		}
		reference, ok := advertisedReference(advertisement, AdvertisedBranch, name)
		if !ok {
			return ReferenceResolution{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
		}
		return issueReferenceResolution(request, ResolvedDefaultBranch, name, reference.oid)
	}
	if commit, err := domain.NewCommitOID(requested); err == nil {
		object, objectErr := NewGitObjectOID(commit.String())
		if objectErr != nil {
			return ReferenceResolution{}, NewExecutorError(OperationEnumerateRefs, FailureInvalidOperation)
		}
		return issueReferenceResolution(request, ResolvedCommit, commit.String(), object)
	}
	if name, ok := strings.CutPrefix(requested, "refs/heads/"); ok {
		return resolveAdvertisedName(request, advertisement, AdvertisedBranch, ResolvedBranch, name)
	}
	if name, ok := strings.CutPrefix(requested, "refs/tags/"); ok {
		return resolveAdvertisedName(request, advertisement, AdvertisedTag, ResolvedTag, name)
	}
	branch, branchFound := advertisedReference(advertisement, AdvertisedBranch, requested)
	tag, tagFound := advertisedReference(advertisement, AdvertisedTag, requested)
	switch {
	case branchFound && tagFound:
		return ReferenceResolution{}, NewExecutorError(OperationEnumerateRefs, FailureReferenceAmbiguous)
	case branchFound:
		return issueReferenceResolution(request, ResolvedBranch, requested, branch.oid)
	case tagFound:
		return issueReferenceResolution(request, ResolvedTag, requested, tag.oid)
	default:
		return ReferenceResolution{}, NewExecutorError(OperationEnumerateRefs, FailureReferenceNotFound)
	}
}

func resolveAdvertisedName(
	request ResolutionRequest,
	advertisement RemoteAdvertisement,
	advertisedKind AdvertisedReferenceKind,
	resolvedKind ResolvedReferenceKind,
	name string,
) (ReferenceResolution, error) {
	reference, ok := advertisedReference(advertisement, advertisedKind, name)
	if !ok {
		return ReferenceResolution{}, NewExecutorError(OperationEnumerateRefs, FailureReferenceNotFound)
	}
	return issueReferenceResolution(request, resolvedKind, name, reference.oid)
}

func issueReferenceResolution(
	request ResolutionRequest,
	kind ResolvedReferenceKind,
	name string,
	object GitObjectOID,
) (ReferenceResolution, error) {
	resolved, err := NewResolvedReference(kind, name)
	if err != nil {
		return ReferenceResolution{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
	}
	result := ReferenceResolution{request: request, resolved: resolved, object: object}
	if !result.Valid() {
		return ReferenceResolution{}, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)
	}
	return result, nil
}

func advertisedReference(advertisement RemoteAdvertisement, kind AdvertisedReferenceKind, name string) (AdvertisedReference, bool) {
	for _, reference := range advertisement.references {
		if reference.kind == kind && reference.name == name {
			return reference, true
		}
	}
	return AdvertisedReference{}, false
}

func requestMatchesResolved(request RequestedReference, resolved ResolvedReference, object GitObjectOID) bool {
	requested, provided := request.Value()
	if !provided {
		return resolved.kind == ResolvedDefaultBranch
	}
	if resolved.kind == ResolvedCommit {
		return requested == resolved.name && requested == object.String()
	}
	if name, ok := strings.CutPrefix(requested, "refs/heads/"); ok {
		return resolved.kind == ResolvedBranch && resolved.name == name
	}
	if name, ok := strings.CutPrefix(requested, "refs/tags/"); ok {
		return resolved.kind == ResolvedTag && resolved.name == name
	}
	return (resolved.kind == ResolvedBranch || resolved.kind == ResolvedTag) && resolved.name == requested
}

func (RemoteAdvertisement) String() string   { return "<git-remote-advertisement:redacted>" }
func (RemoteAdvertisement) GoString() string { return "<git-remote-advertisement:redacted>" }
func (a RemoteAdvertisement) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, a.String())
}
func (a RemoteAdvertisement) MarshalText() ([]byte, error) { return []byte(a.String()), nil }
func (RemoteAdvertisement) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_remote_advertisement": "redacted"})
}

func parseAdvertisedName(reference string) (AdvertisedReferenceKind, string, bool, bool) {
	if name, ok := strings.CutPrefix(reference, "refs/heads/"); ok && validShortReference(name) {
		return AdvertisedBranch, name, false, true
	}
	if name, ok := strings.CutPrefix(reference, "refs/tags/"); ok {
		peeled := strings.HasSuffix(name, "^{}")
		if peeled {
			name = strings.TrimSuffix(name, "^{}")
		}
		if validShortReference(name) {
			return AdvertisedTag, name, peeled, true
		}
	}
	return "", "", false, false
}

func compareAdvertisedReferences(left, right AdvertisedReference) int {
	if comparison := strings.Compare(string(left.kind), string(right.kind)); comparison != 0 {
		return comparison
	}
	return strings.Compare(left.name, right.name)
}
