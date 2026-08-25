package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	githubsource "github.com/alx4j/ai4j/internal/source/github"
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
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input, err := githubsource.NewSelectionInput(test.repository, test.repositoryPresent, test.reference, test.referencePresent)
			if err != nil {
				t.Fatal(err)
			}
			got, err := githubsource.Resolve(input)
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
		if _, err := githubsource.NewSelectionInput(test.repository, test.hasRepo, test.reference, test.hasRef); err == nil {
			t.Errorf("NewSelectionInput(%q,%v,%q,%v) succeeded", test.repository, test.hasRepo, test.reference, test.hasRef)
		}
	}
}

func TestInvalidReferenceRetainsItsSafeClassification(t *testing.T) {
	t.Parallel()

	_, err := githubsource.NewSelectionInput("", false, "-upload-pack=secret", true)
	var selectionErr githubsource.SelectionError
	if !errors.As(err, &selectionErr) || selectionErr.Code() != githubsource.ErrorInvalidReference {
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

	input, err := githubsource.NewSelectionInput("https://evil.example/alx4j/ai4j.git", true, "", false)
	if err != nil {
		t.Fatal(err)
	}
	probe := &recordingProbe{}
	if _, err := githubsource.Qualify(context.Background(), input, probe); err == nil {
		t.Fatal("invalid explicit repository succeeded")
	}
	if probe.calls != 0 {
		t.Fatalf("probe calls = %d", probe.calls)
	}
}

func TestCredentialFreeHandoffAndTransportReconstruction(t *testing.T) {
	t.Parallel()

	for _, repository := range []string{"https://github.com/private/repository.git", "git@github.com:private/repository.git"} {
		effective := mustEffective(t, repository, true, "main", true)
		reconstructed, err := githubsource.ReconstructRemote(effective.Repository(), effective.Transport())
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

	typeOf := reflect.TypeOf(githubsource.EffectiveSource{})
	for index := 0; index < typeOf.NumField(); index++ {
		name := strings.ToLower(typeOf.Field(index).Name)
		if strings.Contains(name, "credential") || strings.Contains(name, "password") || strings.Contains(name, "token") || strings.Contains(name, "helper") {
			t.Fatalf("effective source contains credential field %q", name)
		}
	}
}

func TestQualificationPassesAuthenticationThroughWithoutPersistenceOrFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		repository string
		transport  domain.GitTransport
		credential string
	}{
		{name: "public https", repository: "public/repository", transport: domain.HTTPSGitTransport()},
		{name: "private https", repository: "private/repository", transport: domain.HTTPSGitTransport(), credential: "EXTERNAL_HTTPS_CREDENTIAL"},
		{name: "private ssh", repository: "git@github.com:private/repository.git", transport: domain.SSHGitTransport(), credential: "EXTERNAL_SSH_AGENT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input, err := githubsource.NewSelectionInput(test.repository, true, "main", true)
			if err != nil {
				t.Fatal(err)
			}
			probe := &recordingProbe{externalCredential: test.credential}
			got, err := githubsource.Qualify(context.Background(), input, probe)
			if err != nil {
				t.Fatal(err)
			}
			if probe.calls != 1 || probe.request.Transport() != test.transport || got.Repository() != probe.request.Repository() {
				t.Fatalf("probe = %d %s", probe.calls, probe.request.Transport().String())
			}
			if test.credential != "" && (strings.Contains(got.Repository().String(), test.credential) || strings.Contains(got.Remote().Endpoint(), test.credential)) {
				t.Fatal("external credential entered effective source")
			}
		})
	}
}

func TestQualificationMapsAccessErrorsWithoutRawOutputAndPreservesCancellation(t *testing.T) {
	t.Parallel()

	input, err := githubsource.NewSelectionInput("private/repository", true, "", false)
	if err != nil {
		t.Fatal(err)
	}
	secret := "AUTH_SECRET_CANARY"
	probe := &recordingProbe{err: errors.New(secret)}
	if _, err := githubsource.Qualify(context.Background(), input, probe); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe access error: %v", err)
	} else {
		var selectionErr githubsource.SelectionError
		if !errors.As(err, &selectionErr) || selectionErr.Code() != githubsource.ErrorAccessFailed {
			t.Fatalf("access error = %T %v", err, err)
		}
	}

	probe.err = context.Canceled
	if _, err := githubsource.Qualify(context.Background(), input, probe); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	probe.err = context.DeadlineExceeded
	if _, err := githubsource.Qualify(context.Background(), input, probe); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline = %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	probe.calls = 0
	probe.err = nil
	if _, err := githubsource.Qualify(cancelled, input, probe); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled context = %v", err)
	}
	if probe.calls != 0 {
		t.Fatalf("pre-cancelled probe calls = %d", probe.calls)
	}
}

func mustEffective(t *testing.T, repository string, hasRepository bool, reference string, hasReference bool) githubsource.EffectiveSource {
	t.Helper()
	input, err := githubsource.NewSelectionInput(repository, hasRepository, reference, hasReference)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := githubsource.Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	return effective
}

type recordingProbe struct {
	calls              int
	request            githubsource.EffectiveSource
	externalCredential string
	err                error
}

func (p *recordingProbe) Probe(_ context.Context, request githubsource.EffectiveSource) error {
	p.calls++
	p.request = request
	_ = p.externalCredential // Fixture-owned Git/SSH state; deliberately not copied into request.
	return p.err
}
