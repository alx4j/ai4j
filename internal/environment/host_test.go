package environment_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/environment"
)

func TestClosedHostDimensions(t *testing.T) {
	t.Parallel()

	operatingSystem, err := environment.NewOperatingSystem("darwin")
	if err != nil || operatingSystem != environment.DarwinOperatingSystem() || !operatingSystem.Valid() {
		t.Fatalf("NewOperatingSystem(darwin) = %v, %v", operatingSystem, err)
	}
	architecture, err := environment.NewArchitecture("arm64")
	if err != nil || architecture != environment.ARM64Architecture() || !architecture.Valid() {
		t.Fatalf("NewArchitecture(arm64) = %v, %v", architecture, err)
	}

	for _, value := range []string{"", "Darwin", "linux", "darwin\n", string([]byte{0xff})} {
		_, parseErr := environment.NewOperatingSystem(value)
		requireCode(t, parseErr, environment.CodeInvalidOperatingSystem)
	}
	for _, value := range []string{"", "ARM64", "amd64", "arm64\t", string([]byte{0xff})} {
		_, parseErr := environment.NewArchitecture(value)
		requireCode(t, parseErr, environment.CodeInvalidArchitecture)
	}
	if (environment.OperatingSystem{}).Valid() || (environment.Architecture{}).Valid() {
		t.Fatal("zero host dimensions must be invalid")
	}
}

func TestDarwinVersionPreservesCanonicalComponentCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input      string
		components int
		patch      uint32
		hasPatch   bool
	}{
		{input: "13.0", components: 2},
		{input: "13.0.0", components: 3, hasPatch: true},
		{input: "15.6.1", components: 3, patch: 1, hasPatch: true},
		{input: "4294967295.4294967295", components: 2},
	}
	for _, test := range tests {
		test := test
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()
			version, err := environment.NewDarwinVersion(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if !version.Valid() || version.String() != test.input || version.Components() != test.components {
				t.Fatalf("version = %q/%d, want %q/%d", version.String(), version.Components(), test.input, test.components)
			}
			patch, ok := version.Patch()
			if patch != test.patch || ok != test.hasPatch {
				t.Fatalf("Patch() = %d, %t, want %d, %t", patch, ok, test.patch, test.hasPatch)
			}
		})
	}
}

func TestDarwinVersionRejectsEveryNonCanonicalShape(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"", "0.1", "13", "13.", ".13", "13.0.", "13.0.1.2", "013.0", "13.00", "13.0.01",
		"+13.0", "-13.0", "13.a", "13 0", "13.0\n", "１３.０", "4294967296.0", strings.Repeat("1", 129),
	}
	for _, value := range invalid {
		_, err := environment.NewDarwinVersion(value)
		requireCode(t, err, environment.CodeInvalidDarwinVersion)
	}
	if (environment.DarwinVersion{}).Valid() {
		t.Fatal("zero Darwin version must be invalid")
	}
}

func TestHostTupleRequiresExactDarwinARM64Identity(t *testing.T) {
	t.Parallel()

	version := mustDarwinVersion(t, "15.6")
	tuple, err := environment.NewHostTuple(domain.DarwinHost(), environment.DarwinOperatingSystem(), environment.ARM64Architecture(), version)
	if err != nil || !tuple.Valid() || tuple.String() != "darwin/arm64/15.6" {
		t.Fatalf("NewHostTuple() = %v, %v", tuple, err)
	}
	unknownHost, _ := domain.NewHost("future")
	for _, test := range []struct {
		host domain.Host
		os   environment.OperatingSystem
		arch environment.Architecture
		ver  environment.DarwinVersion
	}{
		{host: unknownHost, os: environment.DarwinOperatingSystem(), arch: environment.ARM64Architecture(), ver: version},
		{host: domain.DarwinHost(), arch: environment.ARM64Architecture(), ver: version},
		{host: domain.DarwinHost(), os: environment.DarwinOperatingSystem(), ver: version},
		{host: domain.DarwinHost(), os: environment.DarwinOperatingSystem(), arch: environment.ARM64Architecture()},
	} {
		_, constructErr := environment.NewHostTuple(test.host, test.os, test.arch, test.ver)
		requireCode(t, constructErr, environment.CodeInvalidHostTuple)
	}
	if (environment.HostTuple{}).Valid() {
		t.Fatal("zero host tuple must be invalid")
	}
	encoded, err := json.Marshal(tuple)
	if err != nil || string(encoded) != `{"os":"darwin","arch":"arm64","version":"15.6"}` {
		t.Fatalf("MarshalJSON() = %s, %v", encoded, err)
	}
}
