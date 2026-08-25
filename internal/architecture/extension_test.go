package architecture_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/registry"
	"github.com/alx4j/ai4j/internal/testkit"
)

func TestRepresentativeAllFakeOrchestrationIsRepeatableAndExtensible(t *testing.T) {
	t.Parallel()

	var baseline []string
	for run := 0; run < 3; run++ {
		fixture := newExtensionFixture(t)
		trace := append(
			runRepresentative(t, fixture.registry, fixture.mvp),
			runRepresentative(t, fixture.registry, fixture.other)...,
		)
		fixture.assertAllPortsObserved(t)
		if run == 0 {
			baseline = trace
			continue
		}
		if !reflect.DeepEqual(trace, baseline) {
			t.Fatalf("run %d trace = %v, want %v", run, trace, baseline)
		}
	}
}

func TestRepresentativeMutationStopsBeforeTargetOnUnknownOrInsufficientDisk(t *testing.T) {
	t.Parallel()

	for name, capacity := range map[string]lifecycle.FilesystemCapacity{
		"unknown":      {Identity: 1, Required: 1},
		"insufficient": {Identity: 1, Required: 2, Available: 1, Known: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			preflight, err := lifecycle.NewDiskPreflightResult([]lifecycle.FilesystemCapacity{capacity})
			if err != nil {
				t.Fatal(err)
			}
			host := testkit.NewHost(nil, testkit.HostScripts{
				Preflights: []testkit.Result[lifecycle.DiskPreflightResult]{{Value: preflight}},
			})
			target := testkit.NewTarget(nil, nil, []testkit.Result[lifecycle.TargetMutationResult]{{Value: lifecycle.TargetMutationResult{Changed: true}}})
			operation, _ := domain.NewOperationID("disk-preflight")
			if err := preflightThenMutate(context.Background(), host, target,
				lifecycle.DiskPreflightRequest{TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.StateRoot, Bytes: 1}},
				lifecycle.TargetMutationRequest{OperationID: operation, Target: domain.ClaudeTarget(), Scope: domain.UserScope(), Kind: lifecycle.UpdateMutation()},
			); err == nil {
				t.Fatal("unsafe disk decision permitted target mutation")
			}
			if len(target.MutationCalls()) != 0 {
				t.Fatal("target mutation occurred after an unsafe disk decision")
			}
		})
	}
}

type extensionFixture struct {
	registry  registry.Registry
	mvp       registry.Selection
	other     registry.Selection
	targets   []*testkit.Target
	hosts     []*testkit.Host
	source    *testkit.Source
	state     *testkit.State
	lock      *testkit.Lock
	locks     []*testkit.LockHandle
	journal   *testkit.Journal
	recovery  *testkit.Recovery
	snapshots []*testkit.Snapshot
}

func newExtensionFixture(t *testing.T) extensionFixture {
	t.Helper()
	secondTarget, _ := domain.NewTarget("second_target")
	secondHost, _ := domain.NewHost("second_host")
	repository, _ := domain.NewRepositoryIdentity("github.com/alx4j/ai4j")
	commitOID, _ := domain.NewCommitOID("0123456789abcdef0123456789abcdef01234567")
	commit, _ := domain.NewCommitIdentity(repository, domain.SHA1ObjectFormat(), commitOID)
	tree, _ := domain.NewTreeOID("89abcdef0123456789abcdef0123456789abcdef")
	digest, _ := domain.NewRenderedDigest("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	operations := make([]domain.OperationID, 2)
	installations := make([]domain.InstallationID, 2)
	tokens := make([]domain.ArtifactToken, 2)
	for index := range operations {
		operations[index], _ = domain.NewOperationID(fmt.Sprintf("operation-%d", index+1))
		installations[index], _ = domain.NewInstallationID(fmt.Sprintf("installation-%d", index+1))
		tokens[index], _ = domain.NewArtifactToken(fmt.Sprintf("%032x", index+1))
	}

	capabilities := domain.MVPCapabilities()
	targets := []*testkit.Target{
		testkit.NewTarget(nil,
			[]testkit.Result[lifecycle.TargetObservation]{{Value: lifecycle.TargetObservation{Target: domain.ClaudeTarget(), Capabilities: capabilities}}},
			[]testkit.Result[lifecycle.TargetMutationResult]{{Value: lifecycle.TargetMutationResult{Changed: true}}}),
		testkit.NewTarget(nil,
			[]testkit.Result[lifecycle.TargetObservation]{{Value: lifecycle.TargetObservation{Target: secondTarget, Capabilities: capabilities}}},
			[]testkit.Result[lifecycle.TargetMutationResult]{{Value: lifecycle.TargetMutationResult{Changed: true}}}),
	}
	hosts := []*testkit.Host{
		newScriptedHost(domain.DarwinHost(), digest),
		newScriptedHost(secondHost, digest),
	}
	snapshots := []*testkit.Snapshot{
		testkit.NewSnapshot("snapshot-1", commit, tree, nil),
		testkit.NewSnapshot("snapshot-2", commit, tree, nil),
	}
	source := testkit.NewSource(nil, []testkit.Result[lifecycle.SourceSnapshot]{
		{Value: snapshots[0]}, {Value: snapshots[1]},
	})
	stateReads := make([]testkit.Result[testkit.StateRead], 2)
	unitResults := []testkit.Result[struct{}]{{}, {}}
	state := testkit.NewState(nil, stateReads, unitResults, unitResults)
	lockHandles := []*testkit.LockHandle{testkit.NewLockHandle(nil), testkit.NewLockHandle(nil)}
	lock := testkit.NewLock(nil, []testkit.Result[lifecycle.LockHandle]{{Value: lockHandles[0]}, {Value: lockHandles[1]}})
	journal := testkit.NewJournal(nil,
		[]testkit.Result[testkit.JournalRead]{{}, {}}, unitResults, unitResults)
	recovery := testkit.NewRecovery(nil,
		[]testkit.Result[[]lifecycle.RecoveryArtifact]{{}, {}}, unitResults, unitResults)
	clock := testkit.NewClock(time.Unix(1, 0).UTC(), time.Unix(2, 0).UTC())
	identifiers := testkit.NewIdentifiers(nil,
		[]testkit.Result[domain.OperationID]{{Value: operations[0]}, {Value: operations[1]}},
		[]testkit.Result[domain.InstallationID]{{Value: installations[0]}, {Value: installations[1]}},
		[]testkit.Result[domain.ArtifactToken]{{Value: tokens[0]}, {Value: tokens[1]}},
	)

	definitions := registry.Definitions{
		Targets: []registry.TargetRegistration{
			targetRegistration(domain.ClaudeTarget(), targets[0], capabilities),
			targetRegistration(secondTarget, targets[1], capabilities),
		},
		Hosts: []registry.HostRegistration{
			hostRegistration(domain.DarwinHost(), hosts[0]),
			hostRegistration(secondHost, hosts[1]),
		},
		Sources: []registry.SourceRegistration{{Mode: domain.GitHubSourceMode(), Acquirer: func() lifecycle.SourceAcquirer { return source }}},
		SourceSelections: []registry.SourceSelectionRegistration{
			{Selection: domain.BuiltInDefaultSource()}, {Selection: domain.ExplicitSource()},
		},
		Scopes:     []registry.ScopeRegistration{{Scope: domain.UserScope()}},
		Selections: []registry.SelectionRegistration{{Mode: domain.WholeToolkitSelection()}},
		States: []registry.StateRegistration{{
			Schema: domain.MVPStateSchema(), Reader: func() lifecycle.InstallationStateReader { return state },
			Writer: func() lifecycle.InstallationStateWriter { return state }, Locks: func() lifecycle.LockAcquirer { return lock },
			Clock: func() lifecycle.Clock { return clock }, Identifiers: func() lifecycle.IdentifierGenerator { return identifiers },
		}},
		Recoveries: []registry.RecoveryRegistration{{
			Policy: domain.ShortLivedRecovery(), JournalReader: func() lifecycle.JournalReader { return journal },
			JournalWriter: func() lifecycle.JournalWriter { return journal }, RecoveryReader: func() lifecycle.RecoveryReader { return recovery },
			RecoveryWriter: func() lifecycle.RecoveryWriter { return recovery },
		}},
	}
	registryValue, err := registry.New(definitions)
	if err != nil {
		t.Fatal(err)
	}
	mvp := registry.MVPSelection()
	other := mvp
	other.Target = secondTarget
	other.Host = secondHost
	return extensionFixture{
		registry: registryValue, mvp: mvp, other: other, targets: targets, hosts: hosts,
		source: source, state: state, lock: lock, locks: lockHandles, journal: journal,
		recovery: recovery, snapshots: snapshots,
	}
}

func targetRegistration(value domain.Target, fake *testkit.Target, capabilities domain.CapabilitySet) registry.TargetRegistration {
	return registry.TargetRegistration{
		Target: value, CandidateCapabilities: capabilities, QualifiedCapabilities: capabilities,
		Observer: func() lifecycle.TargetObserver { return fake }, Mutator: func() lifecycle.TargetMutator { return fake },
	}
}

func hostRegistration(value domain.Host, fake *testkit.Host) registry.HostRegistration {
	return registry.HostRegistration{Host: value, Services: fake}
}

func newScriptedHost(value domain.Host, digest domain.RenderedDigest) *testkit.Host {
	version, _ := lifecycle.NewResourcePolicyVersion("mvp_resource_v1")
	policy, _ := lifecycle.NewHostResourcePolicy(version, 5*time.Minute, 2*time.Minute)
	presence, _ := lifecycle.NewEnvironmentPresenceResult([]lifecycle.EnvironmentPresence{{Name: "HOME", Present: true}})
	return testkit.NewHost(nil, testkit.HostScripts{
		Inspections: []testkit.Result[lifecycle.HostObservation]{{Value: lifecycle.HostObservation{Host: value}}},
		Resources:   []testkit.Result[lifecycle.ResourceObservation]{{Value: lifecycle.ResourceObservation{Exists: true, Kind: lifecycle.RegularResource}}},
		Reads:       []testkit.Result[lifecycle.ResourceReadResult]{{Value: lifecycle.ResourceReadResult{Content: []byte("owned")}}},
		Executables: []testkit.Result[lifecycle.ExecutableObservation]{{Value: lifecycle.ExecutableObservation{
			ResolvedPath: "/usr/bin/native-manager", Authority: lifecycle.TrustedUserOrSystemAuthority,
		}}},
		Preflights:          []testkit.Result[lifecycle.DiskPreflightResult]{{Value: mustDiskPreflightResult()}},
		Files:               []testkit.Result[lifecycle.FileMutationResult]{{Value: lifecycle.FileMutationResult{Digest: digest}}},
		Cleanups:            []testkit.Result[lifecycle.FileCleanupResult]{{Value: lifecycle.FileCleanupResult{Cleanup: lifecycle.CleanupComplete}}},
		ArtifactInspections: []testkit.Result[lifecycle.FileArtifactInspectionResult]{{Value: lifecycle.FileArtifactInspectionResult{}}},
		Processes:           []testkit.Result[lifecycle.ProcessResult]{{Value: lifecycle.ProcessResult{Started: true, Exited: true, ExitCode: 0, Stdout: mustProcessStream("ok")}}},
		Environment:         []testkit.Result[lifecycle.EnvironmentPresenceResult]{{Value: presence}},
		Policy:              policy,
	})
}

func mustProcessStream(value string) lifecycle.ProcessStream {
	stream, ok := lifecycle.NewProcessStream(lifecycle.SanitizedTextOutput, []byte(value), false)
	if !ok {
		panic("valid process stream rejected")
	}
	return stream
}

func mustDiskPreflightResult() lifecycle.DiskPreflightResult {
	result, err := lifecycle.NewDiskPreflightResult([]lifecycle.FilesystemCapacity{{
		Identity: 1, Required: 1, Available: 1, Known: true,
	}})
	if err != nil {
		panic(err)
	}
	return result
}

func runRepresentative(t *testing.T, value registry.Registry, selection registry.Selection) []string {
	t.Helper()
	ctx := context.Background()
	inspection, _ := domain.NewCapabilitySet(domain.InspectionCapability())
	read, err := value.ResolveRead(selection, inspection)
	if err != nil {
		t.Fatal(err)
	}
	targetObservation, err := read.Target.ObserveTarget(ctx, lifecycle.TargetObservationRequest{Target: selection.Target, Scope: selection.Scope})
	if err != nil {
		t.Fatal(err)
	}
	hostObservation, err := read.Host.InspectHost(ctx, lifecycle.HostInspectionRequest{Host: selection.Host})
	if err != nil {
		t.Fatal(err)
	}
	presenceRequest, _ := lifecycle.NewEnvironmentPresenceRequest([]string{"HOME"})
	if presence, presenceErr := read.Environment.InspectEnvironment(ctx, presenceRequest); presenceErr != nil || !presence.Coherent() {
		t.Fatalf("environment presence = %+v, %v", presence.Values(), presenceErr)
	}
	if !read.Policy.ResourcePolicy().Valid() {
		t.Fatal("invalid host resource policy")
	}
	if _, err := read.Resources.CheckResource(ctx, lifecycle.ResourceRequest{Root: lifecycle.StateRoot, Path: "owned/file", Kind: lifecycle.RegularResource}); err != nil {
		t.Fatal(err)
	}
	if _, err := read.Resources.ReadResource(ctx, lifecycle.ResourceReadRequest{Resource: lifecycle.ResourceRequest{Root: lifecycle.StateRoot, Path: "owned/file", Kind: lifecycle.RegularResource}, MaxBytes: 64}); err != nil {
		t.Fatal(err)
	}
	if _, err := read.Resources.CheckExecutable(ctx, lifecycle.ExecutableRequest{
		Candidate: "native-manager", Authority: lifecycle.TrustedUserOrSystemAuthority,
	}); err != nil {
		t.Fatal(err)
	}
	repository, _ := domain.NewRepositoryIdentity("github.com/alx4j/ai4j")
	snapshot, err := read.Source.AcquireSource(ctx, lifecycle.SourceRequest{Mode: selection.SourceMode, Repository: repository, Reference: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := read.State.ReadInstallation(ctx, lifecycle.InstallationKey{Target: selection.Target, Scope: selection.Scope}); err != nil {
		t.Fatal(err)
	}

	update, _ := domain.NewCapabilitySet(domain.UpdateCapability())
	mutation, err := value.ResolveMutation(selection, update)
	if err != nil {
		t.Fatal(err)
	}
	operationID, err := mutation.Identifiers.NewOperationID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	installationID, err := mutation.Identifiers.NewInstallationID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	artifactToken, err := mutation.Identifiers.NewArtifactToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = mutation.Clock.Now()
	lock, err := mutation.Locks.AcquireLock(ctx, lifecycle.LockRequest{Target: selection.Target, Scope: selection.Scope})
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := domain.NewRenderedDigest("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	artifactPlan, _ := lifecycle.PlanFileArtifacts(operationID, artifactToken)
	if err := preflightThenMutate(ctx, mutation.Disk, mutation.Target,
		lifecycle.DiskPreflightRequest{TemporarySource: lifecycle.DiskAllocation{Root: lifecycle.ManagedOutputRoot, Bytes: 1}},
		lifecycle.TargetMutationRequest{OperationID: operationID, Target: selection.Target, Scope: selection.Scope, Kind: lifecycle.UpdateMutation(), Package: digest},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.Files.ReplaceFile(ctx, lifecycle.FileMutation{OperationID: operationID, ArtifactToken: artifactToken, Artifacts: artifactPlan, Root: lifecycle.StateRoot, Destination: "owned/file", Content: []byte("content"), Expected: lifecycle.FileExpectation{State: lifecycle.ExpectAbsent}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.Files.CleanupFile(ctx, lifecycle.CleanupArtifact{}); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.Files.InspectFileArtifacts(ctx, lifecycle.FileArtifactInspectionRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.Processes.RunProcess(ctx, lifecycle.ProcessRequest{Executable: "native-manager"}); err != nil {
		t.Fatal(err)
	}
	record := lifecycle.InstallationRecord{Schema: selection.StateSchema, InstallationID: installationID, Target: selection.Target, Host: selection.Host, Scope: selection.Scope, SourceMode: selection.SourceMode, Selection: selection.SelectionMode}
	if err := mutation.State.WriteInstallation(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := mutation.State.DeleteInstallation(ctx, lifecycle.InstallationKey{Target: selection.Target, Scope: selection.Scope}); err != nil {
		t.Fatal(err)
	}
	journalRecord := lifecycle.JournalRecord{OperationID: operationID, InstallationID: installationID, Phase: "prepared"}
	if _, _, err := mutation.JournalReader.ReadJournal(ctx, installationID); err != nil {
		t.Fatal(err)
	}
	if err := mutation.JournalWriter.WriteJournal(ctx, journalRecord); err != nil {
		t.Fatal(err)
	}
	if err := mutation.JournalWriter.DeleteJournal(ctx, operationID); err != nil {
		t.Fatal(err)
	}
	artifact := lifecycle.RecoveryArtifact{OperationID: operationID, Name: "owned", Digest: digest}
	if _, err := mutation.RecoveryReader.ReadRecovery(ctx, operationID); err != nil {
		t.Fatal(err)
	}
	if err := mutation.RecoveryWriter.WriteRecovery(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	if err := mutation.RecoveryWriter.DeleteRecovery(ctx, operationID); err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	return []string{targetObservation.Target.String(), hostObservation.Host.String(), operationID.String(), installationID.String()}
}

func preflightThenMutate(
	ctx context.Context,
	disk lifecycle.DiskPreflighter,
	target lifecycle.TargetMutator,
	diskRequest lifecycle.DiskPreflightRequest,
	targetRequest lifecycle.TargetMutationRequest,
) error {
	result, err := disk.PreflightDisk(ctx, diskRequest)
	if err != nil {
		return err
	}
	if !result.Coherent() || !result.Sufficient {
		return fmt.Errorf("disk preflight did not establish sufficient capacity")
	}
	_, err = target.MutateTarget(ctx, targetRequest)
	return err
}

func (f extensionFixture) assertAllPortsObserved(t *testing.T) {
	t.Helper()
	for index := range f.targets {
		if len(f.targets[index].ObserveCalls()) != 1 || len(f.targets[index].MutationCalls()) != 1 {
			t.Fatalf("target %d was not observed and mutated exactly once", index)
		}
		if len(f.hosts[index].InspectCalls()) != 1 || len(f.hosts[index].ResourceCalls()) != 1 ||
			len(f.hosts[index].ReadCalls()) != 1 || len(f.hosts[index].ExecutableCalls()) != 1 || len(f.hosts[index].PreflightCalls()) != 1 ||
			len(f.hosts[index].FileCalls()) != 1 || len(f.hosts[index].CleanupCalls()) != 1 || len(f.hosts[index].ArtifactInspectionCalls()) != 1 || len(f.hosts[index].ProcessCalls()) != 1 {
			t.Fatalf("host %d ports were not all observed exactly once", index)
		}
		if f.locks[index].Releases() != 1 || f.snapshots[index].CloseCount() != 1 {
			t.Fatalf("owned handles %d were not released exactly once", index)
		}
	}
	if len(f.source.Calls()) != 2 || len(f.state.ReadCalls()) != 2 || len(f.state.WriteCalls()) != 2 ||
		len(f.state.DeleteCalls()) != 2 || len(f.lock.Calls()) != 2 || len(f.journal.WriteCalls()) != 2 ||
		len(f.recovery.WriteCalls()) != 2 {
		t.Fatal("shared source/state/coordination ports were not all observed twice")
	}
}
