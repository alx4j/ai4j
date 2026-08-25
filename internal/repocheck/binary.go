package repocheck

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/buildinfo"
)

const expectedMainPackage = "github.com/alx4j/ai4j/cmd/ai4j"

// BinaryEvidence is the normalized release-build metadata emitted by the checker.
type BinaryEvidence struct {
	Product     string `json:"product"`
	Executable  string `json:"executable"`
	Version     string `json:"version"`
	Package     string `json:"package"`
	Module      string `json:"module"`
	GoVersion   string `json:"goVersion"`
	Revision    string `json:"revision"`
	BuildTime   string `json:"buildTime"`
	TargetOS    string `json:"targetOs"`
	TargetArch  string `json:"targetArch"`
	CGOEnabled  string `json:"cgoEnabled"`
	TrimmedPath string `json:"trimmedPath"`
	VCSModified string `json:"vcsModified"`
	SHA256      string `json:"sha256"`
}

// ValidateBinary verifies release metadata and returns a path-independent snapshot.
func ValidateBinary(build *debug.BuildInfo, expectedRevision, artifactSHA256 string) (BinaryEvidence, error) {
	version := build.Main.Version
	if version == "" || version == "(devel)" {
		version = buildinfo.DevelopmentVersion
	}
	evidence := BinaryEvidence{
		Product:   "AI4J",
		Version:   version,
		Package:   build.Path,
		Module:    build.Main.Path,
		GoVersion: build.GoVersion,
		SHA256:    artifactSHA256,
	}
	settings := make(map[string]string, len(build.Settings))
	for _, setting := range build.Settings {
		settings[setting.Key] = setting.Value
	}
	evidence.Revision = settings["vcs.revision"]
	evidence.BuildTime = settings["vcs.time"]
	evidence.TargetOS = settings["GOOS"]
	evidence.TargetArch = settings["GOARCH"]
	evidence.CGOEnabled = settings["CGO_ENABLED"]
	evidence.TrimmedPath = settings["-trimpath"]
	evidence.VCSModified = settings["vcs.modified"]

	var problems []error
	if evidence.Package != expectedMainPackage {
		problems = append(problems, fmt.Errorf("main package %q, want %q", evidence.Package, expectedMainPackage))
	}
	if evidence.Module != ExpectedModule {
		problems = append(problems, fmt.Errorf("main module %q, want %q", evidence.Module, ExpectedModule))
	}
	if evidence.GoVersion != ExpectedToolchain {
		problems = append(problems, fmt.Errorf("Go version %q, want %q", evidence.GoVersion, ExpectedToolchain))
	}
	if evidence.Revision != expectedRevision {
		problems = append(problems, fmt.Errorf("VCS revision %q, want %q", evidence.Revision, expectedRevision))
	}
	if parsed, err := time.Parse(time.RFC3339, evidence.BuildTime); err != nil || parsed.Location() != time.UTC {
		problems = append(problems, fmt.Errorf("vcs.time %q is not a UTC RFC3339 timestamp", evidence.BuildTime))
	}
	if evidence.VCSModified != "false" {
		problems = append(problems, fmt.Errorf("vcs.modified %q, want false", evidence.VCSModified))
	}
	switch evidence.TargetOS + "/" + evidence.TargetArch {
	case "darwin/arm64":
		evidence.Executable = "ai4j"
	case "windows/amd64":
		evidence.Executable = "ai4j.exe"
	default:
		problems = append(problems, fmt.Errorf("target %s/%s is not a v1 release target", evidence.TargetOS, evidence.TargetArch))
	}
	if evidence.CGOEnabled != "0" {
		problems = append(problems, fmt.Errorf("CGO_ENABLED %q, want 0", evidence.CGOEnabled))
	}
	if evidence.TrimmedPath != "true" {
		problems = append(problems, fmt.Errorf("-trimpath %q, want true", evidence.TrimmedPath))
	}
	if len(evidence.SHA256) != 64 || evidence.SHA256 != strings.ToLower(evidence.SHA256) || !isHexRevision(evidence.SHA256) {
		problems = append(problems, fmt.Errorf("artifact SHA-256 %q is invalid", evidence.SHA256))
	}

	return evidence, errors.Join(problems...)
}
