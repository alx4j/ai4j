package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

type fakeBundleServices struct {
	mu             sync.Mutex
	closeCalls     int
	closeErr       error
	resourceCalls  int
	resourceStart  chan struct{}
	resourceFinish chan struct{}
}

func (f *fakeBundleServices) InspectHost(context.Context, lifecycle.HostInspectionRequest) (lifecycle.HostObservation, error) {
	return lifecycle.HostObservation{}, nil
}
func (f *fakeBundleServices) InspectEnvironment(context.Context, lifecycle.EnvironmentPresenceRequest) (lifecycle.EnvironmentPresenceResult, error) {
	request, _ := lifecycle.NewEnvironmentPresenceResult([]lifecycle.EnvironmentPresence{{Name: "HOME"}})
	return request, nil
}
func (f *fakeBundleServices) CheckResource(context.Context, lifecycle.ResourceRequest) (lifecycle.ResourceObservation, error) {
	f.mu.Lock()
	f.resourceCalls++
	started, finish := f.resourceStart, f.resourceFinish
	f.mu.Unlock()
	if started != nil {
		close(started)
	}
	if finish != nil {
		<-finish
	}
	return lifecycle.ResourceObservation{}, nil
}
func (*fakeBundleServices) ReadResource(context.Context, lifecycle.ResourceReadRequest) (lifecycle.ResourceReadResult, error) {
	return lifecycle.ResourceReadResult{}, nil
}
func (*fakeBundleServices) CheckExecutable(context.Context, lifecycle.ExecutableRequest) (lifecycle.ExecutableObservation, error) {
	return lifecycle.ExecutableObservation{}, nil
}
func (*fakeBundleServices) PreflightDisk(context.Context, lifecycle.DiskPreflightRequest) (lifecycle.DiskPreflightResult, error) {
	return lifecycle.NewDiskPreflightResult([]lifecycle.FilesystemCapacity{{Identity: 1, Required: 1, Available: 1, Known: true}})
}
func (*fakeBundleServices) ReplaceFile(context.Context, lifecycle.FileMutation) (lifecycle.FileMutationResult, error) {
	return lifecycle.FileMutationResult{}, nil
}
func (*fakeBundleServices) CleanupFile(context.Context, lifecycle.CleanupArtifact) (lifecycle.FileCleanupResult, error) {
	return lifecycle.FileCleanupResult{}, nil
}
func (*fakeBundleServices) InspectFileArtifacts(context.Context, lifecycle.FileArtifactInspectionRequest) (lifecycle.FileArtifactInspectionResult, error) {
	return lifecycle.FileArtifactInspectionResult{}, nil
}
func (*fakeBundleServices) RunProcess(context.Context, lifecycle.ProcessRequest) (lifecycle.ProcessResult, error) {
	return lifecycle.ProcessResult{}, nil
}
func (f *fakeBundleServices) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return f.closeErr
}

func TestBundleOwnsOneConcurrentCloseAndRedactsCloseFailure(t *testing.T) {
	canary := "AI4J_BUNDLE_CLOSE_CANARY"
	services := &fakeBundleServices{closeErr: errors.New(canary)}
	bundle := mustTestBundle(t, services)
	const callers = 32
	errorsSeen := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsSeen <- bundle.Close()
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if !errors.Is(err, errBundleClose) || strings.Contains(fmt.Sprint(err), canary) {
			t.Fatalf("Close() error = %v", err)
		}
	}
	if services.closeCalls != 1 {
		t.Fatalf("underlying close calls = %d", services.closeCalls)
	}
	if err := (*Bundle)(nil).Close(); !errors.Is(err, errInvalidBundle) {
		t.Fatalf("nil Close() error = %v", err)
	}
}

func TestZeroBundleFailsEveryOperationWithoutPanic(t *testing.T) {
	zero := new(Bundle)
	ctx := context.Background()
	request, _ := lifecycle.NewEnvironmentPresenceRequest([]string{"HOME"})
	checks := []func() error{
		func() error { _, err := zero.InspectHost(ctx, lifecycle.HostInspectionRequest{}); return err },
		func() error { _, err := zero.InspectEnvironment(ctx, request); return err },
		func() error { _, err := zero.CheckResource(ctx, lifecycle.ResourceRequest{}); return err },
		func() error { _, err := zero.ReadResource(ctx, lifecycle.ResourceReadRequest{}); return err },
		func() error { _, err := zero.CheckExecutable(ctx, lifecycle.ExecutableRequest{}); return err },
		func() error { _, err := zero.PreflightDisk(ctx, lifecycle.DiskPreflightRequest{}); return err },
		func() error { _, err := zero.ReplaceFile(ctx, lifecycle.FileMutation{}); return err },
		func() error { _, err := zero.CleanupFile(ctx, lifecycle.CleanupArtifact{}); return err },
		func() error {
			_, err := zero.InspectFileArtifacts(ctx, lifecycle.FileArtifactInspectionRequest{})
			return err
		},
		func() error { _, err := zero.RunProcess(ctx, lifecycle.ProcessRequest{}); return err },
		zero.Close,
	}
	for index, check := range checks {
		if err := check(); !errors.Is(err, errInvalidBundle) {
			t.Fatalf("zero Bundle operation %d error = %v", index, err)
		}
	}
	if zero.ResourcePolicy().Valid() {
		t.Fatal("zero Bundle returned a valid policy")
	}
}

func TestBundleFormattingNeverReflectsComponents(t *testing.T) {
	canary := "AI4J_BUNDLE_COMPONENT_CANARY"
	services := &fakeBundleServices{closeErr: errors.New(canary)}
	bundle := mustTestBundle(t, services)
	encodedPointer, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	encodedValue, err := json.Marshal(*bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		fmt.Sprintf("%v", bundle), fmt.Sprintf("%+v", bundle), fmt.Sprintf("%#v", bundle),
		fmt.Sprintf("%v", *bundle), fmt.Sprintf("%+v", *bundle), fmt.Sprintf("%#v", *bundle),
		string(encodedPointer), string(encodedValue),
	} {
		if strings.Contains(value, canary) || strings.Contains(value, "fakeBundleServices") {
			t.Fatalf("Bundle formatting disclosed components: %s", value)
		}
	}
}

func TestBundleCloseWaitsForActiveOperationAndBlocksNewWork(t *testing.T) {
	services := &fakeBundleServices{resourceStart: make(chan struct{}), resourceFinish: make(chan struct{})}
	bundle := mustTestBundle(t, services)
	operationDone := make(chan error, 1)
	go func() {
		_, err := bundle.CheckResource(context.Background(), lifecycle.ResourceRequest{})
		operationDone <- err
	}()
	<-services.resourceStart
	closeDone := make(chan error, 1)
	go func() { closeDone <- bundle.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned during active operation: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(services.resourceFinish)
	if err := <-operationDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.CheckResource(context.Background(), lifecycle.ResourceRequest{}); !errors.Is(err, errBundleClosed) {
		t.Fatalf("post-close operation error = %v", err)
	}
	if !bundle.ResourcePolicy().Valid() {
		t.Fatal("immutable resource policy disappeared after Close")
	}
}

func TestBundleRejectsInvalidComponentsAndTypedNil(t *testing.T) {
	services := &fakeBundleServices{}
	parts := testBundleComponents(t, services)
	parts.processes = nil
	if _, err := newBundle(parts); !errors.Is(err, errInvalidBundle) {
		t.Fatalf("nil process component error = %v", err)
	}
	var typedNil *fakeBundleServices
	parts = testBundleComponents(t, services)
	parts.closer = typedNil
	if _, err := newBundle(parts); !errors.Is(err, errInvalidBundle) {
		t.Fatalf("typed-nil closer error = %v", err)
	}
}

func TestMVPBundleDenyCandidatesAreClosedAndCopied(t *testing.T) {
	candidates := mvpDeniedExecutableCandidates()
	wantCandidates := []string{
		"/bin/sh:true", "/bin/zsh:true", "/usr/bin/env:true",
		"/usr/bin/osascript:true", "/bin/bash:false", "/bin/csh:false", "/bin/tcsh:false",
	}
	gotCandidates := make([]string, len(candidates))
	for index, candidate := range candidates {
		gotCandidates[index] = fmt.Sprintf("%s:%t", candidate.path, candidate.required)
	}
	if fmt.Sprint(gotCandidates) != fmt.Sprint(wantCandidates) {
		t.Fatalf("deny candidates = %v", gotCandidates)
	}
	candidates[0].path = "/unsafe"
	if mvpDeniedExecutableCandidates()[0].path != "/bin/sh" {
		t.Fatal("MVP policy helpers exposed mutable shared state")
	}
}

func mustTestBundle(t *testing.T, services *fakeBundleServices) *Bundle {
	t.Helper()
	bundle, err := newBundle(testBundleComponents(t, services))
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func testBundleComponents(t *testing.T, services *fakeBundleServices) bundleComponents {
	t.Helper()
	version, _ := lifecycle.NewResourcePolicyVersion("mvp_resource_v1")
	policy, _ := lifecycle.NewHostResourcePolicy(version, 5*time.Minute, 2*time.Minute)
	return bundleComponents{
		inspector: services, environment: services, resources: services, disk: services,
		files: services, processes: services, policy: policy, closer: services,
	}
}

var _ io.Closer = (*fakeBundleServices)(nil)
