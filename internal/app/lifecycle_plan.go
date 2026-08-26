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

func (s *lifecycleService) recordForSelection(report validation.LifecycleSelection, selection cli.SelectionOptions, installationID domain.InstallationID, scope cli.Scope, scopeRoot string) (installstate.Record, catalog.Document, error) {
	marketplaceID := marketplaceIDFor(installationID)
	if scope == cli.ScopeProjectShared {
		marketplaceID = report.DeclarationID
		if marketplaceID == "" {
			marketplaceID = report.ToolkitID
		}
	}
	var document catalog.Document
	var err error
	if report.Source.Mode() == cli.SourceDevelopment {
		document, err = catalog.RenderLocalPackage(marketplaceID, report.PackageID, "plugin", "AI4J local toolkit package "+report.PackageID)
	} else {
		document, err = catalog.RenderPackage(marketplaceID, report.PackageID, report.PackagePath, "AI4J toolkit package "+report.PackageID, report.Source.Repository(), report.Source.Commit().OID())
	}
	if err != nil {
		return installstate.Record{}, catalog.Document{}, err
	}
	var requested *string
	if report.Source.HasRequestedRef() {
		value := report.Source.RequestedRef()
		requested = &value
	}
	stateSource := installstate.Source{Mode: "github", Selection: report.Source.Selection().String(), Repository: report.Source.Repository().String(), RequestedRef: requested, RefKind: report.Source.ResolvedRefKind().String(), Commit: report.Source.Commit().OID().String(), RenderedDigest: report.Source.RenderedDigest().String()}
	catalogPath := "state/catalogs/" + installationID.String() + "/.claude-plugin/marketplace.json"
	if report.Source.Mode() == cli.SourceDevelopment {
		stateSource = installstate.Source{Mode: "development_source", Selection: domain.ExplicitSource().String(), Checkout: report.Source.Checkout(), SourceDigest: report.Source.SourceDigest().String(), RenderedDigest: report.Source.RenderedDigest().String(), Dirty: report.Source.Dirty()}
		stateSource.BundleDigest = localBundleDigest(stateSource, selection, report, document.Digest())
		catalogPath = "state/bundles/" + stateSource.BundleDigest + "/.claude-plugin/marketplace.json"
	}
	record := installstate.Record{
		SchemaVersion: installstate.SchemaVersion, InstallationID: installationID.String(), ToolkitID: report.ToolkitID, DeclarationID: report.DeclarationID, ToolkitVersion: report.ToolkitVersion,
		PluginID: report.PackageID, PackagePath: report.PackagePath, MarketplaceID: marketplaceID,
		Source: stateSource,
		Target: "claude", Host: s.host(), Scope: string(scope), ScopeRoot: scopeRoot, Lifecycle: "active",
		Selection:       installstate.Selection{All: selection.SelectAll(), Assets: selection.Assets(), Bundles: selection.Bundles(), Resolved: slices.Clone(report.Resolved)},
		NativeResources: []string{"claude:" + report.PackageID + "@" + marketplaceID, "claude:marketplace:" + marketplaceID},
		Health:          "healthy", AI4JVersion: s.build.Version(),
		Catalog:       installstate.OwnedFile{Path: catalogPath, Checksum: document.Digest()},
		LastOperation: installstate.LastOperation{ID: "operation-pending", Timestamp: time.Unix(0, 0).UTC().Format(time.RFC3339)},
	}
	if len(report.Rules) != 0 {
		rulesID := "ai4j-" + installationID.String()
		rulesRoot := "rules/"
		if scope != cli.ScopeUser {
			rulesRoot = ".claude/rules/"
		}
		if scope == cli.ScopeProjectShared {
			rulesID = marketplaceID
		}
		record.Rules = installstate.OwnedFile{Path: rulesRoot + rulesID + ".md", Checksum: report.RulesChecksum}
	}
	slices.Sort(record.Selection.Assets)
	slices.Sort(record.Selection.Bundles)
	slices.Sort(record.Selection.Resolved)
	slices.Sort(record.NativeResources)
	return record, document, record.Validate()
}

func localBundleDigest(source installstate.Source, selection cli.SelectionOptions, report validation.LifecycleSelection, catalogDigest string) string {
	artifact := sha256.Sum256(report.NativeArtifact)
	parts := []string{"claude-local-bundle", source.Checkout, source.SourceDigest, source.RenderedDigest, report.ToolkitID, report.PackageID, report.PackagePath, catalogDigest, hex.EncodeToString(artifact[:]), strings.Join(report.Resolved, ",")}
	parts = append(parts, strings.Join(selection.Assets(), ","), strings.Join(selection.Bundles(), ","))
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func (s *lifecycleService) planResponse(command cli.Command, execution lifecycleExecution) (cli.Response, error) {
	installation := recordInstallation(execution.before, execution.desired)
	if installation == nil {
		return cli.Response{}, errors.New("planned installation identity is unavailable")
	}
	data, err := cli.NewPlanData(execution.operation, execution.source.Source, *installation, execution.actions, execution.content, execution.conflicts, execution.final, execution.disposition)
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
		specs = append(specs,
			planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, desired.Catalog.Path, absent, catalogChecksum, cli.RecoveryStructuralInverse},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionRegisterMarketplace, desired.MarketplaceID, absent, present, cli.RecoveryExactHandle},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionInstallPlugin, nativePluginID(*desired), absent, present, cli.RecoveryNativeArtifact},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionEnablePlugin, nativePluginID(*desired), present, present, cli.RecoveryExactHandle})
		if hasRules {
			rulesChecksum, err := cli.NewCondition(cli.ConditionMatchesChecksum, desired.Rules.Checksum)
			if err != nil {
				return nil, err
			}
			specs = append(specs, planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteRules, desired.Rules.Path, absent, rulesChecksum, cli.RecoveryStructuralInverse})
		}
	case desired != nil && desired.Lifecycle == "active":
		oldCatalog, err := cli.NewCondition(cli.ConditionMatchesChecksum, before.Catalog.Checksum)
		if err != nil {
			return nil, err
		}
		newCatalog, err := cli.NewCondition(cli.ConditionMatchesChecksum, desired.Catalog.Checksum)
		if err != nil {
			return nil, err
		}
		specs = append(specs,
			planActionSpec{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, desired.Catalog.Path, oldCatalog, newCatalog, cli.RecoveryStructuralInverse},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionRefreshMarketplace, desired.MarketplaceID, present, present, cli.RecoveryExactHandle},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionUpdatePlugin, nativePluginID(*desired), present, present, cli.RecoveryNativeArtifact},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionEnablePlugin, nativePluginID(*desired), present, present, cli.RecoveryExactHandle})
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
		specs = append(specs,
			planActionSpec{cli.ActionOwnerClaude, cli.ActionUninstallPlugin, nativePluginID(*before), present, absent, cli.RecoveryNativeArtifact},
			planActionSpec{cli.ActionOwnerClaude, cli.ActionRemoveMarketplace, before.MarketplaceID, present, absent, cli.RecoveryExactHandle},
			planActionSpec{cli.ActionOwnerAI4J, cli.ActionRemoveCatalog, before.Catalog.Path, present, absent, cli.RecoveryStructuralInverse})
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
	native, problem := s.inspectNative(ctx, desired)
	if problem != nil {
		conflicts = append(conflicts, mustCLIConflict(problem.Code(), "Claude native state", problem.Message()))
	} else {
		if native.MarketplaceRegistered {
			conflicts = append(conflicts, mustCLIConflict("marketplace_identity_conflict", desired.MarketplaceID, "marketplace identity already exists"))
		}
		if native.PluginInstalled {
			conflicts = append(conflicts, mustCLIConflict("plugin_identity_conflict", nativePluginID(desired), "plugin identity already exists"))
		}
	}
	return conflicts
}

func (s *lifecycleService) existingConflicts(ctx context.Context, record installstate.Record, requireEnabled bool) []cli.Conflict {
	var conflicts []cli.Conflict
	if record.Lifecycle == "archived" {
		native, problem := s.inspectNative(ctx, record)
		if problem != nil {
			return []cli.Conflict{mustCLIConflict(problem.Code(), "Claude native state", problem.Message())}
		}
		if native.MarketplaceRegistered || native.PluginInstalled {
			return []cli.Conflict{mustCLIConflict("archived_native_present", nativePluginID(record), "archived installation still has native state")}
		}
		if record.Scope == "project-shared" {
			if !projectMarketplaceAbsent(record) {
				return []cli.Conflict{mustCLIConflict("catalog_drift", ".claude/settings.json", "shared project marketplace appeared after uninstall")}
			}
		}
		return nil
	}
	for _, item := range []struct{ path, checksum, code, resource string }{{s.catalogPath(record), record.Catalog.Checksum, "catalog_drift", record.Catalog.Path}, {s.rulesPath(record), record.Rules.Checksum, "rules_drift", record.Rules.Path}} {
		if item.resource != "" && inspectFileDrift(item.path, item.checksum) != cli.DriftUnchanged {
			if record.Scope == "project-shared" && item.resource == ".claude/settings.json" && inspectProjectMarketplaceDrift(record) == cli.DriftUnchanged {
				continue
			}
			conflicts = append(conflicts, mustCLIConflict(item.code, item.resource, "installation-owned content is missing or modified"))
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
			conflicts = append(conflicts, mustCLIConflict("plugin_missing", nativePluginID(record), "plugin installation is missing"))
		} else if requireEnabled && !native.PluginEnabled {
			conflicts = append(conflicts, mustCLIConflict("plugin_disabled", nativePluginID(record), "plugin is disabled"))
		}
	}
	return conflicts
}

func applyConflictPolicy(conflicts []cli.Conflict, policy cli.ConflictPolicy, owned bool) ([]cli.Conflict, []cli.Conflict) {
	if len(conflicts) == 0 || policy == cli.ConflictFail || !owned {
		return slices.Clone(conflicts), nil
	}
	return nil, slices.Clone(conflicts)
}
