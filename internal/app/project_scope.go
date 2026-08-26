package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/installstate"
)

const projectInspectionTimeout = 15 * time.Second
const maximumProjectMetadataBytes = 1 << 20

func (s *lifecycleService) resolveProjectRoot(ctx context.Context, project string, explicit bool) (string, error) {
	candidate := project
	if !explicit {
		var err error
		candidate, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", errors.New("project is not a directory")
	}
	git, err := s.runner.LookPath("git")
	if err != nil {
		return "", err
	}
	commandContext, cancel := context.WithTimeout(ctx, projectInspectionTimeout)
	defer cancel()
	observation, err := s.runner.Run(commandContext, canonical, git, []string{"rev-parse", "--show-toplevel"}, []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	})
	if err != nil || observation.ExitCode != 0 || len(observation.Stderr) != 0 {
		return "", errors.New("Git root lookup failed")
	}
	root, err := filepath.Abs(strings.TrimSpace(string(observation.Stdout)))
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, canonical)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("project is outside the selected Git worktree")
	}
	return filepath.Clean(root), nil
}

func nativeScope(record installstate.Record) string {
	switch record.Scope {
	case "project-local":
		return "local"
	case "project-shared":
		return "project"
	default:
		return "user"
	}
}

func nativeDirectory(record installstate.Record) string {
	if record.Scope == "user" {
		return ""
	}
	return record.ScopeRoot
}

func (s *lifecycleService) runClaudeFor(ctx context.Context, record installstate.Record, arguments []string) error {
	return s.runClaudeAt(ctx, nativeDirectory(record), arguments)
}

func (s *lifecycleService) inspectProjectLocal(ctx context.Context, before *installstate.Record, desired installstate.Record) error {
	if desired.Scope != "project-local" {
		return nil
	}
	record := desired
	owned := before != nil && before.Lifecycle == "active" && before.Rules != (installstate.OwnedFile{})
	if owned {
		record = *before
	}
	if record.Rules.Path == "" {
		return nil
	}
	git, err := s.runner.LookPath("git")
	if err != nil {
		return err
	}
	commandContext, cancel := context.WithTimeout(ctx, projectInspectionTimeout)
	defer cancel()
	if desired.Rules != (installstate.OwnedFile{}) {
		relative := filepath.ToSlash(desired.Rules.Path)
		tracked, err := s.runner.Run(commandContext, desired.ScopeRoot, git, []string{"ls-files", "--error-unmatch", "--", relative}, []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"})
		if err != nil || tracked.ExitCode != 0 && tracked.ExitCode != 1 {
			return errors.New("project rules tracking could not be inspected")
		}
		if tracked.ExitCode == 0 {
			return errors.New("project rules destination is tracked")
		}
	}
	path, err := s.projectExcludePath(ctx, record)
	if err != nil {
		return err
	}
	contents, _, err := readProjectMetadata(path)
	if err != nil {
		return err
	}
	present := slices.Contains(strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n"), projectExcludeLine(record))
	if present != owned {
		return errors.New("project rules exclusion ownership does not match installation state")
	}
	return nil
}

func (s *lifecycleService) projectExcludePath(ctx context.Context, record installstate.Record) (string, error) {
	git, err := s.runner.LookPath("git")
	if err != nil {
		return "", err
	}
	commandContext, cancel := context.WithTimeout(ctx, projectInspectionTimeout)
	defer cancel()
	observation, err := s.runner.Run(commandContext, record.ScopeRoot, git, []string{"rev-parse", "--git-path", "info/exclude"}, []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"})
	if err != nil || observation.ExitCode != 0 || len(observation.Stderr) != 0 {
		return "", errors.New("Git local exclusion path could not be resolved")
	}
	value := strings.TrimSpace(string(observation.Stdout))
	if value == "" {
		return "", errors.New("Git local exclusion path is empty")
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(record.ScopeRoot, value)
	}
	return filepath.Clean(value), nil
}

func (s *lifecycleService) ensureProjectLocalExclusion(ctx context.Context, record installstate.Record) error {
	if record.Scope != "project-local" || record.Rules.Path == "" {
		return nil
	}
	path, err := s.projectExcludePath(ctx, record)
	if err != nil {
		return err
	}
	return editProjectExclude(path, projectExcludeLine(record), true, false)
}

func (s *lifecycleService) addProjectLocalExclusion(ctx context.Context, record installstate.Record) error {
	if record.Scope != "project-local" || record.Rules.Path == "" {
		return nil
	}
	path, err := s.projectExcludePath(ctx, record)
	if err != nil {
		return err
	}
	return editProjectExclude(path, projectExcludeLine(record), true, true)
}

func (s *lifecycleService) removeProjectLocalExclusion(ctx context.Context, record installstate.Record) error {
	if record.Scope != "project-local" || record.Rules.Path == "" {
		return nil
	}
	path, err := s.projectExcludePath(ctx, record)
	if err != nil {
		return err
	}
	return editProjectExclude(path, projectExcludeLine(record), false, false)
}

func projectExcludeLine(record installstate.Record) string {
	return "/" + filepath.ToSlash(record.Rules.Path)
}

func editProjectExclude(path, ownedLine string, present, rejectExisting bool) error {
	contents, mode, err := readProjectMetadata(path)
	if err != nil {
		return err
	}
	if !present {
		updated, found := removeProjectExcludeLines(contents, ownedLine)
		if !found {
			return nil
		}
		return replaceProjectMetadata(path, updated, mode)
	}
	newline := "\n"
	if bytes.Contains(contents, []byte("\r\n")) {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n")
	found := false
	for _, line := range lines {
		if line == ownedLine {
			found = true
		}
	}
	if rejectExisting && found {
		return errors.New("project rules exclusion appeared after planning")
	}
	if found {
		return nil
	}
	updated := slices.Clone(contents)
	if len(updated) != 0 && updated[len(updated)-1] != '\n' {
		updated = append(updated, []byte(newline)...)
	}
	updated = append(updated, []byte(ownedLine+newline)...)
	return replaceProjectMetadata(path, updated, mode)
}

func removeProjectExcludeLines(contents []byte, ownedLine string) ([]byte, bool) {
	updated := make([]byte, 0, len(contents))
	found := false
	for start := 0; start < len(contents); {
		end := bytes.IndexByte(contents[start:], '\n')
		if end < 0 {
			end = len(contents)
		} else {
			end += start + 1
		}
		lineEnd := end
		if lineEnd > start && contents[lineEnd-1] == '\n' {
			lineEnd--
		}
		if lineEnd > start && contents[lineEnd-1] == '\r' {
			lineEnd--
		}
		if string(contents[start:lineEnd]) == ownedLine {
			found = true
		} else {
			updated = append(updated, contents[start:end]...)
		}
		start = end
	}
	return updated, found
}

func readProjectMetadata(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0o600, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximumProjectMetadataBytes {
		return nil, 0, errors.New("project metadata is unsafe")
	}
	contents, err := os.ReadFile(path)
	if err != nil || len(contents) > maximumProjectMetadataBytes || bytes.IndexByte(contents, 0) >= 0 {
		return nil, 0, errors.New("project metadata could not be read safely")
	}
	return contents, info.Mode().Perm(), nil
}

func replaceProjectMetadata(path string, contents []byte, mode os.FileMode) error {
	if len(contents) > maximumProjectMetadataBytes {
		return errors.New("project metadata exceeds its size limit")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := diskcapacity.Require(filepath.Dir(path), uint64(len(contents))); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ai4j-project-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(mode); err != nil {
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
	return os.Rename(temporaryPath, path)
}
