package gitremote_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	gitremote "github.com/alx4j/ai4j/internal/source/gitremote"
)

func TestParseRepositoryAcceptedCanonicalForms(t *testing.T) {
	t.Parallel()

	longOwner := strings.Repeat("a", 39)
	longRepository := strings.Repeat("b", 100)
	tests := []struct {
		name      string
		input     string
		identity  string
		transport domain.GitTransport
	}{
		{name: "shorthand", input: "alx4j/ai4j", identity: "github.com/alx4j/ai4j", transport: domain.HTTPSGitTransport()},
		{name: "mixed case components", input: "AlX4J/Ai4J", identity: "github.com/alx4j/ai4j", transport: domain.HTTPSGitTransport()},
		{name: "https", input: "https://github.com/alx4j/ai4j.git", identity: "github.com/alx4j/ai4j", transport: domain.HTTPSGitTransport()},
		{name: "mixed case GitHub https path", input: "https://GitHub.com/AlX4J/Ai4J.git", identity: "github.com/alx4j/ai4j", transport: domain.HTTPSGitTransport()},
		{name: "ssh", input: "git@github.com:alx4j/ai4j.git", identity: "github.com/alx4j/ai4j", transport: domain.SSHGitTransport()},
		{name: "mixed case GitHub ssh path", input: "git@github.com:AlX4J/Ai4J.git", identity: "github.com/alx4j/ai4j", transport: domain.SSHGitTransport()},
		{name: "enterprise https", input: "https://Git.Everpure.Example/platform/toolkits/everpure.git", identity: "git.everpure.example/platform/toolkits/everpure", transport: domain.HTTPSGitTransport()},
		{name: "enterprise ssh nested namespace", input: "git@gitlab.barclays.example:division/team/Barclays.git", identity: "gitlab.barclays.example/division/team/Barclays", transport: domain.SSHGitTransport()},
		{name: "intranet https", input: "https://gitlab/division/toolkit.git", identity: "gitlab/division/toolkit", transport: domain.HTTPSGitTransport()},
		{name: "intranet ssh", input: "git@gitlab:division/toolkit.git", identity: "gitlab/division/toolkit", transport: domain.SSHGitTransport()},
		{name: "component boundaries", input: longOwner + "/" + longRepository, identity: "github.com/" + longOwner + "/" + longRepository, transport: domain.HTTPSGitTransport()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := gitremote.ParseRepository(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Identity().String() != test.identity || got.Transport() != test.transport {
				t.Fatalf("ParseRepository() = %s/%s", got.Identity().String(), got.Transport().String())
			}
		})
	}
}

func TestParseRepositoryRejectsEveryUnsupportedFamilyWithoutDisclosure(t *testing.T) {
	t.Parallel()

	secret := "TOKEN_SUPER_SECRET"
	invalidUTF8 := string([]byte{'a', '/', 0xff})
	tests := []string{
		"",
		"-alx4j/ai4j",
		" alx4j/ai4j",
		"alx4j/ai4j ",
		"alx4j/ai\x00j",
		"alx4j/ai\u009bj",
		invalidUTF8,
		"alx4j/ai4j.git",
		"github.com/alx4j/ai4j",
		"https://github.com/alx4j/ai4j",
		"HTTPS://github.com/alx4j/ai4j.git",
		"https://github.com/alx4j/ai4j.git/",
		"https://github.com/alx4j/ai4j.git?token=" + secret,
		"https://github.com/alx4j/ai4j.git?",
		"https://github.com/alx4j/ai4j.git#fragment",
		"https://github.com/alx4j/ai4j.git#",
		"https://github.com:/alx4j/ai4j.git",
		"https://127.0.0.1/alx4j/ai4j.git",
		"https://user:" + secret + "@github.com/alx4j/ai4j.git",
		"git@github.com:alx4j/ai4j",
		"git@127.0.0.1:alx4j/ai4j.git",
		"git@evil.example:../alx4j/ai4j.git",
		"git@evil.example:/alx4j/ai4j.git",
		"ssh://git@github.com/alx4j/ai4j.git",
		"http://github.com/alx4j/ai4j.git",
		"file:///tmp/ai4j",
		"ext::helper " + secret,
		"../alx4j/ai4j",
		`C:\repo\ai4j`,
		"alx4j",
		"alx4j/ai4j/extra",
		"/ai4j",
		"alx4j/",
		strings.Repeat("a", 40) + "/ai4j",
		"owner-/ai4j",
		"own--er/ai4j",
		"alx4j/" + strings.Repeat("b", 101),
		"alx4j/.hidden",
		strings.Repeat("x", 257),
	}
	for _, input := range tests {
		if _, err := gitremote.ParseRepository(input); err == nil {
			t.Errorf("ParseRepository(%q) succeeded", input)
		} else {
			var selectionErr gitremote.SelectionError
			if !errors.As(err, &selectionErr) || selectionErr.Code() != gitremote.ErrorInvalidRepository {
				t.Errorf("ParseRepository(%q) error = %T %v", input, err, err)
			}
			if strings.Contains(err.Error(), secret) || len(err.Error()) > 128 {
				t.Errorf("unsafe error %q", err)
			}
		}
	}
}

func FuzzParseRepositoryIsBounded(f *testing.F) {
	for _, seed := range []string{
		"alx4j/ai4j",
		"https://github.com/alx4j/ai4j.git",
		"git@github.com:alx4j/ai4j.git",
		"--upload-pack=evil",
		string([]byte{0xff, 0xfe}),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		parsed, err := gitremote.ParseRepository(input)
		if err != nil {
			if len(err.Error()) > 128 {
				t.Fatalf("unbounded error: %d", len(err.Error()))
			}
			return
		}
		identity := parsed.Identity().String()
		host, _, _ := strings.Cut(identity, "/")
		if !parsed.Identity().Valid() || !parsed.Transport().Valid() || strings.HasSuffix(identity, ".git") || host != strings.ToLower(host) {
			t.Fatalf("noncanonical parse result: %q", parsed.Identity().String())
		}
	})
}
