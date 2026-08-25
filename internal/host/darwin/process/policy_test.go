package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

type rejectingAuthority struct{}

func (rejectingAuthority) OpenExecutable(context.Context, string, lifecycle.ExecutableExpectation) (*os.File, error) {
	return nil, errors.New("not used")
}

func (rejectingAuthority) OpenDirectory(context.Context, lifecycle.DirectoryExpectation) (*os.File, error) {
	return nil, errors.New("not used")
}

func TestMVPPolicyCopiesClosedProfilesAndDigestRules(t *testing.T) {
	digest := testDigest(t, '1')
	denied := []domain.ExecutableDigest{digest}
	definitions := mvpEnvironmentProfiles()
	value, err := newPolicyWithProfiles(Config{
		Authority: rejectingAuthority{}, SafeWorkingDirectory: testDirectoryExpectation(),
		DeniedExecutableDigests: denied,
	}, definitions)
	if err != nil {
		t.Fatal(err)
	}

	definitions[1].values[0].Value = "unsafe"
	definitions[1].allowExecutableSSH = false
	denied[0] = testDigest(t, '2')

	environment, err := value.ordinaryEnvironment(gitHardenedProfileID, gitHardenedEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := environmentList(environment), gitHardenedEnvironmentList(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("environment = %v", got)
	}
	if err := value.bindExecutableEnvironment(gitHardenedProfileID, environment, executableSSHEnvironment, "/dev/fd/4"); err != nil {
		t.Fatal(err)
	}
	wantWithHelper := []string{
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_LFS_SKIP_SMUDGE=1",
		"GIT_OPTIONAL_LOCKS=0", "GIT_PROTOCOL_FROM_USER=0", "GIT_SSH=/dev/fd/4",
		"GIT_SSH_VARIANT=ssh", "GIT_TERMINAL_PROMPT=0", "LANG=C", "LC_ALL=C",
	}
	if got := environmentList(environment); fmt.Sprint(got) != fmt.Sprint(wantWithHelper) {
		t.Fatalf("helper environment = %v", got)
	}
	if !value.deniedExecutable("/renamed/safe", digest) || !value.deniedExecutable("/usr/bin/BASH", testDigest(t, '3')) {
		t.Fatal("digest or basename denial was not retained")
	}
}

func TestMVPPolicyRequiresEveryExactProfileBinding(t *testing.T) {
	value, err := newPolicy(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	claude := claudeProbeEnvironment()
	git := gitHardenedEnvironment()
	unknown, err := lifecycle.NewProcessEnvironmentProfileID("unknown_v1")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		profile  lifecycle.ProcessEnvironmentProfileID
		bindings []lifecycle.EnvironmentBinding
		valid    bool
	}{
		{name: "isolated exact empty", profile: isolatedProfileID, bindings: []lifecycle.EnvironmentBinding{}, valid: true},
		{name: "isolated nil", profile: isolatedProfileID},
		{name: "git exact", profile: gitHardenedProfileID, bindings: git, valid: true},
		{name: "git missing", profile: gitHardenedProfileID, bindings: git[:len(git)-1]},
		{name: "git extra", profile: gitHardenedProfileID, bindings: append(append([]lifecycle.EnvironmentBinding(nil), git...), lifecycle.EnvironmentBinding{Name: "HOME", Value: "/tmp"})},
		{name: "git altered", profile: gitHardenedProfileID, bindings: replaceEnvironmentValue(git, "GIT_OPTIONAL_LOCKS", "1")},
		{name: "git duplicate", profile: gitHardenedProfileID, bindings: append(append([]lifecycle.EnvironmentBinding(nil), git[:len(git)-1]...), git[0])},
		{name: "claude exact", profile: claudeProbeProfileID, bindings: claude, valid: true},
		{name: "claude missing", profile: claudeProbeProfileID, bindings: claude[:2]},
		{name: "claude locale extra", profile: claudeProbeProfileID, bindings: append(append([]lifecycle.EnvironmentBinding(nil), claude...), lifecycle.EnvironmentBinding{Name: "LANG", Value: "C"})},
		{name: "unknown", profile: unknown, bindings: []lifecycle.EnvironmentBinding{}},
		{name: "zero profile", bindings: []lifecycle.EnvironmentBinding{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment, environmentErr := value.ordinaryEnvironment(test.profile, test.bindings)
			if test.valid {
				if environmentErr != nil {
					t.Fatal(environmentErr)
				}
				if len(environment) != len(test.bindings) {
					t.Fatalf("environment size = %d", len(environment))
				}
				return
			}
			if !errors.Is(environmentErr, errProcessPolicyViolation) || environment != nil {
				t.Fatalf("environment/error = %v / %v", environment, environmentErr)
			}
		})
	}
}

func TestMVPPolicyRestrictsTypedGitSSHToGitProfile(t *testing.T) {
	value, err := newPolicy(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []lifecycle.ProcessEnvironmentProfileID{isolatedProfileID, claudeProbeProfileID} {
		if value.executableEnvironmentAllowed(profile, executableSSHEnvironment) {
			t.Fatalf("profile %q allowed typed GIT_SSH", profile.String())
		}
		if err := value.bindExecutableEnvironment(profile, map[string]string{}, executableSSHEnvironment, "/dev/fd/4"); !errors.Is(err, errProcessPolicyViolation) {
			t.Fatalf("profile %q bind error = %v", profile.String(), err)
		}
	}
	if !value.executableEnvironmentAllowed(gitHardenedProfileID, executableSSHEnvironment) ||
		value.executableEnvironmentAllowed(gitHardenedProfileID, "GIT_SSH_COMMAND") {
		t.Fatal("Git executable environment policy is not exact")
	}
}

func TestPolicyRejectsInvalidConfigurationAndProfiles(t *testing.T) {
	base := testConfig(t)
	testID := mustProcessEnvironmentProfileID("test_v1")
	tests := []struct {
		name        string
		mutate      func(*Config)
		definitions []environmentProfileDefinition
	}{
		{name: "no authority", mutate: func(config *Config) { config.Authority = nil }},
		{name: "typed nil authority", mutate: func(config *Config) { var authority *rejectingAuthority; config.Authority = authority }},
		{name: "no denied digest", mutate: func(config *Config) { config.DeniedExecutableDigests = nil }},
		{name: "duplicate digest", mutate: func(config *Config) {
			config.DeniedExecutableDigests = append(config.DeniedExecutableDigests, config.DeniedExecutableDigests[0])
		}},
		{name: "no profiles", definitions: []environmentProfileDefinition{}},
		{name: "zero profile", definitions: []environmentProfileDefinition{{values: []lifecycle.EnvironmentBinding{}}}},
		{name: "nil values", definitions: []environmentProfileDefinition{{id: testID}}},
		{name: "duplicate profile", definitions: []environmentProfileDefinition{{id: testID, values: []lifecycle.EnvironmentBinding{}}, {id: testID, values: []lifecycle.EnvironmentBinding{}}}},
		{name: "duplicate binding", definitions: []environmentProfileDefinition{{id: testID, values: []lifecycle.EnvironmentBinding{{Name: "LANG", Value: "C"}, {Name: "LANG", Value: "C"}}}}},
		{name: "loader variable", definitions: []environmentProfileDefinition{{id: testID, values: []lifecycle.EnvironmentBinding{{Name: "DYLD_INSERT_LIBRARIES", Value: "/tmp/a"}}}}},
		{name: "untyped git ssh", definitions: []environmentProfileDefinition{{id: testID, values: []lifecycle.EnvironmentBinding{{Name: "GIT_SSH", Value: "/tmp/a"}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := base
			configuration.DeniedExecutableDigests = append([]domain.ExecutableDigest(nil), base.DeniedExecutableDigests...)
			if test.mutate != nil {
				test.mutate(&configuration)
			}
			definitions := test.definitions
			if definitions == nil {
				definitions = mvpEnvironmentProfiles()
			}
			if _, err := newPolicyWithProfiles(configuration, definitions); !errors.Is(err, errInvalidProcessPolicy) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestConfigFormattingNeverDisclosesDigests(t *testing.T) {
	configuration := testConfig(t)
	encoded, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	for _, formatted := range []string{fmt.Sprintf("%v", configuration), fmt.Sprintf("%+v", configuration), fmt.Sprintf("%#v", configuration), string(encoded)} {
		if strings.Contains(formatted, configuration.DeniedExecutableDigests[0].String()) {
			t.Fatalf("format leaked private policy: %s", formatted)
		}
	}
}

func gitHardenedEnvironment() []lifecycle.EnvironmentBinding {
	return append([]lifecycle.EnvironmentBinding(nil), mvpEnvironmentProfiles()[1].values...)
}

func gitHardenedEnvironmentList() []string {
	return []string{
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_LFS_SKIP_SMUDGE=1",
		"GIT_OPTIONAL_LOCKS=0", "GIT_PROTOCOL_FROM_USER=0", "GIT_TERMINAL_PROMPT=0", "LANG=C", "LC_ALL=C",
	}
}

func claudeProbeEnvironment() []lifecycle.EnvironmentBinding {
	return append([]lifecycle.EnvironmentBinding(nil), mvpEnvironmentProfiles()[2].values...)
}

func replaceEnvironmentValue(values []lifecycle.EnvironmentBinding, name, replacement string) []lifecycle.EnvironmentBinding {
	result := append([]lifecycle.EnvironmentBinding(nil), values...)
	for index := range result {
		if result[index].Name == name {
			result[index].Value = replacement
		}
	}
	return result
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Authority: rejectingAuthority{}, SafeWorkingDirectory: testDirectoryExpectation(),
		DeniedExecutableDigests: []domain.ExecutableDigest{testDigest(t, '1')},
	}
}

func testDirectoryExpectation() lifecycle.DirectoryExpectation {
	return lifecycle.DirectoryExpectation{
		Root: lifecycle.StateRoot, Path: "cwd",
		RootIdentity:   lifecycle.ObjectIdentity{Filesystem: 1, Object: 1},
		ParentIdentity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 2},
		Identity:       lifecycle.ObjectIdentity{Filesystem: 1, Object: 3},
	}
}

func testDigest(t *testing.T, digit byte) domain.ExecutableDigest {
	t.Helper()
	value, err := domain.NewExecutableDigest(strings.Repeat(string(digit), 64))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
