// Package app owns the application runner invoked by the process entry point.
package app

import (
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

// Renderer presents one validated response and returns its process exit code.
type Renderer func(io.Writer, cli.Response) (result.ExitCode, error)

// CommandHandler handles one already parsed non-version command. Lifecycle
// stories provide the production implementation behind this seam.
type CommandIO struct {
	Input       io.Reader
	Output      io.Writer
	Interactive bool
}

type CommandHandler func(cli.Request, CommandIO) (cli.Response, error)

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
	parser        cli.Parser
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
		parser:        cli.NewParser(dependencies.Build.TargetOS()),
		version:       versionHandler{build: dependencies.Build, defaultSource: dependencies.DefaultSource},
		otherCommands: dependencies.OtherCommands,
		human:         dependencies.Human,
		json:          dependencies.JSON,
	}, nil
}

// Run parses the complete argv vector before constructing non-version command
// dependencies. Version and usage paths do not read stdin.
func (a Application) Run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	request, err := a.parser.Parse(argv)
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
	if unsupported, ok := request.(cli.UnsupportedRequest); ok {
		response, err = newUnsupportedResponse(unsupported)
	} else if request.Command() == cli.CommandVersion {
		response, err = a.version.Handle(request)
	} else {
		response, err = a.handleOther(request, CommandIO{Input: stdin, Output: stdout, Interactive: terminalPair(stdin, stdout)})
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

func (a Application) handleOther(request cli.Request, commandIO CommandIO) (cli.Response, error) {
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
	return handler(request, commandIO)
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

func newUsageResponse(usageError *cli.UsageError) (cli.Response, error) {
	option := usageError.Option()
	data, err := cli.NewUsageData(usageError.Issue(), option)
	if err != nil {
		option = ""
		data, err = cli.NewUsageData(usageError.Issue(), option)
	}
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
	problem, err := result.NewProblem("invalid_cli_usage", "command line does not match the MVP grammar", context)
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

func newUnsupportedResponse(request cli.UnsupportedRequest) (cli.Response, error) {
	message := request.Message()
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
	return cli.NewResponse(request.Command(), commandResult, nil, cli.UnavailableData{})
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
// exact compiled default-source policy. Lifecycle command composition is added
// by its owning stories.
func Run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	defaultSource, err := productionDefaultSource()
	if err != nil {
		writeOutputFailure(stderr)
		return result.ExitUnexpectedInternal.Int()
	}
	build := buildinfo.Read()
	application, err := NewApplication(Dependencies{
		Build:         build,
		DefaultSource: defaultSource,
		OtherCommands: productionOtherCommands(build),
		Human:         human.Render,
		JSON:          jsonout.Render,
	})
	if err != nil {
		writeOutputFailure(stderr)
		return result.ExitUnexpectedInternal.Int()
	}
	return application.Run(argv, stdin, stdout, stderr)
}
