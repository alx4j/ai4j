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

// LifecyclePackage is one selected atomic Claude package and its exact retained
// rollback material.
type LifecyclePackage struct {
	ID             string
	Path           string
	Component      string
	Source         cli.Source
	NativeArtifact []byte
}

// LifecycleComponent retains the independently resolved selection that
// contributed content to one composed installation.
type LifecycleComponent struct {
	Name             string
	Tag              string
	Source           cli.Source
	ToolkitVersion   string
	RequestedBundle  string
	ResolvedBundles  []string
	ResolvedPackages []string
	ResolvedAssets   []string
}

// LifecycleSelection is the selected, validated Claude package set needed by the
// lifecycle. It intentionally contains no source checkout path: acquisition is
// private and ephemeral, and native registration remains exact-commit based.
type LifecycleSelection struct {
	Source           cli.Source
	ToolkitID        string
	DeclarationID    string
	ToolkitVersion   string
	RequestedBundle  string
	ResolvedBundles  []string
	ResolvedPackages []string
	ResolvedAssets   []string
	Packages         []LifecyclePackage
	Components       []LifecycleComponent
	Content          []cli.ContentItem
	Rules            []byte
	RulesChecksum    string
	Warnings         []result.Warning
	Problems         []result.Problem
	Failure          Failure
}

func (r LifecycleSelection) HasSource() bool { return r.Source.Valid() }

// SelectLifecycle resolves one top-level Claude bundle without writing target or
// installation state.
func (s Service) SelectLifecycle(ctx context.Context, options cli.SourceOptions, bundleID string) (report LifecycleSelection) {
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
	request := selection{target: cli.BuildTargetClaude, host: configuredBuildHost(s.config), bundles: []string{bundleID}}
	resolved, err := resolveCanonicalSelection(validated.model, request)
	if err != nil {
		code, message := packageProblem(err)
		return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem(code, message)}, Failure: FailureValidation}
	}
	if len(resolved.packages) == 0 {
		return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem("unsupported_selection", "Claude lifecycle requires at least one selected native package")}, Failure: FailureValidation}
	}
	if err = validateSelectedExecutableFormats(workspacePath, resolved.assets, request.host, validated.model); err != nil {
		code, message := packageProblem(err)
		return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem(code, message)}, Failure: FailureValidation}
	}
	content, err := selectedContent(workspacePath, validated.model, resolved.assets)
	if err != nil {
		code, message := packageProblem(err)
		return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem(code, message)}, Failure: FailureValidation}
	}
	var rules []byte
	var rulesChecksum string
	for _, selected := range resolved.assets {
		if selected.asset.Ownership != "configuration" {
			continue
		}
		if selected.asset.Type != "instruction" {
			return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem("unsupported_selection", "Claude lifecycle does not support the selected configuration asset type")}, Failure: FailureValidation}
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
	for _, selectedPackage := range resolved.packages {
		nativeContext, cancelNative := context.WithTimeout(operationContext, 2*time.Minute)
		native, runErr := s.config.Runner.Run(nativeContext, filepath.Join(workspacePath, filepath.FromSlash(selectedPackage.Path)), claude, []string{"plugin", "validate", ".", "--strict"}, claudeEnvironment())
		cancelNative()
		if runErr != nil || native.ExitCode != 0 {
			return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem("native_validation_failed", "Claude Code rejected a selected native package")}, Failure: FailureValidation}
		}
	}
	warning, _ := result.NewWarning("active_content_trust", "selected instructions can influence AI behavior and installed executables may later run with your permissions", nil)
	warnings = append(warnings, warning)
	lifecyclePackages := make([]LifecyclePackage, len(resolved.packages))
	resolvedPackageIDs := make([]string, len(resolved.packages))
	retainedBytes := 0
	for index, selectedPackage := range resolved.packages {
		artifact, artifactErr := archiveNativePackage(workspacePath, selectedPackage.Path, validated.model.tracked)
		if artifactErr != nil || len(artifact) > 16<<20-retainedBytes {
			return LifecycleSelection{Source: source, Problems: []result.Problem{mustProblem("native_artifact_failed", "selected native packages could not be retained within rollback bounds")}, Failure: FailureValidation}
		}
		retainedBytes += len(artifact)
		resolvedPackageIDs[index] = selectedPackage.ID
		lifecyclePackages[index] = LifecyclePackage{ID: selectedPackage.ID, Path: selectedPackage.Path, Source: source, NativeArtifact: artifact}
	}
	resolvedAssetIDs := make([]string, len(resolved.assets))
	for index, selectedAsset := range resolved.assets {
		resolvedAssetIDs[index] = selectedAsset.asset.ID
	}
	return LifecycleSelection{
		Source: source, ToolkitID: validated.model.manifest.Toolkit.ID, DeclarationID: declarationID(validated.model.manifest.Toolkit), ToolkitVersion: validated.model.manifest.Toolkit.Version,
		RequestedBundle: bundleID, ResolvedBundles: slices.Clone(resolved.bundles), ResolvedPackages: resolvedPackageIDs, ResolvedAssets: resolvedAssetIDs,
		Packages: lifecyclePackages, Content: content, Rules: slices.Clone(rules), RulesChecksum: rulesChecksum, Warnings: warnings,
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

func lifecycleSelectionFailure(failure Failure, code, message string) LifecycleSelection {
	return LifecycleSelection{Problems: []result.Problem{mustProblem(code, message)}, Failure: failure}
}

func mustProblem(code, message string) result.Problem {
	problem, _ := result.NewProblem(code, message, nil)
	return problem
}
