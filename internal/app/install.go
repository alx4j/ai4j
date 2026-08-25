package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/buildinfo"
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/human"
	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	"github.com/alx4j/ai4j/internal/target/claude/catalog"
	validation "github.com/alx4j/ai4j/internal/validate"
)

const nativeMutationTimeout = 30 * time.Second

type installValidation interface {
	Validate(context.Context, cli.SourceOptions) validation.Report
	InspectPlanInstall(context.Context) ([]cli.Conflict, *result.Problem)
	InspectPlanExisting(context.Context, string, string) ([]cli.Conflict, *result.Problem)
}

type installer struct {
	validation installValidation
	state      installstate.Store
	runner     validation.ProcessRunner
	home       string
	claudeRoot string
	build      buildinfo.Info
	now        func() time.Time
	random     io.Reader
	acquire    func(context.Context) (func() error, error)
}

func (i *installer) Install(ctx context.Context, request cli.InstallRequest, commandIO CommandIO) (cli.Response, error) {
	release, err := i.acquire(ctx)
	if err != nil {
		return installFailure(result.FailureConflict, "mutation_locked", "another AI4J modifying command is running", nil)
	}
	defer func() { _ = release() }()

	if response, stop, inspectErr := i.inspectInterrupted(ctx); inspectErr != nil || stop {
		return response, inspectErr
	}

	record, installed, err := i.state.Load()
	if err != nil {
		return installFailure(result.FailureConflict, "installation_state_invalid", "installation state could not be read", nil)
	}
	if !installed {
		conflicts, problem := i.validation.InspectPlanInstall(ctx)
		if problem != nil {
			return installFailure(result.FailureEnvironment, problem.Code(), problem.Message(), nil)
		}
		if len(conflicts) != 0 {
			return installConflict(conflicts, nil)
		}
	}
	report := i.validation.Validate(ctx, request.Source())
	if len(report.Problems) != 0 || !report.HasSource() {
		commandResult, resultErr := validationCommandResult(report)
		if resultErr != nil {
			return cli.Response{}, resultErr
		}
		return cli.NewResponse(cli.CommandInstall, commandResult, nil, cli.UnavailableData{})
	}
	if installed {
		if record.Source.Repository != report.Source.Repository().String() || record.Source.Commit != report.Source.Commit().OID().String() {
			return installFailure(result.FailureConflict, "already_installed", "a different AI4J source or commit is already installed; use update", report.Warnings)
		}
		conflicts, problem := i.validation.InspectPlanExisting(ctx, record.Catalog.Checksum, record.Rules.Checksum)
		if problem != nil {
			return installFailure(result.FailureEnvironment, problem.Code(), problem.Message(), report.Warnings)
		}
		if len(conflicts) != 0 {
			return installConflict(conflicts, report.Warnings)
		}
		return installNoChange(record)
	}

	plan, err := newInstallPlan(report, nil)
	if err != nil {
		return cli.Response{}, err
	}
	if expected, provided := request.ExpectedCommit(); provided && expected != report.Source.Commit().OID() {
		return installFailure(result.FailureConflict, "expected_commit_mismatch", "resolved source commit does not match --expected-commit", report.Warnings)
	}
	approval, err := approveInstall(request, commandIO, report)
	if err != nil {
		return cli.Response{}, err
	}
	if approval == approvalDeclined {
		return installCancelled(report.Warnings)
	}
	if approval != approvalGranted {
		return installFailure(result.FailureApproval, "approval_required", "installation requires explicit approval", report.Warnings)
	}

	operationID, err := newOperationID(i.random)
	if err != nil {
		return installFailure(result.FailureInternal, "operation_id_unavailable", "installation operation could not be prepared", report.Warnings)
	}
	installationID := plan.InstallationID()
	marker, err := installstate.NewInstallMarker(operationID.String(), installationID.String(), report.Source.Commit().OID().String())
	if err != nil {
		return cli.Response{}, err
	}
	if err := i.state.SaveMarker(marker); err != nil {
		return installFailure(result.FailureInternal, "operation_marker_failed", "installation operation could not be prepared", report.Warnings)
	}

	catalogDocument, err := catalog.Render(report.Source.Repository(), report.Source.Commit().OID())
	if err != nil {
		return i.recoveryResponse(operationID, installationID, "catalog_render_failed", "installation requires recovery", report.Warnings, result.PhaseApplying, nil)
	}
	if err := writeOwnedNew(i.home, i.catalogPath(), catalogDocument.Bytes()); err != nil {
		return i.recoveryResponse(operationID, installationID, "catalog_write_failed", "installation requires recovery", report.Warnings, result.PhaseApplying, nil)
	}
	if err := i.runClaude(ctx, []string{"plugin", "marketplace", "add", i.catalogRoot(), "--scope", "user"}); err != nil {
		return i.recoveryResponse(operationID, installationID, "marketplace_registration_failed", "Claude marketplace registration could not be confirmed; installation requires recovery", report.Warnings, result.PhaseApplying, nil)
	}
	if err := i.runClaude(ctx, []string{"plugin", "install", "ai4j-default@ai4j", "--scope", "user"}); err != nil {
		return i.recoveryResponse(operationID, installationID, "plugin_install_failed", "Claude plugin installation could not be confirmed; installation requires recovery", report.Warnings, result.PhaseApplying, nil)
	}
	if err := writeOwnedNew(i.home, i.rulesPath(), report.Rules); err != nil {
		return i.recoveryResponse(operationID, installationID, "rules_write_failed", "shared rules could not be written; installation requires recovery", report.Warnings, result.PhaseApplying, nil)
	}
	conflicts, problem := i.validation.InspectPlanExisting(ctx, catalogDocument.Digest(), report.RulesChecksum)
	if problem != nil || len(conflicts) != 0 {
		return i.recoveryResponse(operationID, installationID, "installation_verification_failed", "installed state could not be verified; installation requires recovery", report.Warnings, result.PhaseApplying, nil)
	}
	record = i.newRecord(operationID, installationID, report, catalogDocument.Digest())
	if err := i.state.SaveNew(record); err != nil {
		return i.recoveryResponse(operationID, installationID, "state_commit_failed", "installation state could not be committed; installation requires recovery", report.Warnings, result.PhaseApplying, nil)
	}
	if err := i.state.DeleteMarker(); err != nil {
		return i.recoveryResponse(operationID, installationID, "operation_cleanup_failed", "installation was committed but operation cleanup is required", report.Warnings, result.PhaseCommittedCleanupPending, plan.Actions())
	}
	return installCommitted(operationID, installationID, plan.Actions(), plan.ExpectedFinalState(), report.Warnings)
}

func (i *installer) inspectInterrupted(ctx context.Context) (cli.Response, bool, error) {
	marker, present, err := i.state.LoadMarker()
	if err != nil {
		response, responseErr := recoveryWithoutMarkerIdentity("operation_marker_invalid", "an interrupted installation requires manual recovery")
		return response, true, responseErr
	}
	if !present {
		return cli.Response{}, false, nil
	}
	operationID, _ := domain.NewOperationID(marker.OperationID)
	installationID, _ := domain.NewInstallationID(marker.InstallationID)
	record, installed, loadErr := i.state.Load()
	if loadErr == nil && installed && record.LastOperation.ID == marker.OperationID &&
		record.InstallationID == marker.InstallationID && record.Source.Commit == marker.Commit {
		conflicts, problem := i.validation.InspectPlanExisting(ctx, record.Catalog.Checksum, record.Rules.Checksum)
		if problem == nil && len(conflicts) == 0 {
			if deleteErr := i.state.DeleteMarker(); deleteErr == nil {
				return cli.Response{}, false, nil
			}
			response, responseErr := i.recoveryResponse(operationID, installationID, "operation_cleanup_failed", "installation was committed but operation cleanup is required", nil, result.PhaseCommittedCleanupPending, nil)
			return response, true, responseErr
		}
	}
	if loadErr == nil && !installed {
		conflicts, problem := i.validation.InspectPlanInstall(ctx)
		if problem == nil && len(conflicts) == 0 {
			if deleteErr := i.state.DeleteMarker(); deleteErr == nil {
				return cli.Response{}, false, nil
			}
		}
	}
	response, responseErr := i.recoveryResponse(operationID, installationID, "recovery_required", "an interrupted installation requires manual recovery", nil, result.PhaseApplying, nil)
	return response, true, responseErr
}

type approvalDecision string

const (
	approvalMissing  approvalDecision = "missing"
	approvalDeclined approvalDecision = "declined"
	approvalGranted  approvalDecision = "granted"
)

func approveInstall(request cli.InstallRequest, commandIO CommandIO, report validation.Report) (approvalDecision, error) {
	if request.Approved() {
		return approvalGranted, nil
	}
	if request.OutputMode() == cli.OutputJSON || !commandIO.Interactive || commandIO.Input == nil || commandIO.Output == nil {
		return approvalMissing, nil
	}
	planResponse, err := planInstallResponse(report, nil)
	if err != nil {
		return approvalMissing, err
	}
	if _, err := human.Render(commandIO.Output, planResponse); err != nil {
		return approvalMissing, err
	}
	if _, err := io.WriteString(commandIO.Output, "Proceed with installation? [y/N]: "); err != nil {
		return approvalMissing, err
	}
	line, err := bufio.NewReader(io.LimitReader(commandIO.Input, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return approvalMissing, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return approvalGranted, nil
	}
	return approvalDeclined, nil
}

func (i *installer) runClaude(ctx context.Context, arguments []string) error {
	return i.runClaudeAt(ctx, "", arguments)
}

func (i *installer) effectiveClaudeRoot() string {
	if i.claudeRoot != "" {
		return i.claudeRoot
	}
	return filepath.Join(i.home, ".claude")
}

func (i *installer) runClaudeAt(ctx context.Context, directory string, arguments []string) error {
	executable, err := i.runner.LookPath("claude")
	if err != nil {
		return err
	}
	commandContext, cancel := context.WithTimeout(ctx, nativeMutationTimeout)
	defer cancel()
	observation, err := i.runner.Run(commandContext, directory, executable, arguments, []string{
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL=1",
		"DISABLE_UPDATES=1",
	})
	if err != nil || observation.ExitCode != 0 {
		return errors.New("Claude command failed")
	}
	return nil
}

func (i *installer) newRecord(operationID domain.OperationID, installationID domain.InstallationID, report validation.Report, catalogChecksum string) installstate.Record {
	var requested *string
	if report.Source.HasRequestedRef() {
		value := report.Source.RequestedRef()
		requested = &value
	}
	resolved := make([]string, 0, len(report.Content))
	for _, item := range report.Content {
		resolved = append(resolved, item.Identifier())
	}
	slices.Sort(resolved)
	return installstate.Record{
		SchemaVersion: installstate.SchemaVersion, InstallationID: installationID.String(), ToolkitID: "ai4j", PluginID: "ai4j-default",
		Source: installstate.Source{
			Selection: report.Source.Selection().String(), Repository: report.Source.Repository().String(), RequestedRef: requested,
			RefKind: report.Source.ResolvedRefKind().String(), Commit: report.Source.Commit().OID().String(), Mode: "github",
			RenderedDigest: report.Source.RenderedDigest().String(),
		},
		Target: "claude", Host: i.host(), Scope: "user", ScopeRoot: i.home, Lifecycle: "active",
		Selection:       installstate.Selection{All: true, Assets: []string{}, Bundles: []string{}, Resolved: resolved},
		NativeResources: []string{"claude:ai4j-default@ai4j", "claude:marketplace:ai4j"}, Health: "healthy", AI4JVersion: i.build.Version(),
		Catalog: installstate.OwnedFile{Path: "state/catalog/.claude-plugin/marketplace.json", Checksum: catalogChecksum},
		Rules:   installstate.OwnedFile{Path: ".claude/rules/ai4j.md", Checksum: report.RulesChecksum},
		LastOperation: installstate.LastOperation{
			ID: operationID.String(), Timestamp: i.now().UTC().Truncate(time.Second).Format(time.RFC3339),
		},
	}
}

func (i *installer) catalogRoot() string {
	return filepath.Join(i.state.Root(), "catalog")
}

func (i *installer) catalogPath() string {
	return filepath.Join(i.catalogRoot(), ".claude-plugin", "marketplace.json")
}

func (i *installer) rulesPath() string { return filepath.Join(i.home, ".claude", "rules", "ai4j.md") }

func (i *installer) host() string {
	if i.build.TargetOS() == "windows" && i.build.TargetArch() == "amd64" {
		return "windows-amd64"
	}
	return "darwin-arm64"
}

func newOperationID(source io.Reader) (domain.OperationID, error) {
	value := make([]byte, 12)
	if source == nil {
		source = rand.Reader
	}
	if _, err := io.ReadFull(source, value); err != nil {
		return domain.OperationID{}, err
	}
	return domain.NewOperationID("operation-" + hex.EncodeToString(value))
}

func writeOwnedNew(home, path string, contents []byte) error {
	relative, err := filepath.Rel(home, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("owned path is outside the user home")
	}
	current := home
	for _, component := range strings.Split(filepath.Dir(relative), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			if mkdirErr := os.Mkdir(current, 0o700); mkdirErr != nil {
				return mkdirErr
			}
		case statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || hostPathUnsafe(current):
			return errors.New("owned path parent is unsafe")
		}
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("owned destination is occupied")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if hostPathUnsafe(filepath.Dir(path)) {
		return errors.New("owned path parent is unsafe")
	}
	if err := diskcapacity.Require(filepath.Dir(path), uint64(len(contents))); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ai4j-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Link(temporaryPath, path)
}

func installFailure(failure result.Failure, code, message string, warnings []result.Warning) (cli.Response, error) {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: failure, UpdateDisposition: result.UpdateNotChecked, Warnings: warnings, Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandInstall, commandResult, nil, cli.UnavailableData{})
}

func installCancelled(warnings []result.Warning) (cli.Response, error) {
	problem, err := result.NewProblem("installation_cancelled", "installation was declined before mutation", nil)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusCancelled, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureCancellation, UpdateDisposition: result.UpdateNotChecked,
		Warnings: warnings, Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandInstall, commandResult, nil, cli.UnavailableData{})
}

func installConflict(conflicts []cli.Conflict, warnings []result.Warning) (cli.Response, error) {
	problems := make([]result.Problem, 0, len(conflicts))
	for _, conflict := range conflicts {
		item, _ := result.NewContext("resource", conflict.Resource())
		problem, _ := result.NewProblem(conflict.Code(), conflict.Message(), []result.Context{item})
		problems = append(problems, problem)
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureConflict, UpdateDisposition: result.UpdateNotChecked, Warnings: warnings, Errors: problems,
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandInstall, commandResult, nil, cli.UnavailableData{})
}

func (i *installer) recoveryResponse(operationID domain.OperationID, installationID domain.InstallationID, code, message string, warnings []result.Warning, phase result.Phase, actions []cli.Action) (cli.Response, error) {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return cli.Response{}, err
	}
	durable := result.DurableChangeNone
	if phase == result.PhaseCommittedCleanupPending {
		durable = result.DurableCommittedWithDiff
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: phase, Outcome: outcomeForInstallPhase(phase),
		Mutation: result.MutationStarted, DurableChange: durable, Failure: result.FailureRecovery,
		UpdateDisposition: result.UpdateNotChecked, Warnings: warnings, Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	final, _ := cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	data, err := cli.NewMutationData(cli.OperationInstall, commandResult, &installationID, actions, final, result.UpdateNotChecked)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandInstall, commandResult, &operationID, data)
}

func recoveryWithoutMarkerIdentity(code, message string) (cli.Response, error) {
	problem, _ := result.NewProblem(code, message, nil)
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseApplying, Outcome: result.OutcomePending,
		Mutation: result.MutationStarted, DurableChange: result.DurableChangeNone, Failure: result.FailureRecovery,
		UpdateDisposition: result.UpdateNotChecked, Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	final, _ := cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	data, err := cli.NewMutationData(cli.OperationInstall, commandResult, nil, nil, final, result.UpdateNotChecked)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandInstall, commandResult, nil, data)
}

func installNoChange(record installstate.Record) (cli.Response, error) {
	installationID, err := domain.NewInstallationID(record.InstallationID)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusNoChange, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureNone, UpdateDisposition: result.UpdateNotChecked,
	})
	if err != nil {
		return cli.Response{}, err
	}
	final, _ := cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	data, err := cli.NewMutationData(cli.OperationInstall, commandResult, &installationID, nil, final, result.UpdateNotChecked)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandInstall, commandResult, nil, data)
}

func installCommitted(operationID domain.OperationID, installationID domain.InstallationID, actions []cli.Action, final cli.FinalState, warnings []result.Warning) (cli.Response, error) {
	commandResult, err := result.New(result.Facts{
		Status: result.StatusOK, Phase: result.PhaseComplete, Outcome: result.OutcomeCommitted,
		Mutation: result.MutationStarted, DurableChange: result.DurableCommittedWithDiff,
		Failure: result.FailureNone, UpdateDisposition: result.UpdateNotChecked, Warnings: warnings,
	})
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewMutationData(cli.OperationInstall, commandResult, &installationID, actions, final, result.UpdateNotChecked)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandInstall, commandResult, &operationID, data)
}

func outcomeForInstallPhase(phase result.Phase) result.Outcome {
	if phase == result.PhaseCommittedCleanupPending {
		return result.OutcomeCommitted
	}
	return result.OutcomePending
}

func newInstaller(validationService installValidation, state installstate.Store, runner validation.ProcessRunner, home string, build buildinfo.Info, acquire func(context.Context) (func() error, error)) *installer {
	return &installer{
		validation: validationService, state: state, runner: runner, home: home, build: build,
		now: time.Now, random: rand.Reader, acquire: acquire,
	}
}
