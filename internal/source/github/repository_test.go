package github_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	githubsource "github.com/alx4j/ai4j/internal/source/github"
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
		{name: "ssh", input: "git@github.com:alx4j/ai4j.git", identity: "github.com/alx4j/ai4j", transport: domain.SSHGitTransport()},
		{name: "component boundaries", input: longOwner + "/" + longRepository, identity: "github.com/" + longOwner + "/" + longRepository, transport: domain.HTTPSGitTransport()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := githubsource.ParseRepository(test.input)
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
		"https://GitHub.com/alx4j/ai4j.git",
		"https://github.com/alx4j/ai4j.git/",
		"https://github.com/alx4j/ai4j.git?token=" + secret,
		"https://github.com/alx4j/ai4j.git#fragment",
		"https://user:" + secret + "@github.com/alx4j/ai4j.git",
		"git@github.com:alx4j/ai4j",
		"git@evil.example:alx4j/ai4j.git",
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
		if _, err := githubsource.ParseRepository(input); err == nil {
			t.Errorf("ParseRepository(%q) succeeded", input)
		} else {
			var selectionErr githubsource.SelectionError
			if !errors.As(err, &selectionErr) || selectionErr.Code() != githubsource.ErrorInvalidRepository {
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
		parsed, err := githubsource.ParseRepository(input)
		if err != nil {
			if len(err.Error()) > 128 {
				t.Fatalf("unbounded error: %d", len(err.Error()))
			}
			return
		}
		if !parsed.Identity().Valid() || !parsed.Transport().Valid() || strings.HasSuffix(parsed.Identity().String(), ".git") || parsed.Identity().String() != strings.ToLower(parsed.Identity().String()) {
			t.Fatalf("noncanonical parse result: %q", parsed.Identity().String())
		}
	})
}
