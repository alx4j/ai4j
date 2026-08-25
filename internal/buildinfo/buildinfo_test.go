package buildinfo

import (
	"reflect"
	"runtime/debug"
	"testing"
	"time"
)

const fullRevision = "0123456789abcdef0123456789abcdef01234567"

func TestFromBuildInfoSnapshotsCanonicalFacts(t *testing.T) {
	t.Parallel()

	build := debug.BuildInfo{
		GoVersion: "go1.26.6",
		Path:      "github.com/alx4j/ai4j/cmd/ai4j",
		Main:      debug.Module{Path: Module, Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: fullRevision},
			{Key: "vcs.time", Value: "2026-08-18T12:34:56.789+02:00"},
			{Key: "vcs.modified", Value: "false"},
			{Key: "GOOS", Value: "darwin"},
			{Key: "GOARCH", Value: "arm64"},
		},
	}

	got := FromBuildInfo(build)
	wantTime := time.Date(2026, 8, 18, 10, 34, 56, 0, time.UTC)
	if got.Product() != Product || got.Executable() != Executable || got.Module() != Module {
		t.Fatalf("identity = %s/%s/%s, want canonical constants", got.Product(), got.Executable(), got.Module())
	}
	if got.RepositoryIdentity() != RepositoryIdentity || got.RepositoryURL() != RepositoryURL {
		t.Fatalf("repositories = %q/%q", got.RepositoryIdentity(), got.RepositoryURL())
	}
	if got.MainModule() != Module || got.PackagePath() != build.Path || got.Version() != "v1.2.3" || got.GoVersion() != build.GoVersion || got.VCS() != "git" || got.Revision() != fullRevision {
		t.Fatalf("snapshot = module %q package %q version %q go %q VCS %q revision %q", got.MainModule(), got.PackagePath(), got.Version(), got.GoVersion(), got.VCS(), got.Revision())
	}
	if !got.BuildTime().Equal(wantTime) || got.TargetOS() != "darwin" || got.TargetArch() != "arm64" || got.VCSModified() || !got.VCSAvailable() {
		t.Fatalf("compiled facts = time %s target %s/%s modified=%t available=%t", got.BuildTime(), got.TargetOS(), got.TargetArch(), got.VCSModified(), got.VCSAvailable())
	}
}

func TestNewNormalizesDevelopmentAndDirtyVersions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		version  string
		modified bool
		want     string
	}{
		{name: "absent development", want: DevelopmentVersion},
		{name: "go development", version: "(devel)", want: DevelopmentVersion},
		{name: "dirty development", version: "(devel)", modified: true, want: DevelopmentVersion + "+dirty"},
		{name: "dirty tagged", version: "v1.2.3", modified: true, want: "v1.2.3+dirty"},
		{name: "dirty tagged metadata", version: "v1.2.3+release", modified: true, want: "v1.2.3+release.dirty"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := New(Inputs{Version: test.version, VCSModified: test.modified})
			if got.Version() != test.want {
				t.Fatalf("Version() = %q, want %q", got.Version(), test.want)
			}
		})
	}
}

func TestExecutableMatchesTargetOS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		targetOS string
		want     string
	}{
		{targetOS: "darwin", want: Executable},
		{targetOS: "windows", want: WindowsExecutable},
	}
	for _, test := range tests {
		t.Run(test.targetOS, func(t *testing.T) {
			t.Parallel()
			if got := New(Inputs{TargetOS: test.targetOS}).Executable(); got != test.want {
				t.Fatalf("Executable() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFromBuildInfoFailsClosedOnIncompleteOrAmbiguousVCSFacts(t *testing.T) {
	t.Parallel()

	valid := []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: fullRevision},
		{Key: "vcs.time", Value: "2026-08-18T10:00:00Z"},
		{Key: "vcs.modified", Value: "false"},
	}
	tests := []struct {
		name     string
		mainPath string
		path     string
		settings []debug.BuildSetting
	}{
		{name: "missing VCS", settings: valid[1:]},
		{name: "other VCS", settings: append([]debug.BuildSetting{{Key: "vcs", Value: "hg"}}, valid[1:]...)},
		{name: "missing revision", settings: append([]debug.BuildSetting{valid[0]}, valid[2:]...)},
		{name: "missing time", settings: []debug.BuildSetting{valid[0], valid[1], valid[3]}},
		{name: "missing modified", settings: valid[:3]},
		{name: "empty revision", settings: []debug.BuildSetting{valid[0], {Key: "vcs.revision", Value: ""}, valid[2], valid[3]}},
		{name: "malformed time", settings: []debug.BuildSetting{valid[0], valid[1], {Key: "vcs.time", Value: "not-a-time"}, valid[3]}},
		{name: "modified alias", settings: []debug.BuildSetting{valid[0], valid[1], valid[2], {Key: "vcs.modified", Value: "TRUE"}}},
		{name: "duplicate VCS", settings: append(append([]debug.BuildSetting(nil), valid...), valid[0])},
		{name: "duplicate revision", settings: append(append([]debug.BuildSetting(nil), valid...), valid[1])},
		{name: "duplicate time", settings: append(append([]debug.BuildSetting(nil), valid...), valid[2])},
		{name: "duplicate modified", settings: append(append([]debug.BuildSetting(nil), valid...), valid[3])},
		{name: "copied main module", mainPath: "github.com/example/copied", path: CommandPackage, settings: valid},
		{name: "renamed command package", mainPath: Module, path: Module + "/cmd/copied", settings: valid},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mainPath := test.mainPath
			if mainPath == "" {
				mainPath = Module
			}
			path := test.path
			if path == "" {
				path = CommandPackage
			}
			got := FromBuildInfo(debug.BuildInfo{Path: path, Main: debug.Module{Path: mainPath, Version: "v1.2.3"}, GoVersion: "go1.26.6", Settings: test.settings})
			if got.VCSAvailable() {
				t.Fatal("VCSAvailable() = true, want false")
			}
		})
	}
}

func TestInfoHasNoExportedMutableFields(t *testing.T) {
	t.Parallel()

	typeOfInfo := reflect.TypeFor[Info]()
	for index := 0; index < typeOfInfo.NumField(); index++ {
		if typeOfInfo.Field(index).IsExported() {
			t.Fatalf("Info field %q is exported", typeOfInfo.Field(index).Name)
		}
	}
}

func TestReadReturnsCanonicalIdentity(t *testing.T) {
	t.Parallel()

	got := Read()
	wantExecutable := Executable
	if got.TargetOS() == "windows" {
		wantExecutable = WindowsExecutable
	}
	if got.Product() != Product || got.Executable() != wantExecutable || got.Module() != Module || got.RepositoryIdentity() != RepositoryIdentity || got.RepositoryURL() != RepositoryURL {
		t.Fatalf("identity = %s/%s/%s/%s/%s, want canonical constants", got.Product(), got.Executable(), got.Module(), got.RepositoryIdentity(), got.RepositoryURL())
	}
}
