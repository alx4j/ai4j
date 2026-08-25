package lifecycle

import (
	"context"

	"github.com/alx4j/ai4j/internal/domain"
)

type LockRequest struct {
	Target domain.Target
	Scope  domain.Scope
}

// LockHandle is owned by its caller. Release must be called exactly once.
type LockHandle interface{ Release() error }

type LockAcquirer interface {
	AcquireLock(context.Context, LockRequest) (LockHandle, error)
}

type JournalRecord struct {
	OperationID    domain.OperationID
	InstallationID domain.InstallationID
	Phase          string
}

type JournalReader interface {
	ReadJournal(context.Context, domain.InstallationID) (JournalRecord, bool, error)
}

type JournalWriter interface {
	WriteJournal(context.Context, JournalRecord) error
	DeleteJournal(context.Context, domain.OperationID) error
}

type RecoveryArtifact struct {
	OperationID domain.OperationID
	Name        string
	Digest      domain.RenderedDigest
}

type RecoveryReader interface {
	ReadRecovery(context.Context, domain.OperationID) ([]RecoveryArtifact, error)
}

type RecoveryWriter interface {
	WriteRecovery(context.Context, RecoveryArtifact) error
	DeleteRecovery(context.Context, domain.OperationID) error
}
