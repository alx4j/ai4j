package environment_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestExecutableIdentityBindsToolVersionAndHostProof(t *testing.T) {
	t.Parallel()

	identity := validExecutable(t, environment.ClaudeTool())
	if !identity.Valid() || identity.Tool() != environment.ClaudeTool() || identity.Version().Tool() != environment.ClaudeTool() {
		t.Fatalf("identity = %v", identity)
	}
	if identity.ResolvedPath() != "/Users/alex/.local/bin/claude" || identity.Digest().String() != testDigest {
		t.Fatal("identity did not retain the exact host proof")
	}
	if identity.Observation().Profile != identity.Profile() {
		t.Fatal("static executable profile was not retained exactly")
	}
	if (environment.ExecutableIdentity{}).Valid() {
		t.Fatal("zero executable identity must be invalid")
	}
}

func TestExecutableIdentityRejectsMismatchedOrNonRunnableProof(t *testing.T) {
	t.Parallel()

	valid := validExecutable(t, environment.ClaudeTool())
	_, err := environment.NewExecutableIdentity(environment.GitTool(), valid.Version(), valid.Observation())
	requireCode(t, err, environment.CodeInvalidExecutable)

	observation := valid.Observation()
	observation.ResolvedPath = ""
	_, err = environment.NewExecutableIdentity(valid.Tool(), valid.Version(), observation)
	requireCode(t, err, environment.CodeInvalidExecutable)

	observation = valid.Observation()
	observation.Resource.ExecutableDigest = domain.ExecutableDigest{}
	_, err = environment.NewExecutableIdentity(valid.Tool(), valid.Version(), observation)
	requireCode(t, err, environment.CodeInvalidExecutable)

	issue, issueErr := lifecycle.NewStaticExecutableIssueProfile(
		lifecycle.StaticExecutableUnsupported,
		lifecycle.StaticIssueUnsupportedFormat,
	)
	if issueErr != nil {
		t.Fatal(issueErr)
	}
	observation = valid.Observation()
	observation.Profile = issue
	if !observation.Valid() {
		t.Fatal("fixture must isolate execution eligibility from host-observation validity")
	}
	_, err = environment.NewExecutableIdentity(valid.Tool(), valid.Version(), observation)
	requireCode(t, err, environment.CodeInvalidExecutable)
}

func TestExecutableIdentityRequiresNativeARM64Proof(t *testing.T) {
	t.Parallel()

	valid := validExecutable(t, environment.ClaudeTool())
	tests := []struct {
		name    string
		profile lifecycle.StaticExecutableProfile
		valid   bool
	}{
		{name: "x86_64 only", profile: nativeStaticProfile(t, lifecycle.NativeSingleImage, lifecycle.ExecutableX8664)},
		{name: "universal with arm64", profile: nativeStaticProfile(t, lifecycle.NativeMultiImage, lifecycle.ExecutableARM64|lifecycle.ExecutableX8664), valid: true},
		{name: "direct script", profile: directScriptProfile(t)},
		{name: "env script", profile: envScriptProfile(t)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observation := valid.Observation()
			observation.Profile = test.profile
			if !observation.Valid() {
				t.Fatal("fixture must be a valid lifecycle executable observation")
			}
			got, err := environment.NewExecutableIdentity(valid.Tool(), valid.Version(), observation)
			if test.valid {
				if err != nil || !got.Valid() || !got.Profile().ExecutionEligible() {
					t.Fatalf("NewExecutableIdentity() = %v, %v", got, err)
				}
				return
			}
			requireCode(t, err, environment.CodeInvalidExecutable)
		})
	}
}

func nativeStaticProfile(t *testing.T, layout lifecycle.NativeImageLayout, architectures lifecycle.ExecutableArchitectureSet) lifecycle.StaticExecutableProfile {
	t.Helper()
	native, err := lifecycle.NewNativeExecutableProfile(layout, lifecycle.NativeExecutable, architectures)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := lifecycle.NewNativeStaticExecutableProfile(native)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func directScriptProfile(t *testing.T) lifecycle.StaticExecutableProfile {
	t.Helper()
	shebang, err := lifecycle.NewDirectShebangProfile("/bin/sh", "")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := lifecycle.NewScriptStaticExecutableProfile(shebang)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func envScriptProfile(t *testing.T) lifecycle.StaticExecutableProfile {
	t.Helper()
	shebang, err := lifecycle.NewEnvShebangProfile("/usr/bin/env", "node")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := lifecycle.NewScriptStaticExecutableProfile(shebang)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func TestExecutableIdentityFormattingAndJSONRedactHostProof(t *testing.T) {
	t.Parallel()

	identity := validExecutable(t, environment.ClaudeTool())
	formatted := fmt.Sprintf("%v|%+v|%#v|%q", identity, identity, identity, identity)
	encoded, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{formatted, string(encoded)} {
		if strings.Contains(output, identity.ResolvedPath()) || strings.Contains(output, testDigest) || strings.Contains(output, ".local/bin") {
			t.Fatalf("executable identity disclosure = %q", output)
		}
	}
	if !strings.Contains(string(encoded), `"identity":"redacted"`) {
		t.Fatalf("MarshalJSON() = %s", encoded)
	}
}
