package environment_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

const (
	testDigest = "abababababababababababababababababababababababababababababababab"
	pathCanary = "PRIVATE_PATH_CANARY_7f36"
)

func requireCode(t *testing.T, err error, want environment.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %q", want)
	}
	var validation environment.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error type = %T, want environment.ValidationError", err)
	}
	if got := validation.Code(); got != want {
		t.Fatalf("error code = %q, want %q", got, want)
	}
}

func mustDarwinVersion(t *testing.T, value string) environment.DarwinVersion {
	t.Helper()
	version, err := environment.NewDarwinVersion(value)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func mustSemanticVersion(t *testing.T, value string) environment.SemanticVersion {
	t.Helper()
	version, err := environment.NewSemanticVersion(value)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func validHost(t *testing.T) environment.HostTuple {
	t.Helper()
	host, err := environment.NewHostTuple(
		domain.DarwinHost(),
		environment.DarwinOperatingSystem(),
		environment.ARM64Architecture(),
		mustDarwinVersion(t, "15.6.1"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func validExecutable(t *testing.T, tool environment.Tool) environment.ExecutableIdentity {
	t.Helper()
	native, err := lifecycle.NewNativeExecutableProfile(
		lifecycle.NativeSingleImage,
		lifecycle.NativeExecutable,
		lifecycle.ExecutableARM64,
	)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := lifecycle.NewNativeStaticExecutableProfile(native)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := domain.NewExecutableDigest(testDigest)
	if err != nil {
		t.Fatal(err)
	}
	version := mustSemanticVersion(t, "2.1.234")
	resolvedPath := "/Users/alex/.local/bin/claude"
	if tool == environment.GitTool() {
		revision, revisionErr := environment.NewAppleGitRevision("154.3")
		if revisionErr != nil {
			t.Fatal(revisionErr)
		}
		version = mustSemanticVersion(t, "2.39.5")
		resolvedPath = "/usr/bin/git"
		toolVersion, versionErr := environment.NewAppleGitToolVersion(version, revision)
		if versionErr != nil {
			t.Fatal(versionErr)
		}
		return mustExecutable(t, tool, toolVersion, resolvedPath, digest, profile)
	}
	toolVersion, err := environment.NewSemanticToolVersion(tool, version)
	if err != nil {
		t.Fatal(err)
	}
	return mustExecutable(t, tool, toolVersion, resolvedPath, digest, profile)
}

func mustExecutable(
	t *testing.T,
	tool environment.Tool,
	version environment.ToolVersion,
	resolvedPath string,
	digest domain.ExecutableDigest,
	profile lifecycle.StaticExecutableProfile,
) environment.ExecutableIdentity {
	t.Helper()
	observation := lifecycle.ExecutableObservation{
		ResolvedPath: resolvedPath, Authority: lifecycle.TrustedUserOrSystemAuthority,
		Resource: lifecycle.ResourceObservation{
			Exists:             true,
			OwnedByCurrentUser: true,
			Kind:               lifecycle.ExecutableResource,
			OwnerClass:         lifecycle.CurrentUserOwner,
			ExecutableDigest:   digest,
			Mode:               fs.FileMode(0o755),
			Size:               4096,
			LinkCount:          1,
			RootIdentity:       lifecycle.ObjectIdentity{Filesystem: 1, Object: 10},
			ParentIdentity:     lifecycle.ObjectIdentity{Filesystem: 1, Object: 11},
			Identity:           lifecycle.ObjectIdentity{Filesystem: 1, Object: 12},
		},
		Profile: profile,
	}
	identity, err := environment.NewExecutableIdentity(tool, version, observation)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func candidateFacts(t *testing.T) []environment.CandidateCapabilityFact {
	t.Helper()
	values := domain.MVPCapabilities().Values()
	facts := make([]environment.CandidateCapabilityFact, len(values))
	for index, value := range values {
		fact, err := environment.NewCandidateCapabilityFact(value)
		if err != nil {
			t.Fatal(err)
		}
		facts[index] = fact
	}
	return facts
}

func validProfile(t *testing.T) environment.CapabilityProfile {
	t.Helper()
	id, err := environment.NewProfileID("claude-2.1.234-darwin-arm64-v1")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := environment.NewCapabilityProfile(id, candidateFacts(t))
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func mustDirectory(
	t *testing.T,
	role environment.DirectoryRole,
	source environment.DirectorySource,
	presence environment.DirectoryPresence,
	absolutePath string,
) environment.Directory {
	t.Helper()
	directory, err := environment.NewDirectory(role, source, presence, absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func validDirectories(t *testing.T) []environment.Directory {
	t.Helper()
	return []environment.Directory{
		mustDirectory(t, environment.ClaudeConfigurationDirectory(), environment.DefaultDirectorySource(), environment.PresentDirectory(), "/Users/alex/.claude"),
		mustDirectory(t, environment.ClaudeRulesDirectory(), environment.DefaultDirectorySource(), environment.PresentDirectory(), "/Users/alex/.claude/rules"),
		mustDirectory(t, environment.AI4JStateDirectory(), environment.PrivateRuntimeDirectorySource(), environment.PresentDirectory(), "/Users/alex/Library/Application Support/ai4j/state"),
		mustDirectory(t, environment.AI4JRecoveryDirectory(), environment.PrivateRuntimeDirectorySource(), environment.AbsentDirectory(), "/Users/alex/Library/Application Support/ai4j/recovery"),
	}
}

func validObservation(t *testing.T) environment.Observation {
	t.Helper()
	observation, err := environment.NewObservation(
		validHost(t),
		[]environment.ExecutableIdentity{validExecutable(t, environment.GitTool()), validExecutable(t, environment.ClaudeTool())},
		validDirectories(t),
		validProfile(t),
		environment.PolicyNotObservable(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}
