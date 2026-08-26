package validate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/result"
	"github.com/alx4j/ai4j/internal/workspace"
)

type InitReport struct {
	Targets    []cli.BuildTarget
	OutputRoot string
	Artifacts  []cli.BuildArtifact
	Problems   []result.Problem
	Failure    Failure
}

func (s Service) Init(ctx context.Context, request cli.InitRequest) InitReport {
	report := InitReport{Targets: request.Targets(), OutputRoot: request.Output()}
	if ctx == nil || ctx.Err() != nil {
		return initFailure(report, FailureEnvironment, "init_cancelled", "initialization was cancelled")
	}
	output, err := filepath.Abs(request.Output())
	if err != nil || filepath.Clean(output) != output {
		return initFailure(report, FailureEnvironment, "invalid_output", "initialization output path is invalid")
	}
	if _, err := os.Lstat(output); err == nil {
		return initFailure(report, FailureConflict, "output_occupied", "initialization output must not already exist")
	} else if !errors.Is(err, os.ErrNotExist) {
		return initFailure(report, FailureEnvironment, "output_unavailable", "initialization output could not be inspected")
	}
	parent := filepath.Dir(output)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		return initFailure(report, FailureEnvironment, "output_unavailable", "initialization output parent is unavailable")
	}
	if err := s.config.Capacity(parent, maximumMetadataSize); err != nil {
		if code, message, ok := diskCapacityProblem(err); ok {
			return initFailure(report, FailureEnvironment, code, message)
		}
		return initFailure(report, FailureEnvironment, "output_unavailable", "initialization output capacity could not be verified")
	}
	stageWorkspace, err := workspace.Create(parent, workspace.InitStage)
	if err != nil {
		return initFailure(report, FailureEnvironment, "output_unavailable", "initialization staging directory could not be created")
	}
	defer func() { _ = stageWorkspace.Close() }()
	stage := stageWorkspace.Path()
	if err := renderScaffold(stage, request); err != nil {
		return initFailure(report, FailureInternal, "init_render_failed", "toolkit scaffold could not be rendered")
	}
	if err := validateScaffold(stage); err != nil {
		return initFailure(report, FailureInternal, "init_validation_failed", "generated toolkit scaffold did not validate")
	}
	artifacts, _, err := inspectBuildArtifacts(stage)
	if err != nil {
		return initFailure(report, FailureEnvironment, "init_inspection_failed", "generated toolkit scaffold could not be inspected")
	}
	if err := stageWorkspace.Publish(output); err != nil {
		if errors.Is(err, os.ErrExist) {
			return initFailure(report, FailureConflict, "output_occupied", "initialization output must not already exist")
		}
		return initFailure(report, FailureEnvironment, "init_publish_failed", "generated toolkit scaffold could not be published")
	}
	report.Artifacts = artifacts
	return report
}

func renderScaffold(root string, request cli.InitRequest) error {
	targets := request.Targets()
	slices.Sort(targets)
	manifest := toolkitManifest{
		SchemaVersion: 1,
		Toolkit:       toolkitIdentity{ID: "example-toolkit", Version: "0.1.0", DisplayName: "Example Toolkit", Compatibility: compatibility{MinimumCLI: "1.0.0"}},
		Assets:        []asset{}, Bundles: []bundle{}, Targets: map[string]target{},
	}
	for _, targetName := range targets {
		path := "targets/" + string(targetName) + "/example-toolkit"
		manifest.Targets[string(targetName)] = target{Packages: []nativePackage{{ID: "example-toolkit", Path: path, Assets: []string{}}}}
		var manifestPath string
		var native []byte
		if targetName == cli.BuildTargetClaude {
			manifestPath = path + "/.claude-plugin/plugin.json"
			native = []byte("{\n  \"name\": \"example-toolkit\",\n  \"description\": \"Example AI4J toolkit\"\n}\n")
		} else {
			manifestPath = path + "/.codex-plugin/plugin.json"
			native = []byte("{\n  \"name\": \"example-toolkit\",\n  \"version\": \"0.1.0\",\n  \"description\": \"Example AI4J toolkit\"\n}\n")
		}
		if err := writeBuildFile(root, manifestPath, native, 0o644); err != nil {
			return err
		}
	}
	if request.Examples() {
		variants := make([]assetVariant, 0, len(targets))
		for _, targetName := range targets {
			path := "targets/" + string(targetName) + "/example-toolkit/skills/example-skill"
			variants = append(variants, assetVariant{ID: string(targetName), Path: path, Targets: []string{string(targetName)}, Hosts: []string{"darwin-arm64", "windows-amd64"}})
			if err := writeBuildFile(root, path+"/SKILL.md", []byte("---\nname: example-skill\ndescription: Demonstrates a minimal AI4J skill.\n---\n\nFollow the user's request and report the completed result.\n"), 0o644); err != nil {
				return err
			}
			targetConfig := manifest.Targets[string(targetName)]
			targetConfig.Packages[0].Assets = []string{"example-skill"}
			manifest.Targets[string(targetName)] = targetConfig
		}
		manifest.Assets = []asset{{ID: "example-skill", Type: "skill", Ownership: "package", Variants: variants}}
		manifest.Bundles = []bundle{{ID: "examples", Packages: []string{"example-toolkit"}}}
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeBuildFile(root, "toolkit.json", append(manifestBytes, '\n'), 0o644); err != nil {
		return err
	}
	if err := writeBuildFile(root, ".gitignore", []byte("/dist/\n"), 0o644); err != nil {
		return err
	}
	return writeBuildFile(root, "README.md", []byte("# Example Toolkit\n\nValidate and build this toolkit with AI4J before distributing it. Generated output belongs under `dist/`.\n"), 0o644)
}

func validateScaffold(root string) error {
	content, err := os.ReadFile(filepath.Join(root, "toolkit.json"))
	if err != nil {
		return err
	}
	var manifest toolkitManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return err
	}
	if manifest.SchemaVersion != 1 || manifest.Toolkit.ID == "" || len(manifest.Targets) == 0 {
		return errors.New("scaffold manifest is incomplete")
	}
	for _, target := range manifest.Targets {
		if len(target.Packages) == 0 {
			return errors.New("scaffold target has no package")
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(target.Packages[0].Path))); err != nil {
			return err
		}
	}
	return nil
}

func initFailure(report InitReport, failure Failure, code, message string) InitReport {
	problem, _ := result.NewProblem(code, message, nil)
	report.Problems = []result.Problem{problem}
	report.Failure = failure
	return report
}
