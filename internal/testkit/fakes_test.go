package testkit_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/testkit"
)

func TestEveryBlockingFakePropagatesCallerCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target := testkit.NewTarget(nil, nil, nil)
	host := testkit.NewHost(nil, testkit.HostScripts{})
	source := testkit.NewSource(nil, nil)
	state := testkit.NewState(nil, nil, nil, nil)
	lock := testkit.NewLock(nil, nil)
	journal := testkit.NewJournal(nil, nil, nil, nil)
	recovery := testkit.NewRecovery(nil, nil, nil, nil)
	identifiers := testkit.NewIdentifiers(nil, nil, nil, nil)

	assertCancelled(t, func() error { _, err := target.ObserveTarget(ctx, lifecycle.TargetObservationRequest{}); return err })
	assertCancelled(t, func() error { _, err := target.MutateTarget(ctx, lifecycle.TargetMutationRequest{}); return err })
	assertCancelled(t, func() error { _, err := host.InspectHost(ctx, lifecycle.HostInspectionRequest{}); return err })
	assertCancelled(t, func() error {
		_, err := host.InspectEnvironment(ctx, lifecycle.EnvironmentPresenceRequest{})
		return err
	})
	assertCancelled(t, func() error { _, err := host.ReplaceFile(ctx, lifecycle.FileMutation{}); return err })
	assertCancelled(t, func() error { _, err := host.CleanupFile(ctx, lifecycle.CleanupArtifact{}); return err })
	assertCancelled(t, func() error {
		_, err := host.InspectFileArtifacts(ctx, lifecycle.FileArtifactInspectionRequest{})
		return err
	})
	assertCancelled(t, func() error { _, err := host.RunProcess(ctx, lifecycle.ProcessRequest{}); return err })
	assertCancelled(t, func() error { _, err := host.CheckResource(ctx, lifecycle.ResourceRequest{}); return err })
	assertCancelled(t, func() error { _, err := host.ReadResource(ctx, lifecycle.ResourceReadRequest{}); return err })
	assertCancelled(t, func() error { _, err := host.CheckExecutable(ctx, lifecycle.ExecutableRequest{}); return err })
	assertCancelled(t, func() error { _, err := host.PreflightDisk(ctx, lifecycle.DiskPreflightRequest{}); return err })
	assertCancelled(t, func() error { _, err := source.AcquireSource(ctx, lifecycle.SourceRequest{}); return err })
	assertCancelled(t, func() error { _, _, err := state.ReadInstallation(ctx, lifecycle.InstallationKey{}); return err })
	assertCancelled(t, func() error { return state.WriteInstallation(ctx, lifecycle.InstallationRecord{}) })
	assertCancelled(t, func() error { return state.DeleteInstallation(ctx, lifecycle.InstallationKey{}) })
	assertCancelled(t, func() error { _, err := lock.AcquireLock(ctx, lifecycle.LockRequest{}); return err })
	assertCancelled(t, func() error { _, _, err := journal.ReadJournal(ctx, domain.InstallationID{}); return err })
	assertCancelled(t, func() error { return journal.WriteJournal(ctx, lifecycle.JournalRecord{}) })
	assertCancelled(t, func() error { return journal.DeleteJournal(ctx, domain.OperationID{}) })
	assertCancelled(t, func() error { _, err := recovery.ReadRecovery(ctx, domain.OperationID{}); return err })
	assertCancelled(t, func() error { return recovery.WriteRecovery(ctx, lifecycle.RecoveryArtifact{}) })
	assertCancelled(t, func() error { return recovery.DeleteRecovery(ctx, domain.OperationID{}) })
	assertCancelled(t, func() error { _, err := identifiers.NewOperationID(ctx); return err })
	assertCancelled(t, func() error { _, err := identifiers.NewInstallationID(ctx); return err })
	assertCancelled(t, func() error { _, err := identifiers.NewArtifactToken(ctx); return err })
}

func assertCancelled(t *testing.T, call func() error) {
	t.Helper()
	if err := call(); !errors.Is(err, context.Canceled) {
		t.Fatalf("call error = %v, want context.Canceled", err)
	}
}

func TestFakesUseCallerSuppliedSequencesAndDefensiveObservations(t *testing.T) {
	t.Parallel()

	operation, _ := domain.NewOperationID("operation-1")
	installation, _ := domain.NewInstallationID("installation-1")
	token, _ := domain.NewArtifactToken("0123456789abcdef0123456789abcdef")
	digest, _ := domain.NewRenderedDigest("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	target := testkit.NewTarget(nil,
		[]testkit.Result[lifecycle.TargetObservation]{{Value: lifecycle.TargetObservation{Target: domain.ClaudeTarget(), Capabilities: domain.MVPCapabilities()}}},
		[]testkit.Result[lifecycle.TargetMutationResult]{{Value: lifecycle.TargetMutationResult{Changed: true}}},
	)
	host := testkit.NewHost(nil, testkit.HostScripts{
		Files: []testkit.Result[lifecycle.FileMutationResult]{{Value: lifecycle.FileMutationResult{Digest: digest}}},
	})
	clock := testkit.NewClock(time.Unix(1, 0).UTC(), time.Unix(2, 0).UTC())
	identifiers := testkit.NewIdentifiers(nil,
		[]testkit.Result[domain.OperationID]{{Value: operation}},
		[]testkit.Result[domain.InstallationID]{{Value: installation}},
		[]testkit.Result[domain.ArtifactToken]{{Value: token}},
	)

	ctx := context.Background()
	if _, err := target.ObserveTarget(ctx, lifecycle.TargetObservationRequest{Target: domain.ClaudeTarget(), Scope: domain.UserScope()}); err != nil {
		t.Fatal(err)
	}
	content := []byte("owned")
	if _, err := host.ReplaceFile(ctx, lifecycle.FileMutation{OperationID: operation, Content: content}); err != nil {
		t.Fatal(err)
	}
	content[0] = 'X'
	calls := host.FileCalls()
	if string(calls[0].Content) != "owned" {
		t.Fatalf("recorded content changed through caller alias: %q", calls[0].Content)
	}
	calls[0].Content[0] = 'Y'
	if got := string(host.FileCalls()[0].Content); got != "owned" {
		t.Fatalf("recorded content changed through observation alias: %q", got)
	}
	if got := clock.Now(); !got.Equal(time.Unix(1, 0).UTC()) {
		t.Fatalf("first clock value = %v", got)
	}
	if got, err := identifiers.NewOperationID(ctx); err != nil || got != operation {
		t.Fatalf("operation ID = %v, error = %v", got, err)
	}
	if got, err := identifiers.NewInstallationID(ctx); err != nil || got != installation {
		t.Fatalf("installation ID = %v, error = %v", got, err)
	}
	if got, err := identifiers.NewArtifactToken(ctx); err != nil || got != token {
		t.Fatalf("NewArtifactToken() = %v, %v", got, err)
	}
	if _, err := identifiers.NewOperationID(ctx); !errors.Is(err, testkit.ErrScriptExhausted) {
		t.Fatalf("exhausted operation sequence error = %v", err)
	}
}

func TestGateCanBeCancelledWithoutSleep(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	target := testkit.NewTarget(gate, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := target.ObserveTarget(ctx, lifecycle.TargetObservationRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("gated observation error = %v", err)
	}
}

func TestMutableScriptValuesAreCopiedOnIngressAndEgress(t *testing.T) {
	t.Parallel()

	output := []byte("original")
	stream, ok := lifecycle.NewProcessStream(lifecycle.SanitizedTextOutput, output, false)
	if !ok {
		t.Fatal("valid process stream rejected")
	}
	host := testkit.NewHost(nil, testkit.HostScripts{
		Processes: []testkit.Result[lifecycle.ProcessResult]{
			{Value: lifecycle.ProcessResult{Stdout: stream}}, {Value: lifecycle.ProcessResult{Stdout: stream}},
		},
	})
	artifacts := []lifecycle.RecoveryArtifact{{Name: "original"}}
	recovery := testkit.NewRecovery(nil,
		[]testkit.Result[[]lifecycle.RecoveryArtifact]{{Value: artifacts}, {Value: artifacts}}, nil, nil)
	output[0] = 'X'
	artifacts[0].Name = "changed"

	process, err := host.RunProcess(context.Background(), lifecycle.ProcessRequest{})
	if err != nil {
		t.Fatal(err)
	}
	read, err := recovery.ReadRecovery(context.Background(), domain.OperationID{})
	if err != nil {
		t.Fatal(err)
	}
	processOutput, _ := process.Stdout.SanitizedText()
	if processOutput != "original" || read[0].Name != "original" {
		t.Fatalf("script aliases leaked: output %q, artifact %q", processOutput, read[0].Name)
	}
	read[0].Name = "again"
	secondProcess, err := host.RunProcess(context.Background(), lifecycle.ProcessRequest{})
	if err != nil {
		t.Fatal(err)
	}
	secondRead, err := recovery.ReadRecovery(context.Background(), domain.OperationID{})
	if err != nil {
		t.Fatal(err)
	}
	secondOutput, _ := secondProcess.Stdout.SanitizedText()
	if secondOutput != "original" || secondRead[0].Name != "original" {
		t.Fatalf("egress aliases leaked: output %q, artifact %q", secondOutput, secondRead[0].Name)
	}
}

func TestConcurrentFakeCallsAreRaceSafe(t *testing.T) {
	t.Parallel()

	const calls = 32
	results := make([]testkit.Result[lifecycle.ProcessResult], calls)
	for index := range results {
		results[index].Value.Stdout, _ = lifecycle.NewProcessStream(lifecycle.SanitizedTextOutput, []byte("ok"), false)
	}
	host := testkit.NewHost(nil, testkit.HostScripts{Processes: results})
	var group sync.WaitGroup
	errorsChannel := make(chan error, calls)
	for index := 0; index < calls; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := host.RunProcess(context.Background(), lifecycle.ProcessRequest{Arguments: []string{"arg"}})
			errorsChannel <- err
		}()
	}
	group.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("RunProcess() error = %v", err)
		}
	}
	if got := len(host.ProcessCalls()); got != calls {
		t.Fatalf("process call count = %d, want %d", got, calls)
	}
}
