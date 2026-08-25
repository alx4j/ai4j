package repocheck

import (
	"strings"
	"testing"
)

func TestValidateReleaseInputs(t *testing.T) {
	t.Parallel()

	valid := ReleaseInputs{
		GoVersion:     ExpectedToolchain,
		ToolchainMode: "local",
		WorkspaceMode: "off",
		ModuleFile:    "/repo/go.mod",
		ExpectedMod:   "/repo/go.mod",
		Commit:        "0123456789abcdef0123456789abcdef01234567",
	}
	tests := []struct {
		name      string
		change    func(*ReleaseInputs)
		wantError string
	}{
		{name: "valid", change: func(*ReleaseInputs) {}},
		{name: "wrong toolchain", change: func(i *ReleaseInputs) { i.GoVersion = "go1.26.5" }, wantError: "Go runtime"},
		{name: "toolchain download enabled", change: func(i *ReleaseInputs) { i.ToolchainMode = "auto" }, wantError: "GOTOOLCHAIN"},
		{name: "active workspace", change: func(i *ReleaseInputs) { i.WorkspaceMode = "/repo/go.work" }, wantError: "GOWORK"},
		{name: "module override", change: func(i *ReleaseInputs) { i.ModuleFile = "/other/go.mod" }, wantError: "active module"},
		{name: "writable modules", change: func(i *ReleaseInputs) { i.GoFlags = "-mod=mod" }, wantError: "GOFLAGS"},
		{name: "experiment", change: func(i *ReleaseInputs) { i.GoExperiment = "greenteagc" }, wantError: "GOEXPERIMENT"},
		{name: "missing vcs", change: func(i *ReleaseInputs) { i.Commit = "" }, wantError: "VCS revision"},
		{name: "dirty tree", change: func(i *ReleaseInputs) { i.Dirty = true }, wantError: "working tree is dirty"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inputs := valid
			test.change(&inputs)
			err := ValidateReleaseInputs(inputs)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateReleaseInputs() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateReleaseInputs() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}
