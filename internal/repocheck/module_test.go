package repocheck

import (
	"strings"
	"testing"
)

func TestValidateModule(t *testing.T) {
	t.Parallel()

	dependency := ModuleVersion{Path: "golang.org/x/mod", Version: "v0.38.0"}
	valid := ModuleSnapshot{
		Path:           ExpectedModule,
		GoVersion:      ExpectedGo,
		Toolchain:      ExpectedToolchain,
		Requirements:   []ModuleVersion{dependency},
		Sums:           map[ModuleVersion]bool{dependency: true},
		GoModTracked:   true,
		GoSumTracked:   true,
		GoSumIsRegular: true,
	}

	tests := []struct {
		name      string
		change    func(*ModuleSnapshot)
		wantError string
	}{
		{name: "valid", change: func(*ModuleSnapshot) {}},
		{name: "wrong module", change: func(s *ModuleSnapshot) { s.Path = "example.com/wrong" }, wantError: "module path"},
		{name: "wrong language version", change: func(s *ModuleSnapshot) { s.GoVersion = "1.26.1" }, wantError: "Go language version"},
		{name: "wrong toolchain", change: func(s *ModuleSnapshot) { s.Toolchain = "go1.26.5" }, wantError: "toolchain"},
		{name: "local replacement", change: func(s *ModuleSnapshot) {
			s.Replacements = []Replacement{{Old: dependency, New: ModuleVersion{Path: "../mod"}}}
		}, wantError: "local replacement"},
		{name: "untracked go mod", change: func(s *ModuleSnapshot) { s.GoModTracked = false }, wantError: "go.mod is not tracked"},
		{name: "untracked go sum", change: func(s *ModuleSnapshot) { s.GoSumTracked = false }, wantError: "go.sum is not tracked"},
		{name: "non regular go sum", change: func(s *ModuleSnapshot) { s.GoSumIsRegular = false }, wantError: "go.sum is not a regular file"},
		{name: "missing dependency checksum", change: func(s *ModuleSnapshot) { s.Sums = nil }, wantError: "has no content checksum"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := valid
			snapshot.Requirements = append([]ModuleVersion(nil), valid.Requirements...)
			snapshot.Replacements = append([]Replacement(nil), valid.Replacements...)
			snapshot.Sums = make(map[ModuleVersion]bool, len(valid.Sums))
			for module, present := range valid.Sums {
				snapshot.Sums[module] = present
			}
			test.change(&snapshot)

			err := ValidateModule(snapshot)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateModule() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateModule() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}
