package validate

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
)

type renderedUnitV2 struct {
	unit        nativePackageV2
	destination string
}

func renderV2Build(stage, workspace string, model validatedManifestV2, resolved []resolvedAssetV2, target cli.BuildTarget) ([]buildMapping, error) {
	targetConfig, ok := model.manifest.Targets[string(target)]
	if !ok {
		return nil, errors.New("selected target is not declared")
	}
	selected := map[string]resolvedAssetV2{}
	for _, asset := range resolved {
		selected[asset.asset.ID] = asset
	}
	units := []nativePackageV2{}
	for _, unit := range targetConfig.Packages {
		include := len(resolved) == 0
		for _, id := range unit.Assets {
			if _, ok := selected[id]; ok {
				include = true
				break
			}
		}
		if include {
			units = append(units, unit)
		}
	}
	slices.SortFunc(units, func(left, right nativePackageV2) int { return strings.Compare(left.ID, right.ID) })
	rendered := make([]renderedUnitV2, 0, len(units))
	for _, unit := range units {
		destination := "plugin"
		if len(units) > 1 {
			destination = "plugins/" + unit.ID
		}
		if target == cli.BuildTargetClaude {
			if err := copyV2Unit(stage, destination, workspace, unit, model); err != nil {
				return nil, err
			}
		} else if err := renderCodexUnitV2(stage, destination, workspace, unit, model); err != nil {
			return nil, err
		}
		rendered = append(rendered, renderedUnitV2{unit: unit, destination: destination})
	}
	mappings := make([]buildMapping, 0, len(resolved))
	instructionCount := 0
	for _, selectedAsset := range resolved {
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
			if err := copyV2Asset(stage, destination, workspace, selectedAsset.path, model); err != nil {
				return nil, err
			}
			native = destination
		} else {
			native = mappedPackagePathV2(selectedAsset, rendered, target)
			if native == "" {
				return nil, errors.New("selected package asset has no native unit")
			}
		}
		mappings = append(mappings, buildMapping{Canonical: selectedAsset.asset.Type + ":" + selectedAsset.asset.ID, Native: native, Fidelity: "exact"})
	}
	return mappings, nil
}

func copyV2Unit(stage, destination, workspace string, unit nativePackageV2, model validatedManifestV2) error {
	for _, source := range filesUnder(model.tracked, unit.Path) {
		relative := strings.TrimPrefix(source, unit.Path+"/")
		if err := copyTrackedBuildFile(stage, destination+"/"+relative, workspace, source, model.tracked); err != nil {
			return err
		}
	}
	return nil
}

func renderCodexUnitV2(stage, destination, workspace string, unit nativePackageV2, model validatedManifestV2) error {
	codexManifest := unit.Path + "/.codex-plugin/plugin.json"
	if _, ok := model.tracked[codexManifest]; ok {
		return copyV2Unit(stage, destination, workspace, unit, model)
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
	for _, source := range filesUnder(model.tracked, unit.Path+"/agents") {
		if filepath.Ext(source) != ".md" {
			return errors.New("Codex agent source must be Markdown")
		}
		body, err := readTrackedFile(workspace, source, model.tracked)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(filepath.Base(source), ".md")
		if err := writeBuildFile(stage, "configuration/.codex/agents/"+name+".toml", codexAgentNamed(body, name), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func copyV2Asset(stage, destination, workspace, source string, model validatedManifestV2) error {
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

func mappedPackagePathV2(asset resolvedAssetV2, units []renderedUnitV2, target cli.BuildTarget) string {
	for _, rendered := range units {
		if !slices.Contains(rendered.unit.Assets, asset.asset.ID) {
			continue
		}
		if target == cli.BuildTargetCodex && asset.asset.Type == "agent" {
			return "configuration/.codex/agents/" + asset.asset.ID + ".toml"
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

func buildSelections(resolved []resolvedAssetV2) []buildSelection {
	selection := make([]buildSelection, len(resolved))
	for index, asset := range resolved {
		selection[index] = buildSelection{Asset: asset.asset.ID, Variant: asset.variant, Reason: asset.reason, RequestedBy: asset.requestedBy}
	}
	return selection
}

func cliSelectionsV2(resolved []resolvedAssetV2) ([]cli.BuildSelection, error) {
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
