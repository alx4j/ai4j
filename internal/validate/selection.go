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

type resolvedSelection struct {
	bundles  []string
	packages []nativePackage
	assets   []resolvedAsset
}

type bundleExpansion struct {
	bundles  []string
	packages []string
	assets   []string
}

func resolveCanonicalSelection(model validatedManifest, request selection) (resolvedSelection, error) {
	target := string(request.target)
	host := string(request.host)
	packages, ok := model.packages[target]
	if !ok {
		return resolvedSelection{}, validationError("unsupported_capability", "toolkit does not declare the selected target")
	}
	selected := map[string]resolvedAsset{}
	selectedPackages := map[string]nativePackage{}
	selectedBundles := map[string]struct{}{}
	var addAsset func(string, string, string) error
	addAsset = func(id, reason, requestedBy string) error {
		declared, ok := model.assets[id]
		if !ok {
			return validationError("unknown_asset", "selected asset does not exist")
		}
		if _, exists := selected[id]; exists {
			return nil
		}
		if declared.Type == "hook" {
			return validationError("unsupported_capability", "target-native hooks are declared but not emitted by the target renderers")
		}
		path, variant, executable, err := selectVariant(declared, target, host)
		if err != nil {
			return err
		}
		selected[id] = resolvedAsset{asset: declared, path: path, variant: variant, executable: executable, reason: reason, requestedBy: requestedBy}
		dependencies := append([]string(nil), declared.Dependencies...)
		slices.Sort(dependencies)
		for _, dependency := range dependencies {
			dependencyReason := "dependency"
			dependencyRequester := "asset:" + id
			if reason == "all" {
				dependencyReason = "all"
				dependencyRequester = "--all"
			}
			if err := addAsset(dependency, dependencyReason, dependencyRequester); err != nil {
				return err
			}
		}
		return nil
	}
	addPackage := func(id, reason, requestedBy string) error {
		unit, ok := packages[id]
		if !ok {
			return validationError("unsupported_capability", "selected bundle package is unavailable for the selected target")
		}
		if _, exists := selectedPackages[id]; exists {
			return nil
		}
		selectedPackages[id] = unit
		members := append([]string(nil), unit.Assets...)
		slices.Sort(members)
		for _, member := range members {
			if err := addAsset(member, reason, requestedBy); err != nil {
				return err
			}
		}
		return nil
	}
	addExplicitAsset := func(id string) error {
		declared, ok := model.assets[id]
		if !ok {
			return validationError("unknown_asset", "selected asset does not exist")
		}
		if err := addAsset(id, "explicit", "asset:"+id); err != nil {
			return err
		}
		if declared.Ownership == "package" {
			packageID, assigned := model.packageOwners[target][id]
			if !assigned {
				return validationError("unsupported_capability", "selected package asset is unavailable for the selected target")
			}
			return addPackage(packageID, "native_unit", "package:"+packageID)
		}
		return nil
	}
	if request.all {
		packageIDs := make([]string, 0, len(packages))
		for id := range packages {
			packageIDs = append(packageIDs, id)
		}
		slices.Sort(packageIDs)
		for _, id := range packageIDs {
			if err := addPackage(id, "all", "--all"); err != nil {
				return resolvedSelection{}, err
			}
		}
		assetIDs := make([]string, 0, len(model.assets))
		for id, declared := range model.assets {
			if declared.Ownership == "configuration" {
				assetIDs = append(assetIDs, id)
			}
		}
		slices.Sort(assetIDs)
		for _, id := range assetIDs {
			if err := addAsset(id, "all", "--all"); err != nil {
				return resolvedSelection{}, err
			}
		}
	} else {
		assets := append([]string(nil), request.assets...)
		slices.Sort(assets)
		for _, id := range assets {
			if err := addExplicitAsset(id); err != nil {
				return resolvedSelection{}, err
			}
		}
		bundles := append([]string(nil), request.bundles...)
		slices.Sort(bundles)
		for _, id := range bundles {
			expanded, err := expandBundle(model.bundles, id)
			if err != nil {
				return resolvedSelection{}, err
			}
			for _, included := range expanded.bundles {
				selectedBundles[included] = struct{}{}
			}
			for _, packageID := range expanded.packages {
				if err := addPackage(packageID, "native_unit", "package:"+packageID); err != nil {
					return resolvedSelection{}, err
				}
			}
			for _, assetID := range expanded.assets {
				if err := addAsset(assetID, "bundle", "bundle:"+id); err != nil {
					return resolvedSelection{}, err
				}
			}
		}
	}
	assetIDs := selectedIDs(selected)
	resolvedAssets := make([]resolvedAsset, len(assetIDs))
	for index, id := range assetIDs {
		resolvedAssets[index] = selected[id]
	}
	activation := ""
	for _, selectedAsset := range resolvedAssets {
		if selectedAsset.asset.Type != "agent_activation" {
			continue
		}
		if activation != "" {
			return resolvedSelection{}, validationError("conflicting_agent_activation", "selection contains more than one Claude main-agent activation")
		}
		activation = selectedAsset.asset.ID
	}
	packageIDs := make([]string, 0, len(selectedPackages))
	for id := range selectedPackages {
		packageIDs = append(packageIDs, id)
	}
	slices.Sort(packageIDs)
	resolvedPackages := make([]nativePackage, len(packageIDs))
	for index, id := range packageIDs {
		resolvedPackages[index] = selectedPackages[id]
	}
	bundleIDs := make([]string, 0, len(selectedBundles))
	for id := range selectedBundles {
		bundleIDs = append(bundleIDs, id)
	}
	slices.Sort(bundleIDs)
	return resolvedSelection{bundles: bundleIDs, packages: resolvedPackages, assets: resolvedAssets}, nil
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

func expandBundle(bundles map[string]bundle, root string) (bundleExpansion, error) {
	assets := map[string]struct{}{}
	packages := map[string]struct{}{}
	visited := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] == 1 {
			return validationError("bundle_cycle", "bundle graph contains a cycle")
		}
		if visited[id] == 2 {
			return nil
		}
		declared, ok := bundles[id]
		if !ok {
			return validationError("unknown_bundle", "selected bundle does not exist")
		}
		visited[id] = 1
		for _, asset := range declared.Assets {
			assets[asset] = struct{}{}
		}
		for _, packageID := range declared.Packages {
			packages[packageID] = struct{}{}
		}
		nested := append([]string(nil), declared.Bundles...)
		slices.Sort(nested)
		for _, child := range nested {
			if err := visit(child); err != nil {
				return err
			}
		}
		visited[id] = 2
		return nil
	}
	if err := visit(root); err != nil {
		return bundleExpansion{}, err
	}
	resultAssets := make([]string, 0, len(assets))
	for id := range assets {
		resultAssets = append(resultAssets, id)
	}
	slices.Sort(resultAssets)
	resultPackages := make([]string, 0, len(packages))
	for id := range packages {
		resultPackages = append(resultPackages, id)
	}
	slices.Sort(resultPackages)
	resultBundles := make([]string, 0, len(visited))
	for id := range visited {
		resultBundles = append(resultBundles, id)
	}
	slices.Sort(resultBundles)
	return bundleExpansion{bundles: resultBundles, packages: resultPackages, assets: resultAssets}, nil
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
