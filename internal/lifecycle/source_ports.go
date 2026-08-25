package lifecycle

import (
	"context"

	"github.com/alx4j/ai4j/internal/domain"
)

type SourceRequest struct {
	Mode       domain.SourceMode
	Repository domain.RepositoryIdentity
	Reference  string
}

// SourceSnapshot is owned by its caller. Close must be called exactly once.
type SourceSnapshot interface {
	Root() string
	Commit() domain.CommitIdentity
	Tree() domain.TreeOID
	Close() error
}

type SourceAcquirer interface {
	AcquireSource(context.Context, SourceRequest) (SourceSnapshot, error)
}
