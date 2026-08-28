// Package catalog renders exact-commit Claude marketplaces used by AI4J.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/source/gitremote"
)

type Document struct {
	bytes  []byte
	digest string
}

// Package describes one native Claude plugin published by a marketplace.
type Package struct {
	ID          string
	Path        string
	Description string
	Repository  domain.RepositoryIdentity
	Transport   domain.GitTransport
	Commit      domain.CommitOID
}

func (d Document) Bytes() []byte  { return append([]byte(nil), d.bytes...) }
func (d Document) Digest() string { return d.digest }

func Render(repository domain.RepositoryIdentity, transport domain.GitTransport, commit domain.CommitOID) (Document, error) {
	return RenderPackages("ai4j", []Package{
		{ID: "ai4j-review", Path: "plugins/ai4j-review", Description: "Practical repository review guidance and a focused review agent", Repository: repository, Transport: transport, Commit: commit},
		{ID: "ai4j-tools", Path: "plugins/ai4j-tools", Description: "Claude-backed tools for AI4J workflows", Repository: repository, Transport: transport, Commit: commit},
	})
}

// RenderPackage renders a one-package exact-commit Claude marketplace catalog.
func RenderPackage(marketplaceID, packageID, packagePath, description string, repository domain.RepositoryIdentity, transport domain.GitTransport, commit domain.CommitOID) (Document, error) {
	return RenderPackages(marketplaceID, []Package{{ID: packageID, Path: packagePath, Description: description, Repository: repository, Transport: transport, Commit: commit}})
}

// RenderPackages renders an exact-commit Claude marketplace catalog. Packages
// are ordered by ID so equivalent selections always produce identical bytes.
func RenderPackages(marketplaceID string, packages []Package) (Document, error) {
	ordered, err := validateAndSort(marketplaceID, packages, true)
	if err != nil {
		return Document{}, err
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
	document.Plugins = make([]plugin, 0, len(ordered))
	for _, descriptor := range ordered {
		remote, remoteErr := gitremote.ReconstructRemote(descriptor.Repository, descriptor.Transport)
		if remoteErr != nil {
			return Document{}, fmt.Errorf("catalog source is invalid")
		}
		document.Plugins = append(document.Plugins, plugin{
			Name: descriptor.ID,
			Source: source{
				Source: "git-subdir",
				URL:    remote.Endpoint(),
				Path:   descriptor.Path,
				SHA:    descriptor.Commit.String(),
			},
			Description: descriptor.Description,
		})
	}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Document{}, fmt.Errorf("render catalog: %w", err)
	}
	contents = append(contents, '\n')
	digest := sha256.Sum256(contents)
	return Document{bytes: contents, digest: hex.EncodeToString(digest[:])}, nil
}

// RenderLocalPackage renders a one-package local marketplace whose package
// source is relative to its catalog root.
func RenderLocalPackage(marketplaceID, packageID, packagePath, description string) (Document, error) {
	return RenderLocalPackages(marketplaceID, []Package{{ID: packageID, Path: packagePath, Description: description}})
}

// RenderLocalPackages renders a local Claude marketplace. Packages are ordered
// by ID and each source is relative to the catalog root.
func RenderLocalPackages(marketplaceID string, packages []Package) (Document, error) {
	ordered, err := validateAndSort(marketplaceID, packages, false)
	if err != nil {
		return Document{}, err
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
	document.Plugins = make([]struct {
		Name        string `json:"name"`
		Source      string `json:"source"`
		Description string `json:"description"`
	}, 0, len(ordered))
	for _, descriptor := range ordered {
		document.Plugins = append(document.Plugins, struct {
			Name        string `json:"name"`
			Source      string `json:"source"`
			Description string `json:"description"`
		}{Name: descriptor.ID, Source: "./" + descriptor.Path, Description: descriptor.Description})
	}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Document{}, fmt.Errorf("render local catalog: %w", err)
	}
	contents = append(contents, '\n')
	digest := sha256.Sum256(contents)
	return Document{bytes: contents, digest: hex.EncodeToString(digest[:])}, nil
}

func validateAndSort(marketplaceID string, packages []Package, requireSource bool) ([]Package, error) {
	if marketplaceID == "" || len(packages) == 0 {
		return nil, fmt.Errorf("catalog package identity is invalid")
	}
	ordered := append([]Package(nil), packages...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].ID < ordered[j].ID
	})
	for i, descriptor := range ordered {
		if descriptor.ID == "" || descriptor.Path == "" || descriptor.Description == "" ||
			requireSource && (!descriptor.Repository.Valid() || !descriptor.Transport.Valid() || !descriptor.Commit.Valid()) {
			return nil, fmt.Errorf("catalog package identity is invalid")
		}
		if i > 0 && descriptor.ID == ordered[i-1].ID {
			return nil, fmt.Errorf("catalog package identity is duplicated: %s", descriptor.ID)
		}
	}
	return ordered, nil
}
