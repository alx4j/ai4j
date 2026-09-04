package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	gitremote "github.com/alx4j/ai4j/internal/source/gitremote"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func testPlanSourceFrom(t *testing.T, options cli.SourceOptions, commit string) cli.Source {
	t.Helper()
	input, err := gitremote.NewSelectionInput(options.Repository(), options.HasRepository(), options.Reference(), options.HasReference())
	if err != nil {
		t.Fatal(err)
	}
	effective, err := gitremote.Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	request, err := gitsource.NewResolutionRequest(effective)
	if err != nil {
		t.Fatal(err)
	}
	advertisement := "ref: refs/heads/main\tHEAD\n" + commit + "\tHEAD\n" + commit + "\trefs/heads/main\n"
	if options.HasReference() && strings.HasPrefix(options.Reference(), "refs/") {
		advertisement += commit + "\t" + options.Reference() + "\n"
	}
	parsedAdvertisement, err := gitsource.ParseRemoteAdvertisement(request, []byte(advertisement))
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := gitsource.ResolveReference(parsedAdvertisement)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := gitsource.NewSelectedObjectProof(resolution, []byte("commit\n"))
	if err != nil {
		t.Fatal(err)
	}
	provenCommit, err := gitsource.NewDirectProvenCommit(selected)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := gitsource.NewCommitTreeProof(provenCommit, []byte(strings.Repeat("b", 40)+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := gitsource.NewSourceProvenance(proof)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := domain.NewRenderedDigest(strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	build, err := domain.NewBuildCommit(strings.Repeat("d", 40))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := gitsource.NewRenderedProvenance(provenance, digest, build)
	if err != nil {
		t.Fatal(err)
	}
	source, err := cli.NewSource(rendered)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func testInstallationRecord(refKind, commit string) installstate.Record {
	requested := "main"
	scopeRoot, _ := filepath.Abs(".")
	return installstate.Record{
		SchemaVersion: installstate.SchemaVersion, InstallationID: "installation-001", ToolkitID: "ai4j", ToolkitVersion: "1.0.0",
		Packages:      []installstate.NativePackage{{ID: "ai4j-default", Path: "plugins/ai4j-default"}},
		MarketplaceID: "ai4j",
		Source:        installstate.Source{Mode: "git", Transport: "https", Selection: "explicit", Repository: "github.com/alx4j/ai4j", RequestedRef: &requested, RefKind: refKind, Commit: commit, RenderedDigest: strings.Repeat("e", 64)},
		Target:        "claude", Host: "darwin-arm64", Scope: "user", ScopeRoot: scopeRoot, Lifecycle: "active",
		Selection:       installstate.Selection{RequestedBundle: "default", ResolvedBundles: []string{"default"}, ResolvedAssets: []string{"ai4j-rules"}},
		NativeResources: []string{"claude:ai4j-default@ai4j", "claude:marketplace:ai4j"}, Health: "healthy", AI4JVersion: "0.0.0-dev",
		Catalog:       installstate.OwnedFile{Path: "state/catalogs/installation-001/.claude-plugin/marketplace.json", Checksum: strings.Repeat("b", 64)},
		Rules:         installstate.OwnedFile{Path: "rules/ai4j-installation-001.md", Checksum: strings.Repeat("f", 64)},
		LastOperation: installstate.LastOperation{ID: "operation-001", Timestamp: "2026-08-24T12:00:00Z"},
	}
}

func testLifecycleReport(t *testing.T) validation.Report {
	t.Helper()
	return validation.Report{Source: testPlanSourceFrom(t, cli.SourceOptions{}, strings.Repeat("a", 40))}
}
