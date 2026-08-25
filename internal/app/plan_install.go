package app

import (
	"fmt"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
	"github.com/alx4j/ai4j/internal/target/claude/catalog"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func planInstallResponse(report validation.Report, conflicts []cli.Conflict) (cli.Response, error) {
	commandResult, err := validationCommandResult(report)
	if err != nil {
		return cli.Response{}, err
	}
	if len(report.Problems) != 0 || !report.HasSource() {
		return cli.NewResponse(cli.CommandPlanInstall, commandResult, nil, cli.UnavailableData{})
	}

	data, err := newInstallPlan(report, conflicts)
	if err != nil {
		return cli.Response{}, err
	}
	if len(conflicts) != 0 {
		problems := make([]result.Problem, 0, len(conflicts))
		for _, conflict := range conflicts {
			resource, contextErr := result.NewContext("resource", conflict.Resource())
			if contextErr != nil {
				return cli.Response{}, contextErr
			}
			problem, problemErr := result.NewProblem(conflict.Code(), conflict.Message(), []result.Context{resource})
			if problemErr != nil {
				return cli.Response{}, problemErr
			}
			problems = append(problems, problem)
		}
		commandResult, err = result.New(result.Facts{
			Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
			Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
			Failure: result.FailureConflict, UpdateDisposition: result.UpdateNotChecked,
			Warnings: report.Warnings, Errors: problems,
		})
		if err != nil {
			return cli.Response{}, err
		}
	}
	return cli.NewResponse(cli.CommandPlanInstall, commandResult, nil, data)
}

func newInstallPlan(report validation.Report, conflicts []cli.Conflict) (cli.PlanData, error) {
	installation, err := domain.NewInstallationID("install-" + report.Source.Commit().OID().String()[:12])
	if err != nil {
		return cli.PlanData{}, fmt.Errorf("construct planned installation ID: %w", err)
	}
	absent, err := cli.NewCondition(cli.ConditionAbsent, "")
	if err != nil {
		return cli.PlanData{}, err
	}
	present, err := cli.NewCondition(cli.ConditionPresent, "")
	if err != nil {
		return cli.PlanData{}, err
	}
	catalogDocument, err := catalog.Render(report.Source.Repository(), report.Source.Commit().OID())
	if err != nil {
		return cli.PlanData{}, err
	}
	catalogChecksum, err := cli.NewCondition(cli.ConditionMatchesChecksum, catalogDocument.Digest())
	if err != nil {
		return cli.PlanData{}, err
	}
	rulesChecksum, err := cli.NewCondition(cli.ConditionMatchesChecksum, report.RulesChecksum)
	if err != nil {
		return cli.PlanData{}, err
	}

	type actionSpec struct {
		owner    cli.ActionOwner
		kind     cli.ActionKind
		resource string
		before   cli.Condition
		after    cli.Condition
		recovery cli.RecoveryRequirement
	}
	specs := []actionSpec{
		{cli.ActionOwnerAI4J, cli.ActionValidateSource, "toolkit source", present, present, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionWriteCatalog, "AI4J marketplace catalog", absent, catalogChecksum, cli.RecoveryNone},
		{cli.ActionOwnerClaude, cli.ActionRegisterMarketplace, "AI4J marketplace", absent, present, cli.RecoveryNone},
		{cli.ActionOwnerClaude, cli.ActionInstallPlugin, "ai4j-default@ai4j", absent, present, cli.RecoveryNone},
		{cli.ActionOwnerClaude, cli.ActionEnablePlugin, "ai4j-default@ai4j", present, present, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionWriteRules, "Claude user rules/ai4j.md", absent, rulesChecksum, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionCommitState, "AI4J installation state", absent, present, cli.RecoveryNone},
		{cli.ActionOwnerAI4J, cli.ActionCleanup, "temporary source workspace", present, absent, cli.RecoveryNone},
	}
	actions := make([]cli.Action, 0, len(specs))
	for index, spec := range specs {
		action, actionErr := cli.NewAction(index+1, spec.owner, spec.kind, spec.resource, spec.before, spec.after, spec.recovery)
		if actionErr != nil {
			return cli.PlanData{}, fmt.Errorf("construct install action: %w", actionErr)
		}
		actions = append(actions, action)
	}
	final, err := cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	if err != nil {
		return cli.PlanData{}, err
	}
	return cli.NewPlanData(cli.OperationInstall, report.Source, installation, actions, report.Content, conflicts, final, result.UpdateNotChecked)
}
