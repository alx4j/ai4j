package registry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/fault"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/registry"
)

type ports struct {
	hostCalls        int
	environmentCalls int
	policyCalls      int
	resourceCalls    int
	diskCalls        int
	fileCalls        int
	processCalls     int
	policyOverride   *lifecycle.HostResourcePolicy
}

func (*ports) ObserveTarget(context.Context, lifecycle.TargetObservationRequest) (lifecycle.TargetObservation, error) {
	return lifecycle.TargetObservation{}, nil
}
func (*ports) MutateTarget(context.Context, lifecycle.TargetMutationRequest) (lifecycle.TargetMutationResult, error) {
	return lifecycle.TargetMutationResult{}, nil
}
func (p *ports) InspectHost(context.Context, lifecycle.HostInspectionRequest) (lifecycle.HostObservation, error) {
	p.hostCalls++
	return lifecycle.HostObservation{}, nil
}
func (p *ports) ReplaceFile(context.Context, lifecycle.FileMutation) (lifecycle.FileMutationResult, error) {
	p.fileCalls++
	return lifecycle.FileMutationResult{}, nil
}
func (*ports) CleanupFile(context.Context, lifecycle.CleanupArtifact) (lifecycle.FileCleanupResult, error) {
	return lifecycle.FileCleanupResult{}, nil
}
func (*ports) InspectFileArtifacts(context.Context, lifecycle.FileArtifactInspectionRequest) (lifecycle.FileArtifactInspectionResult, error) {
	return lifecycle.FileArtifactInspectionResult{}, nil
}
func (p *ports) RunProcess(context.Context, lifecycle.ProcessRequest) (lifecycle.ProcessResult, error) {
	p.processCalls++
	return lifecycle.ProcessResult{}, nil
}
func (p *ports) CheckResource(context.Context, lifecycle.ResourceRequest) (lifecycle.ResourceObservation, error) {
	p.resourceCalls++
	return lifecycle.ResourceObservation{}, nil
}
func (*ports) ReadResource(context.Context, lifecycle.ResourceReadRequest) (lifecycle.ResourceReadResult, error) {
	return lifecycle.ResourceReadResult{}, nil
}
func (*ports) CheckExecutable(context.Context, lifecycle.ExecutableRequest) (lifecycle.ExecutableObservation, error) {
	return lifecycle.ExecutableObservation{}, nil
}
func (p *ports) PreflightDisk(context.Context, lifecycle.DiskPreflightRequest) (lifecycle.DiskPreflightResult, error) {
	p.diskCalls++
	return lifecycle.NewDiskPreflightResult([]lifecycle.FilesystemCapacity{{
		Identity: 1, Required: 1, Available: 1, Known: true,
	}})
}
func (p *ports) InspectEnvironment(context.Context, lifecycle.EnvironmentPresenceRequest) (lifecycle.EnvironmentPresenceResult, error) {
	p.environmentCalls++
	return lifecycle.NewEnvironmentPresenceResult([]lifecycle.EnvironmentPresence{{Name: "HOME", Present: true}})
}
func (p *ports) ResourcePolicy() lifecycle.HostResourcePolicy {
	p.policyCalls++
	if p.policyOverride != nil {
		return *p.policyOverride
	}
	version, _ := lifecycle.NewResourcePolicyVersion("mvp_resource_v1")
	policy, _ := lifecycle.NewHostResourcePolicy(version, 5*time.Minute, 2*time.Minute)
	return policy
}

func TestRegistryRejectsInvalidAndSnapshotsHostPolicy(t *testing.T) {
	t.Parallel()

	calls := new(factoryCalls)
	invalidDefinitions := mvpDefinitions(calls, domain.MVPCapabilities())
	invalidServices := invalidDefinitions.Hosts[0].Services.(*ports)
	zero := lifecycle.HostResourcePolicy{}
	invalidServices.policyOverride = &zero
	if _, err := registry.NewMVP(invalidDefinitions); !errors.Is(err, fault.ErrInvalidInput) {
		t.Fatalf("invalid policy registration error = %v", err)
	}

	version, _ := lifecycle.NewResourcePolicyVersion("mvp_resource_v1")
	policy, _ := lifecycle.NewHostResourcePolicy(version, 5*time.Minute, 2*time.Minute)
	definitions := mvpDefinitions(new(factoryCalls), domain.MVPCapabilities())
	services := definitions.Hosts[0].Services.(*ports)
	services.policyOverride = &policy
	value, err := registry.NewMVP(definitions)
	if err != nil {
		t.Fatal(err)
	}
	policy = lifecycle.HostResourcePolicy{}
	inspection, _ := domain.NewCapabilitySet(domain.InspectionCapability())
	read, err := value.ResolveRead(registry.MVPSelection(), inspection)
	if err != nil {
		t.Fatal(err)
	}
	if got := read.Policy.ResourcePolicy(); !got.Valid() || got.Version().String() != "mvp_resource_v1" ||
		services.policyCalls != 1 {
		t.Fatalf("snapshotted policy = %#v, calls = %d", got, services.policyCalls)
	}
}
func (*ports) AcquireSource(context.Context, lifecycle.SourceRequest) (lifecycle.SourceSnapshot, error) {
	return nil, nil
}
func (*ports) ReadInstallation(context.Context, lifecycle.InstallationKey) (lifecycle.InstallationRecord, bool, error) {
	return lifecycle.InstallationRecord{}, false, nil
}
func (*ports) WriteInstallation(context.Context, lifecycle.InstallationRecord) error { return nil }
func (*ports) DeleteInstallation(context.Context, lifecycle.InstallationKey) error   { return nil }
func (*ports) AcquireLock(context.Context, lifecycle.LockRequest) (lifecycle.LockHandle, error) {
	return noopLock{}, nil
}
func (*ports) ReadJournal(context.Context, domain.InstallationID) (lifecycle.JournalRecord, bool, error) {
	return lifecycle.JournalRecord{}, false, nil
}
func (*ports) WriteJournal(context.Context, lifecycle.JournalRecord) error { return nil }
func (*ports) DeleteJournal(context.Context, domain.OperationID) error     { return nil }
func (*ports) ReadRecovery(context.Context, domain.OperationID) ([]lifecycle.RecoveryArtifact, error) {
	return nil, nil
}
func (*ports) WriteRecovery(context.Context, lifecycle.RecoveryArtifact) error { return nil }
func (*ports) DeleteRecovery(context.Context, domain.OperationID) error        { return nil }
func (*ports) Now() time.Time                                                  { return time.Unix(0, 0).UTC() }
func (*ports) NewOperationID(context.Context) (domain.OperationID, error) {
	return domain.NewOperationID("operation")
}
func (*ports) NewInstallationID(context.Context) (domain.InstallationID, error) {
	return domain.NewInstallationID("installation")
}
func (*ports) NewArtifactToken(context.Context) (domain.ArtifactToken, error) {
	return domain.NewArtifactToken("0123456789abcdef0123456789abcdef")
}

type noopLock struct{}

func (noopLock) Release() error { return nil }

type factoryCalls struct{ total int }

func (c *factoryCalls) port(value *ports) *ports {
	c.total++
	return value
}

func mvpDefinitions(calls *factoryCalls, qualified domain.CapabilitySet) registry.Definitions {
	value := &ports{}
	return registry.Definitions{
		Targets: []registry.TargetRegistration{{
			Target: domain.ClaudeTarget(), CandidateCapabilities: domain.MVPCapabilities(), QualifiedCapabilities: qualified,
			Observer: func() lifecycle.TargetObserver { return calls.port(value) },
			Mutator:  func() lifecycle.TargetMutator { return calls.port(value) },
		}},
		Hosts: []registry.HostRegistration{{
			Host: domain.DarwinHost(), Services: value,
		}},
		Sources: []registry.SourceRegistration{{Mode: domain.GitHubSourceMode(), Acquirer: func() lifecycle.SourceAcquirer { return calls.port(value) }}},
		SourceSelections: []registry.SourceSelectionRegistration{
			{Selection: domain.BuiltInDefaultSource()}, {Selection: domain.ExplicitSource()},
		},
		Scopes:     []registry.ScopeRegistration{{Scope: domain.UserScope()}},
		Selections: []registry.SelectionRegistration{{Mode: domain.WholeToolkitSelection()}},
		States: []registry.StateRegistration{{
			Schema:      domain.MVPStateSchema(),
			Reader:      func() lifecycle.InstallationStateReader { return calls.port(value) },
			Writer:      func() lifecycle.InstallationStateWriter { return calls.port(value) },
			Locks:       func() lifecycle.LockAcquirer { return calls.port(value) },
			Clock:       func() lifecycle.Clock { return calls.port(value) },
			Identifiers: func() lifecycle.IdentifierGenerator { return calls.port(value) },
		}},
		Recoveries: []registry.RecoveryRegistration{{
			Policy:         domain.ShortLivedRecovery(),
			JournalReader:  func() lifecycle.JournalReader { return calls.port(value) },
			JournalWriter:  func() lifecycle.JournalWriter { return calls.port(value) },
			RecoveryReader: func() lifecycle.RecoveryReader { return calls.port(value) },
			RecoveryWriter: func() lifecycle.RecoveryWriter { return calls.port(value) },
		}},
	}
}

func TestMVPRegistryComposesIndependentQualifiedBindings(t *testing.T) {
	t.Parallel()

	calls := new(factoryCalls)
	value, err := registry.NewMVP(mvpDefinitions(calls, domain.MVPCapabilities()))
	if err != nil {
		t.Fatal(err)
	}
	inspection, _ := domain.NewCapabilitySet(domain.InspectionCapability())
	if _, err := value.ResolveRead(registry.MVPSelection(), inspection); err != nil {
		t.Fatalf("ResolveRead() error = %v", err)
	}
	explicit := registry.MVPSelection()
	explicit.SourceSelection = domain.ExplicitSource()
	if _, err := value.ResolveRead(explicit, inspection); err != nil {
		t.Fatalf("explicit-source ResolveRead() error = %v", err)
	}
	update, _ := domain.NewCapabilitySet(domain.UpdateCapability())
	if _, err := value.ResolveMutation(registry.MVPSelection(), update); err != nil {
		t.Fatalf("ResolveMutation() error = %v", err)
	}
	if calls.total != 15 {
		t.Fatalf("factory calls = %d, want 15", calls.total)
	}
}

func TestHostBindingsProjectOneServicesInstance(t *testing.T) {
	t.Parallel()

	calls := new(factoryCalls)
	definitions := mvpDefinitions(calls, domain.MVPCapabilities())
	services := definitions.Hosts[0].Services.(*ports)
	value, err := registry.NewMVP(definitions)
	if err != nil {
		t.Fatal(err)
	}
	update, _ := domain.NewCapabilitySet(domain.UpdateCapability())
	bindings, err := value.ResolveMutation(registry.MVPSelection(), update)
	if err != nil {
		t.Fatal(err)
	}
	inspection, _ := domain.NewCapabilitySet(domain.InspectionCapability())
	read, err := value.ResolveRead(registry.MVPSelection(), inspection)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = read.Host.InspectHost(context.Background(), lifecycle.HostInspectionRequest{})
	request, _ := lifecycle.NewEnvironmentPresenceRequest([]string{"HOME"})
	_, _ = read.Environment.InspectEnvironment(context.Background(), request)
	_ = read.Policy.ResourcePolicy()
	_, _ = read.Resources.CheckResource(context.Background(), lifecycle.ResourceRequest{})
	_, _ = read.Processes.RunProcess(context.Background(), lifecycle.ProcessRequest{})
	_, _ = bindings.Disk.PreflightDisk(context.Background(), lifecycle.DiskPreflightRequest{})
	_, _ = bindings.Files.ReplaceFile(context.Background(), lifecycle.FileMutation{})
	_, _ = bindings.Processes.RunProcess(context.Background(), lifecycle.ProcessRequest{})
	if services.hostCalls != 1 || services.environmentCalls != 1 || services.policyCalls != 1 ||
		services.resourceCalls != 1 || services.diskCalls != 1 || services.fileCalls != 1 || services.processCalls != 2 {
		t.Fatalf("narrow views did not delegate to one services instance: %+v", services)
	}
	assertNarrowHostView(t, "read host", read.Host)
	assertNarrowHostView(t, "read environment", read.Environment)
	assertNarrowHostView(t, "read policy", read.Policy)
	assertNarrowHostView(t, "read resources", read.Resources)
	assertNarrowHostView(t, "read processes", read.Processes)
	assertNarrowHostView(t, "mutation disk", bindings.Disk)
	assertNarrowHostView(t, "mutation files", bindings.Files)
	assertNarrowHostView(t, "mutation processes", bindings.Processes)
}

func assertNarrowHostView(t *testing.T, name string, value any) {
	t.Helper()
	if _, ok := value.(lifecycle.HostServices); ok {
		t.Fatalf("%s exposes full host services", name)
	}
	if _, ok := value.(interface{ Close() error }); ok {
		t.Fatalf("%s exposes close ownership", name)
	}
	if _, isProcess := value.(lifecycle.ProcessRunner); isProcess && name != "read processes" && name != "mutation processes" {
		t.Fatalf("%s exposes process runner", name)
	}
	if _, isFiles := value.(lifecycle.AtomicFileWriter); isFiles && name != "mutation files" {
		t.Fatalf("%s exposes atomic file writer", name)
	}
}

func TestRejectedDimensionsAndCapabilitiesNeverInvokeAnyFactory(t *testing.T) {
	t.Parallel()

	calls := new(factoryCalls)
	empty, _ := domain.NewCapabilitySet()
	value, err := registry.NewMVP(mvpDefinitions(calls, empty))
	if err != nil {
		t.Fatal(err)
	}
	required, _ := domain.NewCapabilitySet(domain.UpdateCapability())
	if _, err := value.ResolveMutation(registry.MVPSelection(), empty); !errors.Is(err, fault.ErrInvalidInput) {
		t.Fatalf("empty-capability ResolveMutation() error = %v", err)
	}
	if _, err := value.ResolveMutation(registry.MVPSelection(), required); !errors.Is(err, fault.ErrUnsupportedCapability) {
		t.Fatalf("candidate-only ResolveMutation() error = %v", err)
	}
	unknownSchema, _ := domain.NewStateSchemaVersion(2)
	selection := registry.MVPSelection()
	selection.StateSchema = unknownSchema
	if _, err := value.ResolveMutation(selection, required); !errors.Is(err, fault.ErrUnsupportedCapability) {
		t.Fatalf("unknown-schema ResolveMutation() error = %v", err)
	}
	unknownTarget, _ := domain.NewTarget("second_target")
	selection = registry.MVPSelection()
	selection.Target = unknownTarget
	if _, err := value.ResolveRead(selection, required); !errors.Is(err, fault.ErrUnsupportedCapability) {
		t.Fatalf("unknown-target ResolveRead() error = %v", err)
	}
	unknownSourceSelection, _ := domain.NewSourceSelection("future_source")
	selection = registry.MVPSelection()
	selection.SourceSelection = unknownSourceSelection
	if _, err := value.ResolveRead(selection, required); !errors.Is(err, fault.ErrUnsupportedCapability) {
		t.Fatalf("unknown-source-selection ResolveRead() error = %v", err)
	}
	if calls.total != 0 {
		t.Fatalf("rejected lookup invoked %d factories", calls.total)
	}
}

func TestRegistryRejectsDuplicateMissingAndPostMVPDimensions(t *testing.T) {
	t.Parallel()

	calls := new(factoryCalls)
	definitions := mvpDefinitions(calls, domain.MVPCapabilities())
	definitions.Hosts = append(definitions.Hosts, definitions.Hosts[0])
	if _, err := registry.New(definitions); !errors.Is(err, fault.ErrInvalidInput) {
		t.Fatalf("duplicate host error = %v", err)
	}
	if _, err := registry.New(registry.Definitions{}); !errors.Is(err, fault.ErrInvalidInput) {
		t.Fatalf("missing definitions error = %v", err)
	}
	definitions = mvpDefinitions(calls, domain.MVPCapabilities())
	secondTarget, _ := domain.NewTarget("second_target")
	definitions.Targets = append(definitions.Targets, registry.TargetRegistration{
		Target: secondTarget, CandidateCapabilities: domain.MVPCapabilities(), QualifiedCapabilities: domain.MVPCapabilities(),
		Observer: definitions.Targets[0].Observer, Mutator: definitions.Targets[0].Mutator,
	})
	if _, err := registry.NewMVP(definitions); !errors.Is(err, fault.ErrUnsupportedCapability) {
		t.Fatalf("post-MVP target error = %v", err)
	}
	if calls.total != 0 {
		t.Fatalf("registry construction invoked %d factories", calls.total)
	}
}
