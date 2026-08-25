package lifecycle_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestNativeExecutableProfileEnforcesLayoutAndArchitectureInvariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		layout        lifecycle.NativeImageLayout
		architectures lifecycle.ExecutableArchitectureSet
		valid         bool
	}{
		{name: "single arm64", layout: lifecycle.NativeSingleImage, architectures: lifecycle.ExecutableARM64, valid: true},
		{name: "single x86_64", layout: lifecycle.NativeSingleImage, architectures: lifecycle.ExecutableX8664, valid: true},
		{name: "single cannot contain two images", layout: lifecycle.NativeSingleImage, architectures: lifecycle.ExecutableARM64 | lifecycle.ExecutableX8664},
		{name: "multi universal", layout: lifecycle.NativeMultiImage, architectures: lifecycle.ExecutableARM64 | lifecycle.ExecutableX8664, valid: true},
		{name: "zero architecture", layout: lifecycle.NativeMultiImage},
		{name: "unknown architecture bit", layout: lifecycle.NativeMultiImage, architectures: 1 << 7},
		{name: "unknown layout", layout: lifecycle.NativeImageLayout("future"), architectures: lifecycle.ExecutableARM64},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			profile, err := lifecycle.NewNativeExecutableProfile(test.layout, lifecycle.NativeExecutable, test.architectures)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, err=%v", test.valid, err)
			}
			if test.valid && !profile.Valid() {
				t.Fatal("constructed profile is invalid")
			}
		})
	}
}

func TestStaticExecutableIssueKindsAreCoherent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind  lifecycle.StaticExecutableKind
		issue lifecycle.StaticExecutableIssue
		valid bool
	}{
		{kind: lifecycle.StaticExecutableUnsupported, issue: lifecycle.StaticIssueUnsupportedFormat, valid: true},
		{kind: lifecycle.StaticExecutableUnsupported, issue: lifecycle.StaticIssueUnsupportedArchitecture, valid: true},
		{kind: lifecycle.StaticExecutableMalformed, issue: lifecycle.StaticIssueMalformedHeader, valid: true},
		{kind: lifecycle.StaticExecutableMalformed, issue: lifecycle.StaticIssueTooManyArchitectures, valid: true},
		{kind: lifecycle.StaticExecutableTruncated, issue: lifecycle.StaticIssueTruncatedShebang, valid: true},
		{kind: lifecycle.StaticExecutableMalformed, issue: lifecycle.StaticIssueUnsupportedArchitecture},
		{kind: lifecycle.StaticExecutableTruncated, issue: lifecycle.StaticIssueMalformedHeader},
		{kind: lifecycle.StaticExecutableNative, issue: lifecycle.StaticIssueUnsupportedFormat},
	}
	for _, test := range tests {
		profile, err := lifecycle.NewStaticExecutableIssueProfile(test.kind, test.issue)
		if (err == nil) != test.valid {
			t.Fatalf("kind=%q issue=%q valid=%v err=%v", test.kind, test.issue, test.valid, err)
		}
		if test.valid && (!profile.Valid() || profile.ExecutionEligible()) {
			t.Fatalf("issue profile validity mismatch: valid=%v eligible=%v", profile.Valid(), profile.ExecutionEligible())
		}
	}
}

func TestShebangProfilesDistinguishDirectEnvAndAmbiguousForms(t *testing.T) {
	t.Parallel()

	direct, err := lifecycle.NewDirectShebangProfile("/usr/local/bin/node", "--no-warnings")
	if err != nil || !direct.Valid() || !direct.Runnable() || direct.Form() != lifecycle.ShebangDirect ||
		direct.Interpreter() != "/usr/local/bin/node" || direct.FixedArgument() != "--no-warnings" {
		t.Fatalf("direct profile mismatch: %#v, %v", direct, err)
	}
	env, err := lifecycle.NewEnvShebangProfile("/usr/bin/env", "node")
	if err != nil || !env.Valid() || !env.Runnable() || env.Form() != lifecycle.ShebangEnv || env.EnvTarget() != "node" {
		t.Fatalf("env profile mismatch: %#v, %v", env, err)
	}
	ambiguous, err := lifecycle.NewAmbiguousEnvShebangProfile("/usr/bin/env", lifecycle.ShebangEnvOption)
	if err != nil || !ambiguous.Valid() || ambiguous.Runnable() || ambiguous.Form() != lifecycle.ShebangEnvAmbiguous ||
		ambiguous.Ambiguity() != lifecycle.ShebangEnvOption {
		t.Fatalf("ambiguous profile mismatch: %#v, %v", ambiguous, err)
	}
	if _, err := lifecycle.NewEnvShebangProfile("/usr/bin/env", "node --flag"); err == nil {
		t.Fatal("multi-token env target was accepted")
	}
	if _, err := lifecycle.NewDirectShebangProfile("/usr/local/bin/node", "--first --second"); err == nil {
		t.Fatal("multi-token direct shebang argument was accepted")
	}
	for _, target := range []string{"-S", "--", "-i", "NODE_OPTIONS=--require=x"} {
		if _, err := lifecycle.NewEnvShebangProfile("/usr/bin/env", target); err == nil {
			t.Fatalf("option-like env target %q was accepted", target)
		}
	}
}

func TestExecutableProfilesRedactStructuredSourceFacts(t *testing.T) {
	t.Parallel()

	const canary = "PROFILE_CANARY_94D7"
	shebang, err := lifecycle.NewDirectShebangProfile("/private/"+canary+"/interpreter", "--"+canary)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := lifecycle.NewScriptStaticExecutableProfile(shebang)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{
		fmt.Sprintf("%v", profile), fmt.Sprintf("%+v", profile), fmt.Sprintf("%#v", profile), string(encoded),
		fmt.Sprintf("%#v", shebang),
	} {
		if strings.Contains(rendered, canary) {
			t.Fatalf("profile leaked canary: %q", rendered)
		}
	}
}

func TestInterpreterBindingMatchesExactScriptRequirementAndCandidate(t *testing.T) {
	t.Parallel()

	scriptShebang, err := lifecycle.NewEnvShebangProfile("/usr/bin/env", "node")
	if err != nil {
		t.Fatal(err)
	}
	script, err := lifecycle.NewScriptStaticExecutableProfile(scriptShebang)
	if err != nil {
		t.Fatal(err)
	}
	interpreter := validExecutableExpectation(t, nativeProfile(t, lifecycle.ExecutableARM64, lifecycle.NativeExecutable))
	binding := lifecycle.InterpreterBinding{
		Requirement: scriptShebang, Candidate: "node", ResolvedPath: `C:\qualified\node.exe`, Executable: interpreter,
	}
	if !binding.Valid() || !binding.Matches(script) {
		t.Fatalf("valid binding did not match: %#v", binding)
	}
	wrongCandidate := binding
	wrongCandidate.Candidate = "python3"
	if wrongCandidate.Matches(script) {
		t.Fatal("wrong interpreter candidate matched")
	}
	direct, err := lifecycle.NewDirectShebangProfile("/usr/local/bin/node", "")
	if err != nil {
		t.Fatal(err)
	}
	wrongRequirement := binding
	wrongRequirement.Requirement = direct
	if wrongRequirement.Matches(script) {
		t.Fatal("different shebang requirement matched")
	}
}

func TestExecutableExpectationRejectsObservationOnlyAndNonExecutableProfiles(t *testing.T) {
	t.Parallel()

	issue, err := lifecycle.NewStaticExecutableIssueProfile(lifecycle.StaticExecutableUnsupported, lifecycle.StaticIssueUnsupportedFormat)
	if err != nil {
		t.Fatal(err)
	}
	library := nativeProfile(t, lifecycle.ExecutableARM64, lifecycle.NativeSharedLibrary)
	native := nativeProfile(t, lifecycle.ExecutableARM64, lifecycle.NativeExecutable)
	for name, profile := range map[string]lifecycle.StaticExecutableProfile{
		"issue":   issue,
		"library": library,
	} {
		if expectation := validExecutableExpectation(t, profile); expectation.Valid() {
			t.Fatalf("%s profile was accepted for launch", name)
		}
	}
	if expectation := validExecutableExpectation(t, native); !expectation.Valid() {
		t.Fatal("native executable expectation was rejected")
	}
	windowsShaped := validExecutableExpectation(t, native)
	windowsShaped.Mode = 0
	if !windowsShaped.Valid() {
		t.Fatal("neutral executable expectation required POSIX execute bits")
	}
}

func TestExecutableAuthorityClassIsClosedAndOwnerCoherent(t *testing.T) {
	t.Parallel()

	native := nativeProfile(t, lifecycle.ExecutableARM64, lifecycle.NativeExecutable)
	base := validExecutableExpectation(t, native)
	for _, test := range []struct {
		name      string
		authority lifecycle.ExecutableAuthorityClass
		owner     lifecycle.OwnerClass
		valid     bool
	}{
		{name: "trusted current", authority: lifecycle.TrustedUserOrSystemAuthority, owner: lifecycle.CurrentUserOwner, valid: true},
		{name: "trusted system", authority: lifecycle.TrustedUserOrSystemAuthority, owner: lifecycle.SystemOwner, valid: true},
		{name: "current current", authority: lifecycle.CurrentUserAuthority, owner: lifecycle.CurrentUserOwner, valid: true},
		{name: "current system", authority: lifecycle.CurrentUserAuthority, owner: lifecycle.SystemOwner},
		{name: "system system", authority: lifecycle.SystemOwnedChainAuthority, owner: lifecycle.SystemOwner, valid: true},
		{name: "system current", authority: lifecycle.SystemOwnedChainAuthority, owner: lifecycle.CurrentUserOwner},
		{name: "zero", owner: lifecycle.CurrentUserOwner},
		{name: "unknown", authority: lifecycle.ExecutableAuthorityClass("future_v1"), owner: lifecycle.CurrentUserOwner},
		{name: "other owner", authority: lifecycle.TrustedUserOrSystemAuthority, owner: lifecycle.OtherOwner},
	} {
		t.Run(test.name, func(t *testing.T) {
			expectation := base
			expectation.Authority = test.authority
			expectation.OwnerClass = test.owner
			if got := expectation.Valid(); got != test.valid {
				t.Fatalf("expectation.Valid() = %t, want %t", got, test.valid)
			}
			request := lifecycle.ExecutableRequest{Candidate: "tool", Authority: test.authority}
			if got := request.Valid(); got != test.authority.Valid() {
				t.Fatalf("request.Valid() = %t, authority.Valid() = %t", got, test.authority.Valid())
			}
			observation := lifecycle.ExecutableObservation{
				ResolvedPath: "/usr/bin/tool", Authority: test.authority, Profile: native,
				Resource: lifecycle.ResourceObservation{
					Exists: true, Kind: lifecycle.ExecutableResource, OwnerClass: test.owner,
					OwnedByCurrentUser: test.owner == lifecycle.CurrentUserOwner,
					ExecutableDigest:   base.Digest, Mode: 0o755, Size: 1, LinkCount: 1,
					RootIdentity:   lifecycle.ObjectIdentity{Filesystem: 1, Object: 3},
					ParentIdentity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 4},
					Identity:       lifecycle.ObjectIdentity{Filesystem: 1, Object: 2},
				},
			}
			if got := observation.Valid(); got != test.valid {
				t.Fatalf("observation.Valid() = %t, want %t", got, test.valid)
			}
			if test.valid && test.owner == lifecycle.CurrentUserOwner {
				observation.Resource.OwnedByCurrentUser = false
				if observation.Valid() {
					t.Fatal("current-user owner accepted without current-user ownership fact")
				}
			}
			if test.valid && test.owner == lifecycle.SystemOwner {
				observation.Resource.OwnedByCurrentUser = true
				if observation.Valid() {
					t.Fatal("system owner accepted with a current-user ownership fact")
				}
			}
		})
	}
	if (lifecycle.ExecutableRequest{Candidate: "tool"}).Valid() {
		t.Fatal("zero executable authority was accepted")
	}
	if (lifecycle.ExecutableRequest{Candidate: "bad\x00tool", Authority: lifecycle.TrustedUserOrSystemAuthority}).Valid() {
		t.Fatal("invalid executable candidate was accepted")
	}
}

func TestProcessRequestRequiresExplicitBoundedInputsAndMatchingInterpreter(t *testing.T) {
	t.Parallel()

	native := validProcessRequest(t, nativeProfile(t, lifecycle.ExecutableARM64, lifecycle.NativeExecutable))
	if !native.Valid() {
		t.Fatal("valid native request rejected")
	}
	for name, mutate := range map[string]func(*lifecycle.ProcessRequest){
		"zero environment profile": func(request *lifecycle.ProcessRequest) {
			request.EnvironmentProfile = lifecycle.ProcessEnvironmentProfileID{}
		},
		"nil environment": func(request *lifecycle.ProcessRequest) { request.Environment = nil },
		"duplicate environment": func(request *lifecycle.ProcessRequest) {
			request.Environment = []lifecycle.EnvironmentBinding{{Name: "PATH", Value: "/bin"}, {Name: "PATH", Value: "/usr/bin"}}
		},
		"invalid environment name": func(request *lifecycle.ProcessRequest) {
			request.Environment = []lifecycle.EnvironmentBinding{{Name: "BAD-NAME", Value: "value"}}
		},
		"loader injection environment": func(request *lifecycle.ProcessRequest) {
			request.Environment = []lifecycle.EnvironmentBinding{{Name: "DYLD_INSERT_LIBRARIES", Value: "/tmp/inject.dylib"}}
		},
		"runtime injection environment": func(request *lifecycle.ProcessRequest) {
			request.Environment = []lifecycle.EnvironmentBinding{{Name: "NODE_OPTIONS", Value: "--require=/tmp/inject.js"}}
		},
		"unqualified git ssh": func(request *lifecycle.ProcessRequest) {
			request.Environment = []lifecycle.EnvironmentBinding{{Name: "GIT_SSH", Value: "/tmp/helper"}}
		},
		"nul argument":        func(request *lifecycle.ProcessRequest) { request.Arguments = []string{"a\x00b"} },
		"excessive arguments": func(request *lifecycle.ProcessRequest) { request.Arguments = make([]string, 257) },
		"excessive output":    func(request *lifecycle.ProcessRequest) { request.OutputLimitBytes = 16<<20 + 1 },
	} {
		request := native
		mutate(&request)
		if request.Valid() {
			t.Fatalf("%s request was accepted", name)
		}
	}
	defaultCWD := native
	defaultCWD.WorkingDirectory = lifecycle.DirectoryExpectation{}
	if !defaultCWD.Valid() {
		t.Fatal("zero cwd did not request the configured safe default")
	}
	partialCWD := native
	partialCWD.WorkingDirectory = lifecycle.DirectoryExpectation{Root: lifecycle.StateRoot, Path: "cwd"}
	if partialCWD.Valid() {
		t.Fatal("partially specified cwd was accepted")
	}
	typedHelper := native
	typedHelper.ExecutableEnvironment = []lifecycle.ExecutableEnvironmentBinding{{
		Name: "GIT_SSH", ResolvedPath: "/private/bin/ai4j-git-ssh",
		ExpectedExecutable: validExecutableExpectation(t, nativeProfile(t, lifecycle.ExecutableARM64, lifecycle.NativeExecutable)),
	}}
	if !typedHelper.Valid() {
		t.Fatal("separately qualified executable environment binding was rejected")
	}
	typedHelper.Environment = []lifecycle.EnvironmentBinding{{Name: "GIT_SSH", Value: "/tmp/unqualified"}}
	if typedHelper.Valid() {
		t.Fatal("duplicate/unqualified executable environment value was accepted")
	}

	shebang, err := lifecycle.NewEnvShebangProfile("/usr/bin/env", "node")
	if err != nil {
		t.Fatal(err)
	}
	script := validProcessRequest(t, scriptProfile(t, shebang))
	script.Interpreter = lifecycle.InterpreterBinding{
		Requirement: shebang, Candidate: "node", ResolvedPath: "/opt/homebrew/bin/node",
		Executable: validExecutableExpectation(t, nativeProfile(t, lifecycle.ExecutableARM64, lifecycle.NativeExecutable)),
	}
	if !script.Valid() {
		t.Fatal("valid script request rejected")
	}
	script.Interpreter.Candidate = "sh"
	if script.Valid() {
		t.Fatal("mismatched interpreter binding was accepted")
	}
}

func TestProcessAndEnvironmentFormattingNeverExposeValues(t *testing.T) {
	t.Parallel()

	const canary = "ENV_CANARY_741D"
	request := validProcessRequest(t, nativeProfile(t, lifecycle.ExecutableARM64, lifecycle.NativeExecutable))
	request.Environment = []lifecycle.EnvironmentBinding{{Name: "TOKEN", Value: canary}}
	request.ExecutableEnvironment = []lifecycle.ExecutableEnvironmentBinding{{
		Name: "GIT_SSH", ResolvedPath: "/private/" + canary,
		ExpectedExecutable: validExecutableExpectation(t, nativeProfile(t, lifecycle.ExecutableARM64, lifecycle.NativeExecutable)),
	}}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{fmt.Sprintf("%v", request), fmt.Sprintf("%+v", request), fmt.Sprintf("%#v", request), string(encoded), fmt.Sprintf("%#v", request.Environment[0]), fmt.Sprintf("%#v", request.ExecutableEnvironment[0])} {
		if strings.Contains(rendered, canary) {
			t.Fatalf("request leaked environment value: %q", rendered)
		}
	}
}

func TestProcessEnvironmentProfileIDIsBoundedOpaqueHostNeutralText(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"isolated", "git_hardened_v1", "profile9"} {
		profile, err := lifecycle.NewProcessEnvironmentProfileID(value)
		if err != nil || !profile.Valid() || profile.String() != value {
			t.Fatalf("profile %q = %q, %v", value, profile.String(), err)
		}
	}
	for _, value := range []string{"", "Upper", "-option", "two words", "with.dot", strings.Repeat("a", 65), "bad\x00value"} {
		if profile, err := lifecycle.NewProcessEnvironmentProfileID(value); err == nil || profile.Valid() {
			t.Fatalf("invalid profile %q accepted", value)
		}
	}
	if (lifecycle.ProcessEnvironmentProfileID{}).Valid() {
		t.Fatal("zero profile identifier accepted")
	}
}

func nativeProfile(t *testing.T, architectures lifecycle.ExecutableArchitectureSet, role lifecycle.NativeFileRole) lifecycle.StaticExecutableProfile {
	t.Helper()
	native, err := lifecycle.NewNativeExecutableProfile(lifecycle.NativeSingleImage, role, architectures)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := lifecycle.NewNativeStaticExecutableProfile(native)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func scriptProfile(t *testing.T, shebang lifecycle.ShebangProfile) lifecycle.StaticExecutableProfile {
	t.Helper()
	profile, err := lifecycle.NewScriptStaticExecutableProfile(shebang)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func validExecutableExpectation(t *testing.T, profile lifecycle.StaticExecutableProfile) lifecycle.ExecutableExpectation {
	t.Helper()
	digest, err := domain.NewExecutableDigest(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	return lifecycle.ExecutableExpectation{
		Identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 2}, Authority: lifecycle.TrustedUserOrSystemAuthority,
		OwnerClass: lifecycle.CurrentUserOwner,
		Mode:       fs.FileMode(0o755), Digest: digest, Profile: profile,
	}
}

func validProcessRequest(t *testing.T, profile lifecycle.StaticExecutableProfile) lifecycle.ProcessRequest {
	t.Helper()
	environmentProfile, err := lifecycle.NewProcessEnvironmentProfileID("fixture_v1")
	if err != nil {
		t.Fatal(err)
	}
	return lifecycle.ProcessRequest{
		Executable: "/usr/bin/tool", Arguments: []string{"--version"},
		WorkingDirectory: lifecycle.DirectoryExpectation{
			Root: lifecycle.StateRoot, Path: "cwd", RootIdentity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 3},
			ParentIdentity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 3}, Identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 4},
		},
		EnvironmentProfile: environmentProfile,
		Environment:        []lifecycle.EnvironmentBinding{}, Timeout: time.Minute, OutputLimitBytes: 1024,
		TerminationGrace: time.Second, ExpectedExecutable: validExecutableExpectation(t, profile),
	}
}
