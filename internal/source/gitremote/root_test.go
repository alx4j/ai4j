package gitremote_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	gitremote "github.com/alx4j/ai4j/internal/source/gitremote"
)

func TestParseRootDerivesCanonicalRepositories(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		root       string
		transport  domain.GitTransport
		repository string
		identity   string
	}{
		{name: "GitHub HTTPS", input: "https://GitHub.com/Oleksii", root: "https://github.com/oleksii", transport: domain.HTTPSGitTransport(), repository: "common", identity: "github.com/oleksii/common"},
		{name: "enterprise HTTPS nested namespace", input: "https://Git.Everpure.Example/platform/AI", root: "https://git.everpure.example/platform/AI", transport: domain.HTTPSGitTransport(), repository: "everpure", identity: "git.everpure.example/platform/AI/everpure"},
		{name: "enterprise SSH nested namespace", input: "git@GitLab.Barclays.Example:division/team", root: "git@gitlab.barclays.example:division/team", transport: domain.SSHGitTransport(), repository: "barclays", identity: "gitlab.barclays.example/division/team/barclays"},
		{name: "intranet HTTPS", input: "https://gitlab/toolkits", root: "https://gitlab/toolkits", transport: domain.HTTPSGitTransport(), repository: "team-tools", identity: "gitlab/toolkits/team-tools"},
		{name: "intranet SSH", input: "git@gitlab:toolkits", root: "git@gitlab:toolkits", transport: domain.SSHGitTransport(), repository: "team-tools", identity: "gitlab/toolkits/team-tools"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root, err := gitremote.ParseRoot(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if root.String() != test.root || root.Transport() != test.transport {
				t.Fatalf("ParseRoot() = %q/%q, want %q/%q", root.String(), root.Transport(), test.root, test.transport)
			}
			remote, err := root.Repository(test.repository)
			if err != nil {
				t.Fatal(err)
			}
			wantEndpoint := test.root + "/" + test.repository + ".git"
			if remote.Endpoint() != wantEndpoint || remote.Identity().String() != test.identity || remote.Transport() != test.transport {
				t.Fatalf("Repository() = %q/%q/%q, want %q/%q/%q", remote.Endpoint(), remote.Identity(), remote.Transport(), wantEndpoint, test.identity, test.transport)
			}
		})
	}
}

func TestParseRootRejectsUnsafeAndRepositoryInputsWithoutDisclosure(t *testing.T) {
	t.Parallel()

	secret := "TOKEN_SUPER_SECRET"
	invalidUTF8 := string([]byte{'h', 't', 't', 'p', 's', ':', '/', '/', 'g', 'i', 't', '/', 0xff})
	tests := []string{
		"",
		"-https://github.com/oleksii",
		" https://github.com/oleksii",
		"https://github.com/oleksii ",
		"https://github.com/ole\x00ksii",
		"https://github.com/ole\u009bksii",
		invalidUTF8,
		"github.com/oleksii",
		"HTTPS://github.com/oleksii",
		"http://github.com/oleksii",
		"https://github.com",
		"https://github.com/",
		"https://github.com/oleksii/",
		"https://github.com/oleksii.git",
		"https://github.com/oleksii?token=" + secret,
		"https://github.com/oleksii?",
		"https://github.com/oleksii#fragment",
		"https://github.com/oleksii#",
		"https://github.com:/oleksii",
		"https://github.com:443/oleksii",
		"https://127.0.0.1/oleksii",
		"https://user:" + secret + "@github.com/oleksii",
		"https://github.com/oleksii/%2e%2e",
		"git@github.com:",
		"git@github.com:oleksii/",
		"git@github.com:oleksii.git",
		"git@127.0.0.1:oleksii",
		"git@evil.example:../oleksii",
		"git@evil.example:/oleksii",
		"root@github.com:oleksii",
		"ssh://git@github.com/oleksii",
		"file:///tmp/toolkits",
		"ext::helper " + secret,
		strings.Repeat("a", 769),
	}
	for _, input := range tests {
		if _, err := gitremote.ParseRoot(input); err == nil {
			t.Errorf("ParseRoot(%q) succeeded", input)
		} else {
			var selectionErr gitremote.SelectionError
			if !errors.As(err, &selectionErr) || selectionErr.Code() != gitremote.ErrorInvalidRoot {
				t.Errorf("ParseRoot(%q) error = %T %v", input, err, err)
			}
			if strings.Contains(err.Error(), secret) || len(err.Error()) > 128 {
				t.Errorf("unsafe error %q", err)
			}
		}
	}
}

func TestRootRepositoryRejectsUnsafeNamesAndDoesNotMutateRoot(t *testing.T) {
	t.Parallel()

	root, err := gitremote.ParseRoot("git@github.com:oleksii")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "a", "Everpure", "-everpure", "everpure-", "ever_pure", "ever.pure", "ever/pure", "../everpure", "ever\x00pure", strings.Repeat("a", 64)} {
		if _, err := root.Repository(name); err == nil {
			t.Errorf("Repository(%q) succeeded", name)
		}
	}

	common, err := root.Repository("common")
	if err != nil {
		t.Fatal(err)
	}
	everpure, err := root.Repository("everpure")
	if err != nil {
		t.Fatal(err)
	}
	if root.String() != "git@github.com:oleksii" || common.Endpoint() != "git@github.com:oleksii/common.git" || everpure.Endpoint() != "git@github.com:oleksii/everpure.git" {
		t.Fatalf("root or derived remotes changed: %q %q %q", root.String(), common.Endpoint(), everpure.Endpoint())
	}
}

func FuzzParseRootDerivesOnlyCanonicalRepositories(f *testing.F) {
	for _, seed := range []string{
		"https://github.com/oleksii",
		"git@github.com:oleksii",
		"https://git.example/division/team",
		"--upload-pack=evil",
		string([]byte{0xff, 0xfe}),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		root, err := gitremote.ParseRoot(input)
		if err != nil {
			if len(err.Error()) > 128 {
				t.Fatalf("unbounded error: %d", len(err.Error()))
			}
			return
		}
		remote, err := root.Repository("bundle")
		if err != nil {
			t.Fatalf("accepted root cannot derive a repository: %v", err)
		}
		parsed, err := gitremote.ParseRepository(remote.Endpoint())
		if err != nil || parsed.Identity() != remote.Identity() || parsed.Transport() != root.Transport() {
			t.Fatalf("noncanonical derived repository: %q, %v", remote.Endpoint(), err)
		}
	})
}
