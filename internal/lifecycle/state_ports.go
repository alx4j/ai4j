package lifecycle

import (
	"context"

	"github.com/alx4j/ai4j/internal/domain"
)

type InstallationKey struct {
	Target domain.Target
	Scope  domain.Scope
}

type InstallationRecord struct {
	Schema         domain.StateSchemaVersion
	InstallationID domain.InstallationID
	Target         domain.Target
	Host           domain.Host
	Scope          domain.Scope
	SourceMode     domain.SourceMode
	Selection      domain.SelectionMode
}

type InstallationStateReader interface {
	ReadInstallation(context.Context, InstallationKey) (InstallationRecord, bool, error)
}

type InstallationStateWriter interface {
	WriteInstallation(context.Context, InstallationRecord) error
	DeleteInstallation(context.Context, InstallationKey) error
}
