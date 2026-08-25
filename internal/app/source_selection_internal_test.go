package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/human"
	"github.com/alx4j/ai4j/internal/cli/jsonout"
	"github.com/alx4j/ai4j/internal/domain"
	githubsource "github.com/alx4j/ai4j/internal/source/github"
)

func TestSourceSelectorJoinsCLIOptionPresenceAndCanonicalHandoff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		argv       []string
		selection  domain.SourceSelection
		transport  domain.GitTransport
		repository string
		reference  string
		hasRef     bool
	}{
		{name: "omitted", argv: []string{"ai4j", "validate"}, selection: domain.BuiltInDefaultSource(), transport: domain.HTTPSGitTransport(), repository: "github.com/alx4j/ai4j"},
		{name: "reference only", argv: []string{"ai4j", "plan", "install", "--ref", "main"}, selection: domain.BuiltInDefaultSource(), transport: domain.HTTPSGitTransport(), repository: "github.com/alx4j/ai4j", reference: "main", hasRef: true},
		{name: "explicit https", argv: []string{"ai4j", "install", "--repo", "https://github.com/Example/Toolkit.git"}, selection: domain.ExplicitSource(), transport: domain.HTTPSGitTransport(), repository: "github.com/example/toolkit"},
		{name: "explicit ssh", argv: []string{"ai4j", "validate", "--repo", "git@github.com:Example/Toolkit.git", "--ref", "v1"}, selection: domain.ExplicitSource(), transport: domain.SSHGitTransport(), repository: "github.com/example/toolkit", reference: "v1", hasRef: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request, err := cli.NewParser("darwin").Parse(test.argv)
			if err != nil {
				t.Fatal(err)
			}
			probe := &appSourceProbe{}
			selector, err := NewSourceSelector(probe)
			if err != nil {
				t.Fatal(err)
			}
			got, err := selector.Select(context.Background(), sourceOptions(request))
			if err != nil {
				t.Fatal(err)
			}
			if probe.calls != 1 || got.Selection() != test.selection || got.Transport() != test.transport || got.Repository().String() != test.repository {
				t.Fatalf("effective source = %d %s %s %s", probe.calls, got.Selection().String(), got.Transport().String(), got.Repository().String())
			}
			ref, present := got.RequestedReference()
			if ref != test.reference || present != test.hasRef {
				t.Fatalf("reference = %q/%v", ref, present)
			}
		})
	}
}

func TestSourceFailureMappingIsSchemaShapedBoundedAndSecretFree(t *testing.T) {
	t.Parallel()

	secret := "SOURCE_AUTH_SECRET_CANARY"
	request, err := cli.NewParser("darwin").Parse([]string{"ai4j", "validate", "--repo", "private/repository"})
	if err != nil {
		t.Fatal(err)
	}
	selector, err := NewSourceSelector(&appSourceProbe{err: errors.New(secret)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := selector.Select(context.Background(), sourceOptions(request)); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe selector error: %v", err)
	}
	response, err := newSourceFailureResponse(cli.CommandValidate)
	if err != nil {
		t.Fatal(err)
	}
	if response.Result().ExitCode().Int() != 4 || response.Result().Failure().String() != "source" {
		t.Fatalf("source response = %s/%d", response.Result().Failure(), response.Result().ExitCode())
	}

	for name, renderer := range map[string]func(*bytes.Buffer) error{
		"human": func(output *bytes.Buffer) error {
			_, err := human.Render(output, response)
			return err
		},
		"json": func(output *bytes.Buffer) error {
			_, err := jsonout.Render(output, response)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if err := renderer(&output); err != nil {
				t.Fatal(err)
			}
			if output.Len() > 4096 || strings.Contains(output.String(), secret) || strings.Contains(output.String(), "private/repository") {
				t.Fatalf("unsafe %s output: %q", name, output.String())
			}
			if name == "json" {
				var document struct {
					Command  string `json:"command"`
					ExitCode int    `json:"exitCode"`
					Data     any    `json:"data"`
				}
				if err := json.Unmarshal(output.Bytes(), &document); err != nil {
					t.Fatal(err)
				}
				if document.Command != "validate" || document.ExitCode != 4 || document.Data != nil {
					t.Fatalf("source JSON = %#v", document)
				}
			}
		})
	}
}

func TestInvalidExplicitSourceAndReferenceStopBeforeProbe(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"ai4j", "validate", "--repo", "https://evil.example/alx4j/ai4j.git"},
		{"ai4j", "validate", "--ref=-upload-pack=sentinel"},
	}
	for _, argv := range tests {
		request, err := cli.NewParser("darwin").Parse(argv)
		if err != nil {
			t.Fatal(err)
		}
		probe := &appSourceProbe{}
		selector, err := NewSourceSelector(probe)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := selector.Select(context.Background(), sourceOptions(request)); err == nil {
			t.Fatalf("source %q succeeded", argv)
		}
		if probe.calls != 0 {
			t.Fatalf("source %q reached probe", argv)
		}
	}
}

func sourceOptions(request cli.Request) cli.SourceOptions {
	switch value := request.(type) {
	case cli.ValidateRequest:
		return value.Source()
	case cli.PlanInstallRequest:
		return value.Source()
	case cli.InstallRequest:
		return value.Source()
	default:
		panic("request is not a new-source command")
	}
}

type appSourceProbe struct {
	calls int
	err   error
}

func (p *appSourceProbe) Probe(_ context.Context, _ githubsource.EffectiveSource) error {
	p.calls++
	return p.err
}
