package cli

import (
	"fmt"
	"slices"
	"strings"
)

type PlanComponent struct {
	name   string
	tag    string
	source Source
}

func NewPlanComponent(name, tag string, source Source) (PlanComponent, error) {
	if !selectionIdentifier(name) || !bundleTag(tag) || !source.Valid() || source.Mode() != SourceGit ||
		!source.HasRequestedRef() || source.RequestedRef() != "refs/tags/"+tag || source.ResolvedRefKind() != RefTag {
		return PlanComponent{}, fmt.Errorf("plan component is incomplete")
	}
	return PlanComponent{name: name, tag: tag, source: source}, nil
}

func (c PlanComponent) Name() string   { return c.name }
func (c PlanComponent) Tag() string    { return c.tag }
func (c PlanComponent) Source() Source { return c.source }

func validPlanComponents(values []PlanComponent) bool {
	if len(values) < 2 || len(values) > 3 {
		return false
	}
	for index, component := range values {
		if _, err := NewPlanComponent(component.name, component.tag, component.source); err != nil || index > 0 && values[index-1].name >= component.name {
			return false
		}
	}
	return true
}

func sortedPlanComponents(values []PlanComponent) []PlanComponent {
	result := append([]PlanComponent(nil), values...)
	slices.SortFunc(result, func(left, right PlanComponent) int { return strings.Compare(left.name, right.name) })
	return result
}

type RecordedComponent struct {
	name            string
	tag             string
	source          RecordedSource
	toolkitVersion  string
	resolvedBundles []string
	packages        []string
	resolvedAssets  []string
}

func NewRecordedComponent(name, tag string, source RecordedSource, toolkitVersion string, resolvedBundles, packages, resolvedAssets []string) (RecordedComponent, error) {
	value := RecordedComponent{
		name: name, tag: tag, source: source, toolkitVersion: toolkitVersion,
		resolvedBundles: uniqueSortedStrings(resolvedBundles), packages: uniqueSortedStrings(packages), resolvedAssets: uniqueSortedStrings(resolvedAssets),
	}
	if !value.valid() {
		return RecordedComponent{}, fmt.Errorf("recorded composition component is incomplete")
	}
	return value, nil
}

func (c RecordedComponent) Name() string              { return c.name }
func (c RecordedComponent) Tag() string               { return c.tag }
func (c RecordedComponent) Source() RecordedSource    { return c.source }
func (c RecordedComponent) ToolkitVersion() string    { return c.toolkitVersion }
func (c RecordedComponent) ResolvedBundles() []string { return cloneStrings(c.resolvedBundles) }
func (c RecordedComponent) Packages() []string        { return cloneStrings(c.packages) }
func (c RecordedComponent) ResolvedAssets() []string  { return cloneStrings(c.resolvedAssets) }
func (c RecordedComponent) valid() bool {
	if !selectionIdentifier(c.name) || !bundleTag(c.tag) || !c.source.valid() || c.source.Mode() != SourceGit ||
		!c.source.HasRequestedRef() || c.source.RequestedRef() != "refs/tags/"+c.tag || c.source.RefKind() != RefTag ||
		!boundedText(c.toolkitVersion, 64, false) || len(c.resolvedBundles) == 0 || !containsString(c.resolvedBundles, c.name) || len(c.packages) == 0 && len(c.resolvedAssets) == 0 {
		return false
	}
	return validSelectionIdentifiers(c.resolvedBundles) && validSelectionIdentifiers(c.packages) && validSelectionIdentifiers(c.resolvedAssets)
}

func validSelectionIdentifiers(values []string) bool {
	for index, value := range values {
		if !selectionIdentifier(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func cloneRecordedComponents(values []RecordedComponent) []RecordedComponent {
	result := append([]RecordedComponent(nil), values...)
	for index := range result {
		result[index].resolvedBundles = cloneStrings(result[index].resolvedBundles)
		result[index].packages = cloneStrings(result[index].packages)
		result[index].resolvedAssets = cloneStrings(result[index].resolvedAssets)
	}
	return result
}

func validRecordedComponents(values []RecordedComponent) bool {
	if len(values) < 2 || len(values) > 3 {
		return false
	}
	for index, component := range values {
		if !component.valid() || index > 0 && values[index-1].name >= component.name {
			return false
		}
	}
	return true
}
