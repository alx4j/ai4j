package validate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
)

func selectedPackageSources(unit nativePackage, model validatedManifest, selected []resolvedAsset, target cli.BuildTarget) []string {
	selectedPaths := make(map[string]string, len(unit.Assets))
	selectedVariantFiles := make(map[string]struct{})
	for _, item := range selected {
		if !slices.Contains(unit.Assets, item.asset.ID) {
			continue
		}
		selectedPaths[item.asset.ID] = item.path
		if len(item.asset.Variants) != 0 {
			for _, source := range assetFiles(item.path, model.tracked) {
				selectedVariantFiles[source] = struct{}{}
			}
		} else if _, sourceIsFile := model.tracked[item.path]; sourceIsFile {
			selectedVariantFiles[item.path] = struct{}{}
		}
	}

	foreignVariantFiles := make(map[string]struct{})
	for _, assetID := range unit.Assets {
		selectedPath, ok := selectedPaths[assetID]
		if !ok {
			continue
		}
		for _, variant := range model.assets[assetID].Variants {
			if variant.Path == selectedPath {
				continue
			}
			for _, source := range assetFiles(variant.Path, model.tracked) {
				foreignVariantFiles[source] = struct{}{}
			}
		}
	}

	sources := make(map[string]struct{})
	for _, item := range selected {
		if !slices.Contains(unit.Assets, item.asset.ID) {
			continue
		}
		for _, source := range assetFiles(item.path, model.tracked) {
			if nativePluginMetadataPath(source, unit.Path) {
				continue
			}
			if _, foreign := foreignVariantFiles[source]; foreign {
				if _, selectedVariant := selectedVariantFiles[source]; !selectedVariant {
					continue
				}
			}
			sources[source] = struct{}{}
		}
	}
	manifest := unit.Path + "/." + string(target) + "-plugin/plugin.json"
	if _, ok := model.tracked[manifest]; ok {
		sources[manifest] = struct{}{}
	}

	result := make([]string, 0, len(sources))
	for source := range sources {
		result = append(result, source)
	}
	slices.Sort(result)
	return result
}

func materializeSelectedPackage(stage, destination, workspace string, unit nativePackage, model validatedManifest, selected []resolvedAsset, target cli.BuildTarget) error {
	for _, source := range selectedPackageSources(unit, model, selected, target) {
		relative := strings.TrimPrefix(source, unit.Path+"/")
		if err := copyTrackedBuildFile(stage, destination+"/"+relative, workspace, source, model.tracked); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) validateSelectedClaudePackages(ctx context.Context, workspacePath, claude string, model validatedManifest, resolved resolvedSelection) error {
	stage, err := os.MkdirTemp(workspacePath, ".ai4j-claude-packages-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()

	for _, unit := range resolved.packages {
		if err := materializeSelectedPackage(stage, unit.ID, workspacePath, unit, model, resolved.assets, cli.BuildTargetClaude); err != nil {
			return err
		}
		nativeContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
		native, runErr := s.config.Runner.Run(nativeContext, filepath.Join(stage, unit.ID), claude, []string{"plugin", "validate", ".", "--strict"}, claudeEnvironment())
		cancel()
		if runErr != nil || native.ExitCode != 0 {
			return errors.New("Claude rejected the selected native package")
		}
	}
	return nil
}
