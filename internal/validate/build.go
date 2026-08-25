package validate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	"github.com/alx4j/ai4j/internal/workspace"
)

type BuildReport struct {
	Source       cli.Source
	Target       cli.BuildTarget
	Host         cli.BuildHost
	OutputRoot   string
	Artifacts    []cli.BuildArtifact
	Selection    []cli.BuildSelection
	Content      []cli.ContentItem
	Reproducible bool
	Warnings     []result.Warning
	Problems     []result.Problem
	Failure      Failure
}

type buildManifest struct {
	SchemaVersion int                     `json:"schemaVersion"`
	SourceMode    cli.SourceMode          `json:"sourceMode"`
	SourceCommit  string                  `json:"sourceCommit,omitempty"`
	SourceDigest  string                  `json:"sourceDigest"`
	CLIBuild      string                  `json:"cliBuildCommit"`
	Target        cli.BuildTarget         `json:"target"`
	Host          cli.BuildHost           `json:"host"`
	TargetProfile string                  `json:"targetProfile"`
	Reproducible  bool                    `json:"reproducible"`
	Artifacts     []buildManifestArtifact `json:"artifacts"`
	Mappings      []buildMapping          `json:"mappings"`
	Selection     []buildSelection        `json:"selection"`
	Migration     *buildMigration         `json:"migration,omitempty"`
}

type buildSelection struct {
	Asset       string `json:"asset"`
	Variant     string `json:"variant"`
	Reason      string `json:"reason"`
	RequestedBy string `json:"requestedBy"`
}

type buildMigration struct {
	FromSchema int      `json:"fromSchema"`
	ToSchema   int      `json:"toSchema"`
	Changes    []string `json:"changes"`
	Review     []string `json:"review"`
}

type buildManifestArtifact struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes uint64 `json:"sizeBytes"`
}

type buildMapping struct {
	Canonical string `json:"canonical"`
	Native    string `json:"native"`
	Fidelity  string `json:"fidelity"`
}

type codexPluginManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Skills      string `json:"skills,omitempty"`
	MCPServers  string `json:"mcpServers,omitempty"`
}

type codexMCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	EnvVars []string          `json:"env_vars,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func (s Service) Build(ctx context.Context, request cli.BuildRequest) (report BuildReport) {
	report.Target = request.Target()
	report.Host = request.Host()
	report.OutputRoot = request.Output()
	if problem := s.buildPreflight(ctx, request); problem != nil {
		report.Problems = []result.Problem{*problem}
		report.Failure = FailureEnvironment
		return report
	}
	operationContext, cancelOperation := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelOperation()
	sourceWorkspace, err := workspace.Create(s.config.TempRoot, workspace.BuildSource)
	if err != nil {
		return buildFailure(report, FailureEnvironment, "workspace_create_failed", "temporary source workspace could not be created")
	}
	workspacePath := sourceWorkspace.Path()
	defer func() {
		if err := sourceWorkspace.Close(); err != nil && len(report.Problems) == 0 {
			report = buildFailure(report, FailureEnvironment, "workspace_cleanup_failed", "temporary source workspace could not be removed")
		}
	}()

	acquired, err := s.acquireOptions(operationContext, workspacePath, request.Source())
	if err != nil {
		if code, message, ok := diskCapacityProblem(err); ok {
			return buildFailure(report, FailureEnvironment, code, message)
		}
		return buildFailure(report, FailureSource, localSourceErrorCode(err), localSourceErrorMessage(err))
	}
	if acquired.local() {
		output, outputErr := filepath.Abs(request.Output())
		if outputErr != nil || inside(acquired.checkout, output) {
			return buildFailure(report, FailureConflict, "output_inside_source", "build output must be outside the local development checkout")
		}
	}
	validated, err := validatePackage(workspacePath, acquired.inventory)
	if err != nil {
		code, message := packageProblem(err)
		return buildFailure(report, FailureValidation, code, message)
	}
	source, err := s.newCLISource(acquired, validated.digest)
	if err != nil {
		return buildFailure(report, FailureInternal, "internal_error", "build result could not be constructed")
	}
	report.Source = source
	report.Reproducible = !source.Dirty()
	var resolved []resolvedAssetV2
	if validated.v2 != nil {
		resolved, err = resolveSelectionV2(*validated.v2, request)
		if err != nil {
			code, message := packageProblem(err)
			return buildFailure(report, FailureValidation, code, message)
		}
		if err = validateSelectedExecutableFormats(workspacePath, resolved, request.Host(), *validated.v2); err != nil {
			code, message := packageProblem(err)
			return buildFailure(report, FailureValidation, code, message)
		}
		validated.content, err = selectedContentV2(workspacePath, *validated.v2, resolved)
		if err != nil {
			code, message := packageProblem(err)
			return buildFailure(report, FailureValidation, code, message)
		}
		report.Selection, err = cliSelectionsV2(resolved)
		if err != nil {
			return buildFailure(report, FailureInternal, "internal_error", "build selection result could not be constructed")
		}
	} else if !request.SelectAll() {
		return buildFailure(report, FailureValidation, "unsupported_selection", "schema version 1 supports only --all; build previews migration to schema version 2")
	}
	if len(report.Selection) == 0 {
		for _, item := range validated.content {
			selection, selectionErr := cli.NewBuildSelection(item.Identifier(), "default", "all", "--all")
			if selectionErr != nil {
				return buildFailure(report, FailureInternal, "internal_error", "build selection result could not be constructed")
			}
			report.Selection = append(report.Selection, selection)
		}
	}
	var validateOutput func(string) error
	if request.Target() == cli.BuildTargetClaude {
		validateOutput = func(stage string) error {
			claudeExecutable, _ := s.config.Runner.LookPath("claude")
			roots := []string{filepath.Join(stage, "plugin")}
			if entries, readErr := os.ReadDir(filepath.Join(stage, "plugins")); readErr == nil {
				roots = roots[:0]
				for _, entry := range entries {
					if entry.IsDir() {
						roots = append(roots, filepath.Join(stage, "plugins", entry.Name()))
					}
				}
			}
			for _, root := range roots {
				if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
					continue
				}
				nativeContext, cancelNative := context.WithTimeout(operationContext, 2*time.Minute)
				native, runErr := s.config.Runner.Run(nativeContext, root, claudeExecutable, []string{"plugin", "validate", ".", "--strict"}, claudeEnvironment())
				cancelNative()
				if runErr != nil || native.ExitCode != 0 {
					return errNativeBuildValidation
				}
			}
			return nil
		}
	}
	warnings, dependencyProblem := s.checkHostDependencies(validated.content)
	if dependencyProblem != nil {
		report.Problems = []result.Problem{*dependencyProblem}
		report.Failure = FailureValidation
		return report
	}
	artifacts, err := renderBuildOutput(workspacePath, acquired, validated, resolved, source, request, validateOutput, s.config.Capacity)
	if err != nil {
		if code, message, ok := diskCapacityProblem(err); ok {
			return buildFailure(report, FailureEnvironment, code, message)
		}
		if errors.Is(err, errBuildOutputOccupied) {
			return buildFailure(report, FailureConflict, "output_occupied", "build output must not already exist")
		}
		if errors.Is(err, errNativeBuildValidation) {
			return buildFailure(report, FailureValidation, "native_validation_failed", "Claude rejected the rendered output")
		}
		return buildFailure(report, FailureEnvironment, "build_output_failed", "target-native build output could not be created")
	}
	warning, _ := result.NewWarning("active_content_trust", "built instructions can influence AI behavior and built executables may later run with your permissions", nil)
	report.Artifacts = artifacts
	report.Content = validated.content
	report.Warnings = append(warnings, warning)
	return report
}

func (s Service) buildPreflight(ctx context.Context, request cli.BuildRequest) *result.Problem {
	if ctx == nil || ctx.Err() != nil {
		problem, _ := result.NewProblem("build_cancelled", "build was cancelled", nil)
		return &problem
	}
	if !supportedHost(s.config.GOOS, s.config.GOARCH) {
		problem, _ := result.NewProblem("unsupported_host", "build execution requires Darwin ARM64 or Windows AMD64", nil)
		return &problem
	}
	hasSelection := request.SelectAll() || len(request.Assets()) != 0 || len(request.Bundles()) != 0
	if !request.Target().Valid() || !request.Host().Valid() || !hasSelection {
		problem, _ := result.NewProblem("invalid_build", "build target, host, and selection are required", nil)
		return &problem
	}
	gitExecutable, err := s.config.Runner.LookPath("git")
	if err != nil || !s.probeExecutable(ctx, gitExecutable, gitEnvironment()) {
		problem, _ := result.NewProblem("git_unusable", "Git executable is required", nil)
		return &problem
	}
	if request.Target() == cli.BuildTargetClaude {
		claudeExecutable, lookupErr := s.config.Runner.LookPath("claude")
		if lookupErr != nil || !s.probeExecutable(ctx, claudeExecutable, claudeEnvironment()) {
			problem, _ := result.NewProblem("unsupported_capability", "Claude native validation is unavailable", nil)
			return &problem
		}
	}
	return nil
}

var (
	errBuildOutputOccupied   = errors.New("build output is occupied")
	errNativeBuildValidation = errors.New("native build validation failed")
)

func renderBuildOutput(workspacePath string, acquired acquisition, validated packageResult, resolved []resolvedAssetV2, source cli.Source, request cli.BuildRequest, validateOutput func(string) error, capacity func(string, uint64) error) ([]cli.BuildArtifact, error) {
	output, err := filepath.Abs(request.Output())
	if err != nil {
		return nil, fmt.Errorf("canonicalize output: %w", err)
	}
	if filepath.Clean(output) != output {
		return nil, errors.New("canonicalize output: path is not clean")
	}
	if _, err := os.Lstat(output); err == nil {
		return nil, errBuildOutputOccupied
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	parent := filepath.Dir(output)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("output parent is unavailable")
	}
	if err := capacity(parent, acquired.inventory.TreeBytes()+maximumMetadataSize); err != nil {
		return nil, err
	}
	stageWorkspace, err := workspace.Create(parent, workspace.BuildStage)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stageWorkspace.Close() }()
	stage := stageWorkspace.Path()

	var mappings []buildMapping
	if validated.v2 != nil {
		mappings, err = renderV2Build(stage, workspacePath, *validated.v2, resolved, request.Target())
	} else {
		switch request.Target() {
		case cli.BuildTargetClaude:
			mappings, err = renderClaudeBuild(stage, workspacePath, acquired.inventory)
		case cli.BuildTargetCodex:
			mappings, err = renderCodexBuild(stage, workspacePath, acquired.inventory)
		default:
			err = errors.New("unsupported build target")
		}
	}
	if err != nil {
		return nil, err
	}
	if validateOutput != nil {
		if err := validateOutput(stage); err != nil {
			return nil, err
		}
	}
	payloadArtifacts, manifestArtifacts, err := inspectBuildArtifacts(stage)
	if err != nil {
		return nil, err
	}
	sourceCommit := ""
	sourceDigest := source.RenderedDigest().String()
	if source.Mode() == cli.SourceGitHub {
		sourceCommit = source.Commit().OID().String()
	} else {
		sourceDigest = source.SourceDigest().String()
	}
	manifest := buildManifest{
		SchemaVersion: 1, SourceMode: source.Mode(), SourceCommit: sourceCommit, SourceDigest: sourceDigest,
		CLIBuild: source.CLIBuildCommit().String(), Target: request.Target(), Host: request.Host(),
		TargetProfile: buildTargetProfile(request.Target()), Reproducible: !source.Dirty(),
		Artifacts: manifestArtifacts, Mappings: mappings, Selection: buildSelections(resolved),
	}
	if validated.schemaVersion == 1 {
		manifest.Migration = &buildMigration{FromSchema: 1, ToSchema: 2, Changes: []string{"replace MVP plugin envelope with explicit assets, bundles, targets, and native packages", "default build selection to --all"}, Review: []string{"confirm asset ownership and dependencies", "confirm target package membership and compatibility"}}
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := writeBuildFile(stage, "ai4j-build.json", manifestBytes, 0o644); err != nil {
		return nil, err
	}
	manifestArtifact, err := artifactForBytes("ai4j-build.json", manifestBytes)
	if err != nil {
		return nil, err
	}
	artifacts := append(payloadArtifacts, manifestArtifact)
	slices.SortFunc(artifacts, func(left, right cli.BuildArtifact) int { return strings.Compare(left.Path(), right.Path()) })
	if err := stageWorkspace.Publish(output); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, errBuildOutputOccupied
		}
		return nil, err
	}
	return artifacts, nil
}

func buildTargetProfile(target cli.BuildTarget) string {
	switch target {
	case cli.BuildTargetClaude:
		return "claude-plugin-v1"
	case cli.BuildTargetCodex:
		return "codex-plugin-v1"
	default:
		return ""
	}
}

func renderClaudeBuild(stage, workspace string, inventory gitsource.TreeInventory) ([]buildMapping, error) {
	tracked := trackedFiles(inventory)
	var manifest toolkitManifest
	if err := readStrictJSON(workspace, toolkitManifestPath, tracked, &manifest); err != nil {
		return nil, err
	}
	for _, sourcePath := range filesUnder(tracked, manifest.Plugin.Path) {
		relative := strings.TrimPrefix(sourcePath, manifest.Plugin.Path+"/")
		if err := copyTrackedBuildFile(stage, "plugin/"+relative, workspace, sourcePath, tracked); err != nil {
			return nil, err
		}
	}
	for _, rule := range manifest.SharedRules {
		if err := copyTrackedBuildFile(stage, "configuration/rules/"+filepath.Base(rule.Path), workspace, rule.Path, tracked); err != nil {
			return nil, err
		}
	}
	return []buildMapping{
		{Canonical: "skill:repository-review", Native: "plugin/skills/repository-review", Fidelity: "exact"},
		{Canonical: "agent:repository-reviewer", Native: "plugin/agents/repository-reviewer.md", Fidelity: "exact"},
		{Canonical: "instruction:ai4j-rules", Native: "configuration/rules/ai4j.md", Fidelity: "exact"},
		{Canonical: "mcp:claude-tools", Native: "plugin/.mcp.json", Fidelity: "exact"},
		{Canonical: "hook:representative", Native: "unsupported", Fidelity: "unsupported"},
	}, nil
}

func renderCodexBuild(stage, workspace string, inventory gitsource.TreeInventory) ([]buildMapping, error) {
	tracked := trackedFiles(inventory)
	var manifest toolkitManifest
	if err := readStrictJSON(workspace, toolkitManifestPath, tracked, &manifest); err != nil {
		return nil, err
	}
	skillRoot := manifest.Plugin.Path + "/skills"
	for _, sourcePath := range filesUnder(tracked, skillRoot) {
		relative := strings.TrimPrefix(sourcePath, skillRoot+"/")
		if err := copyTrackedBuildFile(stage, "plugin/skills/"+relative, workspace, sourcePath, tracked); err != nil {
			return nil, err
		}
	}
	plugin := codexPluginManifest{Name: "ai4j-default", Version: "0.1.0", Description: "Practical repository review guidance and a focused review agent", Skills: "./skills/", MCPServers: "./.mcp.json"}
	pluginBytes, err := json.MarshalIndent(plugin, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeBuildFile(stage, "plugin/.codex-plugin/plugin.json", append(pluginBytes, '\n'), 0o644); err != nil {
		return nil, err
	}
	mcpPath := manifest.Plugin.Path + "/.mcp.json"
	var claudeMCP mcpManifest
	if err := readStrictJSON(workspace, mcpPath, tracked, &claudeMCP); err != nil {
		return nil, err
	}
	codexMCP := make(map[string]codexMCPServer, len(claudeMCP.Servers))
	for id, server := range claudeMCP.Servers {
		envVars, envErr := environmentNames(server.Env)
		if envErr != nil {
			return nil, envErr
		}
		codexMCP[id] = codexMCPServer{Command: server.Command, Args: append([]string(nil), server.Args...), EnvVars: envVars}
	}
	mcpBytes, err := json.MarshalIndent(codexMCP, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeBuildFile(stage, "plugin/.mcp.json", append(mcpBytes, '\n'), 0o644); err != nil {
		return nil, err
	}
	for _, rule := range manifest.SharedRules {
		if err := copyTrackedBuildFile(stage, "configuration/AGENTS.md", workspace, rule.Path, tracked); err != nil {
			return nil, err
		}
	}
	agentSource := manifest.Plugin.Path + "/agents/repository-reviewer.md"
	agent, err := readTrackedFile(workspace, agentSource, tracked)
	if err != nil {
		return nil, err
	}
	if err := writeBuildFile(stage, "configuration/.codex/agents/repository-reviewer.toml", codexAgent(agent), 0o644); err != nil {
		return nil, err
	}
	return []buildMapping{
		{Canonical: "skill:repository-review", Native: "plugin/skills/repository-review", Fidelity: "exact"},
		{Canonical: "agent:repository-reviewer", Native: "configuration/.codex/agents/repository-reviewer.toml", Fidelity: "exact"},
		{Canonical: "instruction:ai4j-rules", Native: "configuration/AGENTS.md", Fidelity: "exact"},
		{Canonical: "mcp:claude-tools", Native: "plugin/.mcp.json", Fidelity: "exact"},
		{Canonical: "hook:representative", Native: "unsupported", Fidelity: "unsupported"},
	}, nil
}

func trackedFiles(inventory gitsource.TreeInventory) map[string]gitsource.TreeEntry {
	tracked := make(map[string]gitsource.TreeEntry, inventory.PathCount())
	for _, entry := range inventory.Entries() {
		tracked[entry.Path().String()] = entry
	}
	return tracked
}

func copyTrackedBuildFile(stage, destination, workspace, source string, tracked map[string]gitsource.TreeEntry) error {
	entry, ok := tracked[source]
	if !ok || !safeRelative(source) || !safeRelative(destination) {
		return errors.New("build source is not a safe tracked file")
	}
	content, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(source)))
	if err != nil || uint64(len(content)) != entry.SizeBytes() {
		return errors.New("build source changed while rendering")
	}
	mode := os.FileMode(0o644)
	if entry.Mode() == gitsource.SourceExecutableFile {
		mode = 0o755
	}
	return writeBuildFile(stage, destination, content, mode)
}

func inspectBuildArtifacts(root string) ([]cli.BuildArtifact, []buildManifestArtifact, error) {
	var artifacts []cli.BuildArtifact
	var manifest []buildManifestArtifact
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return errors.New("build output contains an unsupported file type")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		artifact, err := artifactForBytes(relative, content)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact)
		manifest = append(manifest, buildManifestArtifact{Path: artifact.Path(), SHA256: artifact.Checksum(), SizeBytes: artifact.SizeBytes()})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	slices.SortFunc(artifacts, func(left, right cli.BuildArtifact) int { return strings.Compare(left.Path(), right.Path()) })
	slices.SortFunc(manifest, func(left, right buildManifestArtifact) int { return strings.Compare(left.Path, right.Path) })
	return artifacts, manifest, nil
}

func artifactForBytes(path string, content []byte) (cli.BuildArtifact, error) {
	digest := sha256.Sum256(content)
	return cli.NewBuildArtifact(path, hex.EncodeToString(digest[:]), uint64(len(content)))
}

func writeBuildFile(root, relative string, content []byte, mode os.FileMode) error {
	destination := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destination, content, mode)
}

func buildFailure(report BuildReport, failure Failure, code, message string) BuildReport {
	problem, _ := result.NewProblem(code, message, nil)
	report.Problems = []result.Problem{problem}
	report.Failure = failure
	return report
}

func codexAgent(source []byte) []byte {
	return codexAgentNamed(source, "repository-reviewer")
}

func codexAgentNamed(source []byte, name string) []byte {
	body := string(source)
	if strings.HasPrefix(body, "---\n") {
		if end := strings.Index(body[4:], "\n---\n"); end >= 0 {
			body = body[4+end+5:]
		}
	}
	return []byte("name = " + strconv.Quote(name) + "\n" +
		"description = \"Reviews a repository change against explicit requirements and acceptance criteria.\"\n" +
		"developer_instructions = " + strconv.Quote(strings.TrimSpace(body)) + "\n")
}
