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
	command := "usage"
	if response.Command().Valid() {
		command = response.Command().String()
	}
	commandResult := response.Result()

	output.line("AI4J")
	output.field("command", command)
	output.field("status", commandResult.Status().String())
	output.field("changed", strconv.FormatBool(commandResult.Changed()))
	output.field("exit-code", strconv.Itoa(commandResult.ExitCode().Int()))
	if response.HasOperationID() {
		output.field("operation-id", response.OperationID().String())
	} else {
		output.field("operation-id", "none")
	}
	output.field("phase", commandResult.Phase().String())
	output.field("outcome", commandResult.Outcome().String())
	output.field("mutation", commandResult.Mutation().String())
	output.field("durable-change", commandResult.DurableChange().String())
	output.field("update-disposition", commandResult.UpdateDisposition().String())

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
		renderPlan(output, data)
	case cli.MutationData:
		renderMutation(output, data)
	case cli.HistoryData:
		renderHistory(output, data)
	case cli.ListData:
		renderList(output, data)
	case cli.StatusData:
		renderStatus(output, data)
	case cli.DoctorData:
		renderDoctor(output, data)
	case cli.VersionData:
		renderVersion(output, data)
	case cli.UnavailableData:
		output.line("data: unavailable")
	default:
		output.fail(ErrUnsupportedData)
	}

	renderDiagnostics(output, "warnings", commandResult.Warnings())
	renderProblems(output, commandResult.Errors())
}

func renderDoctor(output *boundedBuffer, data cli.DoctorData) {
	output.field("installation-id", data.InstallationID().String())
	output.line("checks:")
	for _, check := range data.Checks() {
		output.indentedLine(1, "- "+check.ID()+" status="+string(check.Status())+" summary="+check.Summary())
	}
	if startup, ok := data.StartupCheck(); ok {
		output.line("mcp-startup-check:")
		output.indentedField(1, "server", startup.ServerID())
		output.indentedField(1, "executable", startup.Executable())
		arguments := startup.Arguments()
		for index := range arguments {
			arguments[index] = strconv.Quote(arguments[index])
		}
		output.indentedField(1, "arguments", strings.Join(arguments, " "))
		output.indentedField(1, "working-directory", startup.WorkingDirectory())
		output.indentedField(1, "ownership", startup.Ownership())
		output.indentedField(1, "environment-names", strings.Join(startup.Environment(), ","))
		output.indentedField(1, "result", startup.Result())
		if startup.HasExitCode() {
			output.indentedField(1, "process-exit-code", strconv.Itoa(startup.ExitCode()))
		}
		output.indentedField(1, "warning", "the process runs with current-user permissions and may have side effects")
	}
}

func renderList(output *boundedBuffer, data cli.ListData) {
	installations := data.Installations()
	output.line("installations:")
	output.indentedField(1, "count", strconv.Itoa(len(installations)))
	for _, installation := range installations {
		output.indentedLine(2, "- "+installation.ID().String()+" toolkit="+installation.ToolkitID()+" target="+string(installation.Target())+" scope="+string(installation.Scope())+" lifecycle="+installation.Lifecycle()+" health="+installation.Health())
		if installation.Source().Mode() == cli.SourceDevelopment {
			output.indentedLine(3, "root="+installation.ScopeRoot()+" source="+installation.Source().Checkout()+" digest=sha256:"+installation.Source().SourceDigest().String()+" development=true")
		} else {
			output.indentedLine(3, "root="+installation.ScopeRoot()+" source="+installation.Source().Repository().String()+" commit=sha1:"+installation.Source().Commit().String())
		}
		selection := "all"
		if !installation.SelectAll() {
			selection = "assets=" + strings.Join(installation.Assets(), ",") + " bundles=" + strings.Join(installation.Bundles(), ",")
		}
		output.indentedLine(3, "selection="+selection+" resolved="+strings.Join(installation.Resolved(), ","))
		output.indentedLine(3, "history="+strconv.Itoa(installation.HistoryCount())+" last-operation="+installation.LastOperationID().String())
	}
}

func renderInit(output *boundedBuffer, data cli.InitData) {
	output.line("init:")
	targets := data.Targets()
	output.indentedField(1, "targets", strconv.Itoa(len(targets)))
	for _, target := range targets {
		output.indentedLine(2, "- "+string(target))
	}
	output.indentedField(1, "output-root", data.OutputRoot())
	output.indentedField(1, "validation-valid", strconv.FormatBool(data.ValidationValid()))
	output.indentedField(1, "reproducible", strconv.FormatBool(data.Reproducible()))
	artifacts := data.Artifacts()
	output.indentedField(1, "artifacts", strconv.Itoa(len(artifacts)))
	for _, artifact := range artifacts {
		output.indentedLine(2, "- "+artifact.Path()+" sha256="+artifact.Checksum()+" bytes="+strconv.FormatUint(artifact.SizeBytes(), 10))
	}
}

func renderUsage(output *boundedBuffer, data cli.UsageData) {
	output.line("usage:")
	output.indentedField(1, "issue", string(data.Issue()))
	if data.Option() != "" {
		output.indentedField(1, "option", data.Option())
	}
}

func renderValidate(output *boundedBuffer, data cli.ValidateData) {
	output.line("validation:")
	output.indentedField(1, "valid", strconv.FormatBool(data.ValidationValid()))
	output.indentedField(1, "error-count", strconv.Itoa(data.ErrorCount()))
	output.indentedField(1, "warning-count", strconv.Itoa(data.WarningCount()))
	renderSource(output, data.Source(), 1)
	renderContent(output, data.ActiveContent(), 1)
}

func renderBuild(output *boundedBuffer, data cli.BuildData) {
	output.line("build:")
	output.indentedField(1, "target", string(data.Target()))
	output.indentedField(1, "host", string(data.Host()))
	output.indentedField(1, "output-root", data.OutputRoot())
	output.indentedField(1, "reproducible", strconv.FormatBool(data.Reproducible()))
	output.indentedField(1, "validation-valid", strconv.FormatBool(data.ValidationValid()))
	renderSource(output, data.Source(), 1)
	artifacts := data.Artifacts()
	output.indentedField(1, "artifacts", strconv.Itoa(len(artifacts)))
	for _, artifact := range artifacts {
		output.indentedLine(2, "- "+artifact.Path()+" sha256="+artifact.Checksum()+" bytes="+strconv.FormatUint(artifact.SizeBytes(), 10))
	}
	selection := data.Selection()
	output.indentedField(1, "selection", strconv.Itoa(len(selection)))
	for _, item := range selection {
		output.indentedLine(2, "- "+item.Asset()+" variant="+item.Variant()+" reason="+item.Reason()+" from="+item.RequestedBy())
	}
	renderContent(output, data.ActiveContent(), 1)
}

func renderPlan(output *boundedBuffer, data cli.PlanData) {
	output.line("plan:")
	output.indentedField(1, "operation", data.Operation().String())
	output.indentedField(1, "installation-id", data.InstallationID().String())
	if data.HasSource() {
		renderSource(output, data.Source(), 1)
	}
	renderActions(output, data.Actions(), 1)
	renderContent(output, data.ActiveContent(), 1)
	conflicts := data.Conflicts()
	output.indentedField(1, "conflicts", strconv.Itoa(len(conflicts)))
	for _, conflict := range conflicts {
		output.indentedLine(2, "- "+conflict.Code()+" resource="+conflict.Resource()+" message="+conflict.Message())
	}
	renderFinalState(output, data.ExpectedFinalState(), 1)
}

func renderMutation(output *boundedBuffer, data cli.MutationData) {
	output.line("operation:")
	output.indentedField(1, "name", data.Operation().String())
	if data.HasInstallationID() {
		output.indentedField(1, "installation-id", data.InstallationID().String())
	} else {
		output.indentedField(1, "installation-id", "none")
	}
	operationResult := data.OperationResult()
	output.indentedField(1, "phase", operationResult.Phase().String())
	output.indentedField(1, "outcome", operationResult.Outcome().String())
	output.indentedField(1, "mutation", operationResult.Mutation().String())
	output.indentedField(1, "durable-change", operationResult.DurableChange().String())
	renderActions(output, data.AppliedActions(), 1)
	renderFinalState(output, data.FinalState(), 1)
}

func renderHistory(output *boundedBuffer, data cli.HistoryData) {
	output.line("history:")
	output.indentedField(1, "installation-id", data.InstallationID().String())
	entries := data.Entries()
	output.indentedField(1, "entries", strconv.Itoa(len(entries)))
	for _, entry := range entries {
		output.indentedLine(2, "- "+entry.OperationID().String()+" operation="+entry.Operation().String()+" timestamp="+entry.Timestamp().Format(time.RFC3339)+" restorable="+strconv.FormatBool(entry.Restorable()))
	}
}

func renderStatus(output *boundedBuffer, data cli.StatusData) {
	recovery := data.RecoveryState()
	output.line("installation:")
	if installation, present := data.Installation(); present {
		output.indentedField(1, "id", installation.ID().String())
		output.indentedField(1, "toolkit-id", installation.ToolkitID())
		output.indentedField(1, "native-plugin-id", installation.NativePluginID())
		output.indentedField(1, "toolkit-version", installation.ToolkitVersion())
		output.indentedField(1, "cli-version", installation.CLIVersion())
		if installation.HasExpectedNativeVersion() {
			output.indentedField(1, "expected-native-version", installation.ExpectedNativeVersion())
		}
		renderRecordedSource(output, installation.Source(), 1)
		if summary, ok := data.Summary(); ok {
			output.indentedField(1, "target", string(summary.Target()))
			output.indentedField(1, "scope", string(summary.Scope()))
			output.indentedField(1, "scope-root", summary.ScopeRoot())
			output.indentedField(1, "lifecycle", summary.Lifecycle())
			output.indentedField(1, "history-count", strconv.Itoa(summary.HistoryCount()))
			output.indentedField(1, "last-operation-id", summary.LastOperationID().String())
			output.indentedField(1, "health", summary.Health())
			selection := "all"
			if !summary.SelectAll() {
				selection = "assets=" + strings.Join(summary.Assets(), ",") + " bundles=" + strings.Join(summary.Bundles(), ",")
			}
			output.indentedField(1, "selection", selection)
			output.indentedField(1, "resolved", strings.Join(summary.Resolved(), ","))
		}
	} else if data.UpdateDisposition() == result.UpdateNotInstalled && recovery.State() == cli.RecoveryStateNone {
		output.indentedField(1, "state", "not-installed")
	} else {
		output.indentedField(1, "state", "record-unavailable")
	}

	native := data.NativeState()
	output.line("native:")
	output.indentedField(1, "registration", string(native.Registration()))
	output.indentedField(1, "installation", string(native.Installation()))
	output.indentedField(1, "enablement", string(native.Enablement()))
	output.indentedField(1, "activation", string(native.Activation()))
	output.indentedField(1, "reload", string(native.Reload()))
	output.indentedField(1, "next-session", string(native.NextSession()))
	output.indentedField(1, "policy", string(native.Policy()))
	output.indentedField(1, "version-observation", string(native.VersionStatus()))
	if native.HasVersion() {
		output.indentedField(1, "version", native.Version())
	}

	drift := data.Drift()
	output.field("drift", strconv.Itoa(len(drift)))
	for _, item := range drift {
		output.indentedLine(1, "- "+item.Resource()+" state="+string(item.State()))
	}
	output.field("recovery", string(recovery.State()))
	if recovery.HasPhase() {
		output.indentedField(1, "phase", recovery.Phase().String())
	}
}

func renderVersion(output *boundedBuffer, data cli.VersionData) {
	output.line("version:")
	output.indentedField(1, "product", data.Product())
	output.indentedField(1, "executable", data.Executable())
	output.indentedField(1, "cli-version", data.CLIVersion())
	output.indentedField(1, "cli-source-repository", data.CLISourceRepository().String())
	output.indentedField(1, "cli-source-commit", data.CLISourceCommit().String())
	output.indentedField(1, "go-version", data.GoVersion())
	output.indentedField(1, "build-time", data.BuildTime().Format("2006-01-02T15:04:05Z"))
	output.indentedField(1, "target", data.TargetOS()+"/"+data.TargetArch())
	defaultSource := data.DefaultSource()
	output.indentedField(1, "default-repository", defaultSource.Repository().String())
	output.indentedField(1, "default-ref-policy", string(defaultSource.RefPolicy()))
	if defaultSource.HasReference() {
		output.indentedField(1, "default-reference", defaultSource.Reference())
	} else {
		output.indentedField(1, "default-reference", "repository-default")
	}
}

func renderSource(output *boundedBuffer, source cli.Source, indent int) {
	output.indentedLine(indent, "source:")
	output.indentedField(indent+1, "mode", string(source.Mode()))
	output.indentedField(indent+1, "selection", source.Selection().String())
	if source.Mode() == cli.SourceDevelopment {
		output.indentedField(indent+1, "checkout", source.Checkout())
		output.indentedField(indent+1, "source-digest", "sha256:"+source.SourceDigest().String())
		output.indentedField(indent+1, "dirty", strconv.FormatBool(source.Dirty()))
		return
	}
	output.indentedField(indent+1, "repository", source.Repository().String())
	if source.HasRequestedRef() {
		output.indentedField(indent+1, "requested-ref", source.RequestedRef())
	} else {
		output.indentedField(indent+1, "requested-ref", "repository-default")
	}
	output.indentedField(indent+1, "resolved-ref", string(source.ResolvedRefKind())+":"+source.ResolvedRefName())
	commit := source.Commit()
	output.indentedField(indent+1, "commit", commit.ObjectFormat().String()+":"+commit.OID().String())
}

func renderRecordedSource(output *boundedBuffer, source cli.RecordedSource, indent int) {
	output.indentedLine(indent, "source:")
	output.indentedField(indent+1, "mode", string(source.Mode()))
	output.indentedField(indent+1, "selection", source.Selection().String())
	if source.Mode() == cli.SourceDevelopment {
		output.indentedField(indent+1, "checkout", source.Checkout())
		output.indentedField(indent+1, "source-digest", "sha256:"+source.SourceDigest().String())
		output.indentedField(indent+1, "dirty", strconv.FormatBool(source.Dirty()))
		return
	}
	output.indentedField(indent+1, "repository", source.Repository().String())
	if source.HasRequestedRef() {
		output.indentedField(indent+1, "requested-ref", source.RequestedRef())
	} else {
		output.indentedField(indent+1, "requested-ref", "repository-default")
	}
	output.indentedField(indent+1, "resolved-ref-kind", source.RefKind().String())
	output.indentedField(indent+1, "commit", "sha1:"+source.Commit().String())
}

func renderContent(output *boundedBuffer, values []cli.ContentItem, indent int) {
	output.indentedField(indent, "active-content", strconv.Itoa(len(values)))
	for _, item := range values {
		output.indentedLine(indent+1, "- "+string(item.ComponentType())+"/"+item.Identifier()+" path="+item.SourcePath()+" change="+string(item.Change())+" sha256="+item.Checksum())
		execution, present := item.Execution()
		if !present {
			continue
		}
		output.indentedLine(indent+2, "execution ownership="+string(execution.Ownership())+" dependency="+string(execution.Dependency())+" command="+execution.Command())
		if execution.CWD() != "" {
			output.indentedField(indent+2, "cwd", execution.CWD())
		}
		renderStrings(output, "args", execution.Args(), indent+2)
		placeholders := execution.SupportedPlaceholders()
		placeholderStrings := make([]string, len(placeholders))
		for index, placeholder := range placeholders {
			placeholderStrings[index] = string(placeholder)
		}
		renderStrings(output, "placeholders", placeholderStrings, indent+2)
		renderStrings(output, "environment", execution.Environment(), indent+2)
	}
}

func renderActions(output *boundedBuffer, values []cli.Action, indent int) {
	output.indentedField(indent, "actions", strconv.Itoa(len(values)))
	for _, action := range values {
		output.indentedLine(indent+1, "- "+strconv.Itoa(action.Sequence())+" "+string(action.Owner())+"/"+string(action.Kind())+" resource="+action.Resource()+" pre="+conditionText(action.ExpectedPrecondition())+" post="+conditionText(action.ProposedPostcondition())+" recovery="+string(action.RecoveryRequirement()))
	}
}

func conditionText(value cli.Condition) string {
	if value.HasChecksum() {
		return string(value.State()) + ":sha256:" + value.Checksum()
	}
	return string(value.State())
}

func renderFinalState(output *boundedBuffer, state cli.FinalState, indent int) {
	output.indentedLine(indent, "final-state:")
	output.indentedField(indent+1, "installation", string(state.Installation()))
	output.indentedField(indent+1, "native", string(state.Native()))
	output.indentedField(indent+1, "owned-state", string(state.OwnedState()))
}

func renderStrings(output *boundedBuffer, label string, values []string, indent int) {
	output.indentedField(indent, label, strconv.Itoa(len(values)))
	for _, value := range values {
		output.indentedLine(indent+1, "- "+value)
	}
}

func renderDiagnostics(output *boundedBuffer, label string, values []result.Warning) {
	output.field(label, strconv.Itoa(len(values)))
	for _, value := range values {
		renderDiagnostic(output, value.Code(), value.Message(), value.Context())
	}
}

func renderProblems(output *boundedBuffer, values []result.Problem) {
	output.field("errors", strconv.Itoa(len(values)))
	for _, value := range values {
		renderDiagnostic(output, value.Code(), value.Message(), value.Context())
	}
}

func renderDiagnostic(output *boundedBuffer, code, message string, context []result.Context) {
	output.indentedLine(1, "- "+code+": "+message)
	for _, item := range context {
		output.indentedLine(2, item.Field()+"="+item.Value())
	}
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
