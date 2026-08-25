// Package catalog renders the single exact-commit Claude marketplace used by AI4J.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/alx4j/ai4j/internal/domain"
)

type Document struct {
	bytes  []byte
	digest string
}

func (d Document) Bytes() []byte  { return append([]byte(nil), d.bytes...) }
func (d Document) Digest() string { return d.digest }

func Render(repository domain.RepositoryIdentity, commit domain.CommitOID) (Document, error) {
	return RenderPackage("ai4j", "ai4j-default", "plugins/ai4j-default", "Practical repository review guidance and a focused review agent", repository, commit)
}

// RenderPackage renders one exact-commit Claude marketplace catalog. Callers
// choose stable native identities so independent installations never redirect
// or remove one another.
func RenderPackage(marketplaceID, packageID, packagePath, description string, repository domain.RepositoryIdentity, commit domain.CommitOID) (Document, error) {
	if !repository.Valid() || !commit.Valid() {
		return Document{}, fmt.Errorf("catalog source is invalid")
	}
	if marketplaceID == "" || packageID == "" || packagePath == "" || description == "" {
		return Document{}, fmt.Errorf("catalog package identity is invalid")
	}
	type source struct {
		Source string `json:"source"`
		URL    string `json:"url"`
		Path   string `json:"path"`
		SHA    string `json:"sha"`
	}
	type plugin struct {
		Name        string `json:"name"`
		Source      source `json:"source"`
		Description string `json:"description"`
	}
	document := struct {
		Name  string `json:"name"`
		Owner struct {
			Name string `json:"name"`
		} `json:"owner"`
		Plugins []plugin `json:"plugins"`
	}{Name: marketplaceID}
	document.Owner.Name = "AI4J"
	document.Plugins = []plugin{{
		Name:        packageID,
		Source:      source{Source: "git-subdir", URL: "https://" + repository.String() + ".git", Path: packagePath, SHA: commit.String()},
		Description: description,
	}}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Document{}, fmt.Errorf("render catalog: %w", err)
	}
	contents = append(contents, '\n')
	digest := sha256.Sum256(contents)
	return Document{bytes: contents, digest: hex.EncodeToString(digest[:])}, nil
}

// RenderLocalPackage renders a temporary rollback marketplace whose package
// source is relative to the private retained-artifact root.
func RenderLocalPackage(marketplaceID, packageID, packagePath, description string) (Document, error) {
	if marketplaceID == "" || packageID == "" || packagePath == "" || description == "" {
		return Document{}, fmt.Errorf("catalog package identity is invalid")
	}
	document := struct {
		Name  string `json:"name"`
		Owner struct {
			Name string `json:"name"`
		} `json:"owner"`
		Plugins []struct {
			Name        string `json:"name"`
			Source      string `json:"source"`
			Description string `json:"description"`
		} `json:"plugins"`
	}{Name: marketplaceID}
	document.Owner.Name = "AI4J"
	document.Plugins = append(document.Plugins, struct {
		Name        string `json:"name"`
		Source      string `json:"source"`
		Description string `json:"description"`
	}{Name: packageID, Source: "./" + packagePath, Description: description})
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Document{}, fmt.Errorf("render local catalog: %w", err)
	}
	contents = append(contents, '\n')
	digest := sha256.Sum256(contents)
	return Document{bytes: contents, digest: hex.EncodeToString(digest[:])}, nil
}
