package app

import (
	"maps"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	githubsource "github.com/alx4j/ai4j/internal/source/github"
)

func TestStoredSourceOptionsPreserveSelection(t *testing.T) {
	t.Parallel()
	builtIn := testInstallationRecord("default_branch", strings.Repeat("a", 40))
	builtIn.Source.Selection = domain.BuiltInDefaultSource().String()
	builtIn.Source.RequestedRef = nil
	tracking, err := updateSourceOptions(builtIn)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := exactSourceOptions(builtIn)
	if err != nil {
		t.Fatal(err)
	}
	if tracking.HasRepository() || tracking.HasReference() || exact.HasRepository() || !exact.HasReference() || exact.Reference() != builtIn.Source.Commit {
		t.Fatalf("built-in options: tracking=%#v exact=%#v", tracking, exact)
	}

	explicit := testInstallationRecord("branch", strings.Repeat("a", 40))
	for _, buildOptions := range []func(installstate.Record) (cli.SourceOptions, error){updateSourceOptions, exactSourceOptions} {
		options, optionErr := buildOptions(explicit)
		if optionErr != nil {
			t.Fatal(optionErr)
		}
		if options.Repository() != "https://github.com/alx4j/ai4j.git" || !options.HasRepository() {
			t.Fatalf("explicit repository = %q/%t", options.Repository(), options.HasRepository())
		}
		input, inputErr := githubsource.NewSelectionInput(options.Repository(), options.HasRepository(), options.Reference(), options.HasReference())
		if inputErr != nil {
			t.Fatal(inputErr)
		}
		effective, resolveErr := githubsource.Resolve(input)
		if resolveErr != nil || effective.Repository().String() != explicit.Source.Repository {
			t.Fatalf("effective repository = %q, err = %v", effective.Repository().String(), resolveErr)
		}
	}
}

func TestActiveContentDiffReportsEveryChange(t *testing.T) {
	t.Parallel()
	item := func(identifier, checksum string) cli.ContentItem {
		value, err := cli.NewContentItem(cli.ComponentSkill, identifier, "plugins/ai4j-default/skills/"+identifier, checksum, cli.ContentAdded, nil)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	installed := []cli.ContentItem{
		item("changed", strings.Repeat("1", 64)),
		item("removed", strings.Repeat("2", 64)),
		item("unchanged", strings.Repeat("3", 64)),
	}
	desired := []cli.ContentItem{
		item("added", strings.Repeat("4", 64)),
		item("changed", strings.Repeat("5", 64)),
		item("unchanged", strings.Repeat("3", 64)),
	}
	content, err := diffActiveContent(installed, desired)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]cli.ContentChange, len(content))
	for _, value := range content {
		got[value.Identifier()] = value.Change()
	}
	want := map[string]cli.ContentChange{
		"added": cli.ContentAdded, "changed": cli.ContentChanged,
		"removed": cli.ContentRemoved, "unchanged": cli.ContentUnchanged,
	}
	if !maps.Equal(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
}
