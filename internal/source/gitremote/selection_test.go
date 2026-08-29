package gitremote_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	gitremote "github.com/alx4j/ai4j/internal/source/gitremote"
)

func TestResolveSelectionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		repository        string
		repositoryPresent bool
		reference         string
		referencePresent  bool
		selection         domain.SourceSelection
		identity          string
		transport         domain.GitTransport
		endpoint          string
	}{
		{name: "omitted", selection: domain.BuiltInDefaultSource(), identity: "github.com/alx4j/ai4j", transport: domain.HTTPSGitTransport(), endpoint: "https://github.com/alx4j/ai4j.git"},
		{name: "reference only", reference: "main", referencePresent: true, selection: domain.BuiltInDefaultSource(), identity: "github.com/alx4j/ai4j", transport: domain.HTTPSGitTransport(), endpoint: "https://github.com/alx4j/ai4j.git"},
		{name: "explicit shorthand", repository: "alx4j/ai4j", repositoryPresent: true, selection: domain.ExplicitSource(), identity: "github.com/alx4j/ai4j", transport: domain.HTTPSGitTransport(), endpoint: "https://github.com/alx4j/ai4j.git"},
		{name: "explicit https", repository: "https://github.com/alx4j/ai4j.git", repositoryPresent: true, selection: domain.ExplicitSource(), identity: "github.com/alx4j/ai4j", transport: domain.HTTPSGitTransport(), endpoint: "https://github.com/alx4j/ai4j.git"},
		{name: "explicit ssh", repository: "git@github.com:alx4j/ai4j.git", repositoryPresent: true, reference: "refs/tags/v1.0.0", referencePresent: true, selection: domain.ExplicitSource(), identity: "github.com/alx4j/ai4j", transport: domain.SSHGitTransport(), endpoint: "git@github.com:alx4j/ai4j.git"},
		{name: "third party", repository: "example/toolkit", repositoryPresent: true, selection: domain.ExplicitSource(), identity: "github.com/example/toolkit", transport: domain.HTTPSGitTransport(), endpoint: "https://github.com/example/toolkit.git"},
		{name: "enterprise https", repository: "https://git.everpure.example/platform/toolkits/everpure.git", repositoryPresent: true, selection: domain.ExplicitSource(), identity: "git.everpure.example/platform/toolkits/everpure", transport: domain.HTTPSGitTransport(), endpoint: "https://git.everpure.example/platform/toolkits/everpure.git"},
		{name: "enterprise ssh", repository: "git@gitlab.barclays.example:division/team/Barclays.git", repositoryPresent: true, selection: domain.ExplicitSource(), identity: "gitlab.barclays.example/division/team/Barclays", transport: domain.SSHGitTransport(), endpoint: "git@gitlab.barclays.example:division/team/Barclays.git"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input, err := gitremote.NewSelectionInput(test.repository, test.repositoryPresent, test.reference, test.referencePresent)
			if err != nil {
				t.Fatal(err)
			}
			got, err := gitremote.Resolve(input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Selection() != test.selection || got.Repository().String() != test.identity || got.Transport() != test.transport || got.Remote().Endpoint() != test.endpoint {
				t.Fatalf("Resolve() = %s %s %s %s", got.Selection().String(), got.Repository().String(), got.Transport().String(), got.Remote().Endpoint())
			}
			ref, hasRef := got.RequestedReference()
			if ref != test.reference || hasRef != test.referencePresent {
				t.Fatalf("reference = %q/%v", ref, hasRef)
			}
		})
	}
}

func TestSelectionPresenceAndReferenceSafety(t *testing.T) {
	t.Parallel()

	invalid := []struct {
		repository string
		hasRepo    bool
		reference  string
		hasRef     bool
	}{
		{repository: "", hasRepo: true},
		{repository: "alx4j/ai4j", hasRepo: false},
		{reference: "", hasRef: true},
		{reference: "main", hasRef: false},
		{reference: "-main", hasRef: true},
		{reference: " main", hasRef: true},
		{reference: "main\x00next", hasRef: true},
		{reference: "main\u009bnext", hasRef: true},
		{reference: string([]byte{0xff}), hasRef: true},
		{reference: strings.Repeat("r", 1025), hasRef: true},
	}
	for _, test := range invalid {
		if _, err := gitremote.NewSelectionInput(test.repository, test.hasRepo, test.reference, test.hasRef); err == nil {
			t.Errorf("NewSelectionInput(%q,%v,%q,%v) succeeded", test.repository, test.hasRepo, test.reference, test.hasRef)
		}
	}
}

func TestInvalidReferenceRetainsItsSafeClassification(t *testing.T) {
	t.Parallel()

	_, err := gitremote.NewSelectionInput("", false, "-upload-pack=secret", true)
	var selectionErr gitremote.SelectionError
	if !errors.As(err, &selectionErr) || selectionErr.Code() != gitremote.ErrorInvalidReference {
		t.Fatalf("invalid reference error = %T %v", err, err)
	}
}

func TestDefaultAndExplicitFirstPartyDifferOnlyBySelection(t *testing.T) {
	t.Parallel()

	omitted := mustEffective(t, "", false, "main", true)
	explicit := mustEffective(t, "alx4j/ai4j", true, "main", true)
	if omitted.Selection() != domain.BuiltInDefaultSource() || explicit.Selection() != domain.ExplicitSource() {
		t.Fatal("source selection distinction was lost")
	}
	if omitted.Repository() != explicit.Repository() || omitted.Transport() != explicit.Transport() || omitted.Remote().Endpoint() != explicit.Remote().Endpoint() {
		t.Fatalf("effective source differs beyond selection: %#v %#v", omitted, explicit)
	}
	omittedRef, omittedHasRef := omitted.RequestedReference()
	explicitRef, explicitHasRef := explicit.RequestedReference()
	if omittedRef != explicitRef || omittedHasRef != explicitHasRef {
		t.Fatal("requested reference differs")
	}
}

func TestBadExplicitRepositoryNeverFallsBack(t *testing.T) {
	t.Parallel()

	input, err := gitremote.NewSelectionInput("https://user:secret@evil.example/alx4j/ai4j.git", true, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitremote.Resolve(input); err == nil {
		t.Fatal("invalid explicit repository succeeded")
	}
}

func TestCredentialFreeHandoffAndTransportReconstruction(t *testing.T) {
	t.Parallel()

	for _, repository := range []string{"https://github.com/private/repository.git", "git@github.com:private/repository.git"} {
		effective := mustEffective(t, repository, true, "main", true)
		reconstructed, err := gitremote.ReconstructRemote(effective.Repository(), effective.Transport())
		if err != nil {
			t.Fatal(err)
		}
		if reconstructed.Endpoint() != effective.Remote().Endpoint() {
			t.Fatalf("reconstructed endpoint = %q", reconstructed.Endpoint())
		}
		persisted := struct {
			Repository string              `json:"repository"`
			Transport  domain.GitTransport `json:"transport"`
		}{Repository: effective.Repository().String(), Transport: effective.Transport()}
		encoded, err := json.Marshal(persisted)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		if strings.Contains(text, "https://") || strings.Contains(text, "git@") || strings.Contains(text, "credential") || strings.Contains(text, "token") {
			t.Fatalf("persisted source contains transport endpoint or credential shape: %s", text)
		}
		if text != `{"repository":"github.com/private/repository","transport":"`+effective.Transport().String()+`"}` {
			t.Fatalf("persisted source = %s", text)
		}
	}

	typeOf := reflect.TypeFor[gitremote.EffectiveSource]()
	for field := range typeOf.Fields() {
		name := strings.ToLower(field.Name)
		if strings.Contains(name, "credential") || strings.Contains(name, "password") || strings.Contains(name, "token") || strings.Contains(name, "helper") {
			t.Fatalf("effective source contains credential field %q", name)
		}
	}
}

func mustEffective(t *testing.T, repository string, hasRepository bool, reference string, hasReference bool) gitremote.EffectiveSource {
	t.Helper()
	input, err := gitremote.NewSelectionInput(repository, hasRepository, reference, hasReference)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := gitremote.Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	return effective
}
