package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alx4j/ai4j/internal/buildinfo"
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/host/darwin/installlock"
	"github.com/alx4j/ai4j/internal/hostprocess"
	"github.com/alx4j/ai4j/internal/result"
	validation "github.com/alx4j/ai4j/internal/validate"
)

type commandRouter struct {
	validation validation.Service
	lifecycle  *lifecycleService
	status     statusService
	doctor     *doctorService
}

func productionOtherCommands(build buildinfo.Info) OtherCommandsFactory {
	return func() (CommandHandler, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user home: %w", err)
		}
		claudeRoot, err := productionClaudeRoot(home)
		if err != nil {
			return nil, err
		}
		state, err := productionStateStore(home)
		if err != nil {
			return nil, err
		}
		runner := hostprocess.OSRunner{}
		validator, err := validation.NewService(validation.Config{
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Home: home, ClaudeRoot: claudeRoot,
			BuildCommit: build.Revision(), Runner: runner,
		})
		if err != nil {
			return nil, err
		}
		locker, err := installlock.NewAt(state.Root())
		if err != nil {
			return nil, err
		}
		acquire := func(ctx context.Context) (func() error, error) {
			handle, acquireErr := locker.Acquire(ctx)
			if acquireErr != nil {
				return nil, acquireErr
			}
			return handle.Release, nil
		}
		router := commandRouter{validation: validator}
		router.lifecycle = newLifecycleService(validator, state, runner, home, claudeRoot, build, acquire)
		router.status = statusService{validation: validator, state: state, home: home}
		router.doctor = newDoctorService(state, router.status, validator, runner)
		return newCommandHandler(router, state.DataRoot()), nil
	}
}

func productionClaudeRoot(home string) (string, error) {
	value, present := os.LookupEnv("CLAUDE_CONFIG_DIR")
	if !present {
		return filepath.Join(home, ".claude"), nil
	}
	if value == "" || !filepath.IsAbs(value) {
		return "", errors.New("CLAUDE_CONFIG_DIR must name an absolute directory")
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(value))
	if err != nil {
		return "", errors.New("CLAUDE_CONFIG_DIR is unusable")
	}
	homeRoot, err := filepath.EvalSymlinks(filepath.Clean(home))
	if err != nil {
		return "", errors.New("user home is unusable")
	}
	relative, err := filepath.Rel(homeRoot, root)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("CLAUDE_CONFIG_DIR must be contained in the current user home")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("CLAUDE_CONFIG_DIR is unusable")
	}
	return root, nil
}

func newCommandHandler(router commandRouter, dataRoot string) CommandHandler {
	return func(ctx context.Context, request cli.Request, commandIO CommandIO) (response cli.Response, responseErr error) {
		logSession := startCommandLog(dataRoot, request)
		if logSession != nil {
			commandIO.logSession = logSession
			defer func() { logSession.complete(response, responseErr) }()
		}
		switch command := request.(type) {
		case cli.InitRequest:
			return initResponse(router.validation.Init(ctx, command))
		case cli.ValidateRequest:
			return validateResponse(router.validation.Validate(ctx, command.Source()))
		case cli.BuildRequest:
			return buildResponse(router.validation.Build(ctx, command))
		case cli.SyncRequest:
			return router.lifecycle.Sync(ctx, command, commandIO)
		case cli.RollbackRequest:
			return router.lifecycle.Rollback(ctx, command, commandIO)
		case cli.HistoryRequest:
			return router.lifecycle.History(ctx, command)
		case cli.HistoryPurgeRequest:
			return router.lifecycle.HistoryPurge(ctx, command, commandIO)
		case cli.InstallRequest:
			return router.lifecycle.Install(ctx, command, commandIO)
		case cli.UpdateRequest:
			return router.lifecycle.Update(ctx, command, commandIO)
		case cli.ListRequest:
			return router.status.List(ctx, command)
		case cli.UninstallRequest:
			return router.lifecycle.Uninstall(ctx, command, commandIO)
		case cli.StatusRequest:
			return router.status.Status(ctx, command)
		case cli.DoctorRequest:
			return router.doctor.Doctor(ctx, command, commandIO)
		default:
			return cli.Response{}, fmt.Errorf("command %q is not implemented", request.Command())
		}
	}
}

func initResponse(report validation.InitReport) (cli.Response, error) {
	status := result.StatusOK
	phase := result.PhaseComplete
	outcome := result.OutcomeCommitted
	mutation := result.MutationStarted
	durableChange := result.DurableCommittedWithDiff
	failure := result.FailureNone
	if len(report.Problems) != 0 {
		status = result.StatusError
		phase = result.PhaseNone
		outcome = result.OutcomeNone
		mutation = result.MutationNotStarted
		durableChange = result.DurableChangeNone
		switch report.Failure {
		case validation.FailureEnvironment:
			failure = result.FailureEnvironment
		case validation.FailureValidation:
			failure = result.FailureValidation
		case validation.FailureConflict:
			failure = result.FailureConflict
		default:
			failure = result.FailureInternal
		}
	}
	commandResult, err := result.New(result.Facts{
		Status: status, Phase: phase, Outcome: outcome, Mutation: mutation,
		DurableChange: durableChange, Failure: failure, UpdateDisposition: result.UpdateNotChecked,
		Errors: report.Problems,
	})
	if err != nil {
		return cli.Response{}, err
	}
	if len(report.Problems) != 0 {
		return cli.NewResponse(cli.CommandInit, commandResult, nil, cli.UnavailableData{})
	}
	data, err := cli.NewInitData(report.Targets, report.OutputRoot, report.Artifacts)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandInit, commandResult, nil, data)
}

func buildResponse(report validation.BuildReport) (cli.Response, error) {
	status := result.StatusOK
	phase := result.PhaseComplete
	outcome := result.OutcomeCommitted
	mutation := result.MutationStarted
	durableChange := result.DurableCommittedWithDiff
	failure := result.FailureNone
	if len(report.Problems) != 0 {
		status = result.StatusError
		phase = result.PhaseNone
		outcome = result.OutcomeNone
		mutation = result.MutationNotStarted
		durableChange = result.DurableChangeNone
		switch report.Failure {
		case validation.FailureEnvironment:
			failure = result.FailureEnvironment
		case validation.FailureSource:
			failure = result.FailureSource
		case validation.FailureValidation:
			failure = result.FailureValidation
		case validation.FailureConflict:
			failure = result.FailureConflict
		default:
			failure = result.FailureInternal
		}
	}
	commandResult, err := result.New(result.Facts{
		Status: status, Phase: phase, Outcome: outcome, Mutation: mutation,
		DurableChange: durableChange, Failure: failure, UpdateDisposition: result.UpdateNotChecked,
		Warnings: report.Warnings, Errors: report.Problems,
	})
	if err != nil {
		return cli.Response{}, err
	}
	if len(report.Problems) != 0 {
		return cli.NewResponse(cli.CommandBuild, commandResult, nil, cli.UnavailableData{})
	}
	data, err := cli.NewBuildDataWithSelection(report.Source, report.Target, report.Host, report.OutputRoot, report.Reproducible, report.Artifacts, report.Selection, report.Content)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandBuild, commandResult, nil, data)
}

func validateResponse(report validation.Report) (cli.Response, error) {
	commandResult, err := validationCommandResult(report)
	if err != nil {
		return cli.Response{}, err
	}
	if !report.HasSource() {
		return cli.NewResponse(cli.CommandValidate, commandResult, nil, cli.UnavailableData{})
	}
	data, err := cli.NewValidateData(report.Source, len(report.Problems) == 0, len(report.Problems), len(report.Warnings), report.Content)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandValidate, commandResult, nil, data)
}

func validationCommandResult(report validation.Report) (result.Result, error) {
	status := result.StatusOK
	failure := result.FailureNone
	if len(report.Problems) != 0 {
		status = result.StatusError
		switch report.Failure {
		case validation.FailureEnvironment:
			failure = result.FailureEnvironment
		case validation.FailureSource:
			failure = result.FailureSource
		case validation.FailureValidation:
			failure = result.FailureValidation
		default:
			failure = result.FailureInternal
		}
	}
	return result.New(result.Facts{
		Status: status, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: failure, UpdateDisposition: result.UpdateNotChecked,
		Warnings: report.Warnings, Errors: report.Problems,
	})
}
