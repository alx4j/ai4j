// Package validate implements toolkit validation and target-native builds.
package validate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/alx4j/ai4j/internal/buildinfo"
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	gitremote "github.com/alx4j/ai4j/internal/source/gitremote"
	"github.com/alx4j/ai4j/internal/workspace"
)

type Failure string

const (
	FailureNone        Failure = ""
	FailureEnvironment Failure = "environment"
	FailureSource      Failure = "source"
	FailureValidation  Failure = "validation"
	FailureConflict    Failure = "conflict"
	FailureInternal    Failure = "internal"
)

type Report struct {
	Source        cli.Source
	Content       []cli.ContentItem
	Rules         []byte
	RulesChecksum string
	Warnings      []result.Warning
	Problems      []result.Problem
	Failure       Failure
}

type UpdateReport struct {
	Report      Report
	Disposition gitsource.UpdateDisposition
}

type Config struct {
	GOOS        string
	GOARCH      string
	Home        string
	ClaudeRoot  string
	BuildCommit string
	Runner      ProcessRunner
	TempRoot    string
	Capacity    func(string, uint64) error
}

type Service struct{ config Config }

type acquisition struct {
	provenance   gitsource.SourceProvenance
	inventory    gitsource.TreeInventory
	checkout     string
	sourceDigest string
	dirty        bool
}

func (a acquisition) local() bool { return a.checkout != "" }

func NewProductionService(build buildinfo.Info) Service {
	home, _ := os.UserHomeDir()
	return Service{config: Config{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Home: home, ClaudeRoot: filepath.Join(home, ".claude"),
		BuildCommit: build.Revision(), Runner: OSProcessRunner{}, Capacity: diskcapacity.Require,
	}}
}

func NewService(config Config) (Service, error) {
	if config.GOOS == "" || config.GOARCH == "" || config.Home == "" || config.BuildCommit == "" || config.Runner == nil {
		return Service{}, errors.New("validate service configuration is incomplete")
	}
	if config.ClaudeRoot == "" {
		config.ClaudeRoot = filepath.Join(config.Home, ".claude")
	}
	if !filepath.IsAbs(config.ClaudeRoot) {
		return Service{}, errors.New("Claude configuration root must be absolute")
	}
	if config.Capacity == nil {
		config.Capacity = diskcapacity.Require
	}
	return Service{config: config}, nil
}

func (s Service) Validate(ctx context.Context, options cli.SourceOptions) Report {
	return s.validate(ctx, options, nil)
}

func (s Service) ValidateUpdate(ctx context.Context, options cli.SourceOptions, installed domain.CommitOID) UpdateReport {
	disposition := gitsource.UpdateSourceError
	report := s.validate(ctx, options, func(operationContext context.Context, workspace string, acquired acquisition) error {
		var err error
		disposition, err = s.updateDisposition(operationContext, workspace, installed, acquired.provenance.Commit().OID())
		return err
	})
	if report.Failure != FailureNone {
		disposition = gitsource.UpdateSourceError
	}
	return UpdateReport{Report: report, Disposition: disposition}
}

func (s Service) validate(ctx context.Context, options cli.SourceOptions, inspect func(context.Context, string, acquisition) error) (report Report) {
	if problem := s.preflight(ctx); problem != nil {
		return Report{Problems: []result.Problem{*problem}, Failure: FailureEnvironment}
	}
	operationContext, cancelOperation := context.WithTimeout(ctx, gitsource.AggregateOperationTimeout)
	defer cancelOperation()
	operationWorkspace, err := workspace.Create(s.config.TempRoot, workspace.ValidateSource)
	if err != nil {
		return failureReport(FailureEnvironment, "workspace_create_failed", "temporary source workspace could not be created")
	}
	workspacePath := operationWorkspace.Path()
	defer func() {
		if err := operationWorkspace.Close(); err != nil {
			report = failureReport(FailureEnvironment, "workspace_cleanup_failed", "temporary source workspace could not be removed")
		}
	}()

	acquired, err := s.acquireOptions(operationContext, workspacePath, options)
	if err != nil {
		if code, message, ok := diskCapacityProblem(err); ok {
			return failureReport(FailureEnvironment, code, message)
		}
		return failureReport(FailureSource, localSourceErrorCode(err), localSourceErrorMessage(err))
	}
	if inspect != nil {
		if acquired.local() {
			return failureReport(FailureSource, "source_history_unavailable", "local development sources do not have a remote Git update history")
		}
		if err := inspect(operationContext, workspacePath, acquired); err != nil {
			return failureReport(FailureSource, "source_history_unavailable", "Git source history could not be evaluated")
		}
	}
	validated, err := validatePackage(workspacePath, acquired.inventory)
	if err != nil {
		source, sourceErr := s.newCLISource(acquired, treeDigest(acquired.inventory.Tree()))
		if sourceErr != nil {
			return failureReport(FailureInternal, "internal_error", "validation result could not be constructed")
		}
		code, message := packageProblem(err)
		problem, _ := result.NewProblem(code, message, nil)
		return Report{Source: source, Problems: []result.Problem{problem}, Failure: FailureValidation}
	}
	source, err := s.newCLISource(acquired, validated.digest)
	if err != nil {
		return failureReport(FailureInternal, "internal_error", "validation result could not be constructed")
	}
	resolved, selectionErr := resolveSelection(validated.model, selection{target: cli.BuildTargetClaude, host: configuredBuildHost(s.config), all: true})
	if selectionErr != nil {
		code, message := packageProblem(selectionErr)
		return reportWithSource(source, FailureValidation, code, message)
	}
	if selectionErr = validateSelectedExecutableFormats(workspacePath, resolved, configuredBuildHost(s.config), validated.model); selectionErr != nil {
		code, message := packageProblem(selectionErr)
		return reportWithSource(source, FailureValidation, code, message)
	}
	validated.content, selectionErr = selectedContent(workspacePath, validated.model, resolved)
	if selectionErr != nil {
		code, message := packageProblem(selectionErr)
		return reportWithSource(source, FailureValidation, code, message)
	}
	dependencyWarnings, dependencyProblem := s.checkHostDependencies(validated.content)
	if dependencyProblem != nil {
		return Report{Source: source, Problems: []result.Problem{*dependencyProblem}, Failure: FailureValidation}
	}

	claude, err := s.config.Runner.LookPath("claude")
	if err != nil {
		return reportWithSource(source, FailureEnvironment, "claude_not_found", "Claude Code executable is required")
	}
	if len(validated.nativePackagePaths) == 0 {
		return reportWithSource(source, FailureValidation, "native_validation_failed", "toolkit does not declare a Claude native package")
	}
	for _, packagePath := range validated.nativePackagePaths {
		nativeContext, cancelNative := context.WithTimeout(operationContext, 2*time.Minute)
		native, runErr := s.config.Runner.Run(nativeContext, filepath.Join(workspacePath, filepath.FromSlash(packagePath)), claude, []string{"plugin", "validate", ".", "--strict"}, claudeEnvironment())
		cancelNative()
		if runErr != nil || native.ExitCode != 0 {
			return reportWithSource(source, FailureValidation, "native_validation_failed", "Claude Code rejected the toolkit package")
		}
	}

	warning, err := result.NewWarning("active_content_trust", "validated instructions can influence AI behavior and installed executables may later run with your permissions", nil)
	if err != nil {
		return failureReport(FailureInternal, "internal_error", "validation result could not be constructed")
	}
	dependencyWarnings = append(dependencyWarnings, warning)
	return Report{
		Source: source, Content: validated.content,
		Rules: append([]byte(nil), validated.rules...), RulesChecksum: validated.rulesChecksum,
		Warnings: dependencyWarnings,
	}
}

func (s Service) updateDisposition(ctx context.Context, workspace string, installed, desired domain.CommitOID) (gitsource.UpdateDisposition, error) {
	if !installed.Valid() || !desired.Valid() {
		return gitsource.UpdateSourceError, errors.New("update commits are invalid")
	}
	if installed == desired {
		return gitsource.UpdateNoChange, nil
	}
	command, err := gitsource.NewIsAncestorCommand(installed, desired)
	if err != nil {
		return gitsource.UpdateSourceError, err
	}
	gitExecutable, err := s.config.Runner.LookPath("git")
	if err != nil {
		return gitsource.UpdateSourceError, err
	}
	commandContext, cancel := context.WithTimeout(ctx, command.TimeoutMaximum())
	defer cancel()
	observation, runErr := s.config.Runner.Run(commandContext, filepath.Join(workspace, ".git"), gitExecutable, command.Arguments(), gitEnvironment())
	if runErr != nil {
		return gitsource.UpdateSourceError, runErr
	}
	if observation.ExitCode == 0 {
		return gitsource.UpdateAvailable, nil
	}
	if observation.ExitCode == 1 {
		return gitsource.UpdateRefRewritten, nil
	}
	return gitsource.UpdateSourceError, fmt.Errorf("inspect Git ancestry: exit code %d", observation.ExitCode)
}

func (s Service) checkHostDependencies(content []cli.ContentItem) ([]result.Warning, *result.Problem) {
	checked := map[string]bool{}
	var warnings []result.Warning
	for _, item := range content {
		execution, present := item.Execution()
		if !present || execution.Ownership() != cli.ExecutionHostResolved || checked[execution.Command()] {
			continue
		}
		checked[execution.Command()] = true
		if _, err := s.config.Runner.LookPath(execution.Command()); err == nil {
			continue
		}
		contextItem, _ := result.NewContext("executable", execution.Command())
		if execution.Dependency() == cli.DependencyRequired {
			problem, _ := result.NewProblem("missing_required_runtime", "a required host executable is unavailable", []result.Context{contextItem})
			return nil, &problem
		}
		warning, _ := result.NewWarning("missing_optional_runtime", "an optional host executable is unavailable", []result.Context{contextItem})
		warnings = append(warnings, warning)
	}
	return warnings, nil
}

func (s Service) preflight(ctx context.Context) *result.Problem {
	if ctx == nil || ctx.Err() != nil {
		problem, _ := result.NewProblem("validation_cancelled", "validation was cancelled", nil)
		return &problem
	}
	if !supportedHost(s.config.GOOS, s.config.GOARCH) {
		problem, _ := result.NewProblem("unsupported_host", "AI4J supports Darwin ARM64 and Windows AMD64", nil)
		return &problem
	}
	gitExecutable, err := s.config.Runner.LookPath("git")
	if err != nil {
		problem, _ := result.NewProblem("git_not_found", "Git executable is required", nil)
		return &problem
	}
	claudeExecutable, err := s.config.Runner.LookPath("claude")
	if err != nil {
		problem, _ := result.NewProblem("claude_not_found", "Claude Code executable is required", nil)
		return &problem
	}
	if !s.probeExecutable(ctx, gitExecutable, gitEnvironment()) {
		problem, _ := result.NewProblem("git_unusable", "Git version probe failed", nil)
		return &problem
	}
	if !s.probeExecutable(ctx, claudeExecutable, claudeEnvironment()) {
		problem, _ := result.NewProblem("claude_unusable", "Claude Code version probe failed", nil)
		return &problem
	}
	info, err := os.Stat(s.config.ClaudeRoot)
	if err != nil || !info.IsDir() {
		problem, _ := result.NewProblem("claude_config_unavailable", "effective Claude user configuration directory is required", nil)
		return &problem
	}
	if _, err := domain.NewBuildCommit(s.config.BuildCommit); err != nil {
		problem, _ := result.NewProblem("build_identity_unavailable", "AI4J build commit is unavailable", nil)
		return &problem
	}
	return nil
}

func supportedHost(goos, goarch string) bool {
	return goos == "darwin" && goarch == "arm64" || goos == "windows" && goarch == "amd64"
}

func configuredBuildHost(config Config) cli.BuildHost {
	if config.GOOS == "windows" && config.GOARCH == "amd64" {
		return cli.BuildHostWindowsAMD64
	}
	return cli.BuildHostDarwinARM64
}

func (s Service) probeExecutable(ctx context.Context, executable string, environment []string) bool {
	probeContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	observation, err := s.config.Runner.Run(probeContext, "", executable, []string{"--version"}, environment)
	return err == nil && observation.ExitCode == 0 && len(observation.Stdout) != 0 && len(observation.Stderr) == 0
}

func (s Service) acquire(ctx context.Context, workspace string, effective gitremote.EffectiveSource) (acquisition, error) {
	gitExecutable, err := s.config.Runner.LookPath("git")
	if err != nil {
		return acquisition{}, err
	}
	request, err := gitsource.NewResolutionRequest(effective)
	if err != nil {
		return acquisition{}, err
	}
	authMode := gitsource.AuthenticationCredentialHelperHTTPS
	if effective.Transport() == domain.SSHGitTransport() {
		authMode = gitsource.AuthenticationDefaultKeySSH
	}
	auth, err := gitsource.NewAuthenticationProjection(effective.Repository(), effective.Transport(), authMode)
	if err != nil {
		return acquisition{}, err
	}
	if _, err = s.runGit(ctx, gitExecutable, workspace, gitsource.NewInitializeCommand()); err != nil {
		return acquisition{}, err
	}
	gitDirectory := filepath.Join(workspace, ".git")

	enumerate, err := gitsource.NewEnumerateReferencesCommand(request, auth)
	if err != nil {
		return acquisition{}, err
	}
	output, err := s.runGit(ctx, gitExecutable, gitDirectory, enumerate)
	if err != nil {
		return acquisition{}, err
	}
	advertisement, err := gitsource.ParseRemoteAdvertisement(request, output)
	if err != nil {
		return acquisition{}, err
	}
	resolution, err := gitsource.ResolveReference(request, advertisement)
	if err != nil {
		return acquisition{}, err
	}
	fetch, err := gitsource.NewFetchCommand(resolution, auth)
	if err != nil {
		return acquisition{}, err
	}
	if _, err = s.runGit(ctx, gitExecutable, gitDirectory, fetch); err != nil {
		return acquisition{}, err
	}

	objectCommand, err := gitsource.NewObjectTypeCommand(resolution)
	if err != nil {
		return acquisition{}, err
	}
	output, err = s.runGit(ctx, gitExecutable, gitDirectory, objectCommand)
	if err != nil {
		return acquisition{}, err
	}
	selected, err := gitsource.NewSelectedObjectProof(resolution, output)
	if err != nil {
		return acquisition{}, err
	}
	var commit gitsource.ProvenCommit
	if selected.Type() == gitsource.SelectedCommitObject {
		commit, err = gitsource.NewDirectProvenCommit(selected)
	} else {
		peel, commandErr := gitsource.NewPeelCommitCommand(selected)
		if commandErr != nil {
			return acquisition{}, commandErr
		}
		output, commandErr = s.runGit(ctx, gitExecutable, gitDirectory, peel)
		if commandErr != nil {
			return acquisition{}, commandErr
		}
		commit, err = gitsource.NewPeeledProvenCommit(selected, output)
	}
	if err != nil {
		return acquisition{}, err
	}

	treeCommand, err := gitsource.NewCommitTreeCommand(commit)
	if err != nil {
		return acquisition{}, err
	}
	output, err = s.runGit(ctx, gitExecutable, gitDirectory, treeCommand)
	if err != nil {
		return acquisition{}, err
	}
	proof, err := gitsource.NewCommitTreeProof(commit, output)
	if err != nil {
		return acquisition{}, err
	}
	listTree, err := gitsource.NewListTreeCommand(proof)
	if err != nil {
		return acquisition{}, err
	}
	output, err = s.runGit(ctx, gitExecutable, gitDirectory, listTree)
	if err != nil {
		return acquisition{}, err
	}
	inventory, err := gitsource.ParseTreeInventory(proof.Tree(), output)
	if err != nil {
		return acquisition{}, err
	}
	if err := s.config.Capacity(workspace, inventory.TreeBytes()); err != nil {
		return acquisition{}, err
	}
	plan, err := gitsource.NewMaterializationPlan(proof, inventory)
	if err != nil {
		return acquisition{}, err
	}
	readTree, err := gitsource.NewReadTreeCommand(plan)
	if err != nil {
		return acquisition{}, err
	}
	if _, err = s.runGit(ctx, gitExecutable, gitDirectory, readTree); err != nil {
		return acquisition{}, err
	}
	batches, err := gitsource.PlanCheckoutAttributeBatches(plan)
	if err != nil {
		return acquisition{}, err
	}
	proofs := make([]gitsource.CheckoutAttributeBatchProof, len(batches))
	for index, batch := range batches {
		command, commandErr := gitsource.NewCheckAttributesCommand(batch)
		if commandErr != nil {
			return acquisition{}, commandErr
		}
		output, commandErr = s.runGit(ctx, gitExecutable, gitDirectory, command)
		if commandErr != nil {
			return acquisition{}, commandErr
		}
		proofs[index], commandErr = gitsource.ValidateCheckoutAttributes(batch, output)
		if commandErr != nil {
			return acquisition{}, commandErr
		}
	}
	approval, err := gitsource.CompleteCheckoutAttributeCoverage(plan, proofs)
	if err != nil {
		return acquisition{}, err
	}
	materialize, err := gitsource.NewCheckoutIndexCommand(approval)
	if err != nil {
		return acquisition{}, err
	}
	if _, err = s.runGit(ctx, gitExecutable, gitDirectory, materialize); err != nil {
		return acquisition{}, err
	}
	checkout, err := gitsource.NewCheckoutDetachedCommand(approval)
	if err != nil {
		return acquisition{}, err
	}
	if _, err = s.runGit(ctx, gitExecutable, gitDirectory, checkout); err != nil {
		return acquisition{}, err
	}
	output, err = s.runGit(ctx, gitExecutable, gitDirectory, gitsource.NewListIndexCommand())
	if err != nil || gitsource.ValidateIndex(inventory, output) != nil {
		return acquisition{}, errors.New("materialized index does not match source")
	}
	output, err = s.runGit(ctx, gitExecutable, gitDirectory, gitsource.NewStatusCommand())
	if err != nil || gitsource.ValidateCleanStatus(output) != nil {
		return acquisition{}, errors.New("materialized workspace is not clean")
	}

	provenance, err := gitsource.NewSourceProvenance(proof)
	if err != nil {
		return acquisition{}, err
	}
	return acquisition{provenance: provenance, inventory: inventory}, nil
}

func diskCapacityProblem(err error) (string, string, bool) {
	switch {
	case errors.Is(err, diskcapacity.ErrInsufficient):
		return "insufficient_disk_space", "the destination filesystem does not have enough space for the bounded operation", true
	case errors.Is(err, diskcapacity.ErrUnavailable):
		return "disk_capacity_unavailable", "destination disk capacity could not be verified", true
	default:
		return "", "", false
	}
}

func (s Service) acquireOptions(ctx context.Context, workspace string, options cli.SourceOptions) (acquisition, error) {
	if options.HasCheckout() {
		return s.acquireLocal(ctx, workspace, options)
	}
	selection, err := gitremote.NewSelectionInput(options.Repository(), options.HasRepository(), options.Reference(), options.HasReference())
	if err != nil {
		return acquisition{}, err
	}
	effective, err := gitremote.Resolve(selection)
	if err != nil {
		return acquisition{}, err
	}
	return s.acquire(ctx, workspace, effective)
}

func (s Service) newCLISource(acquired acquisition, digestValue string) (cli.Source, error) {
	digest, err := domain.NewRenderedDigest(digestValue)
	if err != nil {
		return cli.Source{}, err
	}
	buildCommit, err := domain.NewBuildCommit(s.config.BuildCommit)
	if err != nil {
		return cli.Source{}, err
	}
	if acquired.local() {
		sourceDigest, digestErr := domain.NewRenderedDigest(acquired.sourceDigest)
		if digestErr != nil {
			return cli.Source{}, digestErr
		}
		return cli.NewDevelopmentSource(acquired.checkout, sourceDigest, digest, buildCommit, acquired.dirty)
	}
	rendered, err := gitsource.NewRenderedProvenance(acquired.provenance, digest, buildCommit)
	if err != nil {
		return cli.Source{}, err
	}
	return cli.NewSource(rendered)
}

func treeDigest(tree domain.TreeOID) string {
	value := sha256.Sum256([]byte(tree.String()))
	return hex.EncodeToString(value[:])
}

func (s Service) runGit(ctx context.Context, executable, directory string, command gitsource.Command) ([]byte, error) {
	if !command.Valid() {
		return nil, errors.New("invalid Git command")
	}
	commandContext, cancel := context.WithTimeout(ctx, command.TimeoutMaximum())
	defer cancel()
	environment := gitEnvironment()
	if _, authenticated := command.Authentication(); authenticated {
		environment = gitAuthenticatedEnvironment()
	}
	result, err := s.config.Runner.Run(commandContext, directory, executable, command.Arguments(), environment)
	if err != nil || result.ExitCode != 0 || len(result.Stderr) != 0 && command.OutputGrammar() != gitsource.NoOutputGrammar {
		return nil, fmt.Errorf("Git operation %s failed", command.Operation())
	}
	return result.Stdout, nil
}

func gitEnvironment() []string {
	return []string{
		"GIT_ATTR_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_LFS_SKIP_SMUDGE=1", "GIT_OPTIONAL_LOCKS=0", "GIT_PROTOCOL_FROM_USER=0",
		"GIT_TERMINAL_PROMPT=0", "LANG=C", "LC_ALL=C",
	}
}

func gitAuthenticatedEnvironment() []string {
	return []string{
		"GIT_ATTR_NOSYSTEM=1", "GIT_LFS_SKIP_SMUDGE=1",
		"GIT_OPTIONAL_LOCKS=0", "GIT_PROTOCOL_FROM_USER=0", "GIT_TERMINAL_PROMPT=0",
		"LANG=C", "LC_ALL=C",
	}
}

func claudeEnvironment() []string {
	return []string{
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL=1",
		"DISABLE_UPDATES=1",
	}
}

func failureReport(failure Failure, code, message string) Report {
	problem, _ := result.NewProblem(code, message, nil)
	return Report{Problems: []result.Problem{problem}, Failure: failure}
}

func reportWithSource(source cli.Source, failure Failure, code, message string) Report {
	report := failureReport(failure, code, message)
	report.Source = source
	return report
}

func (r Report) HasSource() bool { return r.Source.Valid() }
