package filesystem

import (
	"errors"
	"io"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

const maximumIssuedDirectoryLeafProofs = 16

var (
	errInvalidIssuedProof = errors.New("invalid issued directory proof")
	errIssuedProofChanged = errors.New("issued directory proof changed")
	errIssuedProofLimit   = errors.New("issued directory proof limit reached")
)

type retainedProofAuthority interface {
	io.Closer
}

type issuedLeafProof struct {
	proof     lifecycle.DirectoryLeafProof
	authority retainedProofAuthority
}

// issuedProofLedger retains exact complete proof values. It never treats
// issuer correlation or a reconstructed value as authority.
type issuedProofLedger struct {
	home       lifecycle.UserHomeProof
	homeIssued bool
	leaves     map[string]issuedLeafProof
}

func (l *issuedProofLedger) issueHome(proof lifecycle.UserHomeProof) error {
	if l == nil || !proof.Valid() {
		return errInvalidIssuedProof
	}
	if !l.homeIssued {
		l.home = proof
		l.homeIssued = true
		return nil
	}
	if l.home != proof {
		return errIssuedProofChanged
	}
	return nil
}

func (l *issuedProofLedger) containsHome(proof lifecycle.UserHomeProof) bool {
	return l != nil && l.homeIssued && proof.Valid() && l.home == proof
}

func (l *issuedProofLedger) issueLeaf(
	proof lifecycle.DirectoryLeafProof,
	authority retainedProofAuthority,
) (retainedProofAuthority, bool, error) {
	if l == nil || !proof.Valid() || authority == nil || !l.containsHome(proof.HomeProof()) {
		return nil, false, errInvalidIssuedProof
	}
	if l.leaves == nil {
		l.leaves = make(map[string]issuedLeafProof)
	}
	key := proof.RelativePath().String()
	if existing, ok := l.leaves[key]; ok {
		if existing.proof != proof {
			return nil, false, errIssuedProofChanged
		}
		return existing.authority, true, nil
	}
	if len(l.leaves) >= maximumIssuedDirectoryLeafProofs {
		return nil, false, errIssuedProofLimit
	}
	l.leaves[key] = issuedLeafProof{proof: proof, authority: authority}
	return authority, false, nil
}

func (l *issuedProofLedger) close() error {
	if l == nil {
		return nil
	}
	var result error
	for key, entry := range l.leaves {
		if entry.authority != nil {
			result = errors.Join(result, entry.authority.Close())
		}
		delete(l.leaves, key)
	}
	l.home = lifecycle.UserHomeProof{}
	l.homeIssued = false
	return result
}
