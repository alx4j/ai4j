package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func (s *lifecycleService) catalogPath(record installstate.Record) string {
	if record.Scope == "project-shared" {
		return projectSettingsPath(record)
	}
	return filepath.Join(s.state.DataRoot(), filepath.FromSlash(record.Catalog.Path))
}

func (s *lifecycleService) ownedRoot(path string) string {
	root := s.state.DataRoot()
	relative, err := filepath.Rel(root, path)
	if err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return root
	}
	rulesRoot := filepath.Dir(path)
	if filepath.Base(rulesRoot) == "rules" && filepath.Base(filepath.Dir(rulesRoot)) == ".claude" {
		return filepath.Dir(filepath.Dir(rulesRoot))
	}
	if filepath.Base(path) == "settings.json" && filepath.Base(filepath.Dir(path)) == ".claude" {
		return filepath.Dir(filepath.Dir(path))
	}
	return s.home
}

func (s *lifecycleService) rulesPath(record installstate.Record) string {
	if record.Rules.Path == "" {
		return ""
	}
	return filepath.Join(record.ScopeRoot, filepath.FromSlash(record.Rules.Path))
}

func (s *lifecycleService) recovery(command cli.Command, operation cli.Operation, operationID domain.OperationID, installationID domain.InstallationID, final cli.FinalState, actions []cli.Action, code string) (cli.Response, error) {
	problem, _ := result.NewProblem(code, "operation requires recovery before another mutation", nil)
	commandResult, err := result.New(result.Facts{Status: result.StatusError, Phase: result.PhaseApplying, Outcome: result.OutcomePending, Mutation: result.MutationStarted, DurableChange: result.DurableChangeNone, Failure: result.FailureRecovery, UpdateDisposition: result.UpdateNotChecked, Errors: []result.Problem{problem}})
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewMutationData(operation, commandResult, &installationID, actions, final, result.UpdateNotChecked)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, &operationID, data)
}

func committedResponse(command cli.Command, operation cli.Operation, operationID domain.OperationID, installationID *domain.InstallationID, final cli.FinalState, disposition result.UpdateDisposition, warnings []result.Warning, actions []cli.Action, degraded bool) (cli.Response, error) {
	status := result.StatusOK
	if degraded {
		status = result.StatusDegraded
	}
	commandResult, err := result.New(result.Facts{Status: status, Phase: result.PhaseComplete, Outcome: result.OutcomeCommitted, Mutation: result.MutationStarted, DurableChange: result.DurableCommittedWithDiff, Failure: result.FailureNone, UpdateDisposition: disposition, Warnings: warnings})
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewMutationData(operation, commandResult, installationID, actions, final, disposition)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, &operationID, data)
}

func resolveInteractivePolicy(policy cli.ConflictPolicy, output cli.OutputMode, commandIO CommandIO) (cli.ConflictPolicy, bool, error) {
	if policy != cli.ConflictInteractive {
		return policy, true, nil
	}
	if output == cli.OutputJSON || !commandIO.Interactive || commandIO.Input == nil || commandIO.Output == nil {
		return "", false, nil
	}
	if _, err := io.WriteString(commandIO.Output, "Replace only resources proven owned by this installation? [y/N]: "); err != nil {
		return "", false, err
	}
	buffer := make([]byte, 16)
	count, err := commandIO.Input.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	answer := strings.ToLower(strings.TrimSpace(string(buffer[:count])))
	if answer == "y" || answer == "yes" {
		return cli.ConflictReplaceOwned, true, nil
	}
	return cli.ConflictKeep, true, nil
}

func conflictProblems(conflicts []cli.Conflict) []result.Problem {
	problems := make([]result.Problem, 0, len(conflicts))
	for _, conflict := range conflicts {
		resource, _ := result.NewContext("resource", conflict.Resource())
		problem, _ := result.NewProblem(conflict.Code(), conflict.Message(), []result.Context{resource})
		problems = append(problems, problem)
	}
	return problems
}

func conflictWarnings(conflicts []cli.Conflict) []result.Warning {
	warnings := make([]result.Warning, 0, len(conflicts))
	for _, conflict := range conflicts {
		resource, _ := result.NewContext("resource", conflict.Resource())
		warning, _ := result.NewWarning("kept_"+conflict.Code(), "conflicting installation-owned state was preserved and the installation is degraded", []result.Context{resource})
		warnings = append(warnings, warning)
	}
	return warnings
}

func stopLifecycle(command cli.Command, failure result.Failure, code, message string) (lifecycleExecution, cli.Response, bool, error) {
	response, err := lifecycleFailure(command, failure, code, message, result.UpdateNotChecked, nil)
	return lifecycleExecution{}, response, true, err
}

func stopSelection(command cli.Command, report validation.LifecycleSelection) (lifecycleExecution, cli.Response, bool, error) {
	failure := result.FailureValidation
	switch report.Failure {
	case validation.FailureEnvironment:
		failure = result.FailureEnvironment
	case validation.FailureSource:
		failure = result.FailureSource
	case validation.FailureConflict:
		failure = result.FailureConflict
	case validation.FailureInternal:
		failure = result.FailureInternal
	}
	commandResult, err := result.New(result.Facts{Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone, Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone, Failure: failure, UpdateDisposition: result.UpdateNotChecked, Warnings: report.Warnings, Errors: report.Problems})
	if err != nil {
		return lifecycleExecution{}, cli.Response{}, true, err
	}
	response, err := cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
	return lifecycleExecution{}, response, true, err
}

func stopValidation(command cli.Command, report validation.Report) (lifecycleExecution, cli.Response, bool, error) {
	commandResult, err := validationCommandResult(report)
	if err != nil {
		return lifecycleExecution{}, cli.Response{}, true, err
	}
	response, err := cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
	return lifecycleExecution{}, response, true, err
}

func planAsCommand(response cli.Response, command cli.Command) (cli.Response, error) {
	if response.Command() == command {
		return response, nil
	}
	return cli.NewResponse(command, response.Result(), nil, response.Data())
}

func selectionFromRecord(record installstate.Record) cli.BundleSelection {
	selection, _ := cli.NewBundleSelection(record.Selection.RequestedBundle)
	return selection
}

func recordedLifecycleSelection(record installstate.Record) validation.LifecycleSelection {
	report := validation.LifecycleSelection{
		ToolkitID:        record.ToolkitID,
		DeclarationID:    record.DeclarationID,
		ToolkitVersion:   record.ToolkitVersion,
		RequestedBundle:  record.Selection.RequestedBundle,
		ResolvedBundles:  slices.Clone(record.Selection.ResolvedBundles),
		ResolvedPackages: packageIDs(record.Packages),
		ResolvedAssets:   slices.Clone(record.Selection.ResolvedAssets),
	}
	for _, component := range record.Components {
		report.Components = append(report.Components, validation.LifecycleComponent{
			Name: component.Name, Tag: component.Tag, ToolkitVersion: component.ToolkitVersion,
			RequestedBundle: component.Selection.RequestedBundle, ResolvedBundles: slices.Clone(component.Selection.ResolvedBundles),
			ResolvedPackages: slices.Clone(component.Packages), ResolvedAssets: slices.Clone(component.Selection.ResolvedAssets),
		})
	}
	return report
}

func rollbackUnavailableWarning() result.Warning {
	warning, err := result.NewWarning("rollback_unavailable", "retained rollback artifacts are unavailable; uninstall will remove recorded owned resources without creating a rollback point", nil)
	if err != nil {
		panic(err)
	}
	return warning
}

func installationIDFor(report validation.LifecycleSelection, scope cli.Scope, scopeRoot string) domain.InstallationID {
	sourceIdentity := report.Source.Checkout()
	if report.Source.Mode() == cli.SourceGit {
		sourceIdentity = report.Source.Repository().String()
	}
	digest := sha256.Sum256([]byte(report.ToolkitID + "\x00" + sourceIdentity + "\x00" + string(scope) + "\x00" + filepath.Clean(scopeRoot)))
	id, _ := domain.NewInstallationID(hex.EncodeToString(digest[:4]))
	return id
}

func marketplaceIDFor(installationID domain.InstallationID) string {
	return "ai4j-" + installationID.String()
}

func nativePluginID(pkg installstate.NativePackage, marketplaceID string) string {
	return pkg.ID + "@" + marketplaceID
}

func nativePluginIDs(record installstate.Record) []string {
	ids := make([]string, len(record.Packages))
	for index, pkg := range record.Packages {
		ids[index] = nativePluginID(pkg, record.MarketplaceID)
	}
	return ids
}

func packageChanges(before, after []installstate.NativePackage) (removed, retained, added []installstate.NativePackage) {
	beforeByID := make(map[string]installstate.NativePackage, len(before))
	afterByID := make(map[string]installstate.NativePackage, len(after))
	for _, pkg := range before {
		beforeByID[pkg.ID] = pkg
	}
	for _, pkg := range after {
		afterByID[pkg.ID] = pkg
	}
	for _, pkg := range before {
		if _, exists := afterByID[pkg.ID]; exists {
			retained = append(retained, pkg)
		} else {
			removed = append(removed, pkg)
		}
	}
	for _, pkg := range after {
		if _, exists := beforeByID[pkg.ID]; !exists {
			added = append(added, pkg)
		}
	}
	return removed, retained, added
}

func mustInstallation(value string) domain.InstallationID {
	id, _ := domain.NewInstallationID(value)
	return id
}

func recordInstallation(records ...*installstate.Record) *domain.InstallationID {
	for _, record := range records {
		if record != nil {
			id := mustInstallation(record.InstallationID)
			return &id
		}
	}
	return nil
}

func appendUnique(values []string, value string) []string {
	result := slices.Clone(values)
	if !slices.Contains(result, value) {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func cloneRecordPtr(record *installstate.Record) *installstate.Record {
	if record == nil {
		return nil
	}
	copy := cloneRecord(*record)
	return &copy
}

func cloneRecord(record installstate.Record) installstate.Record {
	record.Selection.ResolvedBundles = slices.Clone(record.Selection.ResolvedBundles)
	record.Selection.ResolvedAssets = slices.Clone(record.Selection.ResolvedAssets)
	record.Packages = slices.Clone(record.Packages)
	record.Components = slices.Clone(record.Components)
	for index := range record.Components {
		record.Components[index].Selection.ResolvedBundles = slices.Clone(record.Components[index].Selection.ResolvedBundles)
		record.Components[index].Selection.ResolvedAssets = slices.Clone(record.Components[index].Selection.ResolvedAssets)
		record.Components[index].Packages = slices.Clone(record.Components[index].Packages)
		if record.Components[index].Source.RequestedRef != nil {
			value := *record.Components[index].Source.RequestedRef
			record.Components[index].Source.RequestedRef = &value
		}
	}
	record.NativeResources = slices.Clone(record.NativeResources)
	record.History = slices.Clone(record.History)
	if record.Source.RequestedRef != nil {
		value := *record.Source.RequestedRef
		record.Source.RequestedRef = &value
	}
	return record
}

func cloneNativeArtifacts(artifacts []installstate.NativeArtifact) []installstate.NativeArtifact {
	cloned := make([]installstate.NativeArtifact, len(artifacts))
	for index, artifact := range artifacts {
		cloned[index] = installstate.NativeArtifact{PackageID: artifact.PackageID, Bytes: slices.Clone(artifact.Bytes)}
	}
	return cloned
}

func recordsEquivalent(left, right installstate.Record) bool {
	left.LastOperation, right.LastOperation = installstate.LastOperation{}, installstate.LastOperation{}
	left.History, right.History = nil, nil
	left.Health, right.Health = "healthy", "healthy"
	return reflect.DeepEqual(left, right)
}

func sameCurrentState(current, expected installstate.Record) bool {
	return recordsEquivalent(current, expected)
}

func recordSourceRevision(record installstate.Record) string {
	if len(record.Components) != 0 {
		parts := []string{"composition-source-revision"}
		for _, component := range record.Components {
			parts = append(parts, component.Name, component.Tag, component.Source.Repository, component.Source.Transport, component.Source.Commit, component.Source.RenderedDigest)
		}
		digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
		return hex.EncodeToString(digest[:])
	}
	if record.Source.Mode == "development_source" {
		return record.Source.SourceDigest
	}
	return record.Source.Commit
}

func cliSourceRevision(source cli.Source) string {
	if source.Mode() == cli.SourceDevelopment {
		return source.SourceDigest().String()
	}
	return source.Commit().OID().String()
}

func mustCLIConflict(code, resource, message string) cli.Conflict {
	conflict, err := cli.NewConflict(code, resource, message)
	if err != nil {
		conflict, _ = cli.NewConflict("lifecycle_conflict", "installation resource", "installation resource is in conflict")
	}
	return conflict
}
