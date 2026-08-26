package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	validation "github.com/alx4j/ai4j/internal/validate"
)

const (
	doctorStartupTimeout = 5 * time.Second
	maximumMCPManifest   = 1 << 20
)

var environmentReference = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

type doctorProcessRunner interface {
	LookPath(string) (string, error)
	RunIsolated(context.Context, string, string, []string, []string) (validation.ProcessResult, error)
}

type doctorService struct {
	state  installstate.Store
	status statusService
	native nativeStatusInspector
	runner doctorProcessRunner
}

type mcpDefinition struct {
	Command     string
	Arguments   []string
	Environment []string
}

func newDoctorService(state installstate.Store, status statusService, native nativeStatusInspector, runner doctorProcessRunner) *doctorService {
	return &doctorService{state: state, status: status, native: native, runner: runner}
}

func (d *doctorService) Doctor(ctx context.Context, request cli.DoctorRequest, commandIO CommandIO) (cli.Response, error) {
	checks, record, definitions, problems := d.inspectStatic(ctx, request.InstallationID().String())
	if len(problems) != 0 {
		data, err := cli.NewDoctorData(request.InstallationID(), checks, nil)
		if err != nil {
			return cli.Response{}, err
		}
		failure := result.FailureRecovery
		if problems[0].Code() == "installation_not_found" {
			failure = result.FailureValidation
		}
		return doctorResponse(data, result.StatusError, failure, problems)
	}
	if !request.HasMCPTest() {
		data, err := cli.NewDoctorData(request.InstallationID(), checks, nil)
		if err != nil {
			return cli.Response{}, err
		}
		status := result.StatusOK
		if hasDoctorWarning(checks) {
			status = result.StatusDegraded
		}
		return doctorResponse(data, status, result.FailureNone, nil)
	}

	definition, present := definitions[request.TestMCP()]
	if !present {
		checks = append(checks, doctorCheck("mcp_selection", cli.DoctorCheckError, "selected MCP server is not present in the retained native package"))
		data, err := cli.NewDoctorData(request.InstallationID(), checks, nil)
		if err != nil {
			return cli.Response{}, err
		}
		return doctorResponse(data, result.StatusError, result.FailureValidation, []result.Problem{doctorProblem("mcp_not_found", "selected MCP server was not found")})
	}
	executable, lookupErr := d.runner.LookPath(definition.Command)
	startup := newStartupCheck(request.TestMCP(), definition, executable, record.ScopeRoot, "not_run", 0, false)
	if lookupErr != nil {
		checks = append(checks, doctorCheck("mcp_executable", cli.DoctorCheckError, "selected MCP executable is unavailable"))
		data, err := cli.NewDoctorData(request.InstallationID(), checks, &startup)
		if err != nil {
			return cli.Response{}, err
		}
		return doctorResponse(data, result.StatusError, result.FailureEnvironment, []result.Problem{doctorProblem("mcp_executable_unavailable", "selected MCP executable is unavailable")})
	}
	environment, missing := isolatedDoctorEnvironment(definition.Environment)
	if len(missing) != 0 {
		checks = append(checks, doctorCheck("mcp_environment", cli.DoctorCheckError, "required MCP environment variables are missing: "+strings.Join(missing, ",")))
		data, err := cli.NewDoctorData(request.InstallationID(), checks, &startup)
		if err != nil {
			return cli.Response{}, err
		}
		return doctorResponse(data, result.StatusError, result.FailureEnvironment, []result.Problem{doctorProblem("mcp_environment_missing", "selected MCP server requires environment variables that are not present")})
	}
	if !request.Approved() {
		data, err := cli.NewDoctorData(request.InstallationID(), checks, &startup)
		if err != nil {
			return cli.Response{}, err
		}
		approval, approvalErr := approveDoctorInteractively(request.OutputMode(), commandIO, data)
		if approvalErr != nil {
			return cli.Response{}, approvalErr
		}
		if approval != approvalGranted {
			return doctorResponse(data, result.StatusError, result.FailureApproval, []result.Problem{doctorProblem("approval_required", "MCP startup requires explicit approval")})
		}
	}

	startupContext, cancel := context.WithTimeout(ctx, doctorStartupTimeout)
	processResult, runErr := d.runner.RunIsolated(startupContext, record.ScopeRoot, executable, definition.Arguments, environment)
	cancel()
	startupResult := "failed"
	status := result.StatusError
	failure := result.FailureEnvironment
	var executionProblems []result.Problem
	hasExitCode := false
	exitCode := 0
	switch {
	case processResult.Started && processResult.TimedOut && errors.Is(runErr, context.DeadlineExceeded):
		startupResult = "timed_out"
		status = result.StatusOK
		failure = result.FailureNone
	case runErr == nil && processResult.Started && processResult.ExitCode == 0:
		startupResult = "exited"
		status = result.StatusOK
		failure = result.FailureNone
		hasExitCode = true
	case runErr == nil && processResult.Started:
		startupResult = "failed"
		hasExitCode = true
		exitCode = processResult.ExitCode
		executionProblems = []result.Problem{doctorProblem("process_startup_check_failed", "MCP process exited unsuccessfully during the startup check")}
	default:
		executionProblems = []result.Problem{doctorProblem("process_startup_check_failed", "MCP process could not be started")}
	}
	startup = newStartupCheck(request.TestMCP(), definition, executable, record.ScopeRoot, startupResult, exitCode, hasExitCode)
	checks = append(checks, doctorCheck("process_startup_check", doctorStatus(status == result.StatusOK), "sanitized MCP process startup result: "+startupResult))
	data, err := cli.NewDoctorData(request.InstallationID(), checks, &startup)
	if err != nil {
		return cli.Response{}, err
	}
	return doctorResponse(data, status, failure, executionProblems)
}

func (d *doctorService) inspectStatic(ctx context.Context, installationID string) ([]cli.DoctorCheck, installstate.Record, map[string]mcpDefinition, []result.Problem) {
	checks := []cli.DoctorCheck{doctorCheck("host", cli.DoctorCheckOK, "running on "+runtime.GOOS+"/"+runtime.GOARCH)}
	record, present, err := d.state.LoadByID(installationID)
	if err != nil {
		checks = append(checks, doctorCheck("state", cli.DoctorCheckError, "installation state is unreadable or invalid"))
		return checks, installstate.Record{}, nil, []result.Problem{doctorProblem("state_invalid", "installation state is unreadable or invalid")}
	}
	if !present {
		checks = append(checks, doctorCheck("state", cli.DoctorCheckError, "installation was not found"))
		return checks, installstate.Record{}, nil, []result.Problem{doctorProblem("installation_not_found", "selected installation was not found")}
	}
	checks = append(checks, doctorCheck("state", cli.DoctorCheckOK, "installation state is structurally valid"))
	if info, statErr := os.Lstat(d.state.Path()); statErr == nil && info.Mode().IsRegular() {
		checks = append(checks, doctorCheck("state_permissions", cli.DoctorCheckOK, "state is stored as a regular file under the private AI4J state root"))
	} else {
		checks = append(checks, doctorCheck("state_permissions", cli.DoctorCheckWarning, "state file permissions or type could not be confirmed"))
	}
	if hostMatches(record.Host) {
		checks = append(checks, doctorCheck("host_profile", cli.DoctorCheckOK, "installation host profile matches the current host"))
	} else {
		checks = append(checks, doctorCheck("host_profile", cli.DoctorCheckWarning, "installation host profile differs from the current host"))
	}
	for _, executable := range []string{"git", record.Target} {
		id := executable + "_executable"
		if _, lookupErr := d.runner.LookPath(executable); lookupErr != nil {
			checks = append(checks, doctorCheck(id, cli.DoctorCheckWarning, executable+" executable was not found"))
		} else {
			checks = append(checks, doctorCheck(id, cli.DoctorCheckOK, executable+" executable is available"))
		}
	}
	history, historyErr := d.state.LoadHistory(record.InstallationID)
	if historyErr != nil {
		checks = append(checks, doctorCheck("history", cli.DoctorCheckError, "history is unreadable or invalid"))
		return checks, record, nil, []result.Problem{doctorProblem("history_invalid", "installation history is unreadable or invalid")}
	}
	checks = append(checks, doctorCheck("history", cli.DoctorCheckOK, fmt.Sprintf("%d committed history entries are structurally valid", len(history))))
	_, markerPresent, markerErr := d.state.LoadMarker()
	if markerErr != nil {
		checks = append(checks, doctorCheck("journal", cli.DoctorCheckError, "operation journal is unreadable or invalid"))
		return checks, record, nil, []result.Problem{doctorProblem("journal_invalid", "operation journal is unreadable or invalid")}
	}
	if markerPresent {
		checks = append(checks, doctorCheck("journal", cli.DoctorCheckWarning, "an incomplete operation journal is present"))
	} else {
		checks = append(checks, doctorCheck("journal", cli.DoctorCheckOK, "no incomplete operation journal is present"))
	}
	for _, drift := range mustInspectDrift(d.status, record) {
		status := cli.DoctorCheckOK
		if drift.State() != cli.DriftUnchanged {
			status = cli.DoctorCheckWarning
		}
		checks = append(checks, doctorCheck("owned_"+sanitizeCheckID(drift.Resource()), status, drift.Resource()+" is "+string(drift.State())))
	}
	if d.native != nil && record.MarketplaceID != "" {
		native, problem := d.native.InspectNativeStatusAt(ctx, nativeDirectory(record), record.MarketplaceID, record.PluginID+"@"+record.MarketplaceID)
		if problem != nil {
			checks = append(checks, doctorCheck("native_state", cli.DoctorCheckWarning, "target-native state is not observable"))
		} else {
			summary := fmt.Sprintf("declared=%t installed=%t enabled=%t", native.MarketplaceRegistered, native.PluginInstalled, native.PluginEnabled)
			checks = append(checks, doctorCheck("native_state", cli.DoctorCheckOK, summary))
		}
	}
	artifact := currentDoctorArtifact(record, history)
	if len(artifact) == 0 {
		checks = append(checks, doctorCheck("package_artifact", cli.DoctorCheckWarning, "retained native package artifact is unavailable"))
	} else {
		checks = append(checks, doctorCheck("package_artifact", cli.DoctorCheckOK, "retained native package artifact is present and bounded"))
	}
	definitions, artifactErr := parseMCPDefinitions(artifact)
	if artifactErr != nil {
		checks = append(checks, doctorCheck("mcp_registration", cli.DoctorCheckWarning, "MCP declarations are unavailable in retained package history"))
		return checks, record, nil, nil
	}
	checks = append(checks, doctorCheck("mcp_registration", cli.DoctorCheckOK, fmt.Sprintf("%d MCP server declarations are structurally valid", len(definitions))))
	serverIDs := make([]string, 0, len(definitions))
	for id := range definitions {
		serverIDs = append(serverIDs, id)
	}
	sort.Strings(serverIDs)
	for _, id := range serverIDs {
		definition := definitions[id]
		if _, lookupErr := d.runner.LookPath(definition.Command); lookupErr != nil {
			checks = append(checks, doctorCheck("mcp_"+sanitizeCheckID(id)+"_executable", cli.DoctorCheckWarning, "declared MCP executable is unavailable"))
		} else {
			checks = append(checks, doctorCheck("mcp_"+sanitizeCheckID(id)+"_executable", cli.DoctorCheckOK, "declared MCP executable is available"))
		}
		_, missing := isolatedDoctorEnvironment(definition.Environment)
		status := cli.DoctorCheckOK
		summary := "all referenced environment variables are present"
		if len(missing) != 0 {
			status = cli.DoctorCheckWarning
			summary = "missing referenced environment variables: " + strings.Join(missing, ",")
		}
		checks = append(checks, doctorCheck("mcp_"+sanitizeCheckID(id)+"_environment", status, summary))
	}
	return checks, record, definitions, nil
}

func currentDoctorArtifact(record installstate.Record, history []installstate.HistoryEntry) []byte {
	for index := len(history) - 1; index >= 0; index-- {
		entry := history[index]
		if entry.After != nil && entry.After.InstallationID == record.InstallationID && len(entry.NativeArtifactAfter) != 0 {
			return slices.Clone(entry.NativeArtifactAfter)
		}
	}
	return nil
}

func parseMCPDefinitions(artifact []byte) (map[string]mcpDefinition, error) {
	if len(artifact) == 0 || len(artifact) > 16<<20 {
		return nil, errors.New("native artifact is unavailable")
	}
	archive, err := zip.NewReader(bytes.NewReader(artifact), int64(len(artifact)))
	if err != nil || len(archive.File) > 4096 {
		return nil, errors.New("native artifact is invalid")
	}
	definitions := make(map[string]mcpDefinition)
	for _, file := range archive.File {
		if path.Base(file.Name) != ".mcp.json" {
			continue
		}
		if file.UncompressedSize64 > maximumMCPManifest {
			return nil, errors.New("MCP manifest exceeds bounds")
		}
		reader, openErr := file.Open()
		if openErr != nil {
			return nil, openErr
		}
		contents, readErr := io.ReadAll(io.LimitReader(reader, maximumMCPManifest+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || len(contents) > maximumMCPManifest {
			return nil, errors.New("MCP manifest is unreadable")
		}
		var document struct {
			Servers map[string]struct {
				Type    string            `json:"type"`
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"mcpServers"`
		}
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&document) != nil || decoder.Decode(new(any)) != io.EOF {
			return nil, errors.New("MCP manifest is invalid")
		}
		for id, server := range document.Servers {
			if !regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`).MatchString(id) || server.Type != "stdio" || !safeProcessField(server.Command) || len(server.Args) > 256 {
				return nil, errors.New("MCP declaration is unsupported")
			}
			for _, argument := range server.Args {
				if !safeProcessField(argument) || environmentReference.MatchString(argument) {
					return nil, errors.New("MCP arguments are unsupported")
				}
			}
			environment := make([]string, 0, len(server.Env))
			for name, value := range server.Env {
				match := environmentReference.FindStringSubmatch(value)
				if len(match) != 2 || match[1] != name {
					return nil, errors.New("MCP environment must use named references")
				}
				environment = append(environment, name)
			}
			sort.Strings(environment)
			definitions[id] = mcpDefinition{Command: server.Command, Arguments: slices.Clone(server.Args), Environment: environment}
		}
	}
	if len(definitions) == 0 || len(definitions) > 16 {
		return nil, errors.New("MCP declaration is unavailable")
	}
	return definitions, nil
}

func isolatedDoctorEnvironment(required []string) ([]string, []string) {
	names := []string{"PATH"}
	if runtime.GOOS == "windows" {
		names = append(names, "SystemRoot", "TEMP", "TMP", "USERPROFILE")
	} else {
		names = append(names, "HOME", "TMPDIR")
	}
	names = append(names, required...)
	sort.Strings(names)
	names = slices.Compact(names)
	environment := make([]string, 0, len(names))
	missingRequired := make([]string, 0)
	for _, name := range names {
		value, present := os.LookupEnv(name)
		if !present || strings.ContainsRune(value, 0) {
			if slices.Contains(required, name) {
				missingRequired = append(missingRequired, name)
			}
			continue
		}
		environment = append(environment, name+"="+value)
	}
	return environment, missingRequired
}

func approveDoctorInteractively(outputMode cli.OutputMode, commandIO CommandIO, data cli.DoctorData) (approvalDecision, error) {
	if outputMode == cli.OutputJSON {
		return approvalMissing, nil
	}
	preview, err := doctorResponse(data, result.StatusDegraded, result.FailureNone, nil)
	if err != nil {
		return approvalMissing, err
	}
	return promptApproval(commandIO, preview, "Start this MCP process for up to 5 seconds? [y/N]: ")
}

func newStartupCheck(serverID string, definition mcpDefinition, executable, workingDirectory, startupResult string, exitCode int, hasExitCode bool) cli.MCPStartupCheck {
	if executable == "" {
		executable = definition.Command
	}
	value, err := cli.NewMCPStartupCheck(serverID, executable, definition.Arguments, definition.Environment, workingDirectory, "package", startupResult, exitCode, hasExitCode)
	if err != nil {
		panic(err)
	}
	return value
}

func mustInspectDrift(status statusService, record installstate.Record) []cli.Drift {
	items, err := status.inspectDrift(record)
	if err != nil {
		return nil
	}
	return items
}

func doctorResponse(data cli.DoctorData, status result.Status, failure result.Failure, problems []result.Problem) (cli.Response, error) {
	var warnings []result.Warning
	if status == result.StatusDegraded {
		warning, err := result.NewWarning("doctor_warning", "one or more static checks require attention", nil)
		if err != nil {
			return cli.Response{}, err
		}
		warnings = []result.Warning{warning}
	}
	commandResult, err := result.New(result.Facts{
		Status: status, Phase: result.PhaseNone, Outcome: result.OutcomeNone, Mutation: result.MutationNotStarted,
		DurableChange: result.DurableChangeNone, Failure: failure, UpdateDisposition: result.UpdateNotChecked, Warnings: warnings, Errors: problems,
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandDoctor, commandResult, nil, data)
}

func doctorCheck(id string, status cli.DoctorCheckStatus, summary string) cli.DoctorCheck {
	value, err := cli.NewDoctorCheck(id, status, summary)
	if err != nil {
		panic(err)
	}
	return value
}

func doctorProblem(code, message string) result.Problem {
	value, err := result.NewProblem(code, message, nil)
	if err != nil {
		panic(err)
	}
	return value
}

func doctorStatus(ok bool) cli.DoctorCheckStatus {
	if ok {
		return cli.DoctorCheckOK
	}
	return cli.DoctorCheckError
}

func hasDoctorWarning(checks []cli.DoctorCheck) bool {
	for _, check := range checks {
		if check.Status() != cli.DoctorCheckOK {
			return true
		}
	}
	return false
}

func hostMatches(host string) bool {
	return host == "darwin-arm64" && runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" ||
		host == "windows-amd64" && runtime.GOOS == "windows" && runtime.GOARCH == "amd64"
}

func sanitizeCheckID(value string) string {
	var output strings.Builder
	for _, character := range strings.ToLower(value) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' {
			output.WriteRune(character)
		} else {
			output.WriteByte('_')
		}
	}
	value = strings.Trim(output.String(), "_")
	if len(value) > 40 {
		value = value[:40]
	}
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return "resource"
	}
	return value
}

func safeProcessField(value string) bool {
	if value == "" || len(value) > 16<<10 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
