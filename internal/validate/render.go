package validate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
)

type renderedUnit struct {
	unit        nativePackage
	destination string
	nativeCodex bool
}

type codexAgentOutput struct {
	source      string
	destination string
}

func codexAgentOutputs(workspace string, model validatedManifest, resolved resolvedSelection, target cli.BuildTarget) (map[string]codexAgentOutput, error) {
	outputs := make(map[string]codexAgentOutput)
	if target != cli.BuildTargetCodex {
		return outputs, nil
	}
	destinations := make(map[string]string)
	for _, selected := range resolved.assets {
		if selected.asset.Ownership != "package" || selected.asset.Type != "agent" {
			continue
		}
		owner, ok := model.packageOwners[string(cli.BuildTargetCodex)][selected.asset.ID]
		if !ok {
			return nil, validationError("invalid_codex_agent", "Codex agent has no native package")
		}
		_, ok = model.packages[string(cli.BuildTargetCodex)][owner]
		if !ok {
			return nil, validationError("invalid_codex_agent", "Codex agent has no native package")
		}
		sources := assetFiles(selected.path, model.tracked)
		if len(sources) != 1 || filepath.Ext(sources[0]) != ".toml" {
			return nil, validationError("invalid_codex_agent", "Codex agent assets must use one target-specific TOML file")
		}
		source, err := readTrackedFile(workspace, sources[0], model.tracked)
		if err != nil {
			return nil, validationError("invalid_codex_agent", "Codex agent configuration could not be read")
		}
		if _, err := codexAgentName(source); err != nil {
			return nil, validationError("invalid_codex_agent", "Codex agent configuration must declare a valid name, description, and developer instructions")
		}
		destination := "configuration/.codex/agents/" + filepath.Base(sources[0])
		collisionKey := strings.ToLower(filepath.ToSlash(destination))
		if previous, collision := destinations[collisionKey]; collision && previous != selected.asset.ID {
			return nil, validationError("codex_agent_output_collision", "Codex agent filenames must be unique across selected packages")
		}
		destinations[collisionKey] = selected.asset.ID
		outputs[selected.asset.ID] = codexAgentOutput{source: sources[0], destination: destination}
	}
	return outputs, nil
}

func renderBuild(stage, workspace string, model validatedManifest, resolved resolvedSelection, codexAgents map[string]codexAgentOutput, target cli.BuildTarget) ([]buildMapping, error) {
	units := append([]nativePackage(nil), resolved.packages...)
	slices.SortFunc(units, func(left, right nativePackage) int { return strings.Compare(left.ID, right.ID) })
	codexPluginUnits := make(map[string]bool, len(units))
	codexPluginCount := 0
	if target == cli.BuildTargetCodex {
		for _, unit := range units {
			if codexUnitProducesPlugin(unit, model, resolved.assets) {
				codexPluginUnits[unit.ID] = true
				codexPluginCount++
			}
		}
	}
	rendered := make([]renderedUnit, 0, len(units))
	for _, unit := range units {
		_, nativeCodex := model.tracked[unit.Path+"/.codex-plugin/plugin.json"]
		destination := "plugin"
		if (target == cli.BuildTargetClaude && len(units) > 1) ||
			(target == cli.BuildTargetCodex && (codexPluginCount != 1 || !codexPluginUnits[unit.ID])) {
			destination = "plugins/" + unit.ID
		}
		if target == cli.BuildTargetClaude {
			if err := materializeSelectedPackage(stage, destination, workspace, unit, model, resolved.assets, target); err != nil {
				return nil, err
			}
		} else if err := renderCodexUnit(stage, destination, workspace, unit, model, resolved.assets); err != nil {
			return nil, err
		}
		rendered = append(rendered, renderedUnit{unit: unit, destination: destination, nativeCodex: target == cli.BuildTargetCodex && nativeCodex})
	}
	if target == cli.BuildTargetCodex {
		if err := renderCodexAgents(stage, workspace, model, resolved.assets, codexAgents); err != nil {
			return nil, err
		}
	}
	mappings := make([]buildMapping, 0, len(resolved.assets))
	instructionCount := 0
	for _, selectedAsset := range resolved.assets {
		native := ""
		if selectedAsset.asset.Ownership == "configuration" {
			destination := "configuration/assets/" + selectedAsset.asset.ID + "/" + filepath.Base(selectedAsset.path)
			if selectedAsset.asset.Type == "instruction" {
				instructionCount++
				if target == cli.BuildTargetClaude {
					destination = "configuration/rules/" + selectedAsset.asset.ID + ".md"
				} else if instructionCount == 1 {
					destination = "configuration/AGENTS.md"
				} else {
					return nil, errors.New("multiple persistent instructions require an unapproved lossy mapping")
				}
			}
			if err := copyAsset(stage, destination, workspace, selectedAsset.path, model); err != nil {
				return nil, err
			}
			native = destination
		} else {
			native = mappedPackagePath(selectedAsset, rendered, codexAgents, target)
			if native == "" {
				return nil, errors.New("selected package asset has no native unit")
			}
		}
		mappings = append(mappings, buildMapping{Canonical: selectedAsset.asset.Type + ":" + selectedAsset.asset.ID, Native: native, Fidelity: "exact"})
	}
	if err := validateRenderedMappings(stage, mappings); err != nil {
		return nil, err
	}
	return mappings, nil
}

func codexUnitProducesPlugin(unit nativePackage, model validatedManifest, selected []resolvedAsset) bool {
	if _, ok := model.tracked[unit.Path+"/.codex-plugin/plugin.json"]; ok {
		return true
	}
	skipped := codexTransformedSources(unit, model)
	hasMCP := false
	for _, item := range selected {
		if !slices.Contains(unit.Assets, item.asset.ID) {
			continue
		}
		if item.asset.Type == "mcp" {
			hasMCP = true
		}
	}
	if hasMCP {
		return true
	}
	for _, source := range selectedPackageSources(unit, model, selected, cli.BuildTargetCodex) {
		if _, skip := skipped[source]; !skip {
			return true
		}
	}
	return false
}

func codexTransformedSources(unit nativePackage, model validatedManifest) map[string]struct{} {
	skipped := map[string]struct{}{unit.Path + "/.claude-plugin/plugin.json": {}}
	for _, assetID := range unit.Assets {
		declared := model.assets[assetID]
		if declared.Type != "agent" && declared.Type != "mcp" {
			continue
		}
		for _, path := range assetPathsForTarget(declared, string(cli.BuildTargetCodex)) {
			for _, source := range assetFiles(path, model.tracked) {
				skipped[source] = struct{}{}
			}
		}
	}
	return skipped
}

func validateCodexRendering(workspace string, model validatedManifest, selected []resolvedAsset) error {
	if err := validateCodexAssetOverlaps(model, selected); err != nil {
		return err
	}
	return validateGeneratedCodexMCPIdentifiers(workspace, model, selected)
}

func validateCodexAssetOverlaps(model validatedManifest, selected []resolvedAsset) error {
	type owner struct {
		id          string
		transformed bool
	}
	owners := make(map[string][]owner)
	for _, item := range selected {
		if item.asset.Ownership != "package" {
			continue
		}
		transformed := item.asset.Type == "agent" || item.asset.Type == "mcp"
		for _, source := range assetFiles(item.path, model.tracked) {
			owners[source] = append(owners[source], owner{id: item.asset.ID, transformed: transformed})
		}
	}
	for _, fileOwners := range owners {
		if len(fileOwners) < 2 {
			continue
		}
		for _, item := range fileOwners {
			if item.transformed {
				return validationError("unsupported_codex_asset_overlap", "Codex agent and MCP assets cannot overlap another selected package asset")
			}
		}
	}
	return nil
}

func validateGeneratedCodexMCPIdentifiers(workspace string, model validatedManifest, selected []resolvedAsset) error {
	serversByPackage := make(map[string]map[string]struct{})
	for _, item := range selected {
		if item.asset.Ownership != "package" || item.asset.Type != "mcp" {
			continue
		}
		owner := model.packageOwners[string(cli.BuildTargetCodex)][item.asset.ID]
		unit, ok := model.packages[string(cli.BuildTargetCodex)][owner]
		if !ok {
			return validationError("invalid_mcp", "Codex MCP asset has no native package")
		}
		if _, native := model.tracked[unit.Path+"/.codex-plugin/plugin.json"]; native {
			continue
		}
		var source mcpManifest
		if err := readStrictJSON(workspace, item.path, model.tracked, &source); err != nil {
			return validationError("invalid_mcp", "Codex MCP asset could not be read")
		}
		servers := serversByPackage[owner]
		if servers == nil {
			servers = make(map[string]struct{})
			serversByPackage[owner] = servers
		}
		for id := range source.Servers {
			if _, duplicate := servers[id]; duplicate {
				return validationError("codex_mcp_output_collision", "Codex MCP server identifiers must be unique across selected assets")
			}
			servers[id] = struct{}{}
		}
	}
	return nil
}

func renderCodexUnit(stage, destination, workspace string, unit nativePackage, model validatedManifest, selected []resolvedAsset) error {
	codexManifest := unit.Path + "/.codex-plugin/plugin.json"
	if _, ok := model.tracked[codexManifest]; ok {
		return materializeSelectedPackage(stage, destination, workspace, unit, model, selected, cli.BuildTargetCodex)
	}
	packageFiles := selectedPackageSources(unit, model, selected, cli.BuildTargetCodex)
	skipped := codexTransformedSources(unit, model)
	var selectedMCP []resolvedAsset
	var commandPaths []string
	for _, item := range selected {
		if !slices.Contains(unit.Assets, item.asset.ID) {
			continue
		}
		switch item.asset.Type {
		case "mcp":
			selectedMCP = append(selectedMCP, item)
		case "prompt":
			for _, source := range assetFiles(item.path, model.tracked) {
				relative := strings.TrimPrefix(source, unit.Path+"/")
				commandPaths = append(commandPaths, "./"+relative)
			}
		}
	}
	hasContent := false
	hasSkills := false
	for _, source := range packageFiles {
		if _, skip := skipped[source]; skip {
			continue
		}
		relative := strings.TrimPrefix(source, unit.Path+"/")
		if err := copyTrackedBuildFile(stage, destination+"/"+relative, workspace, source, model.tracked); err != nil {
			return err
		}
		hasContent = true
		hasSkills = hasSkills || strings.HasPrefix(relative, "skills/")
	}
	hasMCP := len(selectedMCP) != 0
	if hasMCP {
		codexMCP := make(map[string]codexMCPServer)
		for _, item := range selectedMCP {
			var claudeMCP mcpManifest
			if err := readStrictJSON(workspace, item.path, model.tracked, &claudeMCP); err != nil {
				return err
			}
			for id, server := range claudeMCP.Servers {
				if _, duplicate := codexMCP[id]; duplicate {
					return validationError("codex_mcp_output_collision", "Codex MCP server identifiers must be unique across selected assets")
				}
				environment, err := environmentNames(server.Env)
				if err != nil {
					return err
				}
				codexMCP[id] = codexMCPServer{Command: server.Command, Args: append([]string(nil), server.Args...), EnvVars: environment}
			}
		}
		content, err := json.MarshalIndent(codexMCP, "", "  ")
		if err != nil {
			return err
		}
		if err := writeBuildFile(stage, destination+"/.mcp.json", append(content, '\n'), 0o644); err != nil {
			return err
		}
	}
	if !hasContent && !hasMCP {
		return nil
	}
	commands, err := codexManifestPaths(commandPaths)
	if err != nil {
		return err
	}
	plugin := codexPluginManifest{Name: unit.ID, Version: model.manifest.Toolkit.Version, Description: "AI4J generated Codex plugin", Commands: commands}
	if hasSkills {
		plugin.Skills = "./skills/"
	}
	if hasMCP {
		plugin.MCPServers = "./.mcp.json"
	}
	content, err := json.MarshalIndent(plugin, "", "  ")
	if err != nil {
		return err
	}
	if err := writeBuildFile(stage, destination+"/.codex-plugin/plugin.json", append(content, '\n'), 0o644); err != nil {
		return err
	}
	return nil
}

func codexManifestPaths(paths []string) (json.RawMessage, error) {
	values := append([]string(nil), paths...)
	slices.Sort(values)
	values = slices.Compact(values)
	switch len(values) {
	case 0:
		return nil, nil
	case 1:
		return json.Marshal(values[0])
	default:
		return json.Marshal(values)
	}
}

func renderCodexAgents(stage, workspace string, model validatedManifest, selected []resolvedAsset, outputs map[string]codexAgentOutput) error {
	for _, asset := range selected {
		output, ok := outputs[asset.asset.ID]
		if !ok {
			continue
		}
		body, err := readTrackedFile(workspace, output.source, model.tracked)
		if err != nil {
			return err
		}
		if err := writeBuildFile(stage, output.destination, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func copyAsset(stage, destination, workspace, source string, model validatedManifest) error {
	files := assetFiles(source, model.tracked)
	if _, sourceIsFile := model.tracked[source]; sourceIsFile {
		return copyTrackedBuildFile(stage, destination, workspace, files[0], model.tracked)
	}
	for _, file := range files {
		relative := strings.TrimPrefix(file, source+"/")
		if err := copyTrackedBuildFile(stage, strings.TrimSuffix(destination, "/")+"/"+relative, workspace, file, model.tracked); err != nil {
			return err
		}
	}
	return nil
}

func validateRenderedMappings(stage string, mappings []buildMapping) error {
	for _, mapping := range mappings {
		if !safeRelative(mapping.Native) {
			return errors.New("build mapping contains an unsafe native path")
		}
		if _, err := os.Stat(filepath.Join(stage, filepath.FromSlash(mapping.Native))); err != nil {
			return errors.New("build mapping references missing native content")
		}
	}
	return nil
}

func mappedPackagePath(asset resolvedAsset, units []renderedUnit, codexAgents map[string]codexAgentOutput, target cli.BuildTarget) string {
	for _, rendered := range units {
		if !slices.Contains(rendered.unit.Assets, asset.asset.ID) {
			continue
		}
		if target == cli.BuildTargetCodex && asset.asset.Type == "agent" {
			if output, ok := codexAgents[asset.asset.ID]; ok {
				return output.destination
			}
		}
		if target == cli.BuildTargetCodex && asset.asset.Type == "mcp" && !rendered.nativeCodex {
			return rendered.destination + "/.mcp.json"
		}
		relative := strings.TrimPrefix(asset.path, rendered.unit.Path+"/")
		if relative == asset.path {
			return rendered.destination
		}
		return rendered.destination + "/" + relative
	}
	return ""
}

func buildSelections(resolved []resolvedAsset) []buildSelection {
	selection := make([]buildSelection, len(resolved))
	for index, asset := range resolved {
		selection[index] = buildSelection{Asset: asset.asset.ID, Variant: asset.variant, Reason: asset.reason, RequestedBy: asset.requestedBy}
	}
	return selection
}

func cliSelections(resolved []resolvedAsset) ([]cli.BuildSelection, error) {
	selection := make([]cli.BuildSelection, len(resolved))
	for index, asset := range resolved {
		entry, err := cli.NewBuildSelection(asset.asset.ID, asset.variant, asset.reason, asset.requestedBy)
		if err != nil {
			return nil, err
		}
		selection[index] = entry
	}
	return selection, nil
}
