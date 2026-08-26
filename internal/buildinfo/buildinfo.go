// Package buildinfo owns AI4J product identity and compiled build facts.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

const (
	Product              = "AI4J"
	Executable           = "ai4j"
	WindowsExecutable    = "ai4j.exe"
	Module               = "github.com/alx4j/ai4j"
	RepositoryIdentity   = "github.com/alx4j/ai4j"
	RepositoryURL        = "https://github.com/alx4j/ai4j"
	CommandPackage       = Module + "/cmd/ai4j"
	DevelopmentVersion   = "0.0.0-dev"
	dirtyVersionMetadata = "dirty"
)

// Inputs are value-only build facts used to create an immutable Info snapshot.
// Version is the raw debug.BuildInfo Main.Version value. BuildTime is the Go
// vcs.time source epoch, not the wall-clock time at which the compiler ran.
type Inputs struct {
	ModulePath   string
	PackagePath  string
	Version      string
	GoVersion    string
	VCS          string
	Revision     string
	BuildTime    time.Time
	TargetOS     string
	TargetArch   string
	VCSModified  bool
	VCSAvailable bool
}

// Info is an immutable snapshot of facts embedded in one running binary.
type Info struct {
	modulePath   string
	packagePath  string
	version      string
	goVersion    string
	vcs          string
	revision     string
	buildTime    time.Time
	targetOS     string
	targetArch   string
	vcsModified  bool
	vcsAvailable bool
}

// New snapshots build inputs, normalizes development versions, and stores the
// VCS source epoch in UTC with second precision.
func New(inputs Inputs) Info {
	buildTime := inputs.BuildTime
	if !buildTime.IsZero() {
		buildTime = buildTime.UTC().Truncate(time.Second)
	}
	return Info{
		modulePath:   inputs.ModulePath,
		packagePath:  inputs.PackagePath,
		version:      normalizeVersion(inputs.Version, inputs.VCSModified),
		goVersion:    inputs.GoVersion,
		vcs:          inputs.VCS,
		revision:     inputs.Revision,
		buildTime:    buildTime,
		targetOS:     inputs.TargetOS,
		targetArch:   inputs.TargetArch,
		vcsModified:  inputs.VCSModified,
		vcsAvailable: inputs.VCSAvailable,
	}
}

// Read returns product identity together with the current binary's embedded
// build information. It does not cache or mutate process state.
func Read() Info {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return New(Inputs{
			GoVersion:  runtime.Version(),
			TargetOS:   runtime.GOOS,
			TargetArch: runtime.GOARCH,
		})
	}

	return FromBuildInfo(*build)
}

// FromBuildInfo converts standard Go build information into an immutable
// AI4J snapshot. Go's vcs.time is the deterministic build timestamp and
// source epoch exposed by the version contract.
func FromBuildInfo(build debug.BuildInfo) Info {
	inputs := Inputs{
		ModulePath:  build.Main.Path,
		PackagePath: build.Path,
		Version:     build.Main.Version,
		GoVersion:   build.GoVersion,
		TargetOS:    runtime.GOOS,
		TargetArch:  runtime.GOARCH,
	}
	revisionSeen := false
	timeSeen := false
	modifiedSeen := false
	vcsSeen := false
	metadataValid := true

	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs":
			if vcsSeen {
				metadataValid = false
				continue
			}
			vcsSeen = true
			inputs.VCS = setting.Value
			if setting.Value != "git" {
				metadataValid = false
			}
		case "vcs.revision":
			if revisionSeen {
				metadataValid = false
				continue
			}
			revisionSeen = true
			inputs.Revision = setting.Value
		case "vcs.time":
			if timeSeen {
				metadataValid = false
				continue
			}
			timeSeen = true
			parsed, err := time.Parse(time.RFC3339, setting.Value)
			if err != nil {
				metadataValid = false
				continue
			}
			inputs.BuildTime = parsed
		case "vcs.modified":
			if modifiedSeen {
				metadataValid = false
				continue
			}
			modifiedSeen = true
			switch setting.Value {
			case "true":
				inputs.VCSModified = true
			case "false":
				inputs.VCSModified = false
			default:
				metadataValid = false
			}
		case "GOOS":
			inputs.TargetOS = setting.Value
		case "GOARCH":
			inputs.TargetArch = setting.Value
		}
	}
	inputs.VCSAvailable = metadataValid && vcsSeen && revisionSeen && inputs.Revision != "" && timeSeen && !inputs.BuildTime.IsZero() && modifiedSeen && inputs.ModulePath == Module && inputs.PackagePath == CommandPackage

	return New(inputs)
}

func normalizeVersion(version string, modified bool) string {
	if version == "" || version == "(devel)" {
		version = DevelopmentVersion
	}
	if !modified {
		return version
	}
	if strings.Contains(version, "+") {
		return version + "." + dirtyVersionMetadata
	}
	return version + "+" + dirtyVersionMetadata
}

func (Info) Product() string { return Product }
func (i Info) Executable() string {
	if i.targetOS == "windows" {
		return WindowsExecutable
	}
	return Executable
}
func (Info) Module() string             { return Module }
func (Info) RepositoryIdentity() string { return RepositoryIdentity }
func (Info) RepositoryURL() string      { return RepositoryURL }
func (i Info) MainModule() string       { return i.modulePath }
func (i Info) PackagePath() string      { return i.packagePath }
func (i Info) Version() string          { return i.version }
func (i Info) GoVersion() string        { return i.goVersion }
func (i Info) VCS() string              { return i.vcs }
func (i Info) Revision() string         { return i.revision }
func (i Info) BuildTime() time.Time     { return i.buildTime }
func (i Info) TargetOS() string         { return i.targetOS }
func (i Info) TargetArch() string       { return i.targetArch }
func (i Info) VCSModified() bool        { return i.vcsModified }
func (i Info) VCSAvailable() bool       { return i.vcsAvailable }
