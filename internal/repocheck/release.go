package repocheck

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// ReleaseInputs contains environment and VCS facts required before a release-profile build.
type ReleaseInputs struct {
	GoVersion     string
	ToolchainMode string
	WorkspaceMode string
	ModuleFile    string
	ExpectedMod   string
	GoFlags       string
	GoExperiment  string
	Commit        string
	Dirty         bool
}

// ValidateReleaseInputs rejects ambient state that can alter release output.
func ValidateReleaseInputs(inputs ReleaseInputs) error {
	var problems []error
	if inputs.GoVersion != ExpectedToolchain {
		problems = append(problems, fmt.Errorf("Go runtime %q, want %q", inputs.GoVersion, ExpectedToolchain))
	}
	if inputs.ToolchainMode != "local" {
		problems = append(problems, fmt.Errorf("GOTOOLCHAIN %q, want local", inputs.ToolchainMode))
	}
	if inputs.WorkspaceMode != "off" {
		problems = append(problems, fmt.Errorf("GOWORK %q, want off", inputs.WorkspaceMode))
	}
	if inputs.ModuleFile == "" || !samePath(inputs.ModuleFile, inputs.ExpectedMod) {
		problems = append(problems, fmt.Errorf("active module %q, want %q", inputs.ModuleFile, inputs.ExpectedMod))
	}
	if strings.TrimSpace(inputs.GoFlags) != "" {
		problems = append(problems, fmt.Errorf("GOFLAGS must be empty, got %q", inputs.GoFlags))
	}
	if strings.TrimSpace(inputs.GoExperiment) != "" {
		problems = append(problems, fmt.Errorf("GOEXPERIMENT must be empty, got %q", inputs.GoExperiment))
	}
	if !isHexRevision(inputs.Commit) {
		problems = append(problems, errors.New("VCS revision is missing or invalid"))
	}
	if inputs.Dirty {
		problems = append(problems, errors.New("working tree is dirty"))
	}

	return errors.Join(problems...)
}

func samePath(left, right string) bool {
	left, leftErr := filepath.Abs(left)
	right, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func isHexRevision(revision string) bool {
	if len(revision) != 40 && len(revision) != 64 {
		return false
	}
	for _, r := range revision {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return false
		}
	}
	return true
}
