package domain_test

import (
	"encoding/json"
	"reflect"
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

func TestMVPVocabulary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value interface{ String() string }
		want  string
	}{
		{name: "target", value: domain.ClaudeTarget(), want: "claude"},
		{name: "host", value: domain.DarwinHost(), want: "darwin"},
		{name: "scope", value: domain.UserScope(), want: "user"},
		{name: "source mode", value: domain.GitHubSourceMode(), want: "github"},
		{name: "built-in source", value: domain.BuiltInDefaultSource(), want: "built_in_default"},
		{name: "explicit source", value: domain.ExplicitSource(), want: "explicit"},
		{name: "selection", value: domain.WholeToolkitSelection(), want: "whole_toolkit"},
		{name: "recovery", value: domain.ShortLivedRecovery(), want: "short_lived"},
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
	if got := domain.MVPStateSchema().Uint16(); got != 1 {
		t.Fatalf("MVP state schema = %d, want 1", got)
	}
}

func TestVariantConstructorsPreserveFutureValuesAndRejectInvalidWireValues(t *testing.T) {
	t.Parallel()

	future, err := domain.NewTarget("test_target")
	if err != nil {
		t.Fatalf("NewTarget() error = %v", err)
	}
	if future == domain.ClaudeTarget() {
		t.Fatal("future target was mapped to the MVP target")
	}

	invalid := []string{"", "Claude", "codex-target", "../claude", "claude/value"}
	for _, value := range invalid {
		if _, err := domain.NewTarget(value); err == nil {
			t.Errorf("NewTarget(%q) succeeded", value)
		}
	}
	if _, err := domain.NewStateSchemaVersion(0); err == nil {
		t.Error("NewStateSchemaVersion(0) succeeded")
	}
	unknown, err := domain.NewStateSchemaVersion(2)
	if err != nil || unknown == domain.MVPStateSchema() {
		t.Fatalf("unknown schema = %#v, error = %v", unknown, err)
	}
}

func TestCapabilitySetIsDefensiveAndDeterministic(t *testing.T) {
	t.Parallel()

	set := domain.MVPCapabilities()
	values := set.Values()
	if len(values) != 7 {
		t.Fatalf("capability count = %d, want 7", len(values))
	}
	values[0] = domain.UpdateCapability()
	if got := set.Values(); len(got) != 7 || !set.Contains(domain.NativeValidationCapability()) {
		t.Fatalf("set changed through returned slice: %v", got)
	}
	for index := 1; index < len(set.Values()); index++ {
		ordered := set.Values()
		if ordered[index-1].String() >= ordered[index].String() {
			t.Fatalf("capabilities are not sorted: %v", ordered)
		}
	}
	if reflect.TypeOf(domain.ClaudeTarget()) == reflect.TypeOf(domain.DarwinHost()) {
		t.Fatal("target and host types must be distinct")
	}
}
