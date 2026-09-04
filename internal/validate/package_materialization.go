package validate

import (
	"context"
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
		if err := s.validateClaudePackage(ctx, filepath.Join(stage, unit.ID), claude); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) validateRenderedClaudePackages(ctx context.Context, stage string) error {
	claude, _ := s.config.Runner.LookPath("claude")
	roots := []string{filepath.Join(stage, "plugin")}
	if entries, err := os.ReadDir(filepath.Join(stage, "plugins")); err == nil {
		roots = roots[:0]
		for _, entry := range entries {
			if entry.IsDir() {
				roots = append(roots, filepath.Join(stage, "plugins", entry.Name()))
			}
		}
	}
	for _, root := range roots {
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		if err := s.validateClaudePackage(ctx, root, claude); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) validateClaudePackage(ctx context.Context, root, claude string) error {
	nativeContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	native, err := s.config.Runner.Run(nativeContext, root, claude, []string{"plugin", "validate", ".", "--strict"}, claudeEnvironment())
	if err != nil || native.ExitCode != 0 {
		return errNativeBuildValidation
	}
	return nil
}
