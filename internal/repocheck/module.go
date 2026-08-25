// Package repocheck implements repository policy checks shared by local and CI workflows.
package repocheck

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

const (
	ExpectedModule    = "github.com/alx4j/ai4j"
	ExpectedGo        = "1.26.0"
	ExpectedToolchain = "go1.26.6"
)

// ModuleSnapshot contains the repository facts needed by the module policy.
type ModuleSnapshot struct {
	Path           string
	GoVersion      string
	Toolchain      string
	Requirements   []ModuleVersion
	Replacements   []Replacement
	Sums           map[ModuleVersion]bool
	GoModTracked   bool
	GoSumTracked   bool
	GoSumIsRegular bool
}

// ModuleVersion identifies one exact module dependency.
type ModuleVersion struct {
	Path    string
	Version string
}

// Replacement describes a module replacement declared by go.mod.
type Replacement struct {
	Old ModuleVersion
	New ModuleVersion
}

// InspectModule reads the module contract without changing module files or the cache.
func InspectModule(root string, goModTracked, goSumTracked bool) (ModuleSnapshot, error) {
	goModPath := filepath.Join(root, "go.mod")
	contents, err := os.ReadFile(goModPath)
	if err != nil {
		return ModuleSnapshot{}, fmt.Errorf("read go.mod: %w", err)
	}

	parsed, err := modfile.Parse(goModPath, contents, nil)
	if err != nil {
		return ModuleSnapshot{}, fmt.Errorf("parse go.mod: %w", err)
	}

	snapshot := ModuleSnapshot{
		Sums:         make(map[ModuleVersion]bool),
		GoModTracked: goModTracked,
		GoSumTracked: goSumTracked,
	}
	if parsed.Module != nil {
		snapshot.Path = parsed.Module.Mod.Path
	}
	if parsed.Go != nil {
		snapshot.GoVersion = parsed.Go.Version
	}
	if parsed.Toolchain != nil {
		snapshot.Toolchain = parsed.Toolchain.Name
	}
	for _, requirement := range parsed.Require {
		snapshot.Requirements = append(snapshot.Requirements, ModuleVersion{
			Path:    requirement.Mod.Path,
			Version: requirement.Mod.Version,
		})
	}
	for _, replacement := range parsed.Replace {
		snapshot.Replacements = append(snapshot.Replacements, Replacement{
			Old: ModuleVersion{Path: replacement.Old.Path, Version: replacement.Old.Version},
			New: ModuleVersion{Path: replacement.New.Path, Version: replacement.New.Version},
		})
	}

	goSumPath := filepath.Join(root, "go.sum")
	stat, err := os.Stat(goSumPath)
	if err != nil {
		return ModuleSnapshot{}, fmt.Errorf("stat go.sum: %w", err)
	}
	snapshot.GoSumIsRegular = stat.Mode().IsRegular()

	sums, err := os.ReadFile(goSumPath)
	if err != nil {
		return ModuleSnapshot{}, fmt.Errorf("read go.sum: %w", err)
	}
	for line := range strings.SplitSeq(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || strings.HasSuffix(fields[1], "/go.mod") {
			continue
		}
		snapshot.Sums[ModuleVersion{Path: fields[0], Version: fields[1]}] = true
	}

	return snapshot, nil
}

// ValidateModule enforces the canonical module, toolchain, replacement, and checksum policy.
func ValidateModule(snapshot ModuleSnapshot) error {
	var problems []error
	if snapshot.Path != ExpectedModule {
		problems = append(problems, fmt.Errorf("module path %q, want %q", snapshot.Path, ExpectedModule))
	}
	if snapshot.GoVersion != ExpectedGo {
		problems = append(problems, fmt.Errorf("Go language version %q, want %q", snapshot.GoVersion, ExpectedGo))
	}
	if snapshot.Toolchain != ExpectedToolchain {
		problems = append(problems, fmt.Errorf("toolchain %q, want %q", snapshot.Toolchain, ExpectedToolchain))
	}
	if !snapshot.GoModTracked {
		problems = append(problems, errors.New("go.mod is not tracked"))
	}
	if !snapshot.GoSumTracked {
		problems = append(problems, errors.New("go.sum is not tracked"))
	}
	if !snapshot.GoSumIsRegular {
		problems = append(problems, errors.New("go.sum is not a regular file"))
	}

	for _, replacement := range snapshot.Replacements {
		if replacement.New.Version == "" {
			problems = append(problems, fmt.Errorf("local replacement for %s is prohibited", replacement.Old.Path))
		}
	}
	for _, requirement := range snapshot.Requirements {
		checksumModule := requirement
		for _, replacement := range snapshot.Replacements {
			if replacement.Old == requirement && replacement.New.Version != "" {
				checksumModule = replacement.New
				break
			}
		}
		if !snapshot.Sums[checksumModule] {
			problems = append(problems, fmt.Errorf("dependency %s@%s has no content checksum", checksumModule.Path, checksumModule.Version))
		}
	}

	return errors.Join(problems...)
}
