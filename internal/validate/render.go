package validate

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
)

type renderedUnit struct {
	unit        nativePackage
	destination string
}

type codexAgentOutput struct {
	source      string
	destination string
	name        string
}

func codexAgentOutputs(model validatedManifest, resolved resolvedSelection, target cli.BuildTarget) (map[string]codexAgentOutput, error) {
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
		unit, ok := model.packages[string(cli.BuildTargetCodex)][owner]
		if !ok {
			return nil, validationError("invalid_codex_agent", "Codex agent has no native package")
		}
		if _, native := model.tracked[unit.Path+"/.codex-plugin/plugin.json"]; native {
			continue
		}
		sources := assetFiles(selected.path, model.tracked)
		if len(sources) != 1 || filepath.Ext(sources[0]) != ".md" {
			return nil, validationError("invalid_codex_agent", "Codex agent assets must resolve to one Markdown file")
		}
		name := strings.TrimSuffix(filepath.Base(sources[0]), ".md")
		if name == "" {
			return nil, validationError("invalid_codex_agent", "Codex agent filename is invalid")
		}
		destination := "configuration/.codex/agents/" + name + ".toml"
		collisionKey := strings.ToLower(filepath.ToSlash(destination))
		if previous, collision := destinations[collisionKey]; collision && previous != selected.asset.ID {
			return nil, validationError("codex_agent_output_collision", "Codex agent filenames must be unique across selected packages")
		}
		destinations[collisionKey] = selected.asset.ID
		outputs[selected.asset.ID] = codexAgentOutput{source: sources[0], destination: destination, name: name}
	}
	return outputs, nil
}

func renderBuild(stage, workspace string, model validatedManifest, resolved resolvedSelection, codexAgents map[string]codexAgentOutput, target cli.BuildTarget) ([]buildMapping, error) {
	units := append([]nativePackage(nil), resolved.packages...)
	slices.SortFunc(units, func(left, right nativePackage) int { return strings.Compare(left.ID, right.ID) })
	rendered := make([]renderedUnit, 0, len(units))
	for _, unit := range units {
		destination := "plugin"
		if len(units) > 1 {
			destination = "plugins/" + unit.ID
		}
		if target == cli.BuildTargetClaude {
			if err := copyUnit(stage, destination, workspace, unit, model); err != nil {
				return nil, err
			}
		} else if err := renderCodexUnit(stage, destination, workspace, unit, model); err != nil {
			return nil, err
		}
		rendered = append(rendered, renderedUnit{unit: unit, destination: destination})
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
	return mappings, nil
}

func copyUnit(stage, destination, workspace string, unit nativePackage, model validatedManifest) error {
	for _, source := range filesUnder(model.tracked, unit.Path) {
		relative := strings.TrimPrefix(source, unit.Path+"/")
		if err := copyTrackedBuildFile(stage, destination+"/"+relative, workspace, source, model.tracked); err != nil {
			return err
		}
	}
	return nil
}

func renderCodexUnit(stage, destination, workspace string, unit nativePackage, model validatedManifest) error {
	codexManifest := unit.Path + "/.codex-plugin/plugin.json"
	if _, ok := model.tracked[codexManifest]; ok {
		return copyUnit(stage, destination, workspace, unit, model)
	}
	for _, source := range filesUnder(model.tracked, unit.Path+"/skills") {
		relative := strings.TrimPrefix(source, unit.Path+"/skills/")
		if err := copyTrackedBuildFile(stage, destination+"/skills/"+relative, workspace, source, model.tracked); err != nil {
			return err
		}
	}
	mcpSource := unit.Path + "/.mcp.json"
	hasMCP := false
	if _, ok := model.tracked[mcpSource]; ok {
		hasMCP = true
		var claudeMCP mcpManifest
		if err := readStrictJSON(workspace, mcpSource, model.tracked, &claudeMCP); err != nil {
			return err
		}
		codexMCP := make(map[string]codexMCPServer, len(claudeMCP.Servers))
		for id, server := range claudeMCP.Servers {
			environment, err := environmentNames(server.Env)
			if err != nil {
				return err
			}
			codexMCP[id] = codexMCPServer{Command: server.Command, Args: append([]string(nil), server.Args...), EnvVars: environment}
		}
		content, err := json.MarshalIndent(codexMCP, "", "  ")
		if err != nil {
			return err
		}
		if err := writeBuildFile(stage, destination+"/.mcp.json", append(content, '\n'), 0o644); err != nil {
			return err
		}
	}
	plugin := codexPluginManifest{Name: unit.ID, Version: "0.1.0", Description: "AI4J generated Codex plugin"}
	if len(filesUnder(model.tracked, unit.Path+"/skills")) != 0 {
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
		if err := writeBuildFile(stage, output.destination, codexAgentNamed(body, output.name), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func copyAsset(stage, destination, workspace, source string, model validatedManifest) error {
	files := assetFiles(source, model.tracked)
	if len(files) == 1 {
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
		if target == cli.BuildTargetCodex && asset.asset.Type == "mcp" {
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
