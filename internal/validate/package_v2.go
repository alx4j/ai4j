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

type validatedManifestV2 struct {
	manifest toolkitManifestV2
	assets   map[string]assetV2
	bundles  map[string]bundleV2
	tracked  map[string]gitsource.TreeEntry
}

func validatePackage(root string, inventory gitsource.TreeInventory) (packageResult, error) {
	tracked := trackedFiles(inventory)
	content, err := readTrackedFile(root, toolkitManifestPath, tracked)
	if err != nil {
		return packageResult{}, validationError("invalid_toolkit_manifest", "toolkit.json is missing or invalid")
	}
	var header struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(content, &header); err != nil {
		return packageResult{}, validationError("invalid_toolkit_manifest", "toolkit.json is missing or invalid")
	}
	switch header.SchemaVersion {
	case 1:
		return validatePackageMVP(root, inventory)
	case 2:
		return validatePackageV2(root, inventory, tracked)
	default:
		return packageResult{}, validationError("unsupported_schema", "toolkit schema version is not supported")
	}
}

func validatePackageV2(root string, inventory gitsource.TreeInventory, tracked map[string]gitsource.TreeEntry) (packageResult, error) {
	var manifest toolkitManifestV2
	if err := readStrictJSON(root, toolkitManifestPath, tracked, &manifest); err != nil {
		return packageResult{}, validationError("invalid_toolkit_manifest", "toolkit.json does not satisfy the v1 manifest schema")
	}
	model, err := validateManifestV2(manifest, tracked)
	if err != nil {
		return packageResult{}, err
	}
	if err := validateMCPAssetsV2(root, model); err != nil {
		return packageResult{}, err
	}
	content, err := contentForV2(root, model)
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
	modelCopy := model
	var nativePackagePaths []string
	for _, unit := range manifest.Targets["claude"].Packages {
		nativePackagePaths = append(nativePackagePaths, unit.Path)
	}
	slices.Sort(nativePackagePaths)
	nativePackagePaths = slices.Compact(nativePackagePaths)
	return packageResult{
		content: content, rules: rules, rulesChecksum: rulesChecksum, digest: digest, schemaVersion: 2,
		nativePackagePaths: nativePackagePaths, v2: &modelCopy,
	}, nil
}

func validateMCPAssetsV2(root string, model validatedManifestV2) error {
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

func validateManifestV2(manifest toolkitManifestV2, tracked map[string]gitsource.TreeEntry) (validatedManifestV2, error) {
	if manifest.SchemaVersion != 2 || !validV1ID(manifest.Toolkit.ID) || !boundedValue(manifest.Toolkit.Version, 64) ||
		!boundedValue(manifest.Toolkit.DisplayName, 128) || !boundedValue(manifest.Toolkit.Compatibility.MinimumCLI, 64) ||
		(manifest.Toolkit.DeclarationID != "" && !validV1ID(manifest.Toolkit.DeclarationID)) || len(manifest.Assets) > 4096 || len(manifest.Bundles) > 1024 || len(manifest.Targets) == 0 || len(manifest.Targets) > 2 {
		return validatedManifestV2{}, validationError("invalid_toolkit_manifest", "toolkit metadata is incomplete or exceeds v1 bounds")
	}
	model := validatedManifestV2{manifest: manifest, assets: make(map[string]assetV2, len(manifest.Assets)), bundles: make(map[string]bundleV2, len(manifest.Bundles)), tracked: tracked}
	for _, asset := range manifest.Assets {
		if !validV1ID(asset.ID) || !validAssetTypeV2(asset.Type) || (asset.Ownership != "package" && asset.Ownership != "configuration") || len(asset.Dependencies) > 256 || len(asset.Variants) > 16 {
			return validatedManifestV2{}, validationError("invalid_asset", "asset declaration is invalid")
		}
		if _, duplicate := model.assets[asset.ID]; duplicate {
			return validatedManifestV2{}, validationError("duplicate_asset", "asset identifiers must be unique")
		}
		if (asset.Path == "") == (len(asset.Variants) == 0) {
			return validatedManifestV2{}, validationError("invalid_asset", "asset must declare one path or one or more variants")
		}
		if asset.Path != "" && !validAssetPath(asset.Path, tracked) {
			return validatedManifestV2{}, validationError("invalid_asset_path", "asset path is missing or unsafe")
		}
		variantIDs := map[string]struct{}{}
		for _, variant := range asset.Variants {
			if !validV1ID(variant.ID) || !validAssetPath(variant.Path, tracked) || len(variant.Targets) == 0 || len(variant.Hosts) == 0 || !validTargetNames(variant.Targets) || !validHostNames(variant.Hosts) {
				return validatedManifestV2{}, validationError("invalid_variant", "asset variant is invalid")
			}
			if _, duplicate := variantIDs[variant.ID]; duplicate {
				return validatedManifestV2{}, validationError("ambiguous_variant", "asset variant identifiers must be unique")
			}
			variantIDs[variant.ID] = struct{}{}
		}
		if err := validateExecutableV2(asset); err != nil {
			return validatedManifestV2{}, err
		}
		if err := validateOverlaysV2(asset.Overlays); err != nil {
			return validatedManifestV2{}, err
		}
		model.assets[asset.ID] = asset
	}
	for _, asset := range manifest.Assets {
		seen := map[string]struct{}{}
		for _, dependency := range asset.Dependencies {
			if _, ok := model.assets[dependency]; !ok {
				return validatedManifestV2{}, validationError("missing_dependency", "asset dependency does not exist")
			}
			if _, duplicate := seen[dependency]; duplicate {
				return validatedManifestV2{}, validationError("duplicate_dependency", "asset dependency is duplicated")
			}
			seen[dependency] = struct{}{}
		}
	}
	if cycleInAssets(model.assets) {
		return validatedManifestV2{}, validationError("dependency_cycle", "asset dependency graph contains a cycle")
	}
	for _, bundle := range manifest.Bundles {
		if !validV1ID(bundle.ID) || len(bundle.Assets)+len(bundle.Bundles) == 0 || len(bundle.Assets) > 4096 || len(bundle.Bundles) > 1024 {
			return validatedManifestV2{}, validationError("invalid_bundle", "bundle declaration is invalid")
		}
		if _, duplicate := model.bundles[bundle.ID]; duplicate {
			return validatedManifestV2{}, validationError("duplicate_bundle", "bundle identifiers must be unique")
		}
		for _, asset := range bundle.Assets {
			if _, ok := model.assets[asset]; !ok {
				return validatedManifestV2{}, validationError("missing_bundle_asset", "bundle asset does not exist")
			}
		}
		model.bundles[bundle.ID] = bundle
	}
	for _, bundle := range manifest.Bundles {
		for _, nested := range bundle.Bundles {
			if _, ok := model.bundles[nested]; !ok {
				return validatedManifestV2{}, validationError("missing_bundle", "nested bundle does not exist")
			}
		}
	}
	if cycleInBundles(model.bundles) {
		return validatedManifestV2{}, validationError("bundle_cycle", "bundle graph contains a cycle")
	}
	for targetName, target := range manifest.Targets {
		if !validTargetName(targetName) || len(target.Packages) == 0 || len(target.Packages) > 256 {
			return validatedManifestV2{}, validationError("invalid_target", "target package declaration is invalid")
		}
		packageIDs := map[string]struct{}{}
		for _, unit := range target.Packages {
			if !validV1ID(unit.ID) || !safeRelative(unit.Path) || len(filesUnder(tracked, unit.Path)) == 0 || len(unit.Assets) > 4096 {
				return validatedManifestV2{}, validationError("invalid_native_package", "target native package is invalid")
			}
			if _, duplicate := packageIDs[unit.ID]; duplicate {
				return validatedManifestV2{}, validationError("duplicate_native_package", "target native package identifiers must be unique")
			}
			packageIDs[unit.ID] = struct{}{}
			for _, asset := range unit.Assets {
				if _, ok := model.assets[asset]; !ok {
					return validatedManifestV2{}, validationError("missing_native_asset", "target native package references an unknown asset")
				}
			}
		}
	}
	return model, nil
}

func contentForV2(root string, model validatedManifestV2) ([]cli.ContentItem, error) {
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
			variants := append([]assetVariantV2(nil), asset.Variants...)
			slices.SortFunc(variants, func(left, right assetVariantV2) int { return strings.Compare(left.ID, right.ID) })
			sourcePath = variants[0].Path
			for _, variant := range variants {
				paths = append(paths, assetFiles(variant.Path, model.tracked)...)
			}
		}
		checksum, err := digestFiles(root, paths, model.tracked)
		if err != nil {
			return nil, validationError("package_read_failed", "asset content could not be read")
		}
		component, err := componentTypeV2(asset.Type)
		if err != nil {
			return nil, err
		}
		var execution *cli.Execution
		declaration := asset.Executable
		if declaration == nil && len(asset.Variants) != 0 {
			variants := append([]assetVariantV2(nil), asset.Variants...)
			slices.SortFunc(variants, func(left, right assetVariantV2) int { return strings.Compare(left.ID, right.ID) })
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

func validateExecutableV2(asset assetV2) error {
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
		return validateExecutableDeclarationV2(asset.Executable)
	}
	if asset.Executable != nil || len(asset.Variants) == 0 {
		return validationError("invalid_executable", "executable asset declaration is incomplete")
	}
	for _, variant := range asset.Variants {
		if variant.Executable == nil {
			return validationError("invalid_executable", "executable asset variant declaration is incomplete")
		}
		if err := validateExecutableDeclarationV2(variant.Executable); err != nil {
			return err
		}
	}
	return nil
}

func validateExecutableDeclarationV2(value *executableV2) error {
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

func validateOverlaysV2(overlays map[string]targetOverlayV2) error {
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

func componentTypeV2(value string) (cli.ComponentType, error) {
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

func validAssetTypeV2(value string) bool {
	_, err := componentTypeV2(value)
	return err == nil
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

func validV1ID(value string) bool {
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

func cycleInAssets(assets map[string]assetV2) bool {
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

func cycleInBundles(bundles map[string]bundleV2) bool {
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
