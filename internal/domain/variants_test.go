package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
)

func TestGitTransportIsClosedAndCredentialFree(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		value string
		want  domain.GitTransport
	}{
		{value: "https", want: domain.HTTPSGitTransport()},
		{value: "ssh", want: domain.SSHGitTransport()},
	} {
		got, err := domain.NewGitTransport(test.value)
		if err != nil {
			t.Fatalf("NewGitTransport(%q): %v", test.value, err)
		}
		if got != test.want || !got.Valid() {
			t.Fatalf("NewGitTransport(%q) = %#v", test.value, got)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != `"`+test.value+`"` {
			t.Fatalf("transport JSON = %s", encoded)
		}
	}
	for _, value := range []string{"", "http", "file", "https://github.com/alx4j/ai4j.git", "git@github.com"} {
		if _, err := domain.NewGitTransport(value); err == nil {
			t.Errorf("NewGitTransport(%q) succeeded", value)
		} else if strings.Contains(err.Error(), value) && value != "" {
			t.Errorf("NewGitTransport(%q) disclosed raw input", value)
		}
	}
}

func TestBuiltInVocabulary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value interface{ String() string }
		want  string
	}{
		{name: "built-in source", value: domain.BuiltInDefaultSource(), want: "built_in_default"},
		{name: "explicit source", value: domain.ExplicitSource(), want: "explicit"},
		{name: "object format", value: domain.SHA1ObjectFormat(), want: "sha1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.value.String(); got != test.want {
				t.Fatalf("String() = %q, want %q", got, test.want)
			}
		})
	}
}
