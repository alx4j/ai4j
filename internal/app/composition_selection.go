package app

import (
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/result"
	validation "github.com/alx4j/ai4j/internal/validate"
)

type selectedCompositionComponent struct {
	coordinate cli.BundleCoordinate
	report     validation.LifecycleSelection
}

func combineComposition(components []selectedCompositionComponent) validation.LifecycleSelection {
	if len(components) < 2 || len(components) > 3 {
		return compositionFailure("invalid_composition", "a composition requires two or three bundle coordinates")
	}
	items := append([]selectedCompositionComponent(nil), components...)
	slices.SortFunc(items, func(left, right selectedCompositionComponent) int {
		return strings.Compare(left.coordinate.Name(), right.coordinate.Name())
	})
	combined := validation.LifecycleSelection{
		Source:          items[0].report.Source,
		ToolkitID:       "composition",
		DeclarationID:   "composition",
		ToolkitVersion:  "composed",
		RequestedBundle: "composition",
		ResolvedBundles: []string{"composition"},
	}
	packageOwners := make(map[string]string)
	assetOwners := make(map[string]string)
	totalArtifactBytes := 0
	for index, item := range items {
		name, tag, report := item.coordinate.Name(), item.coordinate.Tag(), item.report
		if index > 0 && items[index-1].coordinate.Name() == name {
			return compositionFailure("duplicate_component", "composition bundle names must be unique")
		}
		if !report.HasSource() || len(report.Problems) != 0 {
			return report
		}
		if report.ToolkitID != name || report.RequestedBundle != name {
			return compositionFailure("component_identity_mismatch", "each composition repository must use its bundle name as both toolkit and top-level bundle identifier")
		}
		if report.Source.ResolvedRefKind() != cli.RefTag || !report.Source.HasRequestedRef() || report.Source.RequestedRef() != "refs/tags/"+tag {
			return compositionFailure("component_tag_mismatch", "each composition component must resolve the requested Git tag")
		}
		componentPackages := make([]string, len(report.Packages))
		for packageIndex, pkg := range report.Packages {
			if owner, duplicate := packageOwners[pkg.ID]; duplicate {
				return compositionFailure("composition_collision", "native package "+pkg.ID+" is selected by both "+owner+" and "+name)
			}
			packageOwners[pkg.ID] = name
			componentPackages[packageIndex] = pkg.ID
			totalArtifactBytes += len(pkg.NativeArtifact)
			pkg.Component = name
			pkg.Source = report.Source
			combined.Packages = append(combined.Packages, pkg)
		}
		if totalArtifactBytes > 16<<20 {
			return compositionFailure("native_artifact_failed", "composed native packages exceed the retained rollback limit")
		}
		for _, asset := range report.ResolvedAssets {
			if owner, duplicate := assetOwners[asset]; duplicate {
				return compositionFailure("composition_collision", "asset "+asset+" is selected by both "+owner+" and "+name)
			}
			assetOwners[asset] = name
			combined.ResolvedAssets = append(combined.ResolvedAssets, asset)
		}
		if len(report.Rules) != 0 {
			if len(combined.Rules) != 0 {
				return compositionFailure("composition_collision", "multiple components select the same managed rules output")
			}
			combined.Rules = slices.Clone(report.Rules)
			combined.RulesChecksum = report.RulesChecksum
		}
		combined.Components = append(combined.Components, validation.LifecycleComponent{
			Name: name, Tag: tag, Source: report.Source, ToolkitVersion: report.ToolkitVersion,
			RequestedBundle: report.RequestedBundle, ResolvedBundles: slices.Clone(report.ResolvedBundles),
			ResolvedPackages: componentPackages, ResolvedAssets: slices.Clone(report.ResolvedAssets),
		})
		combined.ResolvedPackages = append(combined.ResolvedPackages, componentPackages...)
		combined.Content = append(combined.Content, report.Content...)
		combined.Warnings = append(combined.Warnings, report.Warnings...)
	}
	slices.SortFunc(combined.Packages, func(left, right validation.LifecyclePackage) int {
		return strings.Compare(left.ID, right.ID)
	})
	slices.Sort(combined.ResolvedPackages)
	slices.Sort(combined.ResolvedAssets)
	return combined
}

func compositionFailure(code, message string) validation.LifecycleSelection {
	problem, _ := result.NewProblem(code, message, nil)
	return validation.LifecycleSelection{Problems: []result.Problem{problem}, Failure: validation.FailureConflict}
}

func compositionFailureFor(component, code, message string) validation.LifecycleSelection {
	problem, _ := result.NewProblem(code, message, compositionContext(component))
	return validation.LifecycleSelection{Problems: []result.Problem{problem}, Failure: validation.FailureSource}
}

func annotateCompositionSelection(report validation.LifecycleSelection, component string) validation.LifecycleSelection {
	for index, problem := range report.Problems {
		report.Problems[index] = annotateCompositionProblem(problem, component)
	}
	return report
}

func annotateCompositionProblem(problem result.Problem, component string) result.Problem {
	context := append(problem.Context(), compositionContext(component)...)
	if len(context) > 16 {
		context = compositionContext(component)
	}
	annotated, _ := result.NewProblem(problem.Code(), problem.Message(), context)
	return annotated
}

func compositionContext(component string) []result.Context {
	context, _ := result.NewContext("component", component)
	return []result.Context{context}
}
