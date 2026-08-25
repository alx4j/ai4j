package validate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/result"
)

const nativeInspectionTimeout = 15 * time.Second

type NativeStatus struct {
	MarketplaceRegistered bool
	PluginInstalled       bool
	PluginEnabled         bool
}

// InspectNativeStatus observes only the documented JSON marketplace and
// plugin lists. It does not refresh, repair, enable, or execute plugin content.
func (s Service) InspectNativeStatus(ctx context.Context) (NativeStatus, *result.Problem) {
	return s.InspectNativeStatusFor(ctx, "ai4j", "ai4j-default@ai4j")
}

func (s Service) InspectNativeStatusFor(ctx context.Context, marketplaceID, pluginID string) (NativeStatus, *result.Problem) {
	return s.InspectNativeStatusAt(ctx, "", marketplaceID, pluginID)
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

// InspectPlanInstall performs only the documented read-only observations that
// are needed to identify an existing AI4J installation or native identity.
func (s Service) InspectPlanInstall(ctx context.Context) ([]cli.Conflict, *result.Problem) {
	if ctx == nil || ctx.Err() != nil {
		return nil, inspectionProblem("plan_cancelled", "install planning was cancelled")
	}

	checks := []struct {
		path     string
		code     string
		resource string
		message  string
	}{
		{filepath.Join(s.config.Home, "Library", "Application Support", "ai4j", "state", "installation.json"), "installation_exists", "AI4J installation state", "an AI4J installation already exists"},
		{filepath.Join(s.config.Home, "Library", "Application Support", "ai4j", "state", "catalog", ".claude-plugin", "marketplace.json"), "catalog_destination_occupied", "AI4J marketplace catalog", "the AI4J catalog destination is already occupied"},
		{filepath.Join(s.config.Home, ".claude", "rules", "ai4j.md"), "rules_destination_occupied", "Claude user rules/ai4j.md", "the AI4J rules destination is already occupied"},
	}
	conflicts := make([]cli.Conflict, 0, len(checks)+2)
	for _, check := range checks {
		_, err := os.Lstat(check.path)
		switch {
		case err == nil:
			conflict, conflictErr := cli.NewConflict(check.code, check.resource, check.message)
			if conflictErr != nil {
				return nil, inspectionProblem("internal_error", "install planning could not construct a conflict")
			}
			conflicts = append(conflicts, conflict)
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return nil, inspectionProblem("owned_state_inspection_failed", "AI4J-owned destinations could not be inspected")
		}
	}

	marketplace, plugin, _, problem := s.inspectNativeIdentities(ctx)
	if problem != nil {
		return nil, problem
	}
	if marketplace {
		conflicts = append(conflicts, mustConflict("marketplace_identity_conflict", "AI4J marketplace", "the Claude marketplace identity already exists"))
	}
	if plugin {
		conflicts = append(conflicts, mustConflict("plugin_identity_conflict", "ai4j-default@ai4j", "the Claude plugin identity already exists"))
	}
	return conflicts, nil
}

// InspectPlanExisting verifies the owned files and native identities required
// by update and uninstall plans without changing either system.
func (s Service) InspectPlanExisting(ctx context.Context, catalogChecksum, rulesChecksum string) ([]cli.Conflict, *result.Problem) {
	return s.inspectPlanExisting(ctx, catalogChecksum, rulesChecksum, true)
}

// InspectUninstall verifies the owned resources and native identities needed
// for removal. A disabled but installed plugin remains safely removable.
func (s Service) InspectUninstall(ctx context.Context, catalogChecksum, rulesChecksum string) ([]cli.Conflict, *result.Problem) {
	return s.inspectPlanExisting(ctx, catalogChecksum, rulesChecksum, false)
}

func (s Service) inspectPlanExisting(ctx context.Context, catalogChecksum, rulesChecksum string, requireEnabled bool) ([]cli.Conflict, *result.Problem) {
	if ctx == nil || ctx.Err() != nil {
		return nil, inspectionProblem("plan_cancelled", "lifecycle planning was cancelled")
	}
	checks := []struct {
		path, checksum, code, resource, message string
	}{
		{filepath.Join(s.config.Home, "Library", "Application Support", "ai4j", "state", "catalog", ".claude-plugin", "marketplace.json"), catalogChecksum, "catalog_drift", "AI4J marketplace catalog", "the AI4J catalog is missing or modified"},
		{filepath.Join(s.config.Home, ".claude", "rules", "ai4j.md"), rulesChecksum, "rules_drift", "Claude user rules/ai4j.md", "the AI4J rules file is missing or modified"},
	}
	var conflicts []cli.Conflict
	for _, check := range checks {
		matches, err := fileMatches(check.path, check.checksum)
		if err != nil {
			return nil, inspectionProblem("owned_state_inspection_failed", "AI4J-owned destinations could not be inspected")
		}
		if !matches {
			conflicts = append(conflicts, mustConflict(check.code, check.resource, check.message))
		}
	}
	marketplace, plugin, enabled, problem := s.inspectNativeIdentities(ctx)
	if problem != nil {
		return nil, problem
	}
	if !marketplace {
		conflicts = append(conflicts, mustConflict("marketplace_missing", "AI4J marketplace", "the AI4J Claude marketplace is missing"))
	}
	if !plugin {
		conflicts = append(conflicts, mustConflict("plugin_missing", "ai4j-default@ai4j", "the AI4J Claude plugin is missing"))
	} else if requireEnabled && !enabled {
		conflicts = append(conflicts, mustConflict("plugin_disabled", "ai4j-default@ai4j", "the AI4J Claude plugin is disabled"))
	}
	return conflicts, nil
}

func (s Service) inspectNativeIdentities(ctx context.Context) (bool, bool, bool, *result.Problem) {
	return s.inspectNativeIdentity(ctx, "ai4j", "ai4j-default@ai4j")
}

func (s Service) inspectNativeIdentity(ctx context.Context, marketplaceID, pluginID string) (bool, bool, bool, *result.Problem) {
	return s.inspectNativeIdentityAt(ctx, "", marketplaceID, pluginID)
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

func fileMatches(path, expected string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(file, 16<<20)); err != nil {
		return false, err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)) == expected, nil
}

func mustConflict(code, resource, message string) cli.Conflict {
	conflict, err := cli.NewConflict(code, resource, message)
	if err != nil {
		panic(err)
	}
	return conflict
}

func inspectionProblem(code, message string) *result.Problem {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return nil
	}
	return &problem
}
