package mismatch

import "github.com/alx4j/ai4j/internal/domain"

func requireCommit(domain.CommitOID) {}

func mismatch(tree domain.TreeOID, rendered domain.RenderedDigest, build domain.BuildCommit) {
	requireCommit(tree)
	requireCommit(rendered)
	requireCommit(build)
}
