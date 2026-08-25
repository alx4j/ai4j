package app

import (
	"context"
	"fmt"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/result"
	githubsource "github.com/alx4j/ai4j/internal/source/github"
)

// SourceSelector is the application-owned join between parsed source-option
// presence and the credential-free GitHub access boundary. Exact provenance is
// added by the later immutable Git source story.
type SourceSelector struct{ probe githubsource.AccessProbe }

func NewSourceSelector(probe githubsource.AccessProbe) (SourceSelector, error) {
	if probe == nil {
		return SourceSelector{}, fmt.Errorf("source selector requires an access probe")
	}
	return SourceSelector{probe: probe}, nil
}

func (s SourceSelector) Select(ctx context.Context, options cli.SourceOptions) (githubsource.EffectiveSource, error) {
	if s.probe == nil {
		return githubsource.EffectiveSource{}, fmt.Errorf("source selector is not configured")
	}
	input, err := githubsource.NewSelectionInput(options.Repository(), options.HasRepository(), options.Reference(), options.HasReference())
	if err != nil {
		return githubsource.EffectiveSource{}, err
	}
	return githubsource.Qualify(ctx, input, s.probe)
}

func newSourceFailureResponse(command cli.Command) (cli.Response, error) {
	problem, err := result.NewProblem("source_selection_failed", "GitHub source could not be selected", nil)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := neutralResult(result.StatusError, result.FailureSource, []result.Problem{problem})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}
