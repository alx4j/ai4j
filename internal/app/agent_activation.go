package app

import "github.com/alx4j/ai4j/internal/installstate"

func hasAgentActivationConflict(records []installstate.Record, desired installstate.Record) bool {
	if desired.Lifecycle != "active" || !desired.AgentActivation {
		return false
	}
	for _, record := range records {
		if record.InstallationID != desired.InstallationID && record.Lifecycle == "active" && record.AgentActivation &&
			record.Target == desired.Target && record.Scope == desired.Scope && installstate.SameScopeRoot(record.ScopeRoot, desired.ScopeRoot) {
			return true
		}
	}
	return false
}
