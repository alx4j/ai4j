package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	"github.com/alx4j/ai4j/internal/target/claude/catalog"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func planUpdateResponse(ctx context.Context, service commandService) (cli.Response, error) {
	record, present, err := service.LoadInstallation()
	if err != nil {
		code := "installation_state_invalid"
		message := "installation state could not be read"
		if errors.Is(err, installstate.ErrUnsupportedSchema) {
			code = "unsupported_state_schema"
			message = "installation state uses an unsupported schema"
		}
		return planUnavailable(cli.CommandUpdate, result.FailureConflict, code, message)
	}
	if !present {
		return planUnavailable(cli.CommandUpdate, result.FailureConflict, "not_installed", "AI4J is not installed")
	}
	installed, err := domain.NewCommitOID(record.Source.Commit)
	if err != nil {
		return cli.Response{}, err
	}
	options, err := updateSourceOptions(record)
	if err != nil {
		return cli.Response{}, err
	}

	var report validation.Report
	var disposition result.UpdateDisposition
	switch record.Source.RefKind {
	case "tag", "commit":
		exactOptions, optionsErr := exactSourceOptions(record)
		if optionsErr != nil {
			return cli.Response{}, optionsErr
		}
		report = service.Validate(ctx, exactOptions)
		disposition = result.UpdatePinned
	default:
		update := service.ValidateUpdate(ctx, options, installed)
		report = update.Report
		switch update.Disposition {
		case gitsource.UpdateNoChange:
			disposition = result.UpdateUpToDate
		case gitsource.UpdateAvailable:
			disposition = result.UpdateAvailable
		case gitsource.UpdateRefRewritten:
			disposition = result.UpdateRefRewritten
		default:
			disposition = result.UpdateUnknown
		}
	}
	if len(report.Problems) != 0 || !report.HasSource() {
		return planValidationUnavailable(cli.CommandUpdate, report)
	}
	conflicts, problem := service.InspectPlanExisting(ctx, record.Catalog.Checksum, record.Rules.Checksum)
	if problem != nil {
		return planUnavailableWithWarnings(cli.CommandUpdate, result.FailureEnvironment, problem.Code(), problem.Message(), report.Warnings)
	}
	if disposition == result.UpdateRefRewritten {
		conflict, conflictErr := cli.NewConflict("ref_rewritten", "toolkit source", "the tracked reference is not a fast-forward update")
		if conflictErr != nil {
			return cli.Response{}, conflictErr
		}
		conflicts = append(conflicts, conflict)
	}

	installation, err := domain.NewInstallationID(record.InstallationID)
	if err != nil {
		return cli.Response{}, err
	}
	contentChange := cli.ContentUnchanged
	var actions []cli.Action
	var content []cli.ContentItem
	if disposition == result.UpdateAvailable {
		actions, err = updateActions(record, report)
		if err != nil {
			return cli.Response{}, err
		}
		exactOptions, optionsErr := exactSourceOptions(record)
		if optionsErr != nil {
			return cli.Response{}, optionsErr
		}
		installedReport := service.Validate(ctx, exactOptions)
		if len(installedReport.Problems) != 0 || !installedReport.HasSource() {
			return planValidationUnavailable(cli.CommandUpdate, installedReport)
		}
		content, err = diffActiveContent(installedReport.Content, report.Content)
	} else {
		content, err = contentWithChange(report.Content, contentChange)
	}
	if err != nil {
		return cli.Response{}, err
	}
	final, err := cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewPlanData(cli.OperationUpdate, report.Source, installation, actions, content, conflicts, final, disposition)
	if err != nil {
		return cli.Response{}, err
	}
	return planResponse(cli.CommandUpdate, data, report.Warnings, conflicts, disposition)
}

func diffActiveContent(installed, desired []cli.ContentItem) ([]cli.ContentItem, error) {
	installedByKey := make(map[string]cli.ContentItem, len(installed))
	desiredByKey := make(map[string]cli.ContentItem, len(desired))
	keys := make(map[string]struct{}, len(installed)+len(desired))
	for _, item := range installed {
		key := contentKey(item)
		installedByKey[key] = item
		keys[key] = struct{}{}
	}
	for _, item := range desired {
		key := contentKey(item)
		desiredByKey[key] = item
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	resultItems := make([]cli.ContentItem, 0, len(ordered))
	for _, key := range ordered {
		before, hadBefore := installedByKey[key]
		after, hasAfter := desiredByKey[key]
		switch {
		case !hadBefore:
			item, err := contentItemWithChange(after, cli.ContentAdded)
			if err != nil {
				return nil, err
			}
			resultItems = append(resultItems, item)
		case !hasAfter:
			item, err := contentItemWithChange(before, cli.ContentRemoved)
			if err != nil {
				return nil, err
			}
			resultItems = append(resultItems, item)
		default:
			change := cli.ContentUnchanged
			if !sameContentItem(before, after) {
				change = cli.ContentChanged
			}
			item, err := contentItemWithChange(after, change)
			if err != nil {
				return nil, err
			}
			resultItems = append(resultItems, item)
		}
	}
	return resultItems, nil
}

func contentKey(item cli.ContentItem) string {
	return string(item.ComponentType()) + "\x00" + item.Identifier()
}

func sameContentItem(first, second cli.ContentItem) bool {
	if first.SourcePath() != second.SourcePath() || first.Checksum() != second.Checksum() {
		return false
	}
	firstExecution, firstPresent := first.Execution()
	secondExecution, secondPresent := second.Execution()
	if firstPresent != secondPresent {
		return false
	}
	if !firstPresent {
		return true
	}
	return firstExecution.Ownership() == secondExecution.Ownership() &&
		firstExecution.Dependency() == secondExecution.Dependency() &&
		firstExecution.Command() == secondExecution.Command() &&
		firstExecution.CWD() == secondExecution.CWD() &&
		slices.Equal(firstExecution.Args(), secondExecution.Args()) &&
		slices.Equal(firstExecution.SupportedPlaceholders(), secondExecution.SupportedPlaceholders()) &&
		slices.Equal(firstExecution.Environment(), secondExecution.Environment())
}

func contentItemWithChange(item cli.ContentItem, change cli.ContentChange) (cli.ContentItem, error) {
	var execution *cli.Execution
	if value, present := item.Execution(); present {
		execution = &value
	}
	return cli.NewContentItem(item.ComponentType(), item.Identifier(), item.SourcePath(), item.Checksum(), change, execution)
}

func planUninstallResponse(ctx context.Context, service commandService) (cli.Response, error) {
	record, present, err := service.LoadInstallation()
	if err != nil {
		code := "installation_state_invalid"
		message := "installation state could not be read"
		if errors.Is(err, installstate.ErrUnsupportedSchema) {
			code = "unsupported_state_schema"
			message = "installation state uses an unsupported schema"
		}
		return planUnavailable(cli.CommandUninstall, result.FailureConflict, code, message)
	}
	if !present {
		return planUnavailable(cli.CommandUninstall, result.FailureConflict, "not_installed", "AI4J is not installed")
	}
	exactOptions, err := exactSourceOptions(record)
	if err != nil {
		return cli.Response{}, err
	}
	report := service.Validate(ctx, exactOptions)
	if len(report.Problems) != 0 || !report.HasSource() {
		return planValidationUnavailable(cli.CommandUninstall, report)
	}
	conflicts, problem := service.InspectPlanExisting(ctx, record.Catalog.Checksum, record.Rules.Checksum)
	if problem != nil {
		return planUnavailableWithWarnings(cli.CommandUninstall, result.FailureEnvironment, problem.Code(), problem.Message(), report.Warnings)
	}
	installation, err := domain.NewInstallationID(record.InstallationID)
	if err != nil {
		return cli.Response{}, err
	}
	actions, err := uninstallActions(record)
	if err != nil {
		return cli.Response{}, err
	}
	content, err := contentWithChange(report.Content, cli.ContentRemoved)
	if err != nil {
		return cli.Response{}, err
	}
	final, err := cli.NewFinalState(cli.StateAbsent, cli.StateAbsent, cli.StateAbsent)
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewPlanData(cli.OperationUninstall, report.Source, installation, actions, content, conflicts, final, result.UpdateNotChecked)
	if err != nil {
		return cli.Response{}, err
	}
	return planResponse(cli.CommandUninstall, data, report.Warnings, conflicts, result.UpdateNotChecked)
}

func updateSourceOptions(record installstate.Record) (cli.SourceOptions, error) {
	if record.Source.Mode == "development_source" {
		return cli.NewDevelopmentSourceOptions(record.Source.Checkout, false)
	}
	repository, repositoryProvided, err := storedSourceRepository(record)
	if err != nil {
		return cli.SourceOptions{}, err
	}
	if record.Source.RequestedRef == nil {
		return cli.NewSourceOptions(repository, repositoryProvided, "", false)
	}
	return cli.NewSourceOptions(repository, repositoryProvided, *record.Source.RequestedRef, true)
}

func exactSourceOptions(record installstate.Record) (cli.SourceOptions, error) {
	if record.Source.Mode == "development_source" {
		return cli.NewDevelopmentSourceOptions(record.Source.Checkout, false)
	}
	repository, repositoryProvided, err := storedSourceRepository(record)
	if err != nil {
		return cli.SourceOptions{}, err
	}
	return cli.NewSourceOptions(repository, repositoryProvided, record.Source.Commit, true)
}

func storedSourceRepository(record installstate.Record) (string, bool, error) {
	if record.Source.Selection == domain.BuiltInDefaultSource().String() {
		return "", false, nil
	}
	identity, err := domain.NewRepositoryIdentity(record.Source.Repository)
	if err != nil {
		return "", false, err
	}
	return "https://" + identity.String() + ".git", true, nil
}

func updateActions(record installstate.Record, report validation.Report) ([]cli.Action, error) {
	oldCatalog, err := cli.NewCondition(cli.ConditionMatchesChecksum, record.Catalog.Checksum)
	if err != nil {
		return nil, err
	}
	newCatalog, err := catalog.Render(report.Source.Repository(), report.Source.Commit().OID())
	if err != nil {
		return nil, err
	}
	newCatalogCondition, err := cli.NewCondition(cli.ConditionMatchesChecksum, newCatalog.Digest())
	if err != nil {
		return nil, err
	}
	oldRules, err := cli.NewCondition(cli.ConditionMatchesChecksum, record.Rules.Checksum)
	if err != nil {
		return nil, err
	}
	newRules, err := cli.NewCondition(cli.ConditionMatchesChecksum, report.RulesChecksum)
	if err != nil {
		return nil, err
	}
	present, err := cli.NewCondition(cli.ConditionPresent, "")
	if err != nil {
		return nil, err
	}
	absent, err := cli.NewCondition(cli.ConditionAbsent, "")
	if err != nil {
		return nil, err
	}
	actions := []planActionSpec{
		{cli.ActionOwnerAI4J, cli.ActionValidateSource, "toolkit source", present, present, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, "AI4J marketplace catalog", oldCatalog, newCatalogCondition, cli.RecoveryNone},
		{cli.ActionOwnerClaude, cli.ActionRefreshMarketplace, "AI4J marketplace", present, present, cli.RecoveryNone},
		{cli.ActionOwnerClaude, cli.ActionUpdatePlugin, "ai4j-default@ai4j", present, present, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionWriteRules, "Claude user rules/ai4j.md", oldRules, newRules, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionCommitState, "AI4J installation state", present, present, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionCleanup, "temporary source workspace", present, absent, cli.RecoveryNone},
	}
	return makeActions(actions)
}

func uninstallActions(record installstate.Record) ([]cli.Action, error) {
	present, err := cli.NewCondition(cli.ConditionPresent, "")
	if err != nil {
		return nil, err
	}
	absent, err := cli.NewCondition(cli.ConditionAbsent, "")
	if err != nil {
		return nil, err
	}
	catalogCondition, err := cli.NewCondition(cli.ConditionMatchesChecksum, record.Catalog.Checksum)
	if err != nil {
		return nil, err
	}
	rulesCondition, err := cli.NewCondition(cli.ConditionMatchesChecksum, record.Rules.Checksum)
	if err != nil {
		return nil, err
	}
	actions := []planActionSpec{
		{cli.ActionOwnerAI4J, cli.ActionValidateSource, "installed toolkit source", present, present, cli.RecoveryNone},
		{cli.ActionOwnerClaude, cli.ActionUninstallPlugin, "ai4j-default@ai4j", present, absent, cli.RecoveryNone},
		{cli.ActionOwnerClaude, cli.ActionRemoveMarketplace, "AI4J marketplace", present, absent, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionRemoveCatalog, "AI4J marketplace catalog", catalogCondition, absent, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionRemoveRules, "Claude user rules/ai4j.md", rulesCondition, absent, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionRemoveState, "AI4J installation state", present, absent, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionCleanup, "temporary source workspace", present, absent, cli.RecoveryNone},
	}
	return makeActions(actions)
}

type planActionSpec struct {
	owner         cli.ActionOwner
	kind          cli.ActionKind
	resource      string
	before, after cli.Condition
	recovery      cli.RecoveryRequirement
}

func makeActions(specs []planActionSpec) ([]cli.Action, error) {
	actions := make([]cli.Action, 0, len(specs))
	for index, spec := range specs {
		action, err := cli.NewAction(index+1, spec.owner, spec.kind, spec.resource, spec.before, spec.after, spec.recovery)
		if err != nil {
			return nil, fmt.Errorf("construct lifecycle action: %w", err)
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func contentWithChange(content []cli.ContentItem, change cli.ContentChange) ([]cli.ContentItem, error) {
	changed := make([]cli.ContentItem, 0, len(content))
	for _, item := range content {
		var execution *cli.Execution
		if value, present := item.Execution(); present {
			execution = &value
		}
		value, err := cli.NewContentItem(item.ComponentType(), item.Identifier(), item.SourcePath(), item.Checksum(), change, execution)
		if err != nil {
			return nil, err
		}
		changed = append(changed, value)
	}
	return changed, nil
}

func planResponse(command cli.Command, data cli.PlanData, warnings []result.Warning, conflicts []cli.Conflict, disposition result.UpdateDisposition) (cli.Response, error) {
	status := result.StatusOK
	failure := result.FailureNone
	var problems []result.Problem
	if len(conflicts) != 0 {
		status = result.StatusError
		failure = result.FailureConflict
		for _, conflict := range conflicts {
			contextItem, _ := result.NewContext("resource", conflict.Resource())
			problem, _ := result.NewProblem(conflict.Code(), conflict.Message(), []result.Context{contextItem})
			problems = append(problems, problem)
		}
	} else if len(data.Actions()) == 0 {
		status = result.StatusNoChange
	}
	commandResult, err := result.New(result.Facts{
		Status: status, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: failure, UpdateDisposition: disposition, Warnings: warnings, Errors: problems,
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, data)
}

func planValidationUnavailable(command cli.Command, report validation.Report) (cli.Response, error) {
	commandResult, err := validationCommandResult(report)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}

func planUnavailable(command cli.Command, failure result.Failure, code, message string) (cli.Response, error) {
	return planUnavailableWithWarnings(command, failure, code, message, nil)
}

func planUnavailableWithWarnings(command cli.Command, failure result.Failure, code, message string, warnings []result.Warning) (cli.Response, error) {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: failure, UpdateDisposition: result.UpdateNotChecked, Warnings: warnings, Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}
