// Package lifecycle owns target-neutral orchestration ports and records.
package lifecycle

import (
	"context"

	"github.com/alx4j/ai4j/internal/domain"
)

type TargetObservationRequest struct {
	Target domain.Target
	Scope  domain.Scope
}

type TargetObservation struct {
	Target       domain.Target
	Capabilities domain.CapabilitySet
}

type TargetMutationKind struct{ value string }

var (
	mutationRegister  = TargetMutationKind{value: "register"}
	mutationInstall   = TargetMutationKind{value: "install"}
	mutationEnable    = TargetMutationKind{value: "enable"}
	mutationUpdate    = TargetMutationKind{value: "update"}
	mutationUninstall = TargetMutationKind{value: "uninstall"}
)

func RegisterMutation() TargetMutationKind  { return mutationRegister }
func InstallMutation() TargetMutationKind   { return mutationInstall }
func EnableMutation() TargetMutationKind    { return mutationEnable }
func UpdateMutation() TargetMutationKind    { return mutationUpdate }
func UninstallMutation() TargetMutationKind { return mutationUninstall }
func (k TargetMutationKind) String() string { return k.value }

type TargetMutationRequest struct {
	OperationID domain.OperationID
	Target      domain.Target
	Scope       domain.Scope
	Kind        TargetMutationKind
	Package     domain.RenderedDigest
}

type TargetMutationResult struct {
	Changed bool
}

type TargetObserver interface {
	ObserveTarget(context.Context, TargetObservationRequest) (TargetObservation, error)
}

type TargetMutator interface {
	MutateTarget(context.Context, TargetMutationRequest) (TargetMutationResult, error)
}
