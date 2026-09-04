package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	"github.com/alx4j/ai4j/internal/target/claude/catalog"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func (s *lifecycleService) recordForSelection(report validation.LifecycleSelection, selection cli.BundleSelection, installationID domain.InstallationID, scope cli.Scope, scopeRoot string) (installstate.Record, catalog.Document, error) {
	if len(report.Components) != 0 {
		return s.recordForComposition(report, installationID, scope, scopeRoot)
	}
	marketplaceID := marketplaceIDFor(installationID)
	if scope == cli.ScopeProjectShared {
		marketplaceID = report.DeclarationID
		if marketplaceID == "" {
			marketplaceID = report.ToolkitID
		}
	}
	packages := make([]installstate.NativePackage, len(report.Packages))
	catalogPackages := make([]catalog.Package, len(report.Packages))
	for index, pkg := range report.Packages {
		packages[index] = installstate.NativePackage{ID: pkg.ID, Path: pkg.Path}
		packagePath := pkg.Path
		if report.Source.Mode() == cli.SourceDevelopment {
			packagePath = "plugins/" + pkg.ID
		}
		catalogPackages[index] = catalog.Package{ID: pkg.ID, Path: packagePath, Description: "AI4J toolkit package " + pkg.ID}
		if report.Source.Mode() != cli.SourceDevelopment {
			catalogPackages[index].Repository = report.Source.Repository()
			catalogPackages[index].Transport = report.Source.Transport()
			catalogPackages[index].Commit = report.Source.Commit().OID()
		}
	}
	var document catalog.Document
	var err error
	if report.Source.Mode() == cli.SourceDevelopment {
		document, err = catalog.RenderLocalPackages(marketplaceID, catalogPackages)
	} else {
		document, err = catalog.RenderPackages(marketplaceID, catalogPackages)
	}
	if err != nil {
		return installstate.Record{}, catalog.Document{}, err
	}
	var requested *string
	if report.Source.HasRequestedRef() {
		value := report.Source.RequestedRef()
		requested = &value
	}
	stateSource := installstate.Source{Mode: "git", Selection: report.Source.Selection().String(), Repository: report.Source.Repository().String(), Transport: report.Source.Transport().String(), RequestedRef: requested, RefKind: report.Source.ResolvedRefKind().String(), Commit: report.Source.Commit().OID().String(), RenderedDigest: report.Source.RenderedDigest().String()}
	catalogPath := "state/catalogs/" + installationID.String() + "/.claude-plugin/marketplace.json"
	if report.Source.Mode() == cli.SourceDevelopment {
		stateSource = installstate.Source{Mode: "development_source", Selection: domain.ExplicitSource().String(), Checkout: report.Source.Checkout(), SourceDigest: report.Source.SourceDigest().String(), RenderedDigest: report.Source.RenderedDigest().String(), Dirty: report.Source.Dirty()}
		stateSource.BundleDigest = localBundleDigest(stateSource, selection, report, document.Digest())
		catalogPath = "state/bundles/" + stateSource.BundleDigest + "/.claude-plugin/marketplace.json"
	}
	record := installstate.Record{
		SchemaVersion: installstate.SchemaVersion, InstallationID: installationID.String(), ToolkitID: report.ToolkitID, DeclarationID: report.DeclarationID, ToolkitVersion: report.ToolkitVersion,
		AgentActivation: report.AgentActivation, Packages: packages, MarketplaceID: marketplaceID,
		Source: stateSource,
		Target: "claude", Host: s.host(), Scope: string(scope), ScopeRoot: scopeRoot, Lifecycle: "active",
		Selection:       installstate.Selection{RequestedBundle: selection.Bundle(), ResolvedBundles: slices.Clone(report.ResolvedBundles), ResolvedAssets: slices.Clone(report.ResolvedAssets)},
		NativeResources: nativeResources(packages, marketplaceID),
		Health:          "healthy", AI4JVersion: s.build.Version(),
		Catalog:       installstate.OwnedFile{Path: catalogPath, Checksum: document.Digest()},
		LastOperation: installstate.LastOperation{ID: "operation-pending", Timestamp: time.Unix(0, 0).UTC().Format(time.RFC3339)},
	}
	err = finalizeLifecycleRecord(&record, report, installationID, scope)
	return record, document, err
}

func (s *lifecycleService) recordForComposition(report validation.LifecycleSelection, installationID domain.InstallationID, scope cli.Scope, scopeRoot string) (installstate.Record, catalog.Document, error) {
	marketplaceID := marketplaceIDFor(installationID)
	if scope == cli.ScopeProjectShared {
		marketplaceID = report.DeclarationID
	}
	packages := make([]installstate.NativePackage, len(report.Packages))
	catalogPackages := make([]catalog.Package, len(report.Packages))
	for index, pkg := range report.Packages {
		if !pkg.Source.Valid() || pkg.Component == "" {
			return installstate.Record{}, catalog.Document{}, errors.New("composition package provenance is incomplete")
		}
		packages[index] = installstate.NativePackage{ID: pkg.ID, Path: pkg.Path, Component: pkg.Component}
		catalogPackages[index] = catalog.Package{
			ID: pkg.ID, Path: pkg.Path, Description: "AI4J toolkit package " + pkg.ID,
			Repository: pkg.Source.Repository(), Transport: pkg.Source.Transport(), Commit: pkg.Source.Commit().OID(),
		}
	}
	document, err := catalog.RenderPackages(marketplaceID, catalogPackages)
	if err != nil {
		return installstate.Record{}, catalog.Document{}, err
	}
	components := make([]installstate.Component, len(report.Components))
	for index, component := range report.Components {
		source, sourceErr := stateSourceFromCLI(component.Source)
		if sourceErr != nil {
			return installstate.Record{}, catalog.Document{}, sourceErr
		}
		components[index] = installstate.Component{
			Name: component.Name, Tag: component.Tag, Source: source, ToolkitVersion: component.ToolkitVersion,
			Selection: installstate.Selection{RequestedBundle: component.RequestedBundle, ResolvedBundles: slices.Clone(component.ResolvedBundles), ResolvedAssets: slices.Clone(component.ResolvedAssets)},
			Packages:  slices.Clone(component.ResolvedPackages),
		}
	}
	record := installstate.Record{
		SchemaVersion: installstate.SchemaVersion, InstallationID: installationID.String(), ToolkitID: "composition", DeclarationID: marketplaceID, ToolkitVersion: "composed",
		AgentActivation: report.AgentActivation, Components: components, Packages: packages, MarketplaceID: marketplaceID,
		Target: "claude", Host: s.host(), Scope: string(scope), ScopeRoot: scopeRoot, Lifecycle: "active",
		Selection:       installstate.Selection{RequestedBundle: "composition", ResolvedBundles: []string{"composition"}, ResolvedAssets: slices.Clone(report.ResolvedAssets)},
		NativeResources: nativeResources(packages, marketplaceID),
		Health:          "healthy", AI4JVersion: s.build.Version(),
		Catalog:       installstate.OwnedFile{Path: "state/catalogs/" + installationID.String() + "/.claude-plugin/marketplace.json", Checksum: document.Digest()},
		LastOperation: installstate.LastOperation{ID: "operation-pending", Timestamp: time.Unix(0, 0).UTC().Format(time.RFC3339)},
	}
	err = finalizeLifecycleRecord(&record, report, installationID, scope)
	return record, document, err
}

func finalizeLifecycleRecord(record *installstate.Record, report validation.LifecycleSelection, installationID domain.InstallationID, scope cli.Scope) error {
	if len(report.Rules) != 0 {
		rulesID := "ai4j-" + installationID.String()
		rulesRoot := "rules/"
		if scope != cli.ScopeUser {
			rulesRoot = ".claude/rules/"
		}
		if scope == cli.ScopeProjectShared {
			rulesID = record.MarketplaceID
		}
		record.Rules = installstate.OwnedFile{Path: rulesRoot + rulesID + ".md", Checksum: report.RulesChecksum}
	}
	if scope == cli.ScopeProjectShared {
		record.NativeCatalog = record.Catalog
	}
	slices.Sort(record.NativeResources)
	return record.Validate()
}

func stateSourceFromCLI(source cli.Source) (installstate.Source, error) {
	if !source.Valid() || source.Mode() != cli.SourceGit || !source.HasRequestedRef() {
		return installstate.Source{}, errors.New("composition source provenance is incomplete")
	}
	requested := source.RequestedRef()
	return installstate.Source{
		Mode: "git", Selection: source.Selection().String(), Repository: source.Repository().String(), Transport: source.Transport().String(),
		RequestedRef: &requested, RefKind: source.ResolvedRefKind().String(), Commit: source.Commit().OID().String(), RenderedDigest: source.RenderedDigest().String(),
	}, nil
}

func localBundleDigest(source installstate.Source, selection cli.BundleSelection, report validation.LifecycleSelection, catalogDigest string) string {
	parts := []string{"claude-local-bundle", source.Checkout, source.SourceDigest, source.RenderedDigest, report.ToolkitID, selection.Bundle(), catalogDigest, strings.Join(report.ResolvedBundles, ","), strings.Join(report.ResolvedPackages, ","), strings.Join(report.ResolvedAssets, ",")}
	for _, pkg := range report.Packages {
		artifact := sha256.Sum256(pkg.NativeArtifact)
		parts = append(parts, pkg.ID, pkg.Path, hex.EncodeToString(artifact[:]))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func nativeResources(packages []installstate.NativePackage, marketplaceID string) []string {
	resources := make([]string, 0, len(packages)+1)
	for _, pkg := range packages {
		resources = append(resources, "claude:"+pkg.ID+"@"+marketplaceID)
	}
	resources = append(resources, "claude:marketplace:"+marketplaceID)
	slices.Sort(resources)
	return resources
}

func (s *lifecycleService) planResponse(command cli.Command, execution lifecycleExecution) (cli.Response, error) {
	installation := recordInstallation(execution.transition.before, execution.transition.desired)
	if installation == nil {
		return cli.Response{}, errors.New("planned installation identity is unavailable")
	}
	var data cli.PlanData
	var err error
	if len(execution.source.Components) != 0 && execution.source.HasSource() {
		components := make([]cli.PlanComponent, len(execution.source.Components))
		for index, component := range execution.source.Components {
			components[index], err = cli.NewPlanComponent(component.Name, component.Tag, component.Source)
			if err != nil {
				return cli.Response{}, err
			}
		}
		data, err = cli.NewCompositionPlanData(execution.operation, components, *installation, execution.actions, execution.content, execution.conflicts, execution.final, execution.disposition)
	} else if execution.source.HasSource() {
		data, err = cli.NewPlanData(execution.operation, execution.source.Source, *installation, execution.actions, execution.content, execution.conflicts, execution.final, execution.disposition)
	} else {
		data, err = cli.NewOfflinePlanData(execution.operation, *installation, execution.actions, execution.conflicts, execution.final)
	}
	if err != nil {
		return cli.Response{}, err
	}
	status := result.StatusOK
	failure := result.FailureNone
	var problems []result.Problem
	warnings := slices.Clone(execution.source.Warnings)
	if len(execution.conflicts) != 0 {
		status = result.StatusError
		failure = result.FailureConflict
		problems = conflictProblems(execution.conflicts)
	} else if len(execution.actions) == 0 {
		status = result.StatusNoChange
	} else if len(execution.degradedConflicts) != 0 {
		status = result.StatusDegraded
		warnings = append(warnings, conflictWarnings(execution.degradedConflicts)...)
	}
	commandResult, err := result.New(result.Facts{Status: status, Phase: result.PhaseNone, Outcome: result.OutcomeNone, Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone, Failure: failure, UpdateDisposition: execution.disposition, Warnings: warnings, Errors: problems})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, data)
}

func (s *lifecycleService) transitionActions(operation cli.Operation, before, desired *installstate.Record, hasRules bool) ([]cli.Action, error) {
	present, err := cli.NewCondition(cli.ConditionPresent, "")
	if err != nil {
		return nil, err
	}
	absent, err := cli.NewCondition(cli.ConditionAbsent, "")
	if err != nil {
		return nil, err
	}
	specs := []planActionSpec{{cli.ActionOwnerAI4J, cli.ActionValidateSource, "toolkit source", present, present, cli.RecoveryNone}, {cli.ActionOwnerAI4J, cli.ActionPrepareRecovery, "durable structural history", absent, present, cli.RecoveryStructuralInverse}}
	switch {
	case desired != nil && desired.Lifecycle == "active" && (before == nil || before.Lifecycle == "archived"):
		catalogChecksum, err := cli.NewCondition(cli.ConditionMatchesChecksum, desired.Catalog.Checksum)
		if err != nil {
			return nil, err
		}
		if desired.Scope == "project-shared" {
			nativeCatalog, nativeErr := projectSharedNativeCatalogFile(*desired)
			if nativeErr != nil {
				return nil, nativeErr
			}
			nativeChecksum, conditionErr := cli.NewCondition(cli.ConditionMatchesChecksum, nativeCatalog.Checksum)
			if conditionErr != nil {
				return nil, conditionErr
			}
			specs = append(specs,
				planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, nativeCatalog.Path, absent, nativeChecksum, cli.RecoveryStructuralInverse},
				planActionSpec{cli.ActionOwnerClaude, cli.ActionRegisterMarketplace, desired.MarketplaceID, absent, present, cli.RecoveryExactHandle},
				planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, desired.Catalog.Path, present, catalogChecksum, cli.RecoveryStructuralInverse})
		} else {
			specs = append(specs,
				planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, desired.Catalog.Path, absent, catalogChecksum, cli.RecoveryStructuralInverse},
				planActionSpec{cli.ActionOwnerClaude, cli.ActionRegisterMarketplace, desired.MarketplaceID, absent, present, cli.RecoveryExactHandle})
		}
		for _, pluginID := range nativePluginIDs(*desired) {
			specs = append(specs,
				planActionSpec{cli.ActionOwnerClaude, cli.ActionInstallPlugin, pluginID, absent, present, cli.RecoveryNativeArtifact},
				planActionSpec{cli.ActionOwnerClaude, cli.ActionEnablePlugin, pluginID, present, present, cli.RecoveryExactHandle})
		}
		if hasRules {
			rulesChecksum, err := cli.NewCondition(cli.ConditionMatchesChecksum, desired.Rules.Checksum)
			if err != nil {
				return nil, err
			}
			specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteRules, desired.Rules.Path, absent, rulesChecksum, cli.RecoveryStructuralInverse})
		}
	case desired != nil && desired.Lifecycle == "active":
		newCatalog, err := cli.NewCondition(cli.ConditionMatchesChecksum, desired.Catalog.Checksum)
		if err != nil {
			return nil, err
		}
		if before.Catalog.Path != desired.Catalog.Path {
			oldCatalog, err := cli.NewCondition(cli.ConditionMatchesChecksum, before.Catalog.Checksum)
			if err != nil {
				return nil, err
			}
			if desired.Source.Mode == "development_source" {
				specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, desired.Catalog.Path, absent, newCatalog, cli.RecoveryStructuralInverse})
			}
			for _, pkg := range before.Packages {
				specs = append(specs, planActionSpec{cli.ActionOwnerClaude, cli.ActionUninstallPlugin, nativePluginID(pkg, before.MarketplaceID), present, absent, cli.RecoveryNativeArtifact})
			}
			if desired.Source.Mode != "development_source" {
				specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, desired.Catalog.Path, absent, newCatalog, cli.RecoveryStructuralInverse})
			}
			specs = append(specs,
				planActionSpec{cli.ActionOwnerClaude, cli.ActionRemoveMarketplace, before.MarketplaceID, present, absent, cli.RecoveryExactHandle},
				planActionSpec{cli.ActionOwnerAI4J, cli.ActionRemoveCatalog, before.Catalog.Path, oldCatalog, absent, cli.RecoveryStructuralInverse},
				planActionSpec{cli.ActionOwnerClaude, cli.ActionRegisterMarketplace, desired.MarketplaceID, absent, present, cli.RecoveryExactHandle})
			for _, pkg := range desired.Packages {
				pluginID := nativePluginID(pkg, desired.MarketplaceID)
				specs = append(specs,
					planActionSpec{cli.ActionOwnerClaude, cli.ActionInstallPlugin, pluginID, absent, present, cli.RecoveryNativeArtifact},
					planActionSpec{cli.ActionOwnerClaude, cli.ActionEnablePlugin, pluginID, present, present, cli.RecoveryExactHandle})
			}
		} else if catalogTransitionNeeded(s, *before, *desired) && before.Scope == "project-shared" {
			beforeNative, nativeErr := projectSharedNativeCatalogFile(*before)
			if nativeErr != nil {
				return nil, nativeErr
			}
			desiredNative, nativeErr := projectSharedNativeCatalogFile(*desired)
			if nativeErr != nil {
				return nil, nativeErr
			}
			beforeNativeCondition, conditionErr := cli.NewCondition(cli.ConditionMatchesChecksum, beforeNative.Checksum)
			if conditionErr != nil {
				return nil, conditionErr
			}
			switch s.inspectProjectSharedNativeCatalogDrift(*before) {
			case cli.DriftMissing:
				beforeNativeCondition = absent
			case cli.DriftModified, cli.DriftConflicting:
				beforeNativeCondition = present
			}
			desiredNativeCondition, conditionErr := cli.NewCondition(cli.ConditionMatchesChecksum, desiredNative.Checksum)
			if conditionErr != nil {
				return nil, conditionErr
			}
			oldSettings, conditionErr := cli.NewCondition(cli.ConditionMatchesChecksum, before.Catalog.Checksum)
			if conditionErr != nil {
				return nil, conditionErr
			}
			for _, pkg := range before.Packages {
				specs = append(specs, planActionSpec{cli.ActionOwnerClaude, cli.ActionUninstallPlugin, nativePluginID(pkg, before.MarketplaceID), present, absent, cli.RecoveryNativeArtifact})
			}
			specs = append(specs,
				planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, desiredNative.Path, beforeNativeCondition, desiredNativeCondition, cli.RecoveryStructuralInverse},
				planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, desired.Catalog.Path, oldSettings, newCatalog, cli.RecoveryStructuralInverse},
				planActionSpec{cli.ActionOwnerClaude, cli.ActionRefreshMarketplace, desired.MarketplaceID, present, present, cli.RecoveryExactHandle})
			for _, pkg := range desired.Packages {
				pluginID := nativePluginID(pkg, desired.MarketplaceID)
				specs = append(specs,
					planActionSpec{cli.ActionOwnerClaude, cli.ActionInstallPlugin, pluginID, absent, present, cli.RecoveryNativeArtifact},
					planActionSpec{cli.ActionOwnerClaude, cli.ActionEnablePlugin, pluginID, present, present, cli.RecoveryExactHandle})
			}
		} else if catalogTransitionNeeded(s, *before, *desired) {
			oldCatalog, err := cli.NewCondition(cli.ConditionMatchesChecksum, before.Catalog.Checksum)
			if err != nil {
				return nil, err
			}
			if before.Scope != "project-shared" && inspectFileDrift(s.catalogPath(*before), before.Catalog.Checksum) != cli.DriftUnchanged {
				oldCatalog = present
			}
			for _, pkg := range before.Packages {
				specs = append(specs, planActionSpec{cli.ActionOwnerClaude, cli.ActionUninstallPlugin, nativePluginID(pkg, before.MarketplaceID), present, absent, cli.RecoveryNativeArtifact})
			}
			specs = append(specs,
				planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, desired.Catalog.Path, oldCatalog, newCatalog, cli.RecoveryStructuralInverse},
				planActionSpec{cli.ActionOwnerClaude, cli.ActionRefreshMarketplace, desired.MarketplaceID, present, present, cli.RecoveryExactHandle})
			for _, pkg := range desired.Packages {
				pluginID := nativePluginID(pkg, desired.MarketplaceID)
				specs = append(specs,
					planActionSpec{cli.ActionOwnerClaude, cli.ActionInstallPlugin, pluginID, absent, present, cli.RecoveryNativeArtifact},
					planActionSpec{cli.ActionOwnerClaude, cli.ActionEnablePlugin, pluginID, present, present, cli.RecoveryExactHandle})
			}
		}
		if hasRules {
			beforeRules := absent
			if before.Rules != (installstate.OwnedFile{}) {
				beforeRules, err = cli.NewCondition(cli.ConditionMatchesChecksum, before.Rules.Checksum)
				if err != nil {
					return nil, err
				}
			}
			afterRules, err := cli.NewCondition(cli.ConditionMatchesChecksum, desired.Rules.Checksum)
			if err != nil {
				return nil, err
			}
			specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteRules, desired.Rules.Path, beforeRules, afterRules, cli.RecoveryStructuralInverse})
		} else if before.Rules != (installstate.OwnedFile{}) {
			beforeRules, err := cli.NewCondition(cli.ConditionMatchesChecksum, before.Rules.Checksum)
			if err != nil {
				return nil, err
			}
			specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionRemoveRules, before.Rules.Path, beforeRules, absent, cli.RecoveryStructuralInverse})
		}
	case desired != nil && desired.Lifecycle == "archived":
		for _, pluginID := range nativePluginIDs(*before) {
			specs = append(specs, planActionSpec{cli.ActionOwnerClaude, cli.ActionUninstallPlugin, pluginID, present, absent, cli.RecoveryNativeArtifact})
		}
		specs = append(specs,
			planActionSpec{cli.ActionOwnerClaude, cli.ActionRemoveMarketplace, before.MarketplaceID, present, absent, cli.RecoveryExactHandle},
			planActionSpec{cli.ActionOwnerAI4J, cli.ActionRemoveCatalog, before.Catalog.Path, present, absent, cli.RecoveryStructuralInverse})
		if before.Scope == "project-shared" && before.NativeCatalog != (installstate.OwnedFile{}) {
			nativeCatalog, nativeErr := projectSharedNativeCatalogFile(*before)
			if nativeErr != nil {
				return nil, nativeErr
			}
			specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionRemoveCatalog, nativeCatalog.Path, present, absent, cli.RecoveryStructuralInverse})
		}
		if before.Rules != (installstate.OwnedFile{}) {
			specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionRemoveRules, before.Rules.Path, present, absent, cli.RecoveryStructuralInverse})
		}
	}
	specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionCommitState, "AI4J installation state and history references", present, present, cli.RecoveryStructuralInverse}, planActionSpec{cli.ActionOwnerAI4J, cli.ActionCleanup, "operation journal", present, absent, cli.RecoveryNone})
	return makeActions(specs)
}

func (s *lifecycleService) installConflicts(ctx context.Context, desired installstate.Record) []cli.Conflict {
	var conflicts []cli.Conflict
	for _, item := range []struct{ path, code, resource string }{{s.catalogPath(desired), "catalog_destination_occupied", desired.Catalog.Path}, {s.rulesPath(desired), "rules_destination_occupied", desired.Rules.Path}} {
		if item.resource == "" {
			continue
		}
		if desired.Scope == "project-shared" && item.resource == ".claude/settings.json" {
			continue
		}
		if _, err := os.Lstat(item.path); err == nil {
			conflicts = append(conflicts, mustCLIConflict(item.code, item.resource, "destination is already occupied"))
		} else if !errors.Is(err, os.ErrNotExist) {
			conflicts = append(conflicts, mustCLIConflict("owned_state_inspection_failed", item.resource, "destination could not be inspected"))
		}
	}
	if desired.Scope == "project-shared" {
		owned, err := projectSharedNativeCatalogFile(desired)
		if err != nil {
			conflicts = append(conflicts, mustCLIConflict("native_catalog_invalid", "project-shared native catalog", "internal catalog provenance is invalid"))
		} else if _, err := os.Lstat(s.projectSharedNativeCatalogPath(desired)); err == nil {
			conflicts = append(conflicts, mustCLIConflict("native_catalog_destination_occupied", owned.Path, "internal catalog destination is already occupied"))
		} else if !errors.Is(err, os.ErrNotExist) {
			conflicts = append(conflicts, mustCLIConflict("owned_state_inspection_failed", owned.Path, "internal catalog destination could not be inspected"))
		}
	}
	native, problem := inspectAnyRecordNative(ctx, s.validation, desired)
	if problem != nil {
		conflicts = append(conflicts, mustCLIConflict(problem.Code(), "Claude native state", problem.Message()))
	} else {
		if native.MarketplaceRegistered {
			conflicts = append(conflicts, mustCLIConflict("marketplace_identity_conflict", desired.MarketplaceID, "marketplace identity already exists"))
		}
		if native.PluginInstalled {
			conflicts = append(conflicts, mustCLIConflict("plugin_identity_conflict", strings.Join(nativePluginIDs(desired), ", "), "one or more plugin identities already exist"))
		}
	}
	return conflicts
}

func (s *lifecycleService) existingConflicts(ctx context.Context, record installstate.Record, requireEnabled bool) []cli.Conflict {
	var conflicts []cli.Conflict
	if record.Lifecycle == "archived" {
		native, problem := inspectAnyRecordNative(ctx, s.validation, record)
		if problem != nil {
			return []cli.Conflict{mustCLIConflict(problem.Code(), "Claude native state", problem.Message())}
		}
		if native.MarketplaceRegistered || native.PluginInstalled {
			return []cli.Conflict{mustCLIConflict("archived_native_present", strings.Join(nativePluginIDs(record), ", "), "archived installation still has native state")}
		}
		if record.Scope == "project-shared" {
			marketplaceDrift := inspectProjectMarketplaceDrift(record)
			if marketplaceDrift != cli.DriftMissing {
				if marketplaceDrift == cli.DriftConflicting {
					return []cli.Conflict{mustCLIConflict("project_settings_unsafe", ".claude/settings.json", "shared project settings are unsafe or cannot be inspected")}
				}
				return []cli.Conflict{mustCLIConflict("project_settings_drift", ".claude/settings.json", "shared project marketplace appeared after uninstall")}
			}
			if !s.projectSharedNativeCatalogAbsent(record) {
				owned, _ := projectSharedNativeCatalogFile(record)
				if s.inspectProjectSharedNativeCatalogDrift(record) == cli.DriftConflicting {
					return []cli.Conflict{mustCLIConflict("native_catalog_unsafe", owned.Path, "internal catalog is unsafe or cannot be inspected")}
				}
				return []cli.Conflict{mustCLIConflict("native_catalog_drift", owned.Path, "archived installation still has an internal catalog")}
			}
		}
		return nil
	}
	for _, item := range []struct{ path, checksum, code, resource string }{{s.catalogPath(record), record.Catalog.Checksum, "catalog_drift", record.Catalog.Path}, {s.rulesPath(record), record.Rules.Checksum, "rules_drift", record.Rules.Path}} {
		if item.resource == "" {
			continue
		}
		state := inspectFileDrift(item.path, item.checksum)
		code := item.code
		if record.Scope == "project-shared" && item.resource == ".claude/settings.json" {
			state = inspectProjectMarketplaceDrift(record)
			code = "project_settings_drift"
		}
		if state != cli.DriftUnchanged {
			message := "installation-owned content is missing or modified"
			if state == cli.DriftConflicting {
				code = unsafeOwnedDriftCode(code)
				message = "installation-owned content is unsafe or cannot be inspected"
			}
			conflicts = append(conflicts, mustCLIConflict(code, item.resource, message))
		}
	}
	if record.Scope == "project-shared" && record.NativeCatalog != (installstate.OwnedFile{}) {
		owned, err := projectSharedNativeCatalogFile(record)
		if err != nil {
			conflicts = append(conflicts, mustCLIConflict("native_catalog_invalid", "project-shared native catalog", "internal catalog provenance is invalid"))
		} else if state := s.inspectProjectSharedNativeCatalogDrift(record); state != cli.DriftUnchanged {
			code := "native_catalog_drift"
			message := "installation-owned internal catalog is missing or modified"
			if state == cli.DriftConflicting {
				code = "native_catalog_unsafe"
				message = "installation-owned internal catalog is unsafe or cannot be inspected"
			}
			conflicts = append(conflicts, mustCLIConflict(code, owned.Path, message))
		}
	}
	native, problem := s.inspectNative(ctx, record)
	if problem != nil {
		conflicts = append(conflicts, mustCLIConflict(problem.Code(), "Claude native state", problem.Message()))
	} else {
		if !native.MarketplaceRegistered {
			conflicts = append(conflicts, mustCLIConflict("marketplace_missing", record.MarketplaceID, "marketplace registration is missing"))
		}
		if !native.PluginInstalled {
			conflicts = append(conflicts, mustCLIConflict("plugin_missing", strings.Join(nativePluginIDs(record), ", "), "one or more plugin installations are missing"))
		} else if requireEnabled && !native.PluginEnabled {
			conflicts = append(conflicts, mustCLIConflict("plugin_disabled", strings.Join(nativePluginIDs(record), ", "), "one or more plugins are disabled"))
		}
	}
	return conflicts
}

func unsafeOwnedDriftCode(code string) string {
	switch code {
	case "catalog_drift":
		return "catalog_unsafe"
	case "rules_drift":
		return "rules_unsafe"
	case "project_settings_drift":
		return "project_settings_unsafe"
	default:
		return "owned_state_unsafe"
	}
}

func applyConflictPolicy(conflicts []cli.Conflict, policy cli.ConflictPolicy, owned bool) ([]cli.Conflict, []cli.Conflict) {
	if len(conflicts) == 0 || policy == cli.ConflictFail || !owned {
		return slices.Clone(conflicts), nil
	}
	return nil, slices.Clone(conflicts)
}

func applyActiveTransitionConflictPolicy(conflicts []cli.Conflict, policy cli.ConflictPolicy) ([]cli.Conflict, []cli.Conflict) {
	if policy == cli.ConflictKeep || policy == cli.ConflictReplaceOwned {
		for _, conflict := range conflicts {
			keepable := conflict.Code() == "rules_drift"
			replaceable := keepable || conflict.Code() == "catalog_drift" || conflict.Code() == "native_catalog_drift"
			if policy == cli.ConflictKeep && !keepable || policy == cli.ConflictReplaceOwned && !replaceable {
				return slices.Clone(conflicts), nil
			}
		}
	}
	return applyConflictPolicy(conflicts, policy, true)
}

func applyRemovalConflictPolicy(conflicts []cli.Conflict, policy cli.ConflictPolicy) ([]cli.Conflict, []cli.Conflict) {
	if policy != cli.ConflictReplaceOwned {
		return slices.Clone(conflicts), nil
	}
	visible := make([]cli.Conflict, 0, len(conflicts))
	degraded := make([]cli.Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		switch conflict.Code() {
		case "catalog_drift", "native_catalog_drift", "rules_drift":
			degraded = append(degraded, conflict)
		default:
			visible = append(visible, conflict)
		}
	}
	return visible, degraded
}

func catalogTransitionNeeded(s *lifecycleService, before, desired installstate.Record) bool {
	if before.Catalog.Checksum != desired.Catalog.Checksum {
		return true
	}
	if before.Scope == "project-shared" {
		return before.NativeCatalog.Checksum != desired.NativeCatalog.Checksum || inspectProjectMarketplaceDrift(before) != cli.DriftUnchanged || s.inspectProjectSharedNativeCatalogDrift(before) != cli.DriftUnchanged
	}
	return inspectFileDrift(s.catalogPath(before), before.Catalog.Checksum) != cli.DriftUnchanged
}
