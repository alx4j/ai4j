// Package registry composes immutable, fail-closed lifecycle implementations.
package registry

import (
	"context"
	"reflect"
	"strconv"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/fault"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

type Selection struct {
	Target          domain.Target
	Host            domain.Host
	Scope           domain.Scope
	SourceMode      domain.SourceMode
	SourceSelection domain.SourceSelection
	SelectionMode   domain.SelectionMode
	RecoveryPolicy  domain.RecoveryPolicy
	StateSchema     domain.StateSchemaVersion
}

func MVPSelection() Selection {
	return Selection{
		Target:          domain.ClaudeTarget(),
		Host:            domain.DarwinHost(),
		Scope:           domain.UserScope(),
		SourceMode:      domain.GitHubSourceMode(),
		SourceSelection: domain.BuiltInDefaultSource(),
		SelectionMode:   domain.WholeToolkitSelection(),
		RecoveryPolicy:  domain.ShortLivedRecovery(),
		StateSchema:     domain.MVPStateSchema(),
	}
}

func (s Selection) valid() bool {
	return s.Target.Valid() && s.Host.Valid() && s.Scope.Valid() && s.SourceMode.Valid() &&
		s.SourceSelection.Valid() && s.SelectionMode.Valid() && s.RecoveryPolicy.Valid() && s.StateSchema.Valid()
}

type TargetRegistration struct {
	Target                domain.Target
	CandidateCapabilities domain.CapabilitySet
	QualifiedCapabilities domain.CapabilitySet
	Observer              func() lifecycle.TargetObserver
	Mutator               func() lifecycle.TargetMutator
}

type HostRegistration struct {
	Host     domain.Host
	Services lifecycle.HostServices
}

type SourceRegistration struct {
	Mode     domain.SourceMode
	Acquirer func() lifecycle.SourceAcquirer
}

type SourceSelectionRegistration struct{ Selection domain.SourceSelection }
type ScopeRegistration struct{ Scope domain.Scope }
type SelectionRegistration struct{ Mode domain.SelectionMode }

type StateRegistration struct {
	Schema      domain.StateSchemaVersion
	Reader      func() lifecycle.InstallationStateReader
	Writer      func() lifecycle.InstallationStateWriter
	Locks       func() lifecycle.LockAcquirer
	Clock       func() lifecycle.Clock
	Identifiers func() lifecycle.IdentifierGenerator
}

type RecoveryRegistration struct {
	Policy         domain.RecoveryPolicy
	JournalReader  func() lifecycle.JournalReader
	JournalWriter  func() lifecycle.JournalWriter
	RecoveryReader func() lifecycle.RecoveryReader
	RecoveryWriter func() lifecycle.RecoveryWriter
}

type Definitions struct {
	Targets          []TargetRegistration
	Hosts            []HostRegistration
	Sources          []SourceRegistration
	SourceSelections []SourceSelectionRegistration
	Scopes           []ScopeRegistration
	Selections       []SelectionRegistration
	States           []StateRegistration
	Recoveries       []RecoveryRegistration
}

type targetRegistration struct {
	candidate domain.CapabilitySet
	qualified domain.CapabilitySet
	observer  func() lifecycle.TargetObserver
	mutator   func() lifecycle.TargetMutator
}

type hostRegistration struct {
	services lifecycle.HostServices
	policy   lifecycle.HostResourcePolicy
}

type Registry struct {
	targets          map[domain.Target]targetRegistration
	hosts            map[domain.Host]hostRegistration
	sources          map[domain.SourceMode]SourceRegistration
	sourceSelections map[domain.SourceSelection]struct{}
	scopes           map[domain.Scope]struct{}
	selections       map[domain.SelectionMode]struct{}
	states           map[domain.StateSchemaVersion]StateRegistration
	recoveries       map[domain.RecoveryPolicy]RecoveryRegistration
}

func New(definitions Definitions) (Registry, error) {
	registry := Registry{
		targets: make(map[domain.Target]targetRegistration), hosts: make(map[domain.Host]hostRegistration),
		sources: make(map[domain.SourceMode]SourceRegistration), sourceSelections: make(map[domain.SourceSelection]struct{}),
		scopes: make(map[domain.Scope]struct{}), selections: make(map[domain.SelectionMode]struct{}),
		states: make(map[domain.StateSchemaVersion]StateRegistration), recoveries: make(map[domain.RecoveryPolicy]RecoveryRegistration),
	}
	if err := registry.addTargets(definitions.Targets); err != nil {
		return Registry{}, err
	}
	if err := registry.addHosts(definitions.Hosts); err != nil {
		return Registry{}, err
	}
	if err := registry.addSources(definitions.Sources); err != nil {
		return Registry{}, err
	}
	for _, entry := range definitions.SourceSelections {
		if !entry.Selection.Valid() || contains(registry.sourceSelections, entry.Selection) {
			return Registry{}, invalidRegistration("source_selection", fault.ReasonInvalidFormat)
		}
		registry.sourceSelections[entry.Selection] = struct{}{}
	}
	for _, entry := range definitions.Scopes {
		if !entry.Scope.Valid() || contains(registry.scopes, entry.Scope) {
			return Registry{}, invalidRegistration("scope", fault.ReasonInvalidFormat)
		}
		registry.scopes[entry.Scope] = struct{}{}
	}
	for _, entry := range definitions.Selections {
		if !entry.Mode.Valid() || contains(registry.selections, entry.Mode) {
			return Registry{}, invalidRegistration("selection", fault.ReasonInvalidFormat)
		}
		registry.selections[entry.Mode] = struct{}{}
	}
	if err := registry.addStates(definitions.States); err != nil {
		return Registry{}, err
	}
	if err := registry.addRecoveries(definitions.Recoveries); err != nil {
		return Registry{}, err
	}
	if len(registry.targets) == 0 || len(registry.hosts) == 0 || len(registry.sources) == 0 ||
		len(registry.sourceSelections) == 0 || len(registry.scopes) == 0 || len(registry.selections) == 0 ||
		len(registry.states) == 0 || len(registry.recoveries) == 0 {
		return Registry{}, invalidRegistration("definitions", fault.ReasonEmpty)
	}
	return registry, nil
}

func NewMVP(definitions Definitions) (Registry, error) {
	registry, err := New(definitions)
	if err != nil {
		return Registry{}, err
	}
	if len(registry.targets) != 1 || len(registry.hosts) != 1 || len(registry.sources) != 1 ||
		len(registry.sourceSelections) != 2 || len(registry.scopes) != 1 || len(registry.selections) != 1 ||
		len(registry.states) != 1 || len(registry.recoveries) != 1 {
		return Registry{}, unsupported("registry", "post_mvp", "mvp")
	}
	target, ok := registry.targets[domain.ClaudeTarget()]
	if !ok || !sameCapabilities(target.candidate, domain.MVPCapabilities()) {
		return Registry{}, unsupported("target", "claude", "mvp_capabilities")
	}
	checks := []bool{
		contains(registry.hosts, domain.DarwinHost()),
		contains(registry.sources, domain.GitHubSourceMode()),
		contains(registry.sourceSelections, domain.BuiltInDefaultSource()),
		contains(registry.sourceSelections, domain.ExplicitSource()),
		contains(registry.scopes, domain.UserScope()),
		contains(registry.selections, domain.WholeToolkitSelection()),
		contains(registry.states, domain.MVPStateSchema()),
		contains(registry.recoveries, domain.ShortLivedRecovery()),
	}
	for _, ok := range checks {
		if !ok {
			return Registry{}, unsupported("registry", "missing_mvp", "mvp")
		}
	}
	return registry, nil
}

type ReadBindings struct {
	Target      lifecycle.TargetObserver
	Host        lifecycle.HostInspector
	Environment lifecycle.EnvironmentInspector
	Policy      lifecycle.HostResourcePolicyProvider
	Resources   lifecycle.ResourceChecker
	Processes   lifecycle.ProcessRunner
	Source      lifecycle.SourceAcquirer
	State       lifecycle.InstallationStateReader
}

type MutationBindings struct {
	Target         lifecycle.TargetMutator
	Disk           lifecycle.DiskPreflighter
	Files          lifecycle.AtomicFileWriter
	Processes      lifecycle.ProcessRunner
	State          lifecycle.InstallationStateWriter
	Locks          lifecycle.LockAcquirer
	JournalReader  lifecycle.JournalReader
	JournalWriter  lifecycle.JournalWriter
	RecoveryReader lifecycle.RecoveryReader
	RecoveryWriter lifecycle.RecoveryWriter
	Clock          lifecycle.Clock
	Identifiers    lifecycle.IdentifierGenerator
}

// Host services are registered as one lifetime-owned implementation, but each
// consumer receives a narrow view. This prevents a read or mutation facet from
// recovering unrelated capabilities through a dynamic type assertion.
type hostInspectorView struct{ services lifecycle.HostServices }

func (v hostInspectorView) InspectHost(
	ctx context.Context,
	request lifecycle.HostInspectionRequest,
) (lifecycle.HostObservation, error) {
	return v.services.InspectHost(ctx, request)
}

type environmentInspectorView struct{ services lifecycle.HostServices }

func (v environmentInspectorView) InspectEnvironment(
	ctx context.Context,
	request lifecycle.EnvironmentPresenceRequest,
) (lifecycle.EnvironmentPresenceResult, error) {
	return v.services.InspectEnvironment(ctx, request)
}

type resourcePolicyView struct{ policy lifecycle.HostResourcePolicy }

func (v resourcePolicyView) ResourcePolicy() lifecycle.HostResourcePolicy {
	return v.policy
}

type resourceCheckerView struct{ services lifecycle.HostServices }

func (v resourceCheckerView) CheckResource(
	ctx context.Context,
	request lifecycle.ResourceRequest,
) (lifecycle.ResourceObservation, error) {
	return v.services.CheckResource(ctx, request)
}

func (v resourceCheckerView) ReadResource(
	ctx context.Context,
	request lifecycle.ResourceReadRequest,
) (lifecycle.ResourceReadResult, error) {
	return v.services.ReadResource(ctx, request)
}

func (v resourceCheckerView) CheckExecutable(
	ctx context.Context,
	request lifecycle.ExecutableRequest,
) (lifecycle.ExecutableObservation, error) {
	return v.services.CheckExecutable(ctx, request)
}

func (v resourceCheckerView) PreflightDisk(
	ctx context.Context,
	request lifecycle.DiskPreflightRequest,
) (lifecycle.DiskPreflightResult, error) {
	return v.services.PreflightDisk(ctx, request)
}

type processRunnerView struct{ services lifecycle.HostServices }

func (v processRunnerView) RunProcess(
	ctx context.Context,
	request lifecycle.ProcessRequest,
) (lifecycle.ProcessResult, error) {
	return v.services.RunProcess(ctx, request)
}

type diskPreflighterView struct{ services lifecycle.HostServices }

func (v diskPreflighterView) PreflightDisk(
	ctx context.Context,
	request lifecycle.DiskPreflightRequest,
) (lifecycle.DiskPreflightResult, error) {
	return v.services.PreflightDisk(ctx, request)
}

type atomicFileWriterView struct{ services lifecycle.HostServices }

func (v atomicFileWriterView) ReplaceFile(
	ctx context.Context,
	request lifecycle.FileMutation,
) (lifecycle.FileMutationResult, error) {
	return v.services.ReplaceFile(ctx, request)
}

func (v atomicFileWriterView) CleanupFile(
	ctx context.Context,
	artifact lifecycle.CleanupArtifact,
) (lifecycle.FileCleanupResult, error) {
	return v.services.CleanupFile(ctx, artifact)
}

func (v atomicFileWriterView) InspectFileArtifacts(
	ctx context.Context,
	request lifecycle.FileArtifactInspectionRequest,
) (lifecycle.FileArtifactInspectionResult, error) {
	return v.services.InspectFileArtifacts(ctx, request)
}

func (r Registry) ResolveRead(selection Selection, required domain.CapabilitySet) (ReadBindings, error) {
	resolved, err := r.validate(selection, required, false)
	if err != nil {
		return ReadBindings{}, err
	}
	services := resolved.host.services
	bindings := ReadBindings{
		Target: resolved.target.observer(), Host: hostInspectorView{services},
		Environment: environmentInspectorView{services}, Policy: resourcePolicyView{resolved.host.policy},
		Resources: resourceCheckerView{services}, Processes: processRunnerView{services},
		Source: resolved.source.Acquirer(), State: resolved.state.Reader(),
	}
	if err := validateBindings([]namedBinding{
		{name: "target", value: bindings.Target}, {name: "host", value: bindings.Host},
		{name: "environment", value: bindings.Environment}, {name: "policy", value: bindings.Policy},
		{name: "resources", value: bindings.Resources}, {name: "processes", value: bindings.Processes},
		{name: "source", value: bindings.Source},
		{name: "state", value: bindings.State},
	}); err != nil {
		return ReadBindings{}, err
	}
	return bindings, nil
}

func (r Registry) ResolveMutation(selection Selection, required domain.CapabilitySet) (MutationBindings, error) {
	if required.Empty() {
		return MutationBindings{}, invalidRegistration("required_capabilities", fault.ReasonEmpty)
	}
	resolved, err := r.validate(selection, required, true)
	if err != nil {
		return MutationBindings{}, err
	}
	services := resolved.host.services
	bindings := MutationBindings{
		Disk:   diskPreflighterView{services},
		Target: resolved.target.mutator(), Files: atomicFileWriterView{services}, Processes: processRunnerView{services},
		State: resolved.state.Writer(), Locks: resolved.state.Locks(), Clock: resolved.state.Clock(),
		Identifiers: resolved.state.Identifiers(), JournalReader: resolved.recovery.JournalReader(),
		JournalWriter: resolved.recovery.JournalWriter(), RecoveryReader: resolved.recovery.RecoveryReader(),
		RecoveryWriter: resolved.recovery.RecoveryWriter(),
	}
	if err := validateBindings([]namedBinding{
		{name: "target", value: bindings.Target}, {name: "disk", value: bindings.Disk}, {name: "files", value: bindings.Files},
		{name: "processes", value: bindings.Processes}, {name: "state", value: bindings.State},
		{name: "locks", value: bindings.Locks}, {name: "journal_reader", value: bindings.JournalReader},
		{name: "journal_writer", value: bindings.JournalWriter}, {name: "recovery_reader", value: bindings.RecoveryReader},
		{name: "recovery_writer", value: bindings.RecoveryWriter}, {name: "clock", value: bindings.Clock},
		{name: "identifiers", value: bindings.Identifiers},
	}); err != nil {
		return MutationBindings{}, err
	}
	return bindings, nil
}

type resolvedRegistrations struct {
	target   targetRegistration
	host     hostRegistration
	source   SourceRegistration
	state    StateRegistration
	recovery RecoveryRegistration
}

func (r Registry) validate(selection Selection, required domain.CapabilitySet, mutation bool) (resolvedRegistrations, error) {
	if !selection.valid() {
		return resolvedRegistrations{}, unsupported("selection", "invalid", "resolution")
	}
	target, ok := r.targets[selection.Target]
	if !ok {
		return resolvedRegistrations{}, unsupported("target", selection.Target.String(), "resolution")
	}
	host, ok := r.hosts[selection.Host]
	if !ok {
		return resolvedRegistrations{}, unsupported("host", selection.Host.String(), "resolution")
	}
	source, ok := r.sources[selection.SourceMode]
	if !ok {
		return resolvedRegistrations{}, unsupported("source_mode", selection.SourceMode.String(), "resolution")
	}
	if !contains(r.sourceSelections, selection.SourceSelection) {
		return resolvedRegistrations{}, unsupported("source_selection", selection.SourceSelection.String(), "resolution")
	}
	if !contains(r.scopes, selection.Scope) {
		return resolvedRegistrations{}, unsupported("scope", selection.Scope.String(), "resolution")
	}
	if !contains(r.selections, selection.SelectionMode) {
		return resolvedRegistrations{}, unsupported("selection_mode", selection.SelectionMode.String(), "resolution")
	}
	state, ok := r.states[selection.StateSchema]
	if !ok {
		return resolvedRegistrations{}, unsupported("state_schema", strconv.Itoa(int(selection.StateSchema.Uint16())), "resolution")
	}
	recovery, ok := r.recoveries[selection.RecoveryPolicy]
	if !ok {
		return resolvedRegistrations{}, unsupported("recovery_policy", selection.RecoveryPolicy.String(), "resolution")
	}
	available := target.candidate
	if mutation {
		available = target.qualified
	}
	if !available.ContainsAll(required) {
		return resolvedRegistrations{}, unsupported("target", selection.Target.String(), firstMissing(required, available).String())
	}
	if mutation && target.mutator == nil {
		return resolvedRegistrations{}, unsupported("target", selection.Target.String(), "mutation_factory")
	}
	return resolvedRegistrations{target: target, host: host, source: source, state: state, recovery: recovery}, nil
}

func (r *Registry) addTargets(entries []TargetRegistration) error {
	for _, entry := range entries {
		if !entry.Target.Valid() || entry.Observer == nil || contains(r.targets, entry.Target) ||
			!entry.CandidateCapabilities.ContainsAll(entry.QualifiedCapabilities) ||
			(!entry.QualifiedCapabilities.Empty() && entry.Mutator == nil) {
			return invalidRegistration("target", fault.ReasonInvalidFormat)
		}
		r.targets[entry.Target] = targetRegistration{
			candidate: cloneCapabilities(entry.CandidateCapabilities), qualified: cloneCapabilities(entry.QualifiedCapabilities),
			observer: entry.Observer, mutator: entry.Mutator,
		}
	}
	return nil
}

func (r *Registry) addHosts(entries []HostRegistration) error {
	for _, entry := range entries {
		if !entry.Host.Valid() || isNil(entry.Services) || contains(r.hosts, entry.Host) {
			return invalidRegistration("host", fault.ReasonInvalidFormat)
		}
		policy := entry.Services.ResourcePolicy()
		if !policy.Valid() {
			return invalidRegistration("host_policy", fault.ReasonInvalidFormat)
		}
		r.hosts[entry.Host] = hostRegistration{services: entry.Services, policy: policy}
	}
	return nil
}

func (r *Registry) addSources(entries []SourceRegistration) error {
	for _, entry := range entries {
		if !entry.Mode.Valid() || entry.Acquirer == nil || contains(r.sources, entry.Mode) {
			return invalidRegistration("source", fault.ReasonInvalidFormat)
		}
		r.sources[entry.Mode] = entry
	}
	return nil
}

func (r *Registry) addStates(entries []StateRegistration) error {
	for _, entry := range entries {
		if !entry.Schema.Valid() || entry.Reader == nil || entry.Writer == nil || entry.Locks == nil ||
			entry.Clock == nil || entry.Identifiers == nil || contains(r.states, entry.Schema) {
			return invalidRegistration("state", fault.ReasonInvalidFormat)
		}
		r.states[entry.Schema] = entry
	}
	return nil
}

func (r *Registry) addRecoveries(entries []RecoveryRegistration) error {
	for _, entry := range entries {
		if !entry.Policy.Valid() || entry.JournalReader == nil || entry.JournalWriter == nil ||
			entry.RecoveryReader == nil || entry.RecoveryWriter == nil || contains(r.recoveries, entry.Policy) {
			return invalidRegistration("recovery", fault.ReasonInvalidFormat)
		}
		r.recoveries[entry.Policy] = entry
	}
	return nil
}

func contains[K comparable, V any](values map[K]V, key K) bool {
	_, ok := values[key]
	return ok
}

func cloneCapabilities(value domain.CapabilitySet) domain.CapabilitySet {
	cloned, err := domain.NewCapabilitySet(value.Values()...)
	if err != nil {
		panic(err)
	}
	return cloned
}

func sameCapabilities(left, right domain.CapabilitySet) bool {
	return left.ContainsAll(right) && right.ContainsAll(left)
}

func firstMissing(required, available domain.CapabilitySet) domain.Capability {
	for _, capability := range required.Values() {
		if !available.Contains(capability) {
			return capability
		}
	}
	return domain.NativeValidationCapability()
}

func unsupported(kind, value, capability string) error {
	detail, err := fault.NewUnsupportedDetail(kind, value, capability)
	if err != nil {
		detail, _ = fault.NewUnsupportedDetail("registration", "unsupported", "resolution")
	}
	return fault.MustNew(fault.UnsupportedCapability, detail, nil)
}

func invalidRegistration(field string, reason fault.InvalidReason) error {
	detail, err := fault.NewInvalidDetail(field, reason)
	if err != nil {
		panic(err)
	}
	return fault.MustNew(fault.InvalidInput, detail, nil)
}

type namedBinding struct {
	name  string
	value any
}

func validateBindings(bindings []namedBinding) error {
	for _, binding := range bindings {
		if isNil(binding.value) {
			return invalidRegistration(binding.name, fault.ReasonEmpty)
		}
	}
	return nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
