package jsonv1

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
)

const SchemaVersion = 1

// Marshal projects a validated neutral response to its concrete v1 DTO.
func Marshal(response cli.Response) ([]byte, error) {
	if !response.Valid() {
		return nil, fmt.Errorf("cannot encode invalid response")
	}

	switch data := response.Data().(type) {
	case cli.UsageData:
		wire := envelope[*UsageData](response, nil)
		context := []Context{{Field: "issue", Value: string(data.Issue())}}
		if data.Option() != "" {
			context = append(context, Context{Field: "option", Value: data.Option()})
		}
		wire.Errors = []Diagnostic{{Code: "invalid_cli_usage", Message: "command line does not match the MVP grammar", Context: context}}
		return json.Marshal(wire)
	case cli.InitData:
		return json.Marshal(envelope(response, initData(data)))
	case cli.ValidateData:
		return json.Marshal(envelope(response, validateData(data)))
	case cli.BuildData:
		return json.Marshal(envelope(response, buildData(data)))
	case cli.PlanData:
		return json.Marshal(envelope(response, planData(data)))
	case cli.MutationData:
		return json.Marshal(envelope(response, mutationData(data)))
	case cli.HistoryData:
		return json.Marshal(envelope(response, historyData(data)))
	case cli.ListData:
		return json.Marshal(envelope(response, listData(data)))
	case cli.StatusData:
		return json.Marshal(envelope(response, statusData(data)))
	case cli.DoctorData:
		return json.Marshal(envelope(response, doctorData(data)))
	case cli.VersionData:
		return json.Marshal(envelope(response, versionData(data)))
	case cli.UnavailableData:
		return marshalUnavailable(response)
	default:
		return nil, fmt.Errorf("unsupported response data %T", data)
	}
}

func envelope[D any](response cli.Response, data D) Envelope[D] {
	var command *string
	if response.Command().Valid() {
		value := response.Command().String()
		command = &value
	}
	var operationID *string
	if response.HasOperationID() {
		value := response.OperationID().String()
		operationID = &value
	}
	commandResult := response.Result()
	return Envelope[D]{
		SchemaVersion: SchemaVersion,
		Command:       command,
		Status:        commandResult.Status().String(),
		Changed:       commandResult.Changed(),
		OperationID:   operationID,
		ExitCode:      commandResult.ExitCode().Int(),
		Data:          data,
		Warnings:      warnings(commandResult.Warnings()),
		Errors:        problems(commandResult.Errors()),
	}
}

func marshalUnavailable(response cli.Response) ([]byte, error) {
	switch response.Command() {
	case cli.CommandInit:
		return json.Marshal(envelope[*InitData](response, nil))
	case cli.CommandValidate:
		return json.Marshal(envelope[*ValidateData](response, nil))
	case cli.CommandBuild:
		return json.Marshal(envelope[*BuildData](response, nil))
	case cli.CommandPlanInstall, cli.CommandPlanUpdate, cli.CommandPlanSync, cli.CommandPlanRollback, cli.CommandPlanUninstall, cli.CommandPlanHistoryPurge:
		return json.Marshal(envelope[*PlanData](response, nil))
	case cli.CommandInstall, cli.CommandUpdate, cli.CommandSync, cli.CommandRollback, cli.CommandUninstall, cli.CommandHistoryPurge:
		return json.Marshal(envelope[*MutationData](response, nil))
	case cli.CommandList:
		return json.Marshal(envelope[*ListData](response, nil))
	case cli.CommandHistory:
		return json.Marshal(envelope[*HistoryData](response, nil))
	case cli.CommandDoctor:
		return json.Marshal(envelope[*DoctorData](response, nil))
	case cli.CommandStatus:
		return json.Marshal(envelope[*StatusData](response, nil))
	case cli.CommandVersion:
		return json.Marshal(envelope[*VersionData](response, nil))
	default:
		return nil, fmt.Errorf("unavailable data requires a canonical command")
	}
}

func listData(value cli.ListData) ListData {
	installations := value.Installations()
	items := make([]InstallationSummary, len(installations))
	for index, installation := range installations {
		items[index] = installationSummary(installation)
	}
	var migration *StateMigration
	if item, ok := value.Migration(); ok {
		migration = &StateMigration{FromVersion: item.FromVersion(), ToVersion: item.ToVersion(), Changes: item.Changes()}
	}
	return ListData{Installations: items, Migration: migration}
}

func initData(value cli.InitData) InitData {
	targets := value.Targets()
	wireTargets := make([]string, len(targets))
	for index, target := range targets {
		wireTargets[index] = string(target)
	}
	artifacts := value.Artifacts()
	wireArtifacts := make([]BuildArtifact, len(artifacts))
	for index, artifact := range artifacts {
		wireArtifacts[index] = BuildArtifact{Path: artifact.Path(), Checksum: Checksum{Algorithm: "sha256", Digest: artifact.Checksum()}, SizeBytes: artifact.SizeBytes()}
	}
	return InitData{Targets: wireTargets, OutputRoot: value.OutputRoot(), Validation: Validation{Valid: true}, Reproducible: true, Artifacts: wireArtifacts}
}

func source(value cli.Source) Source {
	result := Source{SourceMode: string(value.Mode()), SourceSelection: value.Selection().String(), Dirty: value.Dirty(), RenderedDigest: SourceRenderedDigest{Algorithm: "sha256", Digest: value.RenderedDigest().String()}, CLIBuildCommit: SourceBuildCommit{ObjectFormat: domain.SHA1ObjectFormat().String(), OID: value.CLIBuildCommit().String()}}
	if value.Mode() == cli.SourceDevelopment {
		result.Checkout = optionalString(value.Checkout(), true)
		digest := SourceRenderedDigest{Algorithm: "sha256", Digest: value.SourceDigest().String()}
		result.SourceDigest = &digest
		return result
	}
	commit := value.Commit()
	tree := value.RootTree()
	result.Repository = optionalString(value.Repository().String(), true)
	result.RequestedRef = optionalString(value.RequestedRef(), value.HasRequestedRef())
	result.ResolvedRefKind = optionalString(string(value.ResolvedRefKind()), true)
	result.ResolvedRefName = optionalString(value.ResolvedRefName(), true)
	result.TrackingPolicy = optionalString(value.TrackingPolicy().String(), true)
	result.Commit = &SourceCommit{ObjectFormat: commit.ObjectFormat().String(), OID: commit.OID().String()}
	result.RootTree = &SourceTree{ObjectFormat: domain.SHA1ObjectFormat().String(), OID: tree.String()}
	return result
}

func validateData(value cli.ValidateData) ValidateData {
	return ValidateData{Source: source(value.Source()), Validation: Validation{Valid: value.ValidationValid(), ErrorCount: value.ErrorCount(), WarningCount: value.WarningCount()}, ActiveContent: content(value.ActiveContent())}
}

func buildData(value cli.BuildData) BuildData {
	artifacts := value.Artifacts()
	wireArtifacts := make([]BuildArtifact, len(artifacts))
	for index, artifact := range artifacts {
		wireArtifacts[index] = BuildArtifact{Path: artifact.Path(), Checksum: Checksum{Algorithm: "sha256", Digest: artifact.Checksum()}, SizeBytes: artifact.SizeBytes()}
	}
	selection := value.Selection()
	wireSelection := make([]BuildSelection, len(selection))
	for index, item := range selection {
		wireSelection[index] = BuildSelection{Asset: item.Asset(), Variant: item.Variant(), Reason: item.Reason(), RequestedBy: item.RequestedBy()}
	}
	return BuildData{Source: source(value.Source()), Target: string(value.Target()), Host: string(value.Host()), OutputRoot: value.OutputRoot(), Reproducible: value.Reproducible(), Validation: Validation{Valid: true}, Artifacts: wireArtifacts, Selection: wireSelection, ActiveContent: content(value.ActiveContent())}
}

func planData(value cli.PlanData) PlanData {
	var wireSource *Source
	if value.HasSource() {
		converted := source(value.Source())
		wireSource = &converted
	}
	return PlanData{Operation: value.Operation().String(), Source: wireSource, InstallationID: value.InstallationID().String(), Actions: actions(value.Actions()), ActiveContent: content(value.ActiveContent()), Conflicts: conflicts(value.Conflicts()), ExpectedFinalState: finalState(value.ExpectedFinalState()), UpdateDisposition: value.UpdateDisposition().String()}
}

func mutationData(value cli.MutationData) MutationData {
	operationResult := value.OperationResult()
	return MutationData{Operation: value.Operation().String(), OperationResult: OperationResult{Phase: operationResult.Phase().String(), Outcome: operationResult.Outcome().String(), Mutation: operationResult.Mutation().String(), DurableChange: operationResult.DurableChange().String()}, InstallationID: installationID(value.InstallationID(), value.HasInstallationID()), AppliedActions: actions(value.AppliedActions()), FinalState: finalState(value.FinalState()), UpdateDisposition: value.UpdateDisposition().String()}
}

func historyData(value cli.HistoryData) HistoryData {
	entries := value.Entries()
	wireEntries := make([]HistoryDescriptor, len(entries))
	for index, entry := range entries {
		wireEntries[index] = HistoryDescriptor{OperationID: entry.OperationID().String(), Operation: entry.Operation().String(), Timestamp: entry.Timestamp().Format(time.RFC3339), Restorable: entry.Restorable()}
	}
	return HistoryData{InstallationID: value.InstallationID().String(), Entries: wireEntries}
}

func statusData(value cli.StatusData) StatusData {
	var installation *Installation
	var summary *InstallationSummary
	var expectedNativeVersion *string
	if item, ok := value.Installation(); ok {
		installation = &Installation{InstallationID: item.ID().String(), ToolkitID: item.ToolkitID(), NativePluginID: item.NativePluginID(), Source: recordedSource(item.Source()), ToolkitVersion: item.ToolkitVersion(), CLIVersion: item.CLIVersion()}
		expectedNativeVersion = optionalString(item.ExpectedNativeVersion(), item.HasExpectedNativeVersion())
	}
	if item, ok := value.Summary(); ok {
		converted := installationSummary(item)
		summary = &converted
	}
	native := value.NativeState()
	nativeVersion := NativeVersion{Expected: expectedNativeVersion, Observed: optionalString(native.Version(), native.HasVersion()), Observation: nativeVersionObservation(native.VersionStatus())}
	recovery := value.RecoveryState()
	drifts := value.Drift()
	wireDrift := make([]Drift, len(drifts))
	for index, item := range drifts {
		wireDrift[index] = Drift{Resource: item.Resource(), State: string(item.State())}
	}
	return StatusData{Installation: installation, Summary: summary, NativeState: NativeState{Registration: string(native.Registration()), Installation: string(native.Installation()), Enablement: string(native.Enablement()), Activation: string(native.Activation()), Reload: string(native.Reload()), NextSession: string(native.NextSession()), Policy: string(native.Policy()), Version: nativeVersion}, Drift: wireDrift, RecoveryState: RecoveryState{State: string(recovery.State()), Phase: optionalString(recovery.Phase().String(), recovery.HasPhase())}, UpdateDisposition: value.UpdateDisposition().String()}
}

func doctorData(value cli.DoctorData) DoctorData {
	checks := value.Checks()
	wiredChecks := make([]DoctorCheck, len(checks))
	for index, check := range checks {
		wiredChecks[index] = DoctorCheck{ID: check.ID(), Status: string(check.Status()), Summary: check.Summary()}
	}
	var startup *MCPStartupCheck
	if check, ok := value.StartupCheck(); ok {
		startup = &MCPStartupCheck{
			ServerID: check.ServerID(), Executable: check.Executable(), Arguments: check.Arguments(), Environment: check.Environment(),
			WorkingDirectory: check.WorkingDirectory(), Ownership: check.Ownership(), Result: check.Result(),
		}
		if check.HasExitCode() {
			exitCode := check.ExitCode()
			startup.ExitCode = &exitCode
		}
	}
	return DoctorData{InstallationID: value.InstallationID().String(), Checks: wiredChecks, StartupCheck: startup}
}

func installationSummary(installation cli.InstallationSummary) InstallationSummary {
	return InstallationSummary{
		InstallationID: installation.ID().String(), ToolkitID: installation.ToolkitID(), Target: string(installation.Target()), Scope: string(installation.Scope()), ScopeRoot: installation.ScopeRoot(), Lifecycle: installation.Lifecycle(),
		Source: recordedSource(installation.Source()), All: installation.SelectAll(), Assets: installation.Assets(), Bundles: installation.Bundles(), Resolved: installation.Resolved(), Health: installation.Health(), HistoryCount: installation.HistoryCount(), LastOperationID: optionalString(installation.LastOperationID().String(), installation.HasLastOperationID()),
	}
}

func recordedSource(value cli.RecordedSource) RecordedSource {
	result := RecordedSource{SourceMode: string(value.Mode()), SourceSelection: value.Selection().String(), Dirty: value.Dirty()}
	if value.Mode() == cli.SourceDevelopment {
		result.Checkout = optionalString(value.Checkout(), true)
		digest := SourceRenderedDigest{Algorithm: "sha256", Digest: value.SourceDigest().String()}
		result.SourceDigest = &digest
		return result
	}
	result.Repository = optionalString(value.Repository().String(), true)
	result.RequestedRef = optionalString(value.RequestedRef(), value.HasRequestedRef())
	result.ResolvedRefKind = optionalString(value.RefKind().String(), true)
	result.Commit = &SourceCommit{ObjectFormat: "sha1", OID: value.Commit().String()}
	return result
}

func nativeVersionObservation(value cli.NativeVersionStatus) string {
	switch value {
	case cli.NativeVersionMatches, cli.NativeVersionMismatch:
		return "observed"
	case cli.NativeVersionNotApplicable:
		return "not_applicable"
	case cli.NativeVersionUnknown:
		return "unknown"
	case cli.NativeVersionNotObservable:
		return "not_observable"
	default:
		return ""
	}
}

func versionData(value cli.VersionData) VersionData {
	defaultSource := value.DefaultSource()
	return VersionData{Product: value.Product(), Executable: value.Executable(), CLIVersion: value.CLIVersion(), CLISource: BuildCommit{Repository: value.CLISourceRepository().String(), ObjectFormat: "sha1", OID: value.CLISourceCommit().String()}, GoVersion: value.GoVersion(), BuildTime: value.BuildTime().Format("2006-01-02T15:04:05Z"), Target: Target{OS: value.TargetOS(), Arch: value.TargetArch()}, DefaultSource: DefaultSource{Repository: defaultSource.Repository().String(), Reference: optionalString(defaultSource.Reference(), defaultSource.HasReference()), RefPolicy: string(defaultSource.RefPolicy())}}
}

func warnings(values []result.Warning) []Diagnostic {
	output := make([]Diagnostic, len(values))
	for i, value := range values {
		output[i] = diagnostic(value.Code(), value.Message(), value.Context())
	}
	return output
}
func problems(values []result.Problem) []Diagnostic {
	output := make([]Diagnostic, len(values))
	for i, value := range values {
		output[i] = diagnostic(value.Code(), value.Message(), value.Context())
	}
	return output
}
func diagnostic(code, message string, values []result.Context) Diagnostic {
	context := make([]Context, len(values))
	for i, value := range values {
		context[i] = Context{Field: value.Field(), Value: value.Value()}
	}
	return Diagnostic{Code: code, Message: message, Context: context}
}
func installationID(value domain.InstallationID, present bool) *string {
	if !present {
		return nil
	}
	text := value.String()
	return &text
}
func content(values []cli.ContentItem) []ContentItem {
	output := make([]ContentItem, len(values))
	for i, value := range values {
		var execution *Execution
		if item, ok := value.Execution(); ok {
			placeholders := item.SupportedPlaceholders()
			wirePlaceholders := make([]string, len(placeholders))
			for index, placeholder := range placeholders {
				wirePlaceholders[index] = string(placeholder)
			}
			execution = &Execution{Ownership: string(item.Ownership()), Dependency: string(item.Dependency()), Command: item.Command(), Args: item.Args(), CWD: optionalString(item.CWD(), item.CWD() != ""), SupportedPlaceholders: wirePlaceholders, Environment: item.Environment()}
		}
		output[i] = ContentItem{ComponentType: string(value.ComponentType()), Identifier: value.Identifier(), SourcePath: value.SourcePath(), Checksum: Checksum{Algorithm: "sha256", Digest: value.Checksum()}, Change: string(value.Change()), Execution: execution}
	}
	return output
}
func actions(values []cli.Action) []Action {
	output := make([]Action, len(values))
	for i, value := range values {
		output[i] = Action{Sequence: value.Sequence(), Owner: string(value.Owner()), Kind: string(value.Kind()), Resource: value.Resource(), ExpectedPrecondition: condition(value.ExpectedPrecondition()), ProposedPostcondition: condition(value.ProposedPostcondition()), RecoveryRequirement: string(value.RecoveryRequirement())}
	}
	return output
}
func conflicts(values []cli.Conflict) []Conflict {
	output := make([]Conflict, len(values))
	for i, value := range values {
		output[i] = Conflict{Code: value.Code(), Resource: value.Resource(), Message: value.Message()}
	}
	return output
}
func finalState(value cli.FinalState) FinalState {
	return FinalState{Installation: string(value.Installation()), Native: string(value.Native()), OwnedState: string(value.OwnedState())}
}

func condition(value cli.Condition) Condition {
	var checksum *Checksum
	if value.HasChecksum() {
		checksum = &Checksum{Algorithm: "sha256", Digest: value.Checksum()}
	}
	return Condition{State: string(value.State()), Checksum: checksum}
}

func optionalString(value string, present bool) *string {
	if !present {
		return nil
	}
	return &value
}
