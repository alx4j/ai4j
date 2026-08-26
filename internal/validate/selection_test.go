package validate

import (
	"slices"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
)

func TestResolveCanonicalSelectionFlattensAndDeduplicatesNestedBundles(t *testing.T) {
	model := selectionFixtureModel()

	resolved, err := resolveCanonicalSelection(model, selection{
		target:  cli.BuildTargetClaude,
		host:    cli.BuildHostDarwinARM64,
		bundles: []string{"root"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(resolved.bundles, []string{"left", "right", "root", "shared"}) {
		t.Fatalf("resolved bundles = %v", resolved.bundles)
	}
	if !slices.Equal(packageIDs(resolved.packages), []string{"alpha-package", "zeta-package"}) {
		t.Fatalf("resolved packages = %v", packageIDs(resolved.packages))
	}
	if !slices.Equal(resolvedAssetIDs(resolved.assets), []string{"alpha-asset", "root-rules", "shared-rules", "zeta-asset"}) {
		t.Fatalf("resolved assets = %v", resolvedAssetIDs(resolved.assets))
	}
}

func TestResolveCanonicalSelectionExpandsOnlyTheOwningNativePackage(t *testing.T) {
	model := selectionFixtureModel()

	resolved, err := resolveCanonicalSelection(model, selection{
		target: cli.BuildTargetClaude,
		host:   cli.BuildHostDarwinARM64,
		assets: []string{"alpha-asset"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(resolved.bundles) != 0 || !slices.Equal(packageIDs(resolved.packages), []string{"alpha-package"}) ||
		!slices.Equal(resolvedAssetIDs(resolved.assets), []string{"alpha-asset"}) {
		t.Fatalf("resolved selection = bundles:%v packages:%v assets:%v", resolved.bundles, packageIDs(resolved.packages), resolvedAssetIDs(resolved.assets))
	}
}

func TestExpandBundleRejectsCycleAtResolutionBoundary(t *testing.T) {
	bundles := map[string]bundle{
		"left":  {ID: "left", Bundles: []string{"right"}},
		"right": {ID: "right", Bundles: []string{"left"}},
	}

	_, err := expandBundle(bundles, "left")
	code, _ := packageProblem(err)
	if code != "bundle_cycle" {
		t.Fatalf("error = %v", err)
	}
}

func selectionFixtureModel() validatedManifest {
	assets := map[string]asset{
		"alpha-asset":  {ID: "alpha-asset", Type: "skill", Path: "packages/alpha/skill", Ownership: "package"},
		"zeta-asset":   {ID: "zeta-asset", Type: "skill", Path: "packages/zeta/skill", Ownership: "package"},
		"root-rules":   {ID: "root-rules", Type: "instruction", Path: "config/root.md", Ownership: "configuration"},
		"shared-rules": {ID: "shared-rules", Type: "instruction", Path: "config/shared.md", Ownership: "configuration"},
	}
	packages := map[string]nativePackage{
		"alpha-package": {ID: "alpha-package", Path: "packages/alpha", Assets: []string{"alpha-asset"}},
		"zeta-package":  {ID: "zeta-package", Path: "packages/zeta", Assets: []string{"zeta-asset"}},
	}
	return validatedManifest{
		assets: assets,
		bundles: map[string]bundle{
			"root":   {ID: "root", Assets: []string{"root-rules"}, Bundles: []string{"right", "left"}},
			"left":   {ID: "left", Packages: []string{"alpha-package"}, Bundles: []string{"shared"}},
			"right":  {ID: "right", Packages: []string{"zeta-package"}, Bundles: []string{"shared"}},
			"shared": {ID: "shared", Assets: []string{"shared-rules"}, Packages: []string{"alpha-package"}},
		},
		packages: map[string]map[string]nativePackage{"claude": packages},
		packageOwners: map[string]map[string]string{
			"claude": {"alpha-asset": "alpha-package", "zeta-asset": "zeta-package"},
		},
	}
}

func packageIDs(packages []nativePackage) []string {
	ids := make([]string, len(packages))
	for index, unit := range packages {
		ids[index] = unit.ID
	}
	return ids
}

func resolvedAssetIDs(assets []resolvedAsset) []string {
	ids := make([]string, len(assets))
	for index, selected := range assets {
		ids[index] = selected.asset.ID
	}
	return ids
}
