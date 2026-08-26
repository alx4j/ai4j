package validate

import (
	"slices"

	"github.com/alx4j/ai4j/internal/cli"
)

type resolvedAsset struct {
	asset       asset
	path        string
	variant     string
	executable  *executable
	reason      string
	requestedBy string
}

type selection struct {
	target  cli.BuildTarget
	host    cli.BuildHost
	all     bool
	assets  []string
	bundles []string
}

func resolveSelection(model validatedManifest, request selection) ([]resolvedAsset, error) {
	target := string(request.target)
	host := string(request.host)
	targetConfig, ok := model.manifest.Targets[target]
	if !ok {
		return nil, validationError("unsupported_capability", "toolkit does not declare the selected target")
	}
	selected := map[string]resolvedAsset{}
	add := func(id, reason, requestedBy string) error {
		asset, ok := model.assets[id]
		if !ok {
			return validationError("unknown_asset", "selected asset does not exist")
		}
		if _, exists := selected[id]; exists {
			return nil
		}
		if asset.Type == "hook" {
			return validationError("unsupported_capability", "target-native hooks are declared but not emitted by the target renderers")
		}
		path, variant, executable, err := selectVariant(asset, target, host)
		if err != nil {
			return err
		}
		selected[id] = resolvedAsset{asset: asset, path: path, variant: variant, executable: executable, reason: reason, requestedBy: requestedBy}
		return nil
	}
	if request.all {
		ids := make([]string, 0, len(model.assets))
		for id := range model.assets {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			if err := add(id, "all", "--all"); err != nil {
				return nil, err
			}
		}
	} else {
		for _, id := range request.assets {
			if err := add(id, "explicit", "asset:"+id); err != nil {
				return nil, err
			}
		}
		for _, id := range request.bundles {
			if _, ok := model.bundles[id]; !ok {
				return nil, validationError("unknown_bundle", "selected bundle does not exist")
			}
			assets, err := expandBundle(model.bundles, id)
			if err != nil {
				return nil, err
			}
			for _, asset := range assets {
				if err := add(asset, "bundle", "bundle:"+id); err != nil {
					return nil, err
				}
			}
		}
	}
	for {
		before := len(selected)
		ids := selectedIDs(selected)
		for _, id := range ids {
			dependencies := append([]string(nil), model.assets[id].Dependencies...)
			slices.Sort(dependencies)
			for _, dependency := range dependencies {
				if err := add(dependency, "dependency", "asset:"+id); err != nil {
					return nil, err
				}
			}
		}
		for _, unit := range targetConfig.Packages {
			containsSelected := false
			for _, id := range unit.Assets {
				if _, ok := selected[id]; ok {
					containsSelected = true
					break
				}
			}
			if !containsSelected {
				continue
			}
			members := append([]string(nil), unit.Assets...)
			slices.Sort(members)
			for _, id := range members {
				if err := add(id, "native_unit", "package:"+unit.ID); err != nil {
					return nil, err
				}
			}
		}
		if len(selected) == before {
			break
		}
	}
	ids := selectedIDs(selected)
	resolved := make([]resolvedAsset, len(ids))
	for index, id := range ids {
		resolved[index] = selected[id]
	}
	return resolved, nil
}

func selectedIDs(values map[string]resolvedAsset) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func selectVariant(asset asset, target, host string) (string, string, *executable, error) {
	if asset.Path != "" {
		return asset.Path, "default", asset.Executable, nil
	}
	compatible := []assetVariant{}
	for _, variant := range asset.Variants {
		if slices.Contains(variant.Targets, target) && slices.Contains(variant.Hosts, host) {
			compatible = append(compatible, variant)
		}
	}
	if len(compatible) == 0 {
		return "", "", nil, validationError("unsupported_variant", "asset has no compatible target and host variant")
	}
	if len(compatible) != 1 {
		return "", "", nil, validationError("ambiguous_variant", "asset has multiple compatible target and host variants")
	}
	return compatible[0].Path, compatible[0].ID, compatible[0].Executable, nil
}

func expandBundle(bundles map[string]bundle, root string) ([]string, error) {
	assets := map[string]struct{}{}
	visited := map[string]struct{}{}
	var visit func(string) error
	visit = func(id string) error {
		if _, ok := visited[id]; ok {
			return nil
		}
		bundle, ok := bundles[id]
		if !ok {
			return validationError("unknown_bundle", "selected bundle does not exist")
		}
		visited[id] = struct{}{}
		for _, asset := range bundle.Assets {
			assets[asset] = struct{}{}
		}
		nested := append([]string(nil), bundle.Bundles...)
		slices.Sort(nested)
		for _, child := range nested {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(assets))
	for id := range assets {
		result = append(result, id)
	}
	slices.Sort(result)
	return result, nil
}

func selectedContent(root string, model validatedManifest, resolved []resolvedAsset) ([]cli.ContentItem, error) {
	content := make([]cli.ContentItem, 0, len(resolved))
	for _, selected := range resolved {
		checksum, err := digestFiles(root, assetFiles(selected.path, model.tracked), model.tracked)
		if err != nil {
			return nil, validationError("package_read_failed", "selected asset content could not be read")
		}
		component, err := componentType(selected.asset.Type)
		if err != nil {
			return nil, err
		}
		var execution *cli.Execution
		if selected.executable != nil {
			declaration := selected.executable
			dependency := cli.DependencyRequired
			if declaration.Dependency == "optional" {
				dependency = cli.DependencyOptional
			}
			ownership := cli.ExecutionHostResolved
			if selected.asset.Type == "script" || selected.asset.Type == "binary" {
				ownership = cli.ExecutionToolkitOwned
			}
			value, executionErr := cli.NewExecution(ownership, dependency, declaration.Command, declaration.Args, "", nil, declaration.Environment)
			if executionErr != nil {
				return nil, validationError("invalid_executable", "selected asset executable disclosure is invalid")
			}
			execution = &value
		}
		item, err := cli.NewContentItem(component, selected.asset.ID, selected.path, checksum, cli.ContentAdded, execution)
		if err != nil {
			return nil, validationError("invalid_asset", "selected asset disclosure is invalid")
		}
		content = append(content, item)
	}
	return content, nil
}
