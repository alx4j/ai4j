package candidate_as_qualified

import (
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/environment"
)

func asMutationAuthority(candidate environment.CandidateCapabilitySet) domain.CapabilitySet {
	return candidate
}
