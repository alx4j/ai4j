// Package app owns the application runner invoked by the process entry point.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/alx4j/ai4j/internal/buildinfo"
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/human"
	"github.com/alx4j/ai4j/internal/cli/jsonout"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
)

const outputFailureMessage = "ai4j: output failed\n"

const codexNativeLifecycleUnavailable = "Codex exposes plugin lifecycle only through its interactive native plugin browser; build the Codex package with ai4j, then install or manage it through /plugins or the Codex desktop plugin browser"

// Renderer presents one validated response and returns its process exit code.
type Renderer func(io.Writer, cli.Response) (result.ExitCode, error)

// CommandHandler handles one already parsed non-version command.
type CommandIO struct {
	Input       io.Reader
	Output      io.Writer
	Progress    io.Writer
	Interactive bool
	logSession  *commandLogSession
}

type CommandHandler func(context.Context, cli.Request, CommandIO) (cli.Response, error)

func (c CommandIO) bindLogInstallation(installation domain.InstallationID) {
	if c.logSession != nil {
		c.logSession.bindInstallation(installation)
	}
}

// OtherCommandsFactory lazily constructs the non-version command handler.
// Parsing, usage failures, and version handling never call this factory.
type OtherCommandsFactory func() (CommandHandler, error)

// Dependencies are immutable, caller-supplied application facts and seams.
type Dependencies struct {
	Build         buildinfo.Info
	DefaultSource cli.DefaultSource
	OtherCommands OtherCommandsFactory
	Human         Renderer
	JSON          Renderer
}

// Application is a value-only composition root for one AI4J process.
type Application struct {
	version       versionHandler
	otherCommands OtherCommandsFactory
	human         Renderer
	json          Renderer
}

// NewApplication snapshots dependencies without constructing lifecycle or
// adapter-backed command handlers.
func NewApplication(dependencies Dependencies) (Application, error) {
	if dependencies.Human == nil || dependencies.JSON == nil {
		return Application{}, fmt.Errorf("application requires both renderers")
	}
	return Application{
		version:       versionHandler{build: dependencies.Build, defaultSource: dependencies.DefaultSource},
		otherCommands: dependencies.OtherCommands,
		human:         dependencies.Human,
		json:          dependencies.JSON,
	}, nil
}

// Run parses the complete argv vector before constructing non-version command
// dependencies. Version and usage paths do not read stdin.
func (a Application) Run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return a.RunContext(context.Background(), argv, stdin, stdout, stderr)
}

// RunContext runs one command and propagates cancellation to source, target,
// and host operations after parsing succeeds.
func (a Application) RunContext(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	request, err := cli.Parse(argv)
	if err != nil {
		var usageError *cli.UsageError
		if !errors.As(err, &usageError) {
			writeOutputFailure(stderr)
			return result.ExitUnexpectedInternal.Int()
		}
		response, responseErr := newUsageResponse(usageError)
		if responseErr != nil {
			writeOutputFailure(stderr)
			return result.ExitUnexpectedInternal.Int()
		}
		mode := cli.OutputHuman
		if usageError.JSONRequested() {
			mode = cli.OutputJSON
		}
		return a.render(mode, stdout, stderr, response)
	}

	var response cli.Response
	if message, unsupported := unsupportedCapability(request); unsupported {
		response, err = newUnsupportedResponse(request.Command(), message)
	} else if request.Command() == cli.CommandVersion {
		response, err = a.version.Handle(request)
	} else {
		interactive := terminalPair(stdin, stdout)
		commandIO := CommandIO{Input: stdin, Output: stdout, Interactive: interactive}
		if request.OutputMode() == cli.OutputHuman && interactive {
			commandIO.Progress = stderr
			reportProgress(commandIO, commandLogInitialStage(request.Command()), commandProgressMessage(request.Command()))
		}
		response, err = a.handleOther(ctx, request, commandIO)
	}
	if isContextCancellation(err) {
		response, err = cancelledResponse(request.Command(), "command_cancelled", "command was cancelled before mutation", result.UpdateNotChecked, nil)
	}
	if err != nil || !response.Valid() || response.Command() != request.Command() {
		response, err = newUnavailableResponse(request.Command(), "command_unavailable", "command implementation is unavailable")
		if err != nil {
			writeOutputFailure(stderr)
			return result.ExitUnexpectedInternal.Int()
		}
	}
	return a.render(request.OutputMode(), stdout, stderr, response)
}

func (a Application) handleOther(ctx context.Context, request cli.Request, commandIO CommandIO) (cli.Response, error) {
	if a.otherCommands == nil {
		return cli.Response{}, fmt.Errorf("non-version commands are not composed")
	}
	handler, err := a.otherCommands()
	if err != nil {
		return cli.Response{}, err
	}
	if handler == nil {
		return cli.Response{}, fmt.Errorf("non-version command factory returned no handler")
	}
	return handler(ctx, request, commandIO)
}

func terminalPair(input io.Reader, output io.Writer) bool {
	in, inputOK := input.(*os.File)
	out, outputOK := output.(*os.File)
	if !inputOK || !outputOK {
		return false
	}
	inInfo, inErr := in.Stat()
	outInfo, outErr := out.Stat()
	return inErr == nil && outErr == nil && inInfo.Mode()&os.ModeCharDevice != 0 && outInfo.Mode()&os.ModeCharDevice != 0
}

func (a Application) render(mode cli.OutputMode, stdout, stderr io.Writer, response cli.Response) int {
	renderer := a.human
	if mode == cli.OutputJSON {
		renderer = a.json
	}
	exitCode, err := renderer(stdout, response)
	if err != nil || exitCode != response.Result().ExitCode() {
		writeOutputFailure(stderr)
		return result.ExitUnexpectedInternal.Int()
	}
	return exitCode.Int()
}

func writeOutputFailure(stderr io.Writer) {
	if stderr != nil {
		_, _ = io.WriteString(stderr, outputFailureMessage)
	}
}

func reportProgress(commandIO CommandIO, stage, message string) {
	if commandIO.logSession != nil {
		commandIO.logSession.progress(stage)
	}
	if commandIO.Progress == nil || message == "" {
		return
	}
	_, _ = fmt.Fprintln(commandIO.Progress, "ai4j:", message)
}

func commandProgressMessage(command cli.Command) string {
	switch command {
	case cli.CommandInit:
		return "creating and validating the toolkit..."
	case cli.CommandValidate:
		return "resolving and validating the toolkit..."
	case cli.CommandBuild:
		return "resolving, validating, and building the toolkit..."
	case cli.CommandInstall:
		return "checking the source and preparing the installation plan..."
	case cli.CommandUpdate:
		return "checking the source and preparing the update plan..."
	case cli.CommandSync:
		return "checking the selected content and preparing the synchronization plan..."
	case cli.CommandList:
		return "loading managed installations..."
	case cli.CommandStatus:
		return "checking installation health and source updates..."
	case cli.CommandDoctor:
		return "running installation diagnostics..."
	case cli.CommandRollback:
		return "checking saved history and preparing the rollback plan..."
	case cli.CommandUninstall:
		return "checking managed resources and preparing the removal plan..."
	case cli.CommandHistory:
		return "loading installation history..."
	case cli.CommandHistoryPurge:
		return "checking saved history and preparing the cleanup plan..."
	default:
		return ""
	}
}

func newUsageResponse(usageError *cli.UsageError) (cli.Response, error) {
	option := usageError.Option()
	data, err := cli.NewDetailedUsageData(usageError.Issue(), option, usageError.Command())
	if err != nil {
		return cli.Response{}, err
	}
	issue, err := result.NewContext("issue", string(usageError.Issue()))
	if err != nil {
		return cli.Response{}, err
	}
	context := []result.Context{issue}
	if option != "" {
		optionContext, optionErr := result.NewContext("option", option)
		if optionErr != nil {
			return cli.Response{}, optionErr
		}
		context = append(context, optionContext)
	}
	problem, err := result.NewProblem("invalid_cli_usage", "command line does not match the CLI grammar", context)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := neutralResult(result.StatusError, result.FailureUsage, []result.Problem{problem})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse("", commandResult, nil, data)
}

func newUnavailableResponse(command cli.Command, code, message string) (cli.Response, error) {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := neutralResult(result.StatusError, result.FailureInternal, []result.Problem{problem})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}

func unsupportedCapability(request cli.Request) (string, bool) {
	switch command := request.(type) {
	case cli.ValidateRequest:
		return codexNativeLifecycleUnavailable, command.Target() == cli.BuildTargetCodex
	case cli.InstallRequest:
		return codexNativeLifecycleUnavailable, command.Target() == cli.BuildTargetCodex
	default:
		return "", false
	}
}

func newUnsupportedResponse(command cli.Command, message string) (cli.Response, error) {
	if message == "" {
		message = "command capability is not available in this release"
	}
	problem, err := result.NewProblem("unsupported_capability", message, nil)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := neutralResult(result.StatusError, result.FailureEnvironment, []result.Problem{problem})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}

func isContextCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func mutationLockResponse(command cli.Command, err error, disposition result.UpdateDisposition, warnings []result.Warning) (cli.Response, error) {
	if isContextCancellation(err) {
		return cancelledResponse(command, "command_cancelled", "command was cancelled while waiting to start", disposition, warnings)
	}
	return lifecycleFailure(command, result.FailureConflict, "mutation_locked", "another AI4J modifying command is running", disposition, warnings)
}

func cancelledResponse(command cli.Command, code, message string, disposition result.UpdateDisposition, warnings []result.Warning) (cli.Response, error) {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusCancelled, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureCancellation, UpdateDisposition: disposition, Warnings: warnings, Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}

func neutralResult(status result.Status, failure result.Failure, problems []result.Problem) (result.Result, error) {
	return result.New(result.Facts{
		Status:            status,
		Phase:             result.PhaseNone,
		Outcome:           result.OutcomeNone,
		Mutation:          result.MutationNotStarted,
		DurableChange:     result.DurableChangeNone,
		Failure:           failure,
		UpdateDisposition: result.UpdateNotChecked,
		Errors:            problems,
	})
}

func productionDefaultSource() (cli.DefaultSource, error) {
	repository, err := domain.NewRepositoryIdentity(buildinfo.RepositoryIdentity)
	if err != nil {
		return cli.DefaultSource{}, err
	}
	return cli.NewDefaultSource(repository, "", cli.DefaultRepositoryBranch)
}

// Run executes the production composition using embedded build facts and the
// exact compiled default-source policy.
func Run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return RunContext(context.Background(), argv, stdin, stdout, stderr)
}

// RunContext executes the production composition with caller cancellation.
func RunContext(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	defaultSource, err := productionDefaultSource()
	if err != nil {
		writeOutputFailure(stderr)
		return result.ExitUnexpectedInternal.Int()
	}
	build := buildinfo.Read()
	tool := commandLogToolName(argv)
	application, err := NewApplication(Dependencies{
		Build:         build,
		DefaultSource: defaultSource,
		OtherCommands: productionOtherCommands(build, tool),
		Human:         human.Render,
		JSON:          jsonout.Render,
	})
	if err != nil {
		writeOutputFailure(stderr)
		return result.ExitUnexpectedInternal.Int()
	}
	return application.RunContext(ctx, argv, stdin, stdout, stderr)
}
