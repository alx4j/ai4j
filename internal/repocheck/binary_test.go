package repocheck

import (
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
)

func TestValidateBinary(t *testing.T) {
	t.Parallel()

	const revision = "0123456789abcdef0123456789abcdef01234567"
	build := debug.BuildInfo{
		GoVersion: ExpectedToolchain,
		Path:      expectedMainPackage,
		Main:      debug.Module{Path: ExpectedModule, Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.time", Value: "2026-08-24T12:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
			{Key: "GOOS", Value: "darwin"},
			{Key: "GOARCH", Value: "arm64"},
			{Key: "CGO_ENABLED", Value: "0"},
			{Key: "-trimpath", Value: "true"},
		},
	}

	const digest = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	got, err := ValidateBinary(&build, revision, digest)
	if err != nil {
		t.Fatalf("ValidateBinary() error = %v", err)
	}
	want := BinaryEvidence{
		Product:     "AI4J",
		Executable:  "ai4j",
		Version:     "0.0.0-dev",
		Package:     expectedMainPackage,
		Module:      ExpectedModule,
		GoVersion:   ExpectedToolchain,
		Revision:    revision,
		BuildTime:   "2026-08-24T12:00:00Z",
		TargetOS:    "darwin",
		TargetArch:  "arm64",
		CGOEnabled:  "0",
		TrimmedPath: "true",
		VCSModified: "false",
		SHA256:      digest,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("evidence = %#v, want %#v", got, want)
	}
}

func TestValidateBinaryRejectsModifiedBuild(t *testing.T) {
	t.Parallel()

	build := debug.BuildInfo{
		GoVersion: ExpectedToolchain,
		Path:      expectedMainPackage,
		Main:      debug.Module{Path: ExpectedModule},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
			{Key: "vcs.time", Value: "2026-08-24T12:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
			{Key: "GOOS", Value: "darwin"},
			{Key: "GOARCH", Value: "arm64"},
			{Key: "CGO_ENABLED", Value: "0"},
			{Key: "-trimpath", Value: "true"},
		},
	}

	_, err := ValidateBinary(&build, "0123456789abcdef0123456789abcdef01234567", strings.Repeat("a", 64))
	if err == nil || !strings.Contains(err.Error(), "vcs.modified") {
		t.Fatalf("ValidateBinary() error = %v, want modified-build rejection", err)
	}
}

func TestValidateBinaryAcceptsWindowsRelease(t *testing.T) {
	t.Parallel()

	const revision = "0123456789abcdef0123456789abcdef01234567"
	build := debug.BuildInfo{
		GoVersion: ExpectedToolchain,
		Path:      expectedMainPackage,
		Main:      debug.Module{Path: ExpectedModule, Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.time", Value: "2026-08-24T12:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
			{Key: "GOOS", Value: "windows"},
			{Key: "GOARCH", Value: "amd64"},
			{Key: "CGO_ENABLED", Value: "0"},
			{Key: "-trimpath", Value: "true"},
		},
	}

	got, err := ValidateBinary(&build, revision, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("ValidateBinary() error = %v", err)
	}
	if got.Executable != "ai4j.exe" || got.TargetOS != "windows" || got.TargetArch != "amd64" {
		t.Fatalf("Windows evidence = %#v", got)
	}
}

func TestValidateBinaryRejectsUnsupportedReleaseTarget(t *testing.T) {
	t.Parallel()

	const revision = "0123456789abcdef0123456789abcdef01234567"
	build := debug.BuildInfo{
		GoVersion: ExpectedToolchain,
		Path:      expectedMainPackage,
		Main:      debug.Module{Path: ExpectedModule, Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.time", Value: "2026-08-24T12:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
			{Key: "GOOS", Value: "linux"},
			{Key: "GOARCH", Value: "amd64"},
			{Key: "CGO_ENABLED", Value: "0"},
			{Key: "-trimpath", Value: "true"},
		},
	}

	_, err := ValidateBinary(&build, revision, strings.Repeat("a", 64))
	if err == nil || !strings.Contains(err.Error(), "not supported for release") {
		t.Fatalf("ValidateBinary() error = %v", err)
	}
}

func TestValidateBinaryRejectsInvalidReleaseEvidence(t *testing.T) {
	t.Parallel()
	build := debug.BuildInfo{
		GoVersion: ExpectedToolchain,
		Path:      expectedMainPackage,
		Main:      debug.Module{Path: ExpectedModule, Version: "v1.0.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
			{Key: "vcs.time", Value: "not-a-time"},
			{Key: "vcs.modified", Value: "false"},
			{Key: "GOOS", Value: "darwin"},
			{Key: "GOARCH", Value: "arm64"},
			{Key: "CGO_ENABLED", Value: "0"},
			{Key: "-trimpath", Value: "true"},
		},
	}
	_, err := ValidateBinary(&build, "0123456789abcdef0123456789abcdef01234567", strings.Repeat("A", 64))
	if err == nil || !strings.Contains(err.Error(), "vcs.time") || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("ValidateBinary() error = %v", err)
	}
}
