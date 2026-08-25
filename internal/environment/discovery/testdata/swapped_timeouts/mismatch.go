package swapped_timeouts

import (
	"github.com/alx4j/ai4j/internal/environment/discovery"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

func gitMaximum(profile discovery.ProbeProfile) lifecycle.GitTimeoutMaximum {
	return profile.ClaudeTimeoutMaximum()
}
