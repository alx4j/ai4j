package catalog

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
)

func TestRenderPinsExactlyOnePluginToTheFullCommit(t *testing.T) {
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
	if len(document.Plugins) != 1 || document.Plugins[0].Name != "ai4j-default" ||
		document.Plugins[0].Source.Source != "git-subdir" || document.Plugins[0].Source.URL != "https://github.com/alx4j/ai4j.git" ||
		document.Plugins[0].Source.Path != "plugins/ai4j-default" || document.Plugins[0].Source.SHA != commit.String() || document.Plugins[0].Source.Ref != "" {
		t.Fatalf("catalog = %#v", document)
	}
}
