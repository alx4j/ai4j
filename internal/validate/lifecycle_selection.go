package validate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	"github.com/alx4j/ai4j/internal/workspace"
)

// LifecycleSelection is the selected, validated Claude package needed by the
// lifecycle. It intentionally contains no source checkout path: acquisition is
// private and ephemeral, and native registration remains exact-commit based.
type LifecycleSelection struct {
	Source         cli.Source
	ToolkitID      string
	DeclarationID  string
	ToolkitVersion string
	PackageID      string
	PackagePath    string
	Content        []cli.ContentItem
	Rules          []byte
	RulesChecksum  string
	NativeArtifact []byte
	Resolved       []string
	Warnings       []result.Warning
	Problems       []result.Problem
	Failure        Failure
}

func (r LifecycleSelection) HasSource() bool { return r.Source.Valid() }

// SelectLifecycle resolves one Claude selection without writing target or
// installation state.
func (s Service) SelectLifecycle(ctx context.Context, options cli.SourceOptions, all bool, assets, bundles []string) (report LifecycleSelection) {
	if problem := s.preflight(ctx); problem != nil {
		return lifecycleSelectionFailure(FailureEnvironment, problem.Code(), problem.Message())
	}
	operationContext, cancelOperation := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelOperation()
	operationWorkspace, err := workspace.Create(s.config.TempRoot, workspace.Lifecycle)
	if err != nil {
		return lifecycleSelectionFailure(FailureEnvironment, "workspace_create_failed", "temporary source workspace could not be created")
	}
	workspacePath := operationWorkspace.Path()
	defer func() {
		if err := operationWorkspace.Close(); err != nil && len(report.Problems) == 0 {
			report = lifecycleSelectionFailure(FailureEnvironment, "workspace_cleanup_failed", "temporary source workspace could not be removed")
		}
	}()
	acquired, err := s.acquireOptions(operationContext, workspacePath, options)
	if err != nil {
		if code, message, ok := diskCapacityProblem(err); ok {
			return lifecycleSelectionFailure(FailureEnvironment, code, message)
		}
		return lifecycleSelectionFailure(FailureSource, localSourceErrorCode(err), localSourceErrorMessage(err))
	}
	validated, err := validatePackage(workspacePath, acquired.inventory)
	if err != nil {
		code, message := packageProblem(err)
		return lifecycleSelectionFailure(FailureValidation, code, message)
	}
	source, err := s.newCLISource(acquired, validated.digest)
	if err != nil {
		return lifecycleSelectionFailure(FailureInternal, "internal_error", "lifecycle selection could not be constructed")
	}
	request := selection{target: cli.BuildTargetClaude, host: configuredBuildHost(s.config), all: all, assets: slices.Clone(assets), bundles: slices.Clone(bundles)}
	resolved, err := resolveSelection(validated.model, request)
	if err != nil {
		code, message := packageProblem(err)
		return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem(code, message)}, Failure: FailureValidation}
	}
	if err = validateSelectedExecutableFormats(workspacePath, resolved, request.host, validated.model); err != nil {
		code, message := packageProblem(err)
		return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem(code, message)}, Failure: FailureValidation}
	}
	content, err := selectedContent(workspacePath, validated.model, resolved)
	if err != nil {
		code, message := packageProblem(err)
		return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem(code, message)}, Failure: FailureValidation}
	}
	packages := selectedPackages(validated.model.manifest.Targets["claude"].Packages, resolved)
	if len(packages) != 1 {
		return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem("unsupported_selection", "Claude user lifecycle currently requires selection of exactly one native package")}, Failure: FailureValidation}
	}
	var rules []byte
	var rulesChecksum string
	for _, selected := range resolved {
		if selected.asset.Type != "instruction" {
			continue
		}
		if rules != nil {
			return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem("unsupported_selection", "Claude user lifecycle currently supports one selected persistent instruction")}, Failure: FailureValidation}
		}
		rules, err = readTrackedFile(workspacePath, selected.path, validated.model.tracked)
		if err != nil {
			return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem("package_read_failed", "selected instruction could not be read")}, Failure: FailureValidation}
		}
		digest := sha256.Sum256(rules)
		rulesChecksum = hex.EncodeToString(digest[:])
	}
	warnings, dependencyProblem := s.checkHostDependencies(content)
	if dependencyProblem != nil {
		return LifecycleSelection{Source: source, Problems: []result.Problem{*dependencyProblem}, Failure: FailureValidation}
	}
	claude, _ := s.config.Runner.LookPath("claude")
	nativeContext, cancelNative := context.WithTimeout(operationContext, 2*time.Minute)
	native, runErr := s.config.Runner.Run(nativeContext, filepath.Join(workspacePath, filepath.FromSlash(packages[0].Path)), claude, []string{"plugin", "validate", ".", "--strict"}, claudeEnvironment())
	cancelNative()
	if runErr != nil || native.ExitCode != 0 {
		return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem("native_validation_failed", "Claude Code rejected the selected native package")}, Failure: FailureValidation}
	}
	warning, _ := result.NewWarning("active_content_trust", "selected instructions can influence AI behavior and installed executables may later run with your permissions", nil)
	warnings = append(warnings, warning)
	artifact, err := archiveNativePackage(workspacePath, packages[0].Path, validated.model.tracked)
	if err != nil {
		return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem("native_artifact_failed", "selected native package could not be retained for rollback")}, Failure: FailureValidation}
	}
	resolvedIDs := make([]string, len(resolved))
	for index, asset := range resolved {
		resolvedIDs[index] = asset.asset.ID
	}
	return LifecycleSelection{
		Source: source, ToolkitID: validated.model.manifest.Toolkit.ID, DeclarationID: declarationID(validated.model.manifest.Toolkit), ToolkitVersion: validated.model.manifest.Toolkit.Version,
		PackageID: packages[0].ID, PackagePath: packages[0].Path, Content: content, Rules: slices.Clone(rules),
		RulesChecksum: rulesChecksum, NativeArtifact: artifact, Resolved: resolvedIDs, Warnings: warnings,
	}
}

func declarationID(toolkit toolkitIdentity) string {
	if toolkit.DeclarationID != "" {
		return toolkit.DeclarationID
	}
	return toolkit.ID
}

func archiveNativePackage(root, packagePath string, tracked map[string]gitsource.TreeEntry) ([]byte, error) {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, source := range filesUnder(tracked, packagePath) {
		content, err := readTrackedFile(root, source, tracked)
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		relative := strings.TrimPrefix(source, packagePath+"/")
		header := &zip.FileHeader{Name: relative, Method: zip.Store}
		header.SetModTime(time.Unix(0, 0).UTC())
		mode := os.FileMode(0o644)
		if tracked[source].Mode() == gitsource.SourceExecutableFile {
			mode = 0o755
		}
		header.SetMode(mode)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		if _, err := writer.Write(content); err != nil {
			_ = archive.Close()
			return nil, err
		}
	}
	if err := archive.Close(); err != nil || output.Len() > 16<<20 {
		return nil, errors.New("native artifact exceeds retention bounds")
	}
	return output.Bytes(), nil
}

func selectedPackages(packages []nativePackage, resolved []resolvedAsset) []nativePackage {
	selected := make(map[string]struct{}, len(resolved))
	for _, asset := range resolved {
		selected[asset.asset.ID] = struct{}{}
	}
	var result []nativePackage
	for _, unit := range packages {
		for _, asset := range unit.Assets {
			if _, ok := selected[asset]; ok {
				result = append(result, unit)
				break
			}
		}
	}
	return result
}

func lifecycleSelectionFailure(failure Failure, code, message string) LifecycleSelection {
	return LifecycleSelection{Problems: []result.Problem{mustProblem(code, message)}, Failure: failure}
}

func mustProblem(code, message string) result.Problem {
	problem, _ := result.NewProblem(code, message, nil)
	return problem
}
