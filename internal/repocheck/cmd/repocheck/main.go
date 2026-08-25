package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"debug/buildinfo"

	"github.com/alx4j/ai4j/internal/repocheck"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := execute(ctx, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: repocheck <format|module|release-inputs|authorship|binary>")
	}

	root, err := repositoryRoot(ctx)
	if err != nil {
		return err
	}
	switch args[0] {
	case "format":
		if len(args) != 1 {
			return errors.New("usage: repocheck format")
		}
		paths, err := repocheck.TrackedGoFiles(ctx, root)
		if err != nil {
			return err
		}
		return repocheck.CheckFormat(root, paths)
	case "module":
		if len(args) != 1 {
			return errors.New("usage: repocheck module")
		}
		return checkModule(ctx, root)
	case "release-inputs":
		if len(args) != 1 {
			return errors.New("usage: repocheck release-inputs")
		}
		return checkReleaseInputs(ctx, root)
	case "authorship":
		return checkAuthorship(ctx, root, args[1:])
	case "binary":
		return checkBinary(args[1:])
	default:
		return fmt.Errorf("unknown repocheck command %q", args[0])
	}
}

func checkModule(ctx context.Context, root string) error {
	goModTracked := tracked(ctx, root, "go.mod")
	goSumTracked := tracked(ctx, root, "go.sum")
	snapshot, err := repocheck.InspectModule(root, goModTracked, goSumTracked)
	if err != nil {
		return err
	}
	return repocheck.ValidateModule(snapshot)
}

func checkReleaseInputs(ctx context.Context, root string) error {
	goCommand := goBinary()
	moduleFile, err := commandOutput(ctx, root, goCommand, "env", "GOMOD")
	if err != nil {
		return err
	}
	commit, err := commandOutput(ctx, root, "git", "rev-parse", "--verify", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve VCS revision: %w", err)
	}
	status, err := commandOutput(ctx, root, "git", "status", "--porcelain=v1", "--untracked-files=normal", "--", ".", ":(exclude).idea/**")
	if err != nil {
		return fmt.Errorf("inspect working tree: %w", err)
	}

	inputs := repocheck.ReleaseInputs{
		GoVersion:     runtime.Version(),
		ToolchainMode: os.Getenv("GOTOOLCHAIN"),
		WorkspaceMode: os.Getenv("GOWORK"),
		ModuleFile:    moduleFile,
		ExpectedMod:   filepath.Join(root, "go.mod"),
		GoFlags:       os.Getenv("GOFLAGS"),
		GoExperiment:  os.Getenv("GOEXPERIMENT"),
		Commit:        commit,
		Dirty:         status != "",
	}
	if err := repocheck.ValidateReleaseInputs(inputs); err != nil {
		return err
	}
	return checkModule(ctx, root)
}

func checkAuthorship(ctx context.Context, root string, args []string) error {
	flags := flag.NewFlagSet("authorship", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	revision := flags.String("range", "HEAD", "Git revision or range containing real commits")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: repocheck authorship [--range <revision>]")
	}

	commits, err := repocheck.LoadCommits(ctx, root, *revision)
	if err != nil {
		return err
	}
	for _, commit := range commits {
		if err := repocheck.ValidateCommit(commit); err != nil {
			return err
		}
	}
	return repocheck.CheckAutomation(root)
}

func checkBinary(args []string) error {
	flags := flag.NewFlagSet("binary", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	path := flags.String("file", "", "release binary path")
	revision := flags.String("revision", "", "expected VCS revision")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *path == "" || *revision == "" {
		return errors.New("usage: repocheck binary --file <path> --revision <hash>")
	}

	build, err := buildinfo.ReadFile(*path)
	if err != nil {
		return fmt.Errorf("read binary build info: %w", err)
	}
	artifact, err := os.Open(*path)
	if err != nil {
		return fmt.Errorf("open release binary: %w", err)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, artifact); err != nil {
		_ = artifact.Close()
		return fmt.Errorf("hash release binary: %w", err)
	}
	if err := artifact.Close(); err != nil {
		return fmt.Errorf("close release binary: %w", err)
	}
	evidence, err := repocheck.ValidateBinary(build, *revision, fmt.Sprintf("%x", digest.Sum(nil)))
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(evidence)
}

func repositoryRoot(ctx context.Context) (string, error) {
	root, err := commandOutput(ctx, "", "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	return filepath.Clean(root), nil
}

func tracked(ctx context.Context, root, path string) bool {
	command := exec.CommandContext(ctx, "git", "ls-files", "--error-unmatch", "--", path)
	command.Dir = root
	return command.Run() == nil
}

func goBinary() string {
	if path := strings.TrimSpace(os.Getenv("AI4J_GO")); path != "" {
		return path
	}
	return "go"
}

func commandOutput(ctx context.Context, dir, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
