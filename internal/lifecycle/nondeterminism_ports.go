package lifecycle

import (
	"context"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
)

type Clock interface{ Now() time.Time }

type IdentifierGenerator interface {
	NewOperationID(context.Context) (domain.OperationID, error)
	NewInstallationID(context.Context) (domain.InstallationID, error)
	NewArtifactToken(context.Context) (domain.ArtifactToken, error)
}
