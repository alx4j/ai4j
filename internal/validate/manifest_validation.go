package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
)

type validatedManifest struct {
	manifest      toolkitManifest
	assets        map[string]asset
	bundles       map[string]bundle
	packages      map[string]map[string]nativePackage
	packageOwners map[string]map[string]string
	tracked       map[string]gitsource.TreeEntry
}

func validatePackage(root string, inventory gitsource.TreeInventory) (packageResult, error) {
	tracked := trackedFiles(inventory)
	var manifest toolkitManifest
	if err := readStrictJSON(root, toolkitManifestPath, tracked, &manifest); err != nil {
		return packageResult{}, validationError("invalid_toolkit_manifest", "toolkit.json is missing or invalid")
	}
	if manifest.SchemaVersion != 1 {
		return packageResult{}, validationError("unsupported_schema", "toolkit schema version is not supported")
	}
	model, err := validateManifest(root, manifest, tracked)
	if err != nil {
		return packageResult{}, err
	}
	if err := validateMCPAssets(root, model); err != nil {
		return packageResult{}, err
	}
	content, err := contentForManifest(root, model)
	if err != nil {
		return packageResult{}, err
	}
	selected := []string{toolkitManifestPath}
	for _, asset := range manifest.Assets {
		if asset.Path != "" {
			selected = append(selected, assetFiles(asset.Path, tracked)...)
		}
		for _, variant := range asset.Variants {
			selected = append(selected, assetFiles(variant.Path, tracked)...)
		}
	}
	for _, target := range manifest.Targets {
		for _, unit := range target.Packages {
			selected = append(selected, filesUnder(tracked, unit.Path)...)
		}
	}
	digest, err := digestFiles(root, selected, tracked)
	if err != nil {
		return packageResult{}, validationError("package_read_failed", "declared package content could not be read")
	}
	var rules []byte
	var rulesChecksum string
	for _, asset := range manifest.Assets {
		if asset.Type != "instruction" || asset.Path == "" {
			continue
		}
		rules, err = readTrackedFile(root, asset.Path, tracked)
		if err != nil {
			return packageResult{}, validationError("invalid_shared_rules", "persistent instruction content could not be read")
		}
		hash := sha256.Sum256(rules)
		rulesChecksum = hex.EncodeToString(hash[:])
		break
	}
	var nativePackagePaths []string
	for _, unit := range manifest.Targets["claude"].Packages {
		nativePackagePaths = append(nativePackagePaths, unit.Path)
	}
	slices.Sort(nativePackagePaths)
	nativePackagePaths = slices.Compact(nativePackagePaths)
	return packageResult{
		content: content, rules: rules, rulesChecksum: rulesChecksum, digest: digest,
		nativePackagePaths: nativePackagePaths, model: model,
	}, nil
}

func validateMCPAssets(root string, model validatedManifest) error {
	for _, asset := range model.assets {
		if asset.Type != "mcp" {
			continue
		}
		paths := []string{asset.Path}
		if asset.Path == "" {
			paths = paths[:0]
			for _, variant := range asset.Variants {
				paths = append(paths, variant.Path)
			}
		}
		for _, path := range paths {
			var manifest mcpManifest
			if err := readStrictJSON(root, path, model.tracked, &manifest); err != nil || len(manifest.Servers) == 0 {
				return validationError("invalid_mcp", "MCP asset must use a valid command-based declaration")
			}
			for _, server := range manifest.Servers {
				environment, err := environmentNames(server.Env)
				if err != nil {
					return err
				}
				expected := append([]string(nil), asset.Executable.Environment...)
				slices.Sort(expected)
				if server.Command != asset.Executable.Command || !slices.Equal(server.Args, asset.Executable.Args) || !slices.Equal(environment, expected) {
					return validationError("invalid_mcp", "MCP native declaration does not match its canonical executable and environment references")
				}
			}
		}
	}
	return nil
}

func validateManifest(root string, manifest toolkitManifest, tracked map[string]gitsource.TreeEntry) (validatedManifest, error) {
	if err := validateToolkitMetadata(manifest); err != nil {
		return validatedManifest{}, err
	}
	model := validatedManifest{
		manifest: manifest, assets: make(map[string]asset, len(manifest.Assets)), bundles: make(map[string]bundle, len(manifest.Bundles)),
		packages: make(map[string]map[string]nativePackage, len(manifest.Targets)), packageOwners: make(map[string]map[string]string, len(manifest.Targets)), tracked: tracked,
	}
	if err := validateAssetDeclarations(&model); err != nil {
		return validatedManifest{}, err
	}
	assetIDs, err := validateAssetDependencies(model)
	if err != nil {
		return validatedManifest{}, err
	}
	targetNames, packageIDs, err := validateTargetPackages(&model, assetIDs)
	if err != nil {
		return validatedManifest{}, err
	}
	if err := validatePackageDependencies(model, assetIDs, targetNames); err != nil {
		return validatedManifest{}, err
	}
	if err := validateBundleDeclarations(&model, packageIDs); err != nil {
		return validatedManifest{}, err
	}
	if err := validatePackageDisclosure(root, model, targetNames); err != nil {
		return validatedManifest{}, err
	}
	return model, nil
}

func validateToolkitMetadata(manifest toolkitManifest) error {
	if manifest.SchemaVersion != 1 || !validManifestID(manifest.Toolkit.ID) || !boundedValue(manifest.Toolkit.Version, 64) ||
		!boundedValue(manifest.Toolkit.DisplayName, 128) || !boundedValue(manifest.Toolkit.Compatibility.MinimumCLI, 64) ||
		(manifest.Toolkit.DeclarationID != "" && !validManifestID(manifest.Toolkit.DeclarationID)) || len(manifest.Assets) > 4096 || len(manifest.Bundles) > 1024 || len(manifest.Targets) == 0 || len(manifest.Targets) > 2 {
		return validationError("invalid_toolkit_manifest", "toolkit metadata is incomplete or exceeds supported bounds")
	}
	return nil
}

func validateAssetDeclarations(model *validatedManifest) error {
	for _, asset := range model.manifest.Assets {
		if !validManifestID(asset.ID) || !validAssetType(asset.Type) || (asset.Ownership != "package" && asset.Ownership != "configuration") || len(asset.Dependencies) > 256 || len(asset.Variants) > 16 {
			return validationError("invalid_asset", "asset declaration is invalid")
		}
		if _, duplicate := model.assets[asset.ID]; duplicate {
			return validationError("duplicate_asset", "asset identifiers must be unique")
		}
		if (asset.Path == "") == (len(asset.Variants) == 0) {
			return validationError("invalid_asset", "asset must declare one path or one or more variants")
		}
		if asset.Path != "" && !validAssetPath(asset.Path, model.tracked) {
			return validationError("invalid_asset_path", "asset path is missing or unsafe")
		}
		variantIDs := map[string]struct{}{}
		for _, variant := range asset.Variants {
			if !validManifestID(variant.ID) || !validAssetPath(variant.Path, model.tracked) || len(variant.Targets) == 0 || len(variant.Hosts) == 0 || !validTargetNames(variant.Targets) || !validHostNames(variant.Hosts) {
				return validationError("invalid_variant", "asset variant is invalid")
			}
			if _, duplicate := variantIDs[variant.ID]; duplicate {
				return validationError("ambiguous_variant", "asset variant identifiers must be unique")
			}
			variantIDs[variant.ID] = struct{}{}
		}
		if err := validateExecutable(asset); err != nil {
			return err
		}
		if err := validateOverlays(asset.Overlays); err != nil {
			return err
		}
		model.assets[asset.ID] = asset
	}
	return nil
}

func validateAssetDependencies(model validatedManifest) ([]string, error) {
	for _, asset := range model.manifest.Assets {
		seen := map[string]struct{}{}
		for _, dependency := range asset.Dependencies {
			declared, ok := model.assets[dependency]
			if !ok {
				return nil, validationError("missing_dependency", "asset dependency does not exist")
			}
			if _, duplicate := seen[dependency]; duplicate {
				return nil, validationError("duplicate_dependency", "asset dependency is duplicated")
			}
			if declared.Ownership != asset.Ownership {
				return nil, validationError("invalid_dependency_ownership", "asset dependency crosses an ownership boundary")
			}
			seen[dependency] = struct{}{}
		}
	}
	if cycleInAssets(model.assets) {
		return nil, validationError("dependency_cycle", "asset dependency graph contains a cycle")
	}
	assetIDs := make([]string, 0, len(model.assets))
	for assetID := range model.assets {
		assetIDs = append(assetIDs, assetID)
	}
	slices.Sort(assetIDs)
	return assetIDs, nil
}

func validateTargetPackages(model *validatedManifest, assetIDs []string) ([]string, map[string]struct{}, error) {
	packageIDs := map[string]struct{}{}
	targetNames := make([]string, 0, len(model.manifest.Targets))
	for targetName := range model.manifest.Targets {
		targetNames = append(targetNames, targetName)
	}
	slices.Sort(targetNames)
	for _, targetName := range targetNames {
		targetConfig := model.manifest.Targets[targetName]
		if !validTargetName(targetName) || len(targetConfig.Packages) == 0 || len(targetConfig.Packages) > 256 {
			return nil, nil, validationError("invalid_target", "target package declaration is invalid")
		}
		packages := make(map[string]nativePackage, len(targetConfig.Packages))
		owners := map[string]string{}
		roots := make([]string, 0, len(targetConfig.Packages))
		for _, unit := range targetConfig.Packages {
			if !validManifestID(unit.ID) || !safeRelative(unit.Path) || len(filesUnder(model.tracked, unit.Path)) == 0 || len(unit.Assets) > 4096 {
				return nil, nil, validationError("invalid_native_package", "target native package is invalid")
			}
			if _, duplicate := packages[unit.ID]; duplicate {
				return nil, nil, validationError("duplicate_native_package", "target native package identifiers must be unique")
			}
			for _, root := range roots {
				if packagePathsOverlap(root, unit.Path) {
					return nil, nil, validationError("overlapping_native_package", "target native package roots must not overlap")
				}
			}
			members := map[string]struct{}{}
			for _, assetID := range unit.Assets {
				declared, ok := model.assets[assetID]
				if !ok {
					return nil, nil, validationError("missing_native_asset", "target native package references an unknown asset")
				}
				if _, duplicate := members[assetID]; duplicate {
					return nil, nil, validationError("duplicate_native_asset", "target native package repeats an asset")
				}
				if declared.Ownership != "package" || !assetSupportsTarget(declared, targetName) {
					return nil, nil, validationError("invalid_native_asset", "target native package must contain compatible package-owned assets")
				}
				for _, assetPath := range assetPathsForTarget(declared, targetName) {
					if !pathWithinPackage(assetPath, unit.Path) {
						return nil, nil, validationError("native_asset_outside_package", "package-owned asset is outside its native package root")
					}
				}
				if _, assigned := owners[assetID]; assigned {
					return nil, nil, validationError("ambiguous_native_asset", "package-owned asset belongs to multiple native packages")
				}
				members[assetID] = struct{}{}
				owners[assetID] = unit.ID
			}
			packages[unit.ID] = unit
			packageIDs[unit.ID] = struct{}{}
			roots = append(roots, unit.Path)
		}
		for _, assetID := range assetIDs {
			declared := model.assets[assetID]
			if !assetSupportsTarget(declared, targetName) {
				continue
			}
			if declared.Ownership == "package" {
				if _, assigned := owners[assetID]; !assigned {
					return nil, nil, validationError("unassigned_native_asset", "compatible package-owned asset has no native package")
				}
				continue
			}
			for _, assetPath := range assetPathsForTarget(declared, targetName) {
				for _, root := range roots {
					if pathWithinPackage(assetPath, root) {
						return nil, nil, validationError("configuration_asset_inside_package", "configuration-owned asset must be outside native package roots")
					}
				}
			}
		}
		model.packages[targetName] = packages
		model.packageOwners[targetName] = owners
	}
	return targetNames, packageIDs, nil
}

func validatePackageDependencies(model validatedManifest, assetIDs, targetNames []string) error {
	for _, assetID := range assetIDs {
		declared := model.assets[assetID]
		if declared.Ownership != "package" {
			continue
		}
		for _, dependency := range declared.Dependencies {
			for _, targetName := range targetNames {
				owner, selected := model.packageOwners[targetName][assetID]
				if selected && model.packageOwners[targetName][dependency] != owner {
					return validationError("cross_package_dependency", "package-owned asset dependency crosses a native package boundary")
				}
			}
		}
	}
	return nil
}

func validateBundleDeclarations(model *validatedManifest, packageIDs map[string]struct{}) error {
	for _, bundle := range model.manifest.Bundles {
		if !validManifestID(bundle.ID) || len(bundle.Assets)+len(bundle.Packages)+len(bundle.Bundles) == 0 || len(bundle.Assets) > 4096 || len(bundle.Packages) > 256 || len(bundle.Bundles) > 1024 || hasDuplicate(bundle.Assets) || hasDuplicate(bundle.Packages) || hasDuplicate(bundle.Bundles) {
			return validationError("invalid_bundle", "bundle declaration is invalid")
		}
		if _, duplicate := model.bundles[bundle.ID]; duplicate {
			return validationError("duplicate_bundle", "bundle identifiers must be unique")
		}
		for _, assetID := range bundle.Assets {
			declared, ok := model.assets[assetID]
			if !ok {
				return validationError("missing_bundle_asset", "bundle asset does not exist")
			}
			if declared.Ownership != "configuration" {
				return validationError("invalid_bundle_asset", "bundle assets must be configuration-owned")
			}
		}
		for _, packageID := range bundle.Packages {
			if _, ok := packageIDs[packageID]; !ok {
				return validationError("missing_bundle_package", "bundle native package does not exist")
			}
		}
		model.bundles[bundle.ID] = bundle
	}
	for _, bundle := range model.manifest.Bundles {
		for _, nested := range bundle.Bundles {
			if _, ok := model.bundles[nested]; !ok {
				return validationError("missing_bundle", "nested bundle does not exist")
			}
		}
	}
	if cycleInBundles(model.bundles) {
		return validationError("bundle_cycle", "bundle graph contains a cycle")
	}
	return nil
}

func validatePackageDisclosure(root string, model validatedManifest, targetNames []string) error {
	for _, targetName := range targetNames {
		for _, unit := range model.manifest.Targets[targetName].Packages {
			coveredFiles := make(map[string]struct{})
			coveringAssets := make(map[string][]string)
			for _, assetID := range unit.Assets {
				for _, assetPath := range assetPathsForTarget(model.assets[assetID], targetName) {
					for _, file := range assetFiles(assetPath, model.tracked) {
						coveredFiles[file] = struct{}{}
						coveringAssets[file] = append(coveringAssets[file], assetID)
					}
				}
			}
			for _, file := range filesUnder(model.tracked, unit.Path) {
				if nativePluginMetadataPath(file, unit.Path) {
					if err := validateNativePluginMetadata(root, file, model.tracked); err != nil {
						return err
					}
					continue
				}
				if _, covered := coveredFiles[file]; !covered {
					return undeclaredPackageContent()
				}
				if err := validateNativePathDisclosure(file, unit.Path, coveringAssets[file], model.assets); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type nativePluginMetadata struct {
	Schema      json.RawMessage `json:"$schema,omitempty"`
	Name        json.RawMessage `json:"name,omitempty"`
	DisplayName json.RawMessage `json:"displayName,omitempty"`
	Version     json.RawMessage `json:"version,omitempty"`
	Description json.RawMessage `json:"description,omitempty"`
	Author      json.RawMessage `json:"author,omitempty"`
	Homepage    json.RawMessage `json:"homepage,omitempty"`
	Repository  json.RawMessage `json:"repository,omitempty"`
	License     json.RawMessage `json:"license,omitempty"`
	Keywords    json.RawMessage `json:"keywords,omitempty"`
}

func nativePluginMetadataPath(path, packageRoot string) bool {
	return path == packageRoot+"/.claude-plugin/plugin.json" || path == packageRoot+"/.codex-plugin/plugin.json"
}

func validateNativePluginMetadata(root, path string, tracked map[string]gitsource.TreeEntry) error {
	var metadata nativePluginMetadata
	if err := readStrictJSON(root, path, tracked, &metadata); err != nil {
		return undeclaredPackageContent()
	}
	return nil
}

func validateNativePathDisclosure(path, packageRoot string, covering []string, assets map[string]asset) error {
	relative := strings.TrimPrefix(path, strings.TrimSuffix(packageRoot, "/")+"/")
	switch {
	case relative == ".mcp.json":
		if !coveringAssetType(covering, assets, "mcp") {
			return undeclaredPackageContent()
		}
	case strings.HasPrefix(relative, "agents/"):
		if !coveringAssetType(covering, assets, "agent") {
			return undeclaredPackageContent()
		}
	case strings.HasPrefix(relative, "commands/"):
		if !coveringAssetType(covering, assets, "prompt") {
			return undeclaredPackageContent()
		}
	case strings.HasPrefix(relative, "skills/"):
		if !skillDisclosureCovers(covering, assets) {
			return undeclaredPackageContent()
		}
	case relative == ".lsp.json", relative == "settings.json", relative == "settings.local.json",
		strings.HasPrefix(relative, "hooks/"), strings.HasPrefix(relative, "bin/"),
		strings.HasPrefix(relative, "monitors/"), strings.HasPrefix(relative, "output-styles/"),
		strings.HasPrefix(relative, "outputStyles/"):
		return undeclaredPackageContent()
	}
	return nil
}

func coveringAssetType(covering []string, assets map[string]asset, expected string) bool {
	for _, assetID := range covering {
		if assets[assetID].Type == expected {
			return true
		}
	}
	return false
}

func skillDisclosureCovers(covering []string, assets map[string]asset) bool {
	var skills []asset
	for _, assetID := range covering {
		if assets[assetID].Type == "skill" {
			skills = append(skills, assets[assetID])
		}
	}
	if len(skills) == 0 {
		return false
	}
	for _, assetID := range covering {
		if assets[assetID].Type == "skill" {
			continue
		}
		declared := false
		for _, skill := range skills {
			if slices.Contains(skill.Dependencies, assetID) {
				declared = true
				break
			}
		}
		if !declared {
			return false
		}
	}
	return true
}

func undeclaredPackageContent() error {
	return validationError("undeclared_package_content", "native package contains content not covered by its declared assets")
}

func contentForManifest(root string, model validatedManifest) ([]cli.ContentItem, error) {
	ids := make([]string, 0, len(model.assets))
	for id := range model.assets {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	content := make([]cli.ContentItem, 0, len(ids))
	for _, id := range ids {
		asset := model.assets[id]
		paths := []string{}
		sourcePath := asset.Path
		if asset.Path != "" {
			paths = append(paths, assetFiles(asset.Path, model.tracked)...)
		} else {
			variants := append([]assetVariant(nil), asset.Variants...)
			slices.SortFunc(variants, func(left, right assetVariant) int { return strings.Compare(left.ID, right.ID) })
			sourcePath = variants[0].Path
			for _, variant := range variants {
				paths = append(paths, assetFiles(variant.Path, model.tracked)...)
			}
		}
		checksum, err := digestFiles(root, paths, model.tracked)
		if err != nil {
			return nil, validationError("package_read_failed", "asset content could not be read")
		}
		component, err := componentType(asset.Type)
		if err != nil {
			return nil, err
		}
		var execution *cli.Execution
		declaration := asset.Executable
		if declaration == nil && len(asset.Variants) != 0 {
			variants := append([]assetVariant(nil), asset.Variants...)
			slices.SortFunc(variants, func(left, right assetVariant) int { return strings.Compare(left.ID, right.ID) })
			declaration = variants[0].Executable
		}
		if declaration != nil {
			dependency := cli.DependencyRequired
			if declaration.Dependency == "optional" {
				dependency = cli.DependencyOptional
			}
			ownership := cli.ExecutionHostResolved
			if asset.Type == "script" || asset.Type == "binary" {
				ownership = cli.ExecutionToolkitOwned
			}
			value, executionErr := cli.NewExecution(ownership, dependency, declaration.Command, declaration.Args, "", nil, declaration.Environment)
			if executionErr != nil {
				return nil, validationError("invalid_executable", "asset executable disclosure is invalid")
			}
			execution = &value
		}
		item, err := cli.NewContentItem(component, id, sourcePath, checksum, cli.ContentAdded, execution)
		if err != nil {
			return nil, validationError("invalid_asset", "asset disclosure is invalid")
		}
		content = append(content, item)
	}
	return content, nil
}

func validateExecutable(asset asset) error {
	wantsExecutable := asset.Type == "script" || asset.Type == "binary" || asset.Type == "mcp"
	if !wantsExecutable {
		if asset.Executable != nil {
			return validationError("invalid_executable", "non-executable asset declares executable behavior")
		}
		for _, variant := range asset.Variants {
			if variant.Executable != nil {
				return validationError("invalid_executable", "non-executable asset variant declares executable behavior")
			}
		}
		return nil
	}
	if asset.Path != "" {
		if asset.Executable == nil {
			return validationError("invalid_executable", "executable asset declaration is incomplete")
		}
		return validateExecutableDeclaration(asset.Executable)
	}
	if asset.Executable != nil || len(asset.Variants) == 0 {
		return validationError("invalid_executable", "executable asset declaration is incomplete")
	}
	for _, variant := range asset.Variants {
		if variant.Executable == nil {
			return validationError("invalid_executable", "executable asset variant declaration is incomplete")
		}
		if err := validateExecutableDeclaration(variant.Executable); err != nil {
			return err
		}
	}
	return nil
}

func validateExecutableDeclaration(value *executable) error {
	if !boundedValue(value.Command, 1024) || (value.Dependency != "required" && value.Dependency != "optional") || len(value.Args) > 128 || len(value.Environment) > 128 {
		return validationError("invalid_executable", "asset executable declaration is invalid")
	}
	for _, name := range value.Environment {
		if !validEnvironmentName(name) {
			return validationError("invalid_environment_reference", "environment reference name is invalid")
		}
	}
	return nil
}

func validateOverlays(overlays map[string]targetOverlay) error {
	for target, overlay := range overlays {
		if !validTargetName(target) || len(overlay.Tools) > 128 || len(overlay.Environment) > 128 || len(overlay.HookEvents) > 128 || len(overlay.Model) > 256 {
			return validationError("invalid_target_overlay", "target overlay is invalid")
		}
		for _, name := range overlay.Environment {
			if !validEnvironmentName(name) {
				return validationError("invalid_environment_reference", "target overlay environment reference is invalid")
			}
		}
	}
	return nil
}

func componentType(value string) (cli.ComponentType, error) {
	switch value {
	case "skill":
		return cli.ComponentSkill, nil
	case "agent":
		return cli.ComponentAgent, nil
	case "instruction":
		return cli.ComponentSharedInstruction, nil
	case "prompt":
		return cli.ComponentPromptTemplate, nil
	case "reference":
		return cli.ComponentReference, nil
	case "support":
		return cli.ComponentSupport, nil
	case "script":
		return cli.ComponentScript, nil
	case "binary":
		return cli.ComponentBinary, nil
	case "mcp":
		return cli.ComponentMCP, nil
	case "hook":
		return cli.ComponentHook, nil
	case "extension":
		return cli.ComponentExtension, nil
	default:
		return "", validationError("invalid_asset", "asset type is unsupported")
	}
}

func validAssetType(value string) bool {
	_, err := componentType(value)
	return err == nil
}

func assetSupportsTarget(value asset, target string) bool {
	if value.Path != "" {
		return true
	}
	for _, variant := range value.Variants {
		if slices.Contains(variant.Targets, target) {
			return true
		}
	}
	return false
}

func assetPathsForTarget(value asset, target string) []string {
	if value.Path != "" {
		return []string{value.Path}
	}
	var paths []string
	for _, variant := range value.Variants {
		if slices.Contains(variant.Targets, target) {
			paths = append(paths, variant.Path)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

func packagePathsOverlap(left, right string) bool {
	return pathWithinPackage(left, right) || pathWithinPackage(right, left)
}

func pathWithinPackage(candidate, root string) bool {
	return candidate == root || strings.HasPrefix(candidate, strings.TrimSuffix(root, "/")+"/")
}

func assetFiles(path string, tracked map[string]gitsource.TreeEntry) []string {
	if _, ok := tracked[path]; ok {
		return []string{path}
	}
	return filesUnder(tracked, path)
}

func validAssetPath(path string, tracked map[string]gitsource.TreeEntry) bool {
	if !safeRelative(path) {
		return false
	}
	_, file := tracked[path]
	return file || len(filesUnder(tracked, path)) != 0
}

func validManifestID(value string) bool {
	if len(value) < 2 || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
			return false
		}
	}
	return true
}

func boundedValue(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func validTargetName(value string) bool { return value == "claude" || value == "codex" }
func validTargetNames(values []string) bool {
	for _, value := range values {
		if !validTargetName(value) {
			return false
		}
	}
	return !hasDuplicate(values)
}
func validHostNames(values []string) bool {
	for _, value := range values {
		if value != "darwin-arm64" && value != "windows-amd64" {
			return false
		}
	}
	return !hasDuplicate(values)
}
func hasDuplicate(values []string) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func cycleInAssets(assets map[string]asset) bool {
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(id string) bool {
		if state[id] == 1 {
			return true
		}
		if state[id] == 2 {
			return false
		}
		state[id] = 1
		for _, dependency := range assets[id].Dependencies {
			if visit(dependency) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for id := range assets {
		if visit(id) {
			return true
		}
	}
	return false
}

func cycleInBundles(bundles map[string]bundle) bool {
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(id string) bool {
		if state[id] == 1 {
			return true
		}
		if state[id] == 2 {
			return false
		}
		state[id] = 1
		for _, nested := range bundles[id].Bundles {
			if visit(nested) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for id := range bundles {
		if visit(id) {
			return true
		}
	}
	return false
}
