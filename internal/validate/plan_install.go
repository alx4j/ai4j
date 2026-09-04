package validate

import (
	"context"
	"encoding/json"
	"time"

	"github.com/alx4j/ai4j/internal/result"
)

const nativeInspectionTimeout = 15 * time.Second

type NativeStatus struct {
	MarketplaceRegistered bool
	PluginInstalled       bool
	PluginEnabled         bool
}

// InspectNativeStatusAt observes project-scoped Claude state from the selected
// project directory. User scope uses an empty directory.
func (s Service) InspectNativeStatusAt(ctx context.Context, directory, marketplaceID, pluginID string) (NativeStatus, *result.Problem) {
	marketplace, plugin, enabled, problem := s.inspectNativeIdentityAt(ctx, directory, marketplaceID, pluginID)
	if problem != nil {
		return NativeStatus{}, problem
	}
	return NativeStatus{MarketplaceRegistered: marketplace, PluginInstalled: plugin, PluginEnabled: enabled}, nil
}

func (s Service) inspectNativeIdentityAt(ctx context.Context, directory, marketplaceID, pluginID string) (bool, bool, bool, *result.Problem) {
	claude, err := s.config.Runner.LookPath("claude")
	if err != nil {
		return false, false, false, inspectionProblem("claude_not_found", "Claude Code executable is required")
	}
	queries := [][]string{
		{"plugin", "marketplace", "list", "--json"},
		{"plugin", "list", "--json"},
	}
	outputs := make([][]byte, len(queries))
	for index, arguments := range queries {
		queryContext, cancel := context.WithTimeout(ctx, nativeInspectionTimeout)
		observation, runErr := s.config.Runner.Run(queryContext, directory, claude, arguments, claudeEnvironment())
		cancel()
		if runErr != nil || observation.ExitCode != 0 || len(observation.Stderr) != 0 {
			return false, false, false, inspectionProblem("native_inspection_failed", "Claude plugin state could not be inspected")
		}
		outputs[index] = observation.Stdout
	}
	var marketplaces []struct {
		Name string `json:"name"`
	}
	var plugins []struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if len(outputs[0]) == 0 || json.Unmarshal(outputs[0], &marketplaces) != nil ||
		len(outputs[1]) == 0 || json.Unmarshal(outputs[1], &plugins) != nil {
		return false, false, false, inspectionProblem("native_inspection_failed", "Claude plugin state returned invalid JSON")
	}
	marketplaceFound := false
	for _, marketplace := range marketplaces {
		marketplaceFound = marketplaceFound || marketplace.Name == marketplaceID
	}
	pluginFound, pluginEnabled := false, false
	for _, plugin := range plugins {
		if plugin.ID == pluginID {
			pluginFound = true
			pluginEnabled = plugin.Enabled
		}
	}
	return marketplaceFound, pluginFound, pluginEnabled, nil
}

func inspectionProblem(code, message string) *result.Problem {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return nil
	}
	return &problem
}
