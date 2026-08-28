// Package human renders CLI responses as bounded deterministic plain text.
package human

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/result"
)

const maxOutputBytes = 1 << 20

// ErrOutputTooLarge reports that a valid response cannot fit within the
// bounded human-output contract.
var ErrOutputTooLarge = errors.New("human output exceeds the size limit")

// ErrUnsupportedData fails closed if the sealed CLI data set is extended
// without adding a human rendering contract.
var ErrUnsupportedData = errors.New("human renderer does not support the response data")

// Render validates and fully materializes plain text before writing it. Human
// rendering has no JSON dependency and emits no ANSI control sequences.
func Render(output io.Writer, response cli.Response) (result.ExitCode, error) {
	if !response.Valid() {
		return result.ExitUnexpectedInternal, fmt.Errorf("render human response: invalid response")
	}
	exitCode := response.Result().ExitCode()
	if output == nil {
		return exitCode, fmt.Errorf("render human response: output is required")
	}

	buffer := boundedBuffer{limit: maxOutputBytes}
	renderEnvelope(&buffer, response)
	if buffer.err != nil {
		return exitCode, fmt.Errorf("render human response: %w", buffer.err)
	}

	document := []byte(buffer.String())
	written, err := output.Write(document)
	if err != nil {
		return exitCode, fmt.Errorf("render human response: %w", err)
	}
	if written != len(document) {
		return exitCode, fmt.Errorf("render human response: %w", io.ErrShortWrite)
	}
	return exitCode, nil
}

func renderEnvelope(output *boundedBuffer, response cli.Response) {
	commandResult := response.Result()

	switch data := response.Data().(type) {
	case cli.UsageData:
		renderUsage(output, data)
	case cli.InitData:
		renderInit(output, data)
	case cli.ValidateData:
		renderValidate(output, data)
	case cli.BuildData:
		renderBuild(output, data)
	case cli.PlanData:
		renderPlan(output, data, commandResult)
	case cli.MutationData:
		renderMutation(output, data, commandResult)
	case cli.HistoryData:
		renderHistory(output, data)
	case cli.ListData:
		renderList(output, data)
	case cli.StatusData:
		renderStatus(output, data, commandResult)
	case cli.DoctorData:
		renderDoctor(output, data, commandResult)
	case cli.VersionData:
		renderVersion(output, data)
	case cli.UnavailableData:
		command := "command"
		if response.Command().Valid() {
			command = response.Command().String()
		}
		output.line("AI4J could not complete the " + command + " command.")
		output.line("No detailed result is available.")
	default:
		output.fail(ErrUnsupportedData)
	}
	if response.HasOperationID() {
		output.line("")
		output.line("Operation ID: " + response.OperationID().String())
	}
	if _, usage := response.Data().(cli.UsageData); !usage {
		renderDiagnostics(output, "Warnings", commandResult.Warnings())
		renderProblems(output, commandResult.Errors())
	}
}

func renderDoctor(output *boundedBuffer, data cli.DoctorData, commandResult result.Result) {
	if commandResult.Status() == result.StatusOK || commandResult.Status() == result.StatusNoChange {
		output.line("AI4J found no problems with installation " + data.InstallationID().String() + ".")
	} else {
		output.line("AI4J diagnostics for installation " + data.InstallationID().String() + ":")
	}
	output.line("")
	for _, check := range data.Checks() {
		output.line("[" + doctorMarker(check.Status()) + "] " + check.Summary() + " (" + check.ID() + ")")
	}
	if startup, ok := data.StartupCheck(); ok {
		output.line("")
		output.line("MCP startup check")
		output.indentedField(1, "Server", startup.ServerID())
		output.indentedField(1, "Executable", startup.Executable())
		arguments := startup.Arguments()
		for index := range arguments {
			arguments[index] = strconv.Quote(arguments[index])
		}
		output.indentedField(1, "Arguments", strings.Join(arguments, " "))
		output.indentedField(1, "Working directory", startup.WorkingDirectory())
		output.indentedField(1, "Environment names", strings.Join(startup.Environment(), ", "))
		output.indentedField(1, "Result", humanize(startup.Result()))
		if startup.HasExitCode() {
			output.indentedField(1, "Process exit code", strconv.Itoa(startup.ExitCode()))
		}
		output.indentedLine(1, "This check runs the process with your current user permissions and may have side effects.")
	}
}

func renderList(output *boundedBuffer, data cli.ListData) {
	installations := data.Installations()
	if len(installations) == 0 {
		output.line("No AI4J-managed toolkit installations were found.")
		return
	}
	output.line("Found " + count(len(installations), "AI4J-managed installation", "AI4J-managed installations") + ".")
	for _, installation := range installations {
		output.line("")
		output.line(installation.ID().String())
		output.indentedField(1, "Toolkit", installation.ToolkitID())
		output.indentedField(1, "Target", humanize(string(installation.Target())))
		output.indentedField(1, "Scope", humanize(string(installation.Scope())))
		output.indentedField(1, "Location", installation.ScopeRoot())
		output.indentedField(1, "Lifecycle", humanize(installation.Lifecycle()))
		if installation.Source().Mode() == cli.SourceDevelopment {
			output.indentedField(1, "Source", installation.Source().Checkout()+" (local checkout)")
		} else {
			output.indentedField(1, "Source", installation.Source().Repository().String()+" at "+installation.Source().Commit().String())
		}
		renderInstallationSelection(output, installation, 1)
		output.indentedField(1, "Native packages", strings.Join(installation.Packages(), ", "))
		output.indentedField(1, "Resolved assets", strings.Join(installation.ResolvedAssets(), ", "))
		output.indentedField(1, "Recorded operations", strconv.Itoa(installation.HistoryCount()))
		output.indentedLine(1, "Next: ai4j status "+installation.ID().String())
	}
}

func renderInit(output *boundedBuffer, data cli.InitData) {
	targets := data.Targets()
	output.line("Created a toolkit for " + humanList(targetStrings(targets)) + " in " + data.OutputRoot() + ".")
	output.line("Validation: " + passFail(data.ValidationValid()) + ". Reproducible build: " + yesNo(data.Reproducible()) + ".")
	artifacts := data.Artifacts()
	output.line("")
	output.line("Created files (" + strconv.Itoa(len(artifacts)) + "):")
	for _, artifact := range artifacts {
		output.indentedLine(1, "- "+artifact.Path()+" ("+formatBytes(artifact.SizeBytes())+", SHA-256 "+artifact.Checksum()+")")
	}
}

func renderUsage(output *boundedBuffer, data cli.UsageData) {
	output.line(usageMessage(data))
	output.line("Issue code: " + string(data.Issue()))
	command, hasCommand := data.Command()
	if data.Option() != "" {
		if hasCommand && data.Option() == "installation" && usesPositionalInstallation(command) {
			output.line("Argument: <INSTALLATION_ID>")
		} else {
			output.line("Option: --" + data.Option())
		}
	}
	if hasCommand {
		output.line("Usage: " + commandUsage(command))
	} else {
		output.line("Available commands: init, validate, build, install, list, status, update, sync, doctor, history, rollback, uninstall, version")
	}
}

func usesPositionalInstallation(command cli.Command) bool {
	switch command {
	case cli.CommandUpdate, cli.CommandSync, cli.CommandStatus, cli.CommandDoctor, cli.CommandRollback, cli.CommandUninstall, cli.CommandHistory, cli.CommandHistoryPurge:
		return true
	default:
		return false
	}
}

func renderValidate(output *boundedBuffer, data cli.ValidateData) {
	if data.ValidationValid() {
		output.line("Toolkit validation passed.")
	} else {
		output.line("Toolkit validation failed with " + count(data.ErrorCount(), "error", "errors") + ".")
	}
	output.line("Warnings: " + strconv.Itoa(data.WarningCount()) + ".")
	renderSource(output, data.Source(), 0)
	renderContent(output, data.ActiveContent(), 0)
}

func renderBuild(output *boundedBuffer, data cli.BuildData) {
	output.line("Built the " + humanize(string(data.Target())) + " package for " + humanize(string(data.Host())) + " in " + data.OutputRoot() + ".")
	output.line("Validation: " + passFail(data.ValidationValid()) + ". Reproducible build: " + yesNo(data.Reproducible()) + ".")
	renderSource(output, data.Source(), 0)
	artifacts := data.Artifacts()
	output.line("")
	output.line("Artifacts (" + strconv.Itoa(len(artifacts)) + "):")
	for _, artifact := range artifacts {
		output.indentedLine(1, "- "+artifact.Path()+" ("+formatBytes(artifact.SizeBytes())+", SHA-256 "+artifact.Checksum()+")")
	}
	selection := data.Selection()
	output.line("")
	output.line("Selected content (" + strconv.Itoa(len(selection)) + "):")
	for _, item := range selection {
		output.indentedLine(1, "- "+item.Asset()+" ("+humanize(item.Variant())+"; "+humanize(item.Reason())+" via "+item.RequestedBy()+")")
	}
	renderContent(output, data.ActiveContent(), 0)
}

func renderPlan(output *boundedBuffer, data cli.PlanData, commandResult result.Result) {
	if len(data.Actions()) == 0 && len(data.Conflicts()) == 0 {
		output.line("No changes are needed for installation " + data.InstallationID().String() + ".")
	} else {
		output.line(planHeadline(data.Operation(), data.InstallationID().String()))
		output.line("Review the source, active content, and changes below before confirming.")
	}
	if data.HasSource() {
		renderSource(output, data.Source(), 0)
	}
	renderActions(output, data.Actions(), 0)
	renderContent(output, data.ActiveContent(), 0)
	conflicts := data.Conflicts()
	if len(conflicts) != 0 {
		output.line("")
		output.line("Conflicts (" + strconv.Itoa(len(conflicts)) + "):")
		for _, conflict := range conflicts {
			output.indentedLine(1, "- "+conflict.Message()+" ["+conflict.Code()+"]")
			output.indentedField(2, "Resource", conflict.Resource())
		}
	}
	renderFinalState(output, data.ExpectedFinalState(), 0)
	if commandResult.Status() == result.StatusError {
		output.line("")
		output.line("AI4J will not make changes while these conflicts remain.")
	}
}

func renderMutation(output *boundedBuffer, data cli.MutationData, commandResult result.Result) {
	operationResult := data.OperationResult()
	installation := "the selected installation"
	if data.HasInstallationID() {
		installation = "installation " + data.InstallationID().String()
	}
	switch {
	case commandResult.Status() == result.StatusNoChange:
		output.line("No changes were needed for " + installation + ".")
	case operationResult.Phase() == result.PhaseComplete:
		output.line(mutationSuccess(data.Operation(), data.InstallationID().String(), data.HasInstallationID()))
	case operationResult.Outcome() == result.OutcomeRolledBack:
		output.line("AI4J could not complete the operation and restored the previous state for " + installation + ".")
	case operationResult.Phase() == result.PhaseCommittedCleanupPending:
		output.line("The operation completed for " + installation + ", but cleanup still requires attention.")
	default:
		output.line("AI4J could not complete the operation for " + installation + ".")
	}
	if operationResult.Phase() != result.PhaseNone && operationResult.Phase() != result.PhaseComplete {
		output.line("Current phase: " + humanize(operationResult.Phase().String()) + ".")
	}
	renderActions(output, data.AppliedActions(), 0)
	renderFinalState(output, data.FinalState(), 0)
	if data.HasInstallationID() {
		switch data.Operation() {
		case cli.OperationInstall, cli.OperationUpdate, cli.OperationSync, cli.OperationRollback:
			output.line("")
			output.line("Next: ai4j status " + data.InstallationID().String())
		}
	}
}

func renderHistory(output *boundedBuffer, data cli.HistoryData) {
	entries := data.Entries()
	if len(entries) == 0 {
		output.line("Installation " + data.InstallationID().String() + " has no recorded operations.")
		return
	}
	output.line("History for installation " + data.InstallationID().String() + " (" + count(len(entries), "operation", "operations") + "):")
	for _, entry := range entries {
		restore := "rollback unavailable"
		if entry.Restorable() {
			restore = "rollback available"
		}
		output.indentedLine(1, "- "+entry.Timestamp().Format(time.RFC3339)+" - "+humanize(entry.Operation().String())+" ("+entry.OperationID().String()+", "+restore+")")
	}
}

func renderStatus(output *boundedBuffer, data cli.StatusData, commandResult result.Result) {
	recovery := data.RecoveryState()
	archived := statusIsArchived(data)
	if installation, present := data.Installation(); present {
		output.line(statusHeadline(data, commandResult, installation.ID().String()))
		output.line("")
		output.line("Toolkit")
		output.indentedField(1, "Name", installation.ToolkitID())
		output.indentedField(1, "Version", installation.ToolkitVersion())
		pluginIDs := installation.NativePluginIDs()
		pluginLabel := "Claude plugins"
		if len(pluginIDs) == 1 {
			pluginLabel = "Claude plugin"
		}
		output.indentedField(1, pluginLabel, strings.Join(pluginIDs, ", "))
		output.indentedField(1, "Installed by AI4J", installation.CLIVersion())
		if installation.HasExpectedNativeVersion() {
			output.indentedField(1, "Expected native version", installation.ExpectedNativeVersion())
		}
		renderRecordedSource(output, installation.Source(), 0)
		if summary, ok := data.Summary(); ok {
			output.line("")
			output.line("Placement")
			output.indentedField(1, "Target", humanize(string(summary.Target())))
			output.indentedField(1, "Scope", humanize(string(summary.Scope())))
			output.indentedField(1, "Location", summary.ScopeRoot())
			output.indentedField(1, "Lifecycle", humanize(summary.Lifecycle()))
			renderInstallationSelection(output, summary, 1)
			output.indentedField(1, "Native packages", strings.Join(summary.Packages(), ", "))
			output.indentedField(1, "Resolved assets", strings.Join(summary.ResolvedAssets(), ", "))
			output.indentedField(1, "Recorded operations", strconv.Itoa(summary.HistoryCount()))
			if summary.HasLastOperationID() {
				output.indentedField(1, "Last operation", summary.LastOperationID().String())
			}
		}
	} else if data.UpdateDisposition() == result.UpdateNotInstalled && recovery.State() == cli.RecoveryStateNone {
		output.line("AI4J could not find the selected installation.")
	} else {
		output.line("AI4J could not read the installation record.")
	}

	native := data.NativeState()
	if _, present := data.Installation(); present && !archived {
		output.line("")
		output.line("Claude integration")
		output.indentedLine(1, nativeLine("Marketplace", string(native.Registration()), native.Registration() == cli.NativeRegistered))
		output.indentedLine(1, nativeLine("Plugin", string(native.Installation()), native.Installation() == cli.NativeInstalled))
		output.indentedLine(1, nativeLine("Enablement", string(native.Enablement()), native.Enablement() == cli.NativeEnabled))
		if native.HasVersion() {
			output.indentedField(1, "Observed version", native.Version()+" ("+humanize(string(native.VersionStatus()))+")")
		}
	}

	drift := data.Drift()
	if len(drift) != 0 {
		output.line("")
		output.line("Managed files")
		for _, item := range drift {
			marker := "ATTENTION"
			if item.State() == cli.DriftUnchanged {
				marker = "OK"
			}
			output.indentedLine(1, "["+marker+"] "+item.Resource()+" - "+humanize(string(item.State())))
		}
	}
	if recovery.State() != cli.RecoveryStateNone {
		output.line("")
		output.line("Recovery: " + humanize(string(recovery.State())) + ".")
		if recovery.HasPhase() {
			output.line("Interrupted phase: " + humanize(recovery.Phase().String()) + ".")
		}
	}
	renderUpdateStatus(output, data)
}

func renderInstallationSelection(output *boundedBuffer, installation cli.InstallationSummary, indent int) {
	requested := installation.RequestedBundle()
	resolved := strings.Join(installation.ResolvedBundles(), ", ")
	if resolved == "" {
		resolved = "None recorded"
	}
	output.indentedField(indent, "Requested bundle", requested)
	output.indentedField(indent, "Resolved bundles", resolved)
}

func renderVersion(output *boundedBuffer, data cli.VersionData) {
	output.line(data.Product() + " " + data.CLIVersion() + " (" + data.TargetOS() + "/" + data.TargetArch() + ")")
	output.line("Executable: " + data.Executable())
	output.line("Built with " + data.GoVersion() + " at " + data.BuildTime().Format("2006-01-02T15:04:05Z") + ".")
	output.line("Source: " + data.CLISourceRepository().String() + " at " + data.CLISourceCommit().String())
	defaultSource := data.DefaultSource()
	if defaultSource.HasReference() {
		output.line("Default toolkit source: " + defaultSource.Repository().String() + " at " + defaultSource.Reference())
	} else {
		output.line("Default toolkit source: " + defaultSource.Repository().String() + " (repository default branch)")
	}
}

func renderSource(output *boundedBuffer, source cli.Source, indent int) {
	output.line("")
	output.indentedLine(indent, "Source")
	if source.Mode() == cli.SourceDevelopment {
		output.indentedField(indent+1, "Local checkout", source.Checkout())
		output.indentedField(indent+1, "Content digest", "SHA-256 "+source.SourceDigest().String())
		output.indentedField(indent+1, "Uncommitted changes included", yesNo(source.Dirty()))
		return
	}
	output.indentedField(indent+1, "Repository", source.Repository().String())
	output.indentedField(indent+1, "Transport", source.Transport().String())
	if source.HasRequestedRef() {
		output.indentedField(indent+1, "Requested reference", source.RequestedRef())
	} else {
		output.indentedField(indent+1, "Requested reference", "repository default branch")
	}
	commit := source.Commit()
	output.indentedField(indent+1, "Exact commit", commit.OID().String())
}

func renderRecordedSource(output *boundedBuffer, source cli.RecordedSource, indent int) {
	output.line("")
	output.indentedLine(indent, "Source")
	if source.Mode() == cli.SourceDevelopment {
		output.indentedField(indent+1, "Local checkout", source.Checkout())
		output.indentedField(indent+1, "Installed content digest", "SHA-256 "+source.SourceDigest().String())
		output.indentedField(indent+1, "Uncommitted changes included", yesNo(source.Dirty()))
		return
	}
	output.indentedField(indent+1, "Repository", source.Repository().String())
	output.indentedField(indent+1, "Transport", source.Transport().String())
	if source.HasRequestedRef() {
		output.indentedField(indent+1, "Requested reference", source.RequestedRef())
	} else {
		output.indentedField(indent+1, "Requested reference", "repository default branch")
	}
	output.indentedField(indent+1, "Exact commit", source.Commit().String())
}

func renderContent(output *boundedBuffer, values []cli.ContentItem, indent int) {
	output.line("")
	output.indentedLine(indent, "Active content ("+strconv.Itoa(len(values))+")")
	if len(values) == 0 {
		output.indentedLine(indent+1, "None.")
		return
	}
	for _, item := range values {
		output.indentedLine(indent+1, "- "+humanize(string(item.ComponentType()))+" "+item.Identifier()+" - "+humanize(string(item.Change())))
		output.indentedField(indent+2, "Source", item.SourcePath())
		output.indentedField(indent+2, "SHA-256", item.Checksum())
		execution, present := item.Execution()
		if !present {
			continue
		}
		output.indentedLine(indent+2, "Executable content")
		output.indentedField(indent+3, "Command", execution.Command())
		output.indentedField(indent+3, "Ownership", humanize(string(execution.Ownership())))
		output.indentedField(indent+3, "Dependency", humanize(string(execution.Dependency())))
		if execution.CWD() != "" {
			output.indentedField(indent+3, "Working directory", execution.CWD())
		}
		renderStrings(output, "Arguments", execution.Args(), indent+3)
		placeholders := execution.SupportedPlaceholders()
		placeholderStrings := make([]string, len(placeholders))
		for index, placeholder := range placeholders {
			placeholderStrings[index] = string(placeholder)
		}
		renderStrings(output, "Supported placeholders", placeholderStrings, indent+3)
		renderStrings(output, "Environment names", execution.Environment(), indent+3)
	}
}

func renderActions(output *boundedBuffer, values []cli.Action, indent int) {
	output.line("")
	output.indentedLine(indent, "Changes ("+strconv.Itoa(len(values))+")")
	if len(values) == 0 {
		output.indentedLine(indent+1, "None.")
		return
	}
	for _, action := range values {
		output.indentedLine(indent+1, strconv.Itoa(action.Sequence())+". "+humanize(string(action.Kind()))+": "+action.Resource())
		output.indentedField(indent+2, "Owner", humanize(string(action.Owner())))
		output.indentedField(indent+2, "Before", conditionText(action.ExpectedPrecondition()))
		output.indentedField(indent+2, "After", conditionText(action.ProposedPostcondition()))
	}
}

func conditionText(value cli.Condition) string {
	if value.HasChecksum() {
		return humanize(string(value.State())) + " (SHA-256 " + value.Checksum() + ")"
	}
	return humanize(string(value.State()))
}

func renderFinalState(output *boundedBuffer, state cli.FinalState, indent int) {
	output.line("")
	output.indentedLine(indent, "Final state")
	output.indentedField(indent+1, "Installation record", humanize(string(state.Installation())))
	output.indentedField(indent+1, "Claude integration", humanize(string(state.Native())))
	output.indentedField(indent+1, "AI4J-managed files", humanize(string(state.OwnedState())))
}

func renderStrings(output *boundedBuffer, label string, values []string, indent int) {
	if len(values) == 0 {
		return
	}
	output.indentedLine(indent, label+":")
	for _, value := range values {
		output.indentedLine(indent+1, "- "+value)
	}
}

func renderDiagnostics(output *boundedBuffer, label string, values []result.Warning) {
	if len(values) == 0 {
		return
	}
	output.line("")
	output.line(label + ":")
	for _, value := range values {
		renderDiagnostic(output, value.Code(), value.Message(), value.Context())
	}
}

func renderProblems(output *boundedBuffer, values []result.Problem) {
	if len(values) == 0 {
		return
	}
	output.line("")
	output.line("Problems:")
	for _, value := range values {
		renderDiagnostic(output, value.Code(), value.Message(), value.Context())
	}
}

func renderDiagnostic(output *boundedBuffer, code, message string, context []result.Context) {
	output.indentedLine(1, "- "+sentence(message)+" ["+code+"]")
	for _, item := range context {
		output.indentedField(2, humanize(item.Field()), item.Value())
	}
}

func doctorMarker(status cli.DoctorCheckStatus) string {
	switch status {
	case cli.DoctorCheckOK:
		return "OK"
	case cli.DoctorCheckWarning:
		return "WARN"
	default:
		return "ERROR"
	}
}

func usageMessage(data cli.UsageData) string {
	switch data.Issue() {
	case cli.UsageMissingExecutable:
		return "AI4J could not determine how it was started."
	case cli.UsageAlternateExecutable:
		return "Run this program as ai4j (ai4j.exe on Windows)."
	case cli.UsageMissingCommand:
		return "No AI4J command was provided."
	case cli.UsageUnknownCommand:
		return "That AI4J command is not supported."
	case cli.UsageUnexpectedArgument:
		return "This command received an unexpected positional argument."
	case cli.UsageUnknownOption:
		return "This command received an unknown option."
	case cli.UsageMisplacedOption:
		return "Place the command name before its options."
	case cli.UsageInapplicableOption:
		return "This option does not apply to the selected command."
	case cli.UsageDuplicateOption:
		return "This option was provided more than once."
	case cli.UsageMissingOptionValue:
		return "A required argument or option value is missing."
	case cli.UsageEmptyOptionValue:
		return "An option value cannot be empty."
	case cli.UsageUnexpectedOptionValue:
		return "This flag does not accept a value."
	case cli.UsageInvalidOptionValue:
		return "An argument or option value is invalid."
	default:
		return "The command line is invalid."
	}
}

func commandUsage(command cli.Command) string {
	switch command {
	case cli.CommandInit:
		return "ai4j init --target <claude|codex> --output <DIRECTORY> [--examples]"
	case cli.CommandValidate:
		return "ai4j validate [--repo <OWNER/REPO> | --source <PATH>] --target <claude|codex>"
	case cli.CommandBuild:
		return "ai4j build [--repo <OWNER/REPO> | --source <PATH>] --target <TARGET> --host <HOST> --output <DIRECTORY> (--all | --asset <ID> | --bundle <ID>)"
	case cli.CommandInstall:
		return "ai4j install [source options] --target claude --scope <SCOPE> --bundle <ID>"
	case cli.CommandUpdate:
		return "ai4j update <INSTALLATION_ID> [options]"
	case cli.CommandSync:
		return "ai4j sync <INSTALLATION_ID> --bundle <ID> [options]"
	case cli.CommandList:
		return "ai4j list [--target claude] [--scope <SCOPE>]"
	case cli.CommandStatus:
		return "ai4j status <INSTALLATION_ID>"
	case cli.CommandDoctor:
		return "ai4j doctor <INSTALLATION_ID> [--test-mcp <SERVER_ID>]"
	case cli.CommandRollback:
		return "ai4j rollback <INSTALLATION_ID> [--operation <OPERATION_ID>] [options]"
	case cli.CommandUninstall:
		return "ai4j uninstall <INSTALLATION_ID> [options]"
	case cli.CommandHistory:
		return "ai4j history <INSTALLATION_ID>"
	case cli.CommandHistoryPurge:
		return "ai4j history purge <INSTALLATION_ID> (--operation <OPERATION_ID> | --expired | --all) [options]"
	case cli.CommandVersion:
		return "ai4j version"
	default:
		return "ai4j <COMMAND> [options]"
	}
}

func planHeadline(operation cli.Operation, installationID string) string {
	switch operation {
	case cli.OperationInstall:
		return "Plan: Install the toolkit as " + installationID + "."
	case cli.OperationUpdate:
		return "Plan: Update installation " + installationID + "."
	case cli.OperationSync:
		return "Plan: Change the selected content for installation " + installationID + "."
	case cli.OperationRollback:
		return "Plan: Roll back installation " + installationID + "."
	case cli.OperationUninstall:
		return "Plan: Remove installation " + installationID + "."
	case cli.OperationHistoryPurge:
		return "Plan: Remove saved history from installation " + installationID + "."
	default:
		return "Plan: Change installation " + installationID + "."
	}
}

func mutationSuccess(operation cli.Operation, installationID string, hasInstallationID bool) string {
	installation := "the selected installation"
	if hasInstallationID {
		installation = "installation " + installationID
	}
	switch operation {
	case cli.OperationInstall:
		if hasInstallationID {
			return "Installed the toolkit successfully as " + installationID + "."
		}
		return "Installed the toolkit successfully."
	case cli.OperationUpdate:
		return "Updated " + installation + " successfully."
	case cli.OperationSync:
		return "Changed the selected content for " + installation + " successfully."
	case cli.OperationRollback:
		return "Rolled back " + installation + " successfully."
	case cli.OperationUninstall:
		return "Removed " + installation + " successfully."
	case cli.OperationHistoryPurge:
		return "Removed saved history from " + installation + " successfully."
	default:
		return "Changed " + installation + " successfully."
	}
}

func statusHeadline(data cli.StatusData, commandResult result.Result, installationID string) string {
	if data.RecoveryState().State() != cli.RecoveryStateNone {
		return "Installation " + installationID + " requires recovery."
	}
	if statusIsArchived(data) {
		return "Installation " + installationID + " is archived."
	}
	if statusNeedsAttention(data) {
		return "Installation " + installationID + " needs attention."
	}
	if commandResult.Status() == result.StatusError && data.UpdateDisposition() == result.UpdateUnknown {
		return "Installation " + installationID + " is healthy, but AI4J could not check for updates."
	}
	return "Installation " + installationID + " is healthy."
}

func statusNeedsAttention(data cli.StatusData) bool {
	for _, item := range data.Drift() {
		if item.State() != cli.DriftUnchanged {
			return true
		}
	}
	native := data.NativeState()
	return native.Registration() != cli.NativeRegistered || native.Installation() != cli.NativeInstalled || native.Enablement() != cli.NativeEnabled
}

func nativeLine(label, state string, healthy bool) string {
	marker := "ATTENTION"
	if healthy {
		marker = "OK"
	} else if strings.Contains(state, "unknown") || strings.Contains(state, "not_observable") {
		marker = "UNKNOWN"
	}
	return "[" + marker + "] " + label + ": " + humanize(state)
}

func renderUpdateStatus(output *boundedBuffer, data cli.StatusData) {
	output.line("")
	output.line("Updates")
	installation, installed := data.Installation()
	switch data.UpdateDisposition() {
	case result.UpdateNotChecked:
		if data.RecoveryState().State() != cli.RecoveryStateNone {
			output.indentedLine(1, "The update check was skipped until the installation can be recovered.")
		} else if statusIsArchived(data) {
			output.indentedLine(1, "This archived installation has no active files to check, so AI4J did not look for source updates.")
		} else {
			output.indentedLine(1, "The update check was not performed.")
		}
	case result.UpdateUpToDate:
		output.indentedLine(1, "No update is available.")
	case result.UpdateAvailable:
		output.indentedLine(1, "An update is available.")
		if installed {
			output.indentedLine(1, "Next: ai4j update "+installation.ID().String())
		}
	case result.UpdatePinned:
		output.indentedLine(1, "This installation is pinned to its selected source revision.")
	case result.UpdateRefRewritten:
		output.indentedLine(1, "The selected source reference no longer points to the installed commit. Review it before updating.")
	case result.UpdateUnknown:
		output.indentedLine(1, "AI4J could not check the source for updates.")
	case result.UpdateNotInstalled:
		output.indentedLine(1, "No installation was found to update.")
	default:
		output.indentedLine(1, "The update check was skipped until the installation can be recovered.")
	}
}

func statusIsArchived(data cli.StatusData) bool {
	summary, ok := data.Summary()
	return ok && summary.Lifecycle() == "archived"
}

func targetStrings(targets []cli.BuildTarget) []string {
	values := make([]string, len(targets))
	for index, target := range targets {
		values[index] = humanize(string(target))
	}
	return values
}

func humanList(values []string) string {
	switch len(values) {
	case 0:
		return "no targets"
	case 1:
		return values[0]
	case 2:
		return values[0] + " and " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", and " + values[len(values)-1]
	}
}

func count(value int, singular, plural string) string {
	label := plural
	if value == 1 {
		label = singular
	}
	return strconv.Itoa(value) + " " + label
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func passFail(value bool) string {
	if value {
		return "passed"
	}
	return "failed"
}

func formatBytes(value uint64) string {
	if value < 1024 {
		return strconv.FormatUint(value, 10) + " B"
	}
	if value < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(value)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(value)/(1024*1024))
}

func humanize(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "_", " "), "-", " ")
}

func sentence(value string) string {
	if value == "" {
		return value
	}
	value = strings.ToUpper(value[:1]) + value[1:]
	if !strings.ContainsAny(value[len(value)-1:], ".!?") {
		value += "."
	}
	return value
}

type boundedBuffer struct {
	builder strings.Builder
	limit   int
	err     error
}

func (b *boundedBuffer) String() string { return b.builder.String() }

func (b *boundedBuffer) line(value string) {
	b.write(value)
	b.write("\n")
}

func (b *boundedBuffer) field(name, value string) {
	b.line(name + ": " + value)
}

func (b *boundedBuffer) indentedField(indent int, name, value string) {
	b.indentedLine(indent, name+": "+value)
}

func (b *boundedBuffer) indentedLine(indent int, value string) {
	b.line(strings.Repeat("  ", indent) + value)
}

func (b *boundedBuffer) write(value string) {
	if b.err != nil {
		return
	}
	if len(value) > b.limit-b.builder.Len() {
		b.err = ErrOutputTooLarge
		return
	}
	_, _ = b.builder.WriteString(value)
}

func (b *boundedBuffer) fail(err error) {
	if b.err == nil {
		b.err = err
	}
}
