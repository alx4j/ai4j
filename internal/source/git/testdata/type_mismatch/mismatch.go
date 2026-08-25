package mismatch

import (
	"github.com/alx4j/ai4j/internal/domain"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
)

func rendered(source gitsource.SourceProvenance, tree domain.TreeOID, digest domain.RenderedDigest, build domain.BuildCommit) {
	gitsource.NewRenderedProvenance(source, build, digest)
	gitsource.NewRenderedProvenance(source, tree, build)
}
