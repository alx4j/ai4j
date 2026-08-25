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
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/host/darwin/installlock"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	validation "github.com/alx4j/ai4j/internal/validate"
)

type commandService interface {
	Validate(context.Context, cli.SourceOptions) validation.Report
	ValidateUpdate(context.Context, cli.SourceOptions, domain.CommitOID) validation.UpdateReport
	InspectPlanInstall(context.Context) ([]cli.Conflict, *result.Problem)
	InspectPlanExisting(context.Context, string, string) ([]cli.Conflict, *result.Problem)
	InspectUninstall(context.Context, string, string) ([]cli.Conflict, *result.Problem)
	LoadInstallation() (installstate.Record, bool, error)
	Install(context.Context, cli.InstallRequest, CommandIO) (cli.Response, error)
	Update(context.Context, cli.UpdateRequest, CommandIO) (cli.Response, error)
	Uninstall(context.Context, cli.UninstallRequest, CommandIO) (cli.Response, error)
	Status(context.Context, cli.StatusRequest) (cli.Response, error)
}

type buildCommandService interface {
	Build(context.Context, cli.BuildRequest) validation.BuildReport
}

type initCommandService interface {
	Init(context.Context, cli.InitRequest) validation.InitReport
}

type listCommandService interface {
	List(context.Context, cli.ListRequest) (cli.Response, error)
}

type doctorCommandService interface {
	Doctor(context.Context, cli.DoctorRequest, CommandIO) (cli.Response, error)
}

type v1CommandService interface {
	Install(context.Context, cli.InstallRequest, CommandIO) (cli.Response, error)
	Update(context.Context, cli.UpdateRequest, CommandIO) (cli.Response, error)
	Sync(context.Context, cli.SyncRequest, CommandIO) (cli.Response, error)
	Rollback(context.Context, cli.RollbackRequest, CommandIO) (cli.Response, error)
	Uninstall(context.Context, cli.UninstallRequest, CommandIO) (cli.Response, error)
	History(context.Context, cli.HistoryRequest) (cli.Response, error)
	HistoryPurge(context.Context, cli.HistoryPurgeRequest, CommandIO) (cli.Response, error)
}

type productionCommandService struct {
	validation.Service
	state     installstate.Store
	installer *installer
	lifecycle *lifecycleService
	v1        *v1LifecycleService
	status    statusService
	doctor    *doctorService
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
		runner := validation.OSProcessRunner{}
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
		service := productionCommandService{Service: validator, state: state}
		service.installer = newInstaller(validator, state, runner, home, build, acquire)
		service.installer.claudeRoot = claudeRoot
		service.lifecycle = newLifecycleService(service.installer, validator)
		service.v1 = newV1LifecycleService(service.installer, validator)
		service.status = statusService{validation: validator, state: state, home: home}
		service.doctor = newDoctorService(state, service.status, validator, runner)
		return newCommandHandler(service), nil
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

func (s productionCommandService) LoadInstallation() (installstate.Record, bool, error) {
	return s.state.Load()
}

func (s productionCommandService) Install(ctx context.Context, request cli.InstallRequest, commandIO CommandIO) (cli.Response, error) {
	if request.V1() {
		return s.v1.Install(ctx, request, commandIO)
	}
	return s.installer.Install(ctx, request, commandIO)
}

func (s productionCommandService) Update(ctx context.Context, request cli.UpdateRequest, commandIO CommandIO) (cli.Response, error) {
	if request.V1() {
		return s.v1.Update(ctx, request, commandIO)
	}
	return s.lifecycle.Update(ctx, request, commandIO)
}

func (s productionCommandService) Uninstall(ctx context.Context, request cli.UninstallRequest, commandIO CommandIO) (cli.Response, error) {
	if request.V1() {
		return s.v1.Uninstall(ctx, request, commandIO)
	}
	return s.lifecycle.Uninstall(ctx, request, commandIO)
}

func (s productionCommandService) Status(ctx context.Context, request cli.StatusRequest) (cli.Response, error) {
	return s.status.Status(ctx, request)
}

func (s productionCommandService) List(ctx context.Context, request cli.ListRequest) (cli.Response, error) {
	return s.status.List(ctx, request)
}

func (s productionCommandService) Doctor(ctx context.Context, request cli.DoctorRequest, commandIO CommandIO) (cli.Response, error) {
	return s.doctor.Doctor(ctx, request, commandIO)
}

func (s productionCommandService) Sync(ctx context.Context, request cli.SyncRequest, commandIO CommandIO) (cli.Response, error) {
	return s.v1.Sync(ctx, request, commandIO)
}

func (s productionCommandService) Rollback(ctx context.Context, request cli.RollbackRequest, commandIO CommandIO) (cli.Response, error) {
	return s.v1.Rollback(ctx, request, commandIO)
}

func (s productionCommandService) History(ctx context.Context, request cli.HistoryRequest) (cli.Response, error) {
	return s.v1.History(ctx, request)
}

func (s productionCommandService) HistoryPurge(ctx context.Context, request cli.HistoryPurgeRequest, commandIO CommandIO) (cli.Response, error) {
	return s.v1.HistoryPurge(ctx, request, commandIO)
}

func newCommandHandler(service commandService) CommandHandler {
	return func(request cli.Request, commandIO CommandIO) (cli.Response, error) {
		switch command := request.(type) {
		case cli.UnsupportedRequest:
			return newUnsupportedResponse(command)
		case cli.InitRequest:
			initializer, ok := service.(initCommandService)
			if !ok {
				return cli.Response{}, fmt.Errorf("init command service is unavailable")
			}
			return initResponse(initializer.Init(context.Background(), command))
		case cli.ValidateRequest:
			return validateResponse(service.Validate(context.Background(), command.Source()))
		case cli.BuildRequest:
			builder, ok := service.(buildCommandService)
			if !ok {
				return cli.Response{}, fmt.Errorf("build command service is unavailable")
			}
			return buildResponse(builder.Build(context.Background(), command))
		case cli.SyncRequest:
			if v1, ok := service.(v1CommandService); ok {
				return v1.Sync(context.Background(), command, commandIO)
			}
		case cli.RollbackRequest:
			if v1, ok := service.(v1CommandService); ok {
				return v1.Rollback(context.Background(), command, commandIO)
			}
		case cli.HistoryRequest:
			if v1, ok := service.(v1CommandService); ok {
				return v1.History(context.Background(), command)
			}
		case cli.HistoryPurgeRequest:
			if v1, ok := service.(v1CommandService); ok {
				return v1.HistoryPurge(context.Background(), command, commandIO)
			}
		case cli.InstallRequest:
			if command.V1() {
				if v1, ok := service.(v1CommandService); ok {
					return v1.Install(context.Background(), command, commandIO)
				}
			}
			if command.DryRun() {
				report := service.Validate(context.Background(), command.Source())
				if len(report.Problems) != 0 {
					return planInstallResponse(report, nil)
				}
				conflicts, problem := service.InspectPlanInstall(context.Background())
				if problem != nil {
					report.Problems = []result.Problem{*problem}
					report.Failure = validation.FailureEnvironment
					return planInstallResponse(report, nil)
				}
				return planInstallResponse(report, conflicts)
			}
			return service.Install(context.Background(), command, commandIO)
		case cli.UpdateRequest:
			if command.V1() {
				if v1, ok := service.(v1CommandService); ok {
					return v1.Update(context.Background(), command, commandIO)
				}
			}
			if command.DryRun() {
				return planUpdateResponse(context.Background(), service)
			}
			return service.Update(context.Background(), command, commandIO)
		case cli.ListRequest:
			lister, ok := service.(listCommandService)
			if !ok {
				return cli.Response{}, fmt.Errorf("list command service is unavailable")
			}
			return lister.List(context.Background(), command)
		case cli.UninstallRequest:
			if command.V1() {
				if v1, ok := service.(v1CommandService); ok {
					return v1.Uninstall(context.Background(), command, commandIO)
				}
			}
			if command.DryRun() {
				return planUninstallResponse(context.Background(), service)
			}
			return service.Uninstall(context.Background(), command, commandIO)
		case cli.StatusRequest:
			return service.Status(context.Background(), command)
		case cli.DoctorRequest:
			doctor, ok := service.(doctorCommandService)
			if !ok {
				return cli.Response{}, fmt.Errorf("doctor command service is unavailable")
			}
			return doctor.Doctor(context.Background(), command, commandIO)
		default:
			return cli.Response{}, fmt.Errorf("command %q is not implemented", request.Command())
		}
		return cli.Response{}, fmt.Errorf("command %q service is unavailable", request.Command())
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
