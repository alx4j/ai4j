package catalog

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
)

func TestRenderPinsDefaultPackagesToTheFullCommit(t *testing.T) {
	t.Parallel()
	repository, _ := domain.NewRepositoryIdentity("github.com/alx4j/ai4j")
	commit, _ := domain.NewCommitOID(strings.Repeat("a", 40))
	first, err := Render(repository, commit)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(repository, commit)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Bytes()) != string(second.Bytes()) || first.Digest() != second.Digest() || len(first.Digest()) != 64 {
		t.Fatal("catalog output is not deterministic")
	}
	var document struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Source string `json:"source"`
				URL    string `json:"url"`
				Path   string `json:"path"`
				SHA    string `json:"sha"`
				Ref    string `json:"ref"`
			} `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(first.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Plugins) != 2 || document.Plugins[0].Name != "ai4j-review" || document.Plugins[1].Name != "ai4j-tools" ||
		document.Plugins[0].Source.Source != "git-subdir" || document.Plugins[0].Source.URL != "https://github.com/alx4j/ai4j.git" ||
		document.Plugins[0].Source.Path != "plugins/ai4j-review" || document.Plugins[0].Source.SHA != commit.String() || document.Plugins[0].Source.Ref != "" ||
		document.Plugins[1].Source.Source != "git-subdir" || document.Plugins[1].Source.URL != "https://github.com/alx4j/ai4j.git" ||
		document.Plugins[1].Source.Path != "plugins/ai4j-tools" || document.Plugins[1].Source.SHA != commit.String() || document.Plugins[1].Source.Ref != "" {
		t.Fatalf("catalog = %#v", document)
	}
}

func TestRenderPackagesSortsPluginsAndPinsEachToTheFullCommit(t *testing.T) {
	t.Parallel()
	repository, _ := domain.NewRepositoryIdentity("github.com/alx4j/ai4j")
	commit, _ := domain.NewCommitOID(strings.Repeat("b", 40))
	packages := []Package{
		{ID: "review", Path: "plugins/review", Description: "Review guidance"},
		{ID: "agents", Path: "plugins/agents", Description: "Focused agents"},
	}

	first, err := RenderPackages("ai4j-selection", packages, repository, commit)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderPackages("ai4j-selection", []Package{packages[1], packages[0]}, repository, commit)
	if err != nil {
		t.Fatal(err)
	}

	if string(first.Bytes()) != string(second.Bytes()) || first.Digest() != second.Digest() {
		t.Fatal("catalog output depends on input package order")
	}
	if packages[0].ID != "review" || packages[1].ID != "agents" {
		t.Fatal("catalog rendering mutated the caller's package order")
	}
	var document struct {
		Plugins []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Source      struct {
				Source string `json:"source"`
				URL    string `json:"url"`
				Path   string `json:"path"`
				SHA    string `json:"sha"`
			} `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(first.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Plugins) != 2 {
		t.Fatalf("plugin count = %d, want 2", len(document.Plugins))
	}
	if document.Plugins[0].Name != "agents" || document.Plugins[0].Description != "Focused agents" || document.Plugins[0].Source.Path != "plugins/agents" {
		t.Fatalf("first plugin = %#v", document.Plugins[0])
	}
	if document.Plugins[1].Name != "review" || document.Plugins[1].Description != "Review guidance" || document.Plugins[1].Source.Path != "plugins/review" {
		t.Fatalf("second plugin = %#v", document.Plugins[1])
	}
	for _, plugin := range document.Plugins {
		if plugin.Source.Source != "git-subdir" || plugin.Source.URL != "https://github.com/alx4j/ai4j.git" || plugin.Source.SHA != commit.String() {
			t.Fatalf("plugin source = %#v", plugin.Source)
		}
	}
}

func TestRenderLocalPackagesSortsPluginsAndUsesRelativeSources(t *testing.T) {
	t.Parallel()

	document, err := RenderLocalPackages("ai4j-retained", []Package{
		{ID: "review", Path: "plugins/review", Description: "Review guidance"},
		{ID: "agents", Path: "plugins/agents", Description: "Focused agents"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var decoded struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(document.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Plugins) != 2 || decoded.Plugins[0].Name != "agents" || decoded.Plugins[0].Source != "./plugins/agents" || decoded.Plugins[1].Name != "review" || decoded.Plugins[1].Source != "./plugins/review" {
		t.Fatalf("plugins = %#v", decoded.Plugins)
	}
}

func TestRenderPackagesRejectsInvalidPackageSets(t *testing.T) {
	t.Parallel()
	repository, _ := domain.NewRepositoryIdentity("github.com/alx4j/ai4j")
	commit, _ := domain.NewCommitOID(strings.Repeat("c", 40))
	tests := []struct {
		name     string
		packages []Package
	}{
		{name: "empty", packages: nil},
		{name: "missing identity", packages: []Package{{ID: "review", Path: "", Description: "Review guidance"}}},
		{name: "duplicate identity", packages: []Package{{ID: "review", Path: "plugins/review", Description: "First"}, {ID: "review", Path: "plugins/review-copy", Description: "Second"}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := RenderPackages("ai4j-selection", test.packages, repository, commit); err == nil {
				t.Fatal("RenderPackages succeeded")
			}
		})
	}
}
