package app

import (
	"slices"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func TestCombineCompositionRejectsMultipleRulesOutputs(t *testing.T) {
	t.Parallel()
	common := compositionSelectionFixture(t, "common", "v1", []byte("common rules"))
	company := compositionSelectionFixture(t, "company", "v2", []byte("company rules\n"))

	report := combineComposition([]selectedCompositionComponent{company, common})

	if len(report.Problems) != 1 || report.Problems[0].Code() != "composition_collision" || len(report.Rules) != 0 {
		t.Fatalf("composition result = %#v", report)
	}
}

func TestCombineCompositionRejectsMultipleAgentActivations(t *testing.T) {
	t.Parallel()
	common := compositionSelectionFixture(t, "common", "v1", nil)
	company := compositionSelectionFixture(t, "company", "v2", nil)
	common.report.AgentActivation = true
	company.report.AgentActivation = true

	report := combineComposition([]selectedCompositionComponent{company, common})

	if len(report.Problems) != 1 || report.Problems[0].Code() != "composition_collision" {
		t.Fatalf("composition result = %#v", report)
	}
}

func compositionSelectionFixture(t *testing.T, name, tag string, rules []byte) selectedCompositionComponent {
	t.Helper()
	options, err := cli.NewSourceOptions("https://github.com/example/"+name+".git", true, "refs/tags/"+tag, true)
	if err != nil {
		t.Fatal(err)
	}
	coordinate, err := cli.NewBundleCoordinate(name, tag)
	if err != nil {
		t.Fatal(err)
	}
	packageID := name + "-plugin"
	return selectedCompositionComponent{
		coordinate: coordinate,
		report: validation.LifecycleSelection{
			Source:           testPlanSourceFrom(t, options, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			ToolkitID:        name,
			DeclarationID:    name,
			ToolkitVersion:   "1.0.0",
			RequestedBundle:  name,
			ResolvedBundles:  []string{name},
			ResolvedPackages: []string{packageID},
			ResolvedAssets:   []string{name + "-rules"},
			Packages:         []validation.LifecyclePackage{{ID: packageID, Path: "plugins/" + packageID, NativeArtifact: testLifecycleArtifact()}},
			Rules:            slices.Clone(rules),
			RulesChecksum:    sha256Digest(rules),
		},
	}
}
