package git

import (
	"slices"
	"strings"
	"testing"
)

func TestGitProcessEnvironmentIsExact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		goos       string
		restricted bool
		ambient    []string
		want       []string
	}{
		{
			name:       "restricted Unix",
			goos:       "linux",
			restricted: true,
			ambient: []string{
				"PATH=/usr/local/bin:/usr/bin",
				"HOME=/home/initial",
				"XDG_CONFIG_HOME=/home/alice/.config",
				"TMPDIR=/tmp/ai4j",
				"HTTP_PROXY=http://proxy.example",
				"https_proxy=https://proxy.example/a=b",
				"SSL_CERT_FILE=/etc/ssl/custom.pem",
				"SSH_AUTH_SOCK=/run/user/1000/agent.sock",
				"GIT_CONFIG_COUNT=1",
				"GIT_CONFIG_KEY_0=core.sshCommand",
				"GIT_CONFIG_VALUE_0=malicious-ssh",
				"GIT_DIR=/tmp/foreign.git",
				"GIT_SSH_COMMAND=malicious-ssh",
				"GIT_CONFIG_GLOBAL=/tmp/foreign.gitconfig",
				"GIT_TERMINAL_PROMPT=1",
				"GCM_INTERACTIVE=always",
				"SSH_ASKPASS_REQUIRE=force",
				"LANG=fr_FR.UTF-8",
				"HOME=/home/alice",
				"PATH=/safe/bin",
				"PATH=/ignored\x00suffix",
				"malformed",
				"=empty-name",
			},
			want: []string{
				"PATH=/safe/bin",
				"HOME=/home/alice",
				"XDG_CONFIG_HOME=/home/alice/.config",
				"TMPDIR=/tmp/ai4j",
				"HTTP_PROXY=http://proxy.example",
				"https_proxy=https://proxy.example/a=b",
				"SSL_CERT_FILE=/etc/ssl/custom.pem",
				"SSH_AUTH_SOCK=/run/user/1000/agent.sock",
				"GIT_CONFIG_GLOBAL=/dev/null",
				"GIT_CONFIG_NOSYSTEM=1",
				"GIT_ATTR_NOSYSTEM=1",
				"GIT_LFS_SKIP_SMUDGE=1",
				"GIT_OPTIONAL_LOCKS=0",
				"GIT_PROTOCOL_FROM_USER=0",
				"GIT_TERMINAL_PROMPT=0",
				"GCM_INTERACTIVE=never",
				"SSH_ASKPASS_REQUIRE=never",
				"LANG=C",
				"LC_ALL=C",
			},
		},
		{
			name:       "authenticated Unix",
			goos:       "darwin",
			restricted: false,
			ambient: []string{
				"PATH=/opt/homebrew/bin:/usr/bin",
				"HOME=/Users/alice",
				"TMPDIR=/var/folders/session",
				"HTTPS_PROXY=https://proxy.example",
				"http_proxy=http://lower-proxy.example",
				"CURL_CA_BUNDLE=/Users/alice/corporate-ca.pem",
				"SSH_AGENT_PID=42",
				"GIT_CONFIG_COUNT=2",
				"GIT_CONFIG_KEY_0=credential.helper",
				"GIT_DIR=/tmp/foreign.git",
				"GIT_SSH_COMMAND=malicious-ssh",
				"GIT_CONFIG_NOSYSTEM=1",
			},
			want: []string{
				"PATH=/opt/homebrew/bin:/usr/bin",
				"HOME=/Users/alice",
				"TMPDIR=/var/folders/session",
				"HTTPS_PROXY=https://proxy.example",
				"http_proxy=http://lower-proxy.example",
				"CURL_CA_BUNDLE=/Users/alice/corporate-ca.pem",
				"SSH_AGENT_PID=42",
				"GIT_ATTR_NOSYSTEM=1",
				"GIT_LFS_SKIP_SMUDGE=1",
				"GIT_OPTIONAL_LOCKS=0",
				"GIT_PROTOCOL_FROM_USER=0",
				"GIT_TERMINAL_PROMPT=0",
				"GCM_INTERACTIVE=never",
				"SSH_ASKPASS_REQUIRE=never",
				"LANG=C",
				"LC_ALL=C",
			},
		},
		{
			name:       "restricted Windows",
			goos:       "windows",
			restricted: true,
			ambient: []string{
				"Path=C:\\initial",
				"path=C:\\Git\\cmd;C:\\Windows\\System32",
				"temp=C:\\Temp",
				"https_proxy=https://proxy.example",
				"ssl_cert_dir=C:\\Certificates",
				"ssh_auth_sock=\\\\.\\pipe\\openssh-ssh-agent",
				"SystemRoot=C:\\Windows",
				"ComSpec=C:\\Windows\\System32\\cmd.exe",
				"UserProfile=C:\\Users\\alice",
				"AppData=C:\\Users\\alice\\AppData\\Roaming",
				"Git_Config_Count=1",
				"git_config_key_0=core.sshCommand",
				"git_dir=C:\\foreign.git",
				"git_ssh_command=malicious-ssh",
				"git_terminal_prompt=1",
			},
			want: []string{
				"PATH=C:\\Git\\cmd;C:\\Windows\\System32",
				"TEMP=C:\\Temp",
				"HTTPS_PROXY=https://proxy.example",
				"SSL_CERT_DIR=C:\\Certificates",
				"SSH_AUTH_SOCK=\\\\.\\pipe\\openssh-ssh-agent",
				"SYSTEMROOT=C:\\Windows",
				"COMSPEC=C:\\Windows\\System32\\cmd.exe",
				"USERPROFILE=C:\\Users\\alice",
				"APPDATA=C:\\Users\\alice\\AppData\\Roaming",
				"GIT_CONFIG_GLOBAL=NUL",
				"GIT_CONFIG_NOSYSTEM=1",
				"GIT_ATTR_NOSYSTEM=1",
				"GIT_LFS_SKIP_SMUDGE=1",
				"GIT_OPTIONAL_LOCKS=0",
				"GIT_PROTOCOL_FROM_USER=0",
				"GIT_TERMINAL_PROMPT=0",
				"GCM_INTERACTIVE=never",
				"SSH_ASKPASS_REQUIRE=never",
				"LANG=C",
				"LC_ALL=C",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			before := slices.Clone(test.ambient)
			got := gitProcessEnvironment(test.goos, test.ambient, test.restricted)

			if !slices.Equal(got, test.want) {
				t.Fatalf("environment = %#v, want %#v", got, test.want)
			}
			if !slices.Equal(test.ambient, before) {
				t.Fatalf("ambient environment mutated: %#v, want %#v", test.ambient, before)
			}
			assertEnvironmentExcludes(t, test.goos, got,
				"GIT_CONFIG_COUNT",
				"GIT_CONFIG_KEY_0",
				"GIT_DIR",
				"GIT_SSH_COMMAND",
			)
		})
	}
}

func TestAuthenticatedEnvironmentRetainsConfigDiscovery(t *testing.T) {
	t.Parallel()

	ambient := []string{
		"HOME=/home/alice",
		"XDG_CONFIG_HOME=/home/alice/.config",
		"GIT_CONFIG_GLOBAL=/tmp/foreign.gitconfig",
		"GIT_CONFIG_NOSYSTEM=1",
	}

	got := gitProcessEnvironment("linux", ambient, false)

	if !slices.Contains(got, "HOME=/home/alice") || !slices.Contains(got, "XDG_CONFIG_HOME=/home/alice/.config") {
		t.Fatalf("user config discovery inputs absent: %#v", got)
	}
	assertEnvironmentExcludes(t, "linux", got, "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM")
}

func assertEnvironmentExcludes(t *testing.T, goos string, environment []string, names ...string) {
	t.Helper()

	for _, binding := range environment {
		name, _, ok := strings.Cut(binding, "=")
		if !ok {
			t.Fatalf("malformed environment binding %q", binding)
		}
		for _, excluded := range names {
			matches := name == excluded
			if goos == "windows" {
				matches = strings.EqualFold(name, excluded)
			}
			if matches {
				t.Fatalf("environment contains excluded variable %q: %#v", excluded, environment)
			}
		}
	}
}
