package git

import (
	"runtime"
	"strings"
)

var portableGitEnvironmentNames = []string{
	"PATH",
	"HOME",
	"XDG_CONFIG_HOME",
	"TMPDIR",
	"TMP",
	"TEMP",
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"ALL_PROXY",
	"NO_PROXY",
	"http_proxy",
	"https_proxy",
	"all_proxy",
	"no_proxy",
	"SSL_CERT_FILE",
	"SSL_CERT_DIR",
	"CURL_CA_BUNDLE",
	"SSH_AUTH_SOCK",
	"SSH_AGENT_PID",
}

var windowsGitEnvironmentNames = []string{
	"PATHEXT",
	"SYSTEMROOT",
	"WINDIR",
	"COMSPEC",
	"USERPROFILE",
	"HOMEDRIVE",
	"HOMEPATH",
	"APPDATA",
	"LOCALAPPDATA",
	"PROGRAMDATA",
}

var fixedGitEnvironment = []string{
	"GIT_ATTR_NOSYSTEM=1",
	"GIT_LFS_SKIP_SMUDGE=1",
	"GIT_OPTIONAL_LOCKS=0",
	"GIT_PROTOCOL_FROM_USER=0",
	"GIT_TERMINAL_PROMPT=0",
	"GCM_INTERACTIVE=never",
	"SSH_ASKPASS_REQUIRE=never",
	"LANG=C",
	"LC_ALL=C",
}

// RestrictedProcessEnvironment returns the complete environment for Git
// operations that must not discover system or user Git configuration.
func RestrictedProcessEnvironment(ambient []string) []string {
	return gitProcessEnvironment(runtime.GOOS, ambient, true)
}

// AuthenticatedProcessEnvironment returns the complete environment for Git
// operations that may use normal system and user configuration to discover
// credential helpers and SSH settings.
func AuthenticatedProcessEnvironment(ambient []string) []string {
	return gitProcessEnvironment(runtime.GOOS, ambient, false)
}

func gitProcessEnvironment(goos string, ambient []string, restricted bool) []string {
	names := allowedGitEnvironmentNames(goos)
	values := make(map[string]string, len(names))
	canonical := make(map[string]string, len(names))
	for _, name := range names {
		canonical[gitEnvironmentKey(goos, name)] = name
	}
	for _, binding := range ambient {
		name, value, ok := strings.Cut(binding, "=")
		if !ok || name == "" || strings.ContainsRune(name, 0) || strings.ContainsRune(value, 0) {
			continue
		}
		key := gitEnvironmentKey(goos, name)
		if canonicalName, allowed := canonical[key]; allowed {
			values[canonicalName] = value
		}
	}

	capacity := len(values) + len(fixedGitEnvironment)
	if restricted {
		capacity += 2
	}
	environment := make([]string, 0, capacity)
	for _, name := range names {
		if value, present := values[name]; present {
			environment = append(environment, name+"="+value)
		}
	}
	if restricted {
		environment = append(environment,
			"GIT_CONFIG_GLOBAL="+gitNullDevice(goos),
			"GIT_CONFIG_NOSYSTEM=1",
		)
	}
	environment = append(environment, fixedGitEnvironment...)
	return environment
}

func allowedGitEnvironmentNames(goos string) []string {
	names := make([]string, 0, len(portableGitEnvironmentNames)+len(windowsGitEnvironmentNames))
	for _, name := range portableGitEnvironmentNames {
		if goos == "windows" && name != strings.ToUpper(name) {
			continue
		}
		names = append(names, name)
	}
	if goos == "windows" {
		names = append(names, windowsGitEnvironmentNames...)
	}
	return names
}

func gitEnvironmentKey(goos, name string) string {
	if goos == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func gitNullDevice(goos string) string {
	if goos == "windows" {
		return "NUL"
	}
	return "/dev/null"
}
