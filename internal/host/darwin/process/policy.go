// Package process implements the Darwin process boundary used by AI4J.
package process

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

const (
	maximumEnvironmentProfiles = 16
	maximumPolicyNames         = 128
	maximumEnvironmentName     = 128
	maximumEnvironmentValue    = 64 << 10
	maximumPolicyEnvironment   = 256 << 10
	executableSSHEnvironment   = "GIT_SSH"
	executableSSHVariant       = "GIT_SSH_VARIANT"
	executableSSHVariantValue  = "ssh"
	isolatedProfileName        = "isolated"
	gitHardenedProfileName     = "git_hardened_v1"
	claudeProbeProfileName     = "claude_probe_v1"
)

var (
	isolatedProfileID    = mustProcessEnvironmentProfileID(isolatedProfileName)
	gitHardenedProfileID = mustProcessEnvironmentProfileID(gitHardenedProfileName)
	claudeProbeProfileID = mustProcessEnvironmentProfileID(claudeProbeProfileName)
)

// DescriptorAuthority supplies qualified, still-open host objects. The
// process adapter never reopens a caller-provided path by itself.
type DescriptorAuthority interface {
	OpenExecutable(context.Context, string, lifecycle.ExecutableExpectation) (*os.File, error)
	OpenDirectory(context.Context, lifecycle.DirectoryExpectation) (*os.File, error)
}

// Config is copied by New. Environment values and executable digests are
// deliberately redacted by all generic formatting paths.
type Config struct {
	Authority               DescriptorAuthority
	SafeWorkingDirectory    lifecycle.DirectoryExpectation
	DeniedExecutableDigests []domain.ExecutableDigest
}

func (Config) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<darwin-process-config:redacted>")
}

func (Config) MarshalText() ([]byte, error) {
	return []byte("<darwin-process-config:redacted>"), nil
}

func (Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"config": "redacted"})
}

type policy struct {
	profiles        map[string]environmentProfile
	deniedDigests   map[string]struct{}
	deniedBasenames map[string]struct{}
}

type environmentProfile struct {
	values             map[string]string
	allowExecutableSSH bool
}

type environmentProfileDefinition struct {
	id                 lifecycle.ProcessEnvironmentProfileID
	values             []lifecycle.EnvironmentBinding
	allowExecutableSSH bool
}

func newPolicy(config Config) (policy, error) {
	return newPolicyWithProfiles(config, mvpEnvironmentProfiles())
}

func newPolicyWithProfiles(config Config, definitions []environmentProfileDefinition) (policy, error) {
	if nilDescriptorAuthority(config.Authority) || !config.SafeWorkingDirectory.Valid() ||
		len(definitions) == 0 || len(definitions) > maximumEnvironmentProfiles ||
		len(config.DeniedExecutableDigests) == 0 || len(config.DeniedExecutableDigests) > maximumPolicyNames {
		return policy{}, errInvalidProcessPolicy
	}

	result := policy{
		profiles:        make(map[string]environmentProfile, len(definitions)),
		deniedDigests:   make(map[string]struct{}, len(config.DeniedExecutableDigests)),
		deniedBasenames: builtinDeniedBasenames(),
	}
	totalBytes := 0
	for _, definition := range definitions {
		if !definition.id.Valid() || definition.values == nil || len(definition.values) > maximumPolicyNames {
			return policy{}, errInvalidProcessPolicy
		}
		key := definition.id.String()
		if _, duplicate := result.profiles[key]; duplicate {
			return policy{}, errInvalidProcessPolicy
		}
		profile := environmentProfile{
			values:             make(map[string]string, len(definition.values)),
			allowExecutableSSH: definition.allowExecutableSSH,
		}
		for _, binding := range definition.values {
			if !safeEnvironmentName(binding.Name) || hardDeniedEnvironmentName(binding.Name) ||
				!safeEnvironmentValue(binding.Value) {
				return policy{}, errInvalidProcessPolicy
			}
			if _, duplicate := profile.values[binding.Name]; duplicate {
				return policy{}, errInvalidProcessPolicy
			}
			addition := len(binding.Name) + len(binding.Value)
			if totalBytes > maximumPolicyEnvironment-addition {
				return policy{}, errInvalidProcessPolicy
			}
			profile.values[binding.Name] = binding.Value
			totalBytes += addition
		}
		result.profiles[key] = profile
	}
	for _, digest := range config.DeniedExecutableDigests {
		if !digest.Valid() {
			return policy{}, errInvalidProcessPolicy
		}
		key := digest.String()
		if _, duplicate := result.deniedDigests[key]; duplicate {
			return policy{}, errInvalidProcessPolicy
		}
		result.deniedDigests[key] = struct{}{}
	}
	return result, nil
}

func mustProcessEnvironmentProfileID(value string) lifecycle.ProcessEnvironmentProfileID {
	result, err := lifecycle.NewProcessEnvironmentProfileID(value)
	if err != nil {
		panic("invalid built-in process environment profile")
	}
	return result
}

func mvpEnvironmentProfiles() []environmentProfileDefinition {
	return []environmentProfileDefinition{
		{id: isolatedProfileID, values: []lifecycle.EnvironmentBinding{}},
		{
			id: gitHardenedProfileID,
			values: []lifecycle.EnvironmentBinding{
				{Name: "GIT_CONFIG_GLOBAL", Value: "/dev/null"},
				{Name: "GIT_CONFIG_NOSYSTEM", Value: "1"},
				{Name: "GIT_LFS_SKIP_SMUDGE", Value: "1"},
				{Name: "GIT_OPTIONAL_LOCKS", Value: "0"},
				{Name: "GIT_PROTOCOL_FROM_USER", Value: "0"},
				{Name: "GIT_TERMINAL_PROMPT", Value: "0"},
				{Name: "LANG", Value: "C"},
				{Name: "LC_ALL", Value: "C"},
			},
			allowExecutableSSH: true,
		},
		{
			id: claudeProbeProfileID,
			values: []lifecycle.EnvironmentBinding{
				{Name: "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", Value: "1"},
				{Name: "CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL", Value: "1"},
				{Name: "DISABLE_UPDATES", Value: "1"},
			},
		},
	}
}

func nilDescriptorAuthority(authority DescriptorAuthority) bool {
	if authority == nil {
		return true
	}
	value := reflect.ValueOf(authority)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (p policy) ordinaryEnvironment(
	profileID lifecycle.ProcessEnvironmentProfileID,
	request []lifecycle.EnvironmentBinding,
) (map[string]string, error) {
	profile, ok := p.profiles[profileID.String()]
	if !profileID.Valid() || !ok || request == nil || len(request) != len(profile.values) {
		return nil, errProcessPolicyViolation
	}
	values := make(map[string]string, len(request))
	total := 0
	for _, binding := range request {
		expected, present := profile.values[binding.Name]
		if !present || binding.Value != expected || !safeEnvironmentValue(binding.Value) {
			return nil, errProcessPolicyViolation
		}
		if _, duplicate := values[binding.Name]; duplicate {
			return nil, errProcessPolicyViolation
		}
		addition := len(binding.Name) + len(binding.Value)
		if total > maximumPolicyEnvironment-addition {
			return nil, errProcessPolicyViolation
		}
		values[binding.Name] = binding.Value
		total += addition
	}
	return values, nil
}

func (p policy) executableEnvironmentAllowed(profileID lifecycle.ProcessEnvironmentProfileID, name string) bool {
	profile, present := p.profiles[profileID.String()]
	return present && profile.allowExecutableSSH && name == executableSSHEnvironment
}

func (p policy) bindExecutableEnvironment(
	profileID lifecycle.ProcessEnvironmentProfileID,
	values map[string]string,
	name, value string,
) error {
	if !p.executableEnvironmentAllowed(profileID, name) {
		return errProcessPolicyViolation
	}
	if _, duplicate := values[name]; duplicate {
		return errProcessPolicyViolation
	}
	values[name] = value
	if name == executableSSHEnvironment {
		if _, duplicate := values[executableSSHVariant]; duplicate {
			delete(values, name)
			return errProcessPolicyViolation
		}
		values[executableSSHVariant] = executableSSHVariantValue
	}
	return nil
}

func (p policy) deniedExecutable(locator string, digest domain.ExecutableDigest) bool {
	base := locator
	if slash := strings.LastIndexAny(base, `/\\`); slash >= 0 {
		base = base[slash+1:]
	}
	_, deniedName := p.deniedBasenames[strings.ToLower(base)]
	_, deniedDigest := p.deniedDigests[digest.String()]
	return deniedName || deniedDigest
}

func environmentList(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+values[name])
	}
	return result
}

func builtinDeniedBasenames() map[string]struct{} {
	names := []string{
		"sh", "bash", "zsh", "ksh", "csh", "tcsh", "fish", "dash",
		"sudo", "doas", "su", "pkexec", "env", "osascript",
	}
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result
}

func safeEnvironmentName(value string) bool {
	if value == "" || len(value) > maximumEnvironmentName {
		return false
	}
	for index, character := range value {
		if !(character == '_' || character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' || index > 0 && character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func safeEnvironmentValue(value string) bool {
	if len(value) > maximumEnvironmentValue || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func hardDeniedEnvironmentName(value string) bool {
	for _, prefix := range []string{"DYLD_", "LD_", "GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	switch value {
	case "BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "CDPATH", "GLOBIGNORE", "PROMPT_COMMAND", "PS4", "ZDOTDIR",
		"NODE_OPTIONS", "NODE_PATH", "PERL5OPT", "RUBYOPT", "PYTHONINSPECT", "PYTHONSTARTUP",
		"GIT_CONFIG_COUNT", "GIT_EXEC_PATH", "GIT_SSH", "GIT_SSH_VARIANT", "GIT_SSH_COMMAND", "GIT_PROXY_COMMAND", "GIT_ASKPASS", "SSH_ASKPASS":
		return true
	default:
		return false
	}
}
