package testkit

import (
	"context"
	"sync"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

type HostScripts struct {
	Inspections         []Result[lifecycle.HostObservation]
	Files               []Result[lifecycle.FileMutationResult]
	Cleanups            []Result[lifecycle.FileCleanupResult]
	ArtifactInspections []Result[lifecycle.FileArtifactInspectionResult]
	Processes           []Result[lifecycle.ProcessResult]
	Resources           []Result[lifecycle.ResourceObservation]
	Reads               []Result[lifecycle.ResourceReadResult]
	Executables         []Result[lifecycle.ExecutableObservation]
	Preflights          []Result[lifecycle.DiskPreflightResult]
	Environment         []Result[lifecycle.EnvironmentPresenceResult]
	Policy              lifecycle.HostResourcePolicy
}

type Host struct {
	mu                      sync.Mutex
	gate                    <-chan struct{}
	inspections             *script[lifecycle.HostObservation]
	files                   *script[lifecycle.FileMutationResult]
	cleanups                *script[lifecycle.FileCleanupResult]
	artifactInspections     *script[lifecycle.FileArtifactInspectionResult]
	processes               *script[lifecycle.ProcessResult]
	resources               *script[lifecycle.ResourceObservation]
	reads                   *script[lifecycle.ResourceReadResult]
	executables             *script[lifecycle.ExecutableObservation]
	preflights              *script[lifecycle.DiskPreflightResult]
	environment             *script[lifecycle.EnvironmentPresenceResult]
	policy                  lifecycle.HostResourcePolicy
	inspectCalls            []lifecycle.HostInspectionRequest
	fileCalls               []lifecycle.FileMutation
	cleanupCalls            []lifecycle.CleanupArtifact
	artifactInspectionCalls []lifecycle.FileArtifactInspectionRequest
	processCalls            []lifecycle.ProcessRequest
	resourceCalls           []lifecycle.ResourceRequest
	readCalls               []lifecycle.ResourceReadRequest
	executableCalls         []lifecycle.ExecutableRequest
	preflightCalls          []lifecycle.DiskPreflightRequest
	environmentCalls        []lifecycle.EnvironmentPresenceRequest
}

func NewHost(gate <-chan struct{}, scripts HostScripts) *Host {
	processes := append([]Result[lifecycle.ProcessResult](nil), scripts.Processes...)
	preflights := append([]Result[lifecycle.DiskPreflightResult](nil), scripts.Preflights...)
	for index := range preflights {
		preflights[index].Value.Filesystems = append([]lifecycle.FilesystemCapacity(nil), preflights[index].Value.Filesystems...)
	}
	reads := append([]Result[lifecycle.ResourceReadResult](nil), scripts.Reads...)
	for index := range reads {
		reads[index].Value.Content = append([]byte(nil), reads[index].Value.Content...)
	}
	return &Host{
		gate: gate, inspections: newScript(scripts.Inspections), files: newScript(scripts.Files), cleanups: newScript(scripts.Cleanups), artifactInspections: newScript(scripts.ArtifactInspections),
		processes: newScript(processes), resources: newScript(scripts.Resources), reads: newScript(reads),
		executables: newScript(scripts.Executables), preflights: newScript(preflights),
		environment: newScript(scripts.Environment), policy: scripts.Policy,
	}
}

func (f *Host) InspectEnvironment(ctx context.Context, request lifecycle.EnvironmentPresenceRequest) (lifecycle.EnvironmentPresenceResult, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.EnvironmentPresenceResult{}, err
	}
	f.mu.Lock()
	f.environmentCalls = append(f.environmentCalls, request)
	f.mu.Unlock()
	return f.environment.nextResult()
}

func (f *Host) ResourcePolicy() lifecycle.HostResourcePolicy { return f.policy }

func (f *Host) InspectFileArtifacts(ctx context.Context, request lifecycle.FileArtifactInspectionRequest) (lifecycle.FileArtifactInspectionResult, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.FileArtifactInspectionResult{}, err
	}
	f.mu.Lock()
	f.artifactInspectionCalls = append(f.artifactInspectionCalls, request)
	f.mu.Unlock()
	result, err := f.artifactInspections.nextResult()
	result.Artifacts = append([]lifecycle.CleanupArtifact(nil), result.Artifacts...)
	result.Conflicts = append([]lifecycle.FileRecoveryConflict(nil), result.Conflicts...)
	return result, err
}

func (f *Host) CleanupFile(ctx context.Context, artifact lifecycle.CleanupArtifact) (lifecycle.FileCleanupResult, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.FileCleanupResult{}, err
	}
	f.mu.Lock()
	f.cleanupCalls = append(f.cleanupCalls, artifact)
	f.mu.Unlock()
	return f.cleanups.nextResult()
}

func (f *Host) ReadResource(ctx context.Context, request lifecycle.ResourceReadRequest) (lifecycle.ResourceReadResult, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.ResourceReadResult{}, err
	}
	f.mu.Lock()
	f.readCalls = append(f.readCalls, request)
	f.mu.Unlock()
	result, err := f.reads.nextResult()
	result.Content = append([]byte(nil), result.Content...)
	return result, err
}

func (f *Host) InspectHost(ctx context.Context, request lifecycle.HostInspectionRequest) (lifecycle.HostObservation, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.HostObservation{}, err
	}
	f.mu.Lock()
	f.inspectCalls = append(f.inspectCalls, request)
	f.mu.Unlock()
	return f.inspections.nextResult()
}

func (f *Host) ReplaceFile(ctx context.Context, request lifecycle.FileMutation) (lifecycle.FileMutationResult, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.FileMutationResult{}, err
	}
	request.Content = append([]byte(nil), request.Content...)
	f.mu.Lock()
	f.fileCalls = append(f.fileCalls, request)
	f.mu.Unlock()
	return f.files.nextResult()
}

func (f *Host) RunProcess(ctx context.Context, request lifecycle.ProcessRequest) (lifecycle.ProcessResult, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.ProcessResult{}, err
	}
	request.Arguments = append([]string(nil), request.Arguments...)
	request.Environment = append([]lifecycle.EnvironmentBinding(nil), request.Environment...)
	f.mu.Lock()
	f.processCalls = append(f.processCalls, request)
	f.mu.Unlock()
	result, err := f.processes.nextResult()
	return result, err
}

func (f *Host) CheckResource(ctx context.Context, request lifecycle.ResourceRequest) (lifecycle.ResourceObservation, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.ResourceObservation{}, err
	}
	f.mu.Lock()
	f.resourceCalls = append(f.resourceCalls, request)
	f.mu.Unlock()
	return f.resources.nextResult()
}

func (f *Host) CheckExecutable(ctx context.Context, request lifecycle.ExecutableRequest) (lifecycle.ExecutableObservation, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.ExecutableObservation{}, err
	}
	f.mu.Lock()
	f.executableCalls = append(f.executableCalls, request)
	f.mu.Unlock()
	return f.executables.nextResult()
}

func (f *Host) PreflightDisk(ctx context.Context, request lifecycle.DiskPreflightRequest) (lifecycle.DiskPreflightResult, error) {
	if err := waitForContext(ctx, f.gate); err != nil {
		return lifecycle.DiskPreflightResult{}, err
	}
	f.mu.Lock()
	f.preflightCalls = append(f.preflightCalls, request)
	f.mu.Unlock()
	result, err := f.preflights.nextResult()
	result.Filesystems = append([]lifecycle.FilesystemCapacity(nil), result.Filesystems...)
	return result, err
}

func (f *Host) FileCalls() []lifecycle.FileMutation {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := append([]lifecycle.FileMutation(nil), f.fileCalls...)
	for index := range result {
		result[index].Content = append([]byte(nil), result[index].Content...)
	}
	return result
}

func (f *Host) CleanupCalls() []lifecycle.CleanupArtifact {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.CleanupArtifact(nil), f.cleanupCalls...)
}

func (f *Host) ArtifactInspectionCalls() []lifecycle.FileArtifactInspectionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.FileArtifactInspectionRequest(nil), f.artifactInspectionCalls...)
}

func (f *Host) InspectCalls() []lifecycle.HostInspectionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.HostInspectionRequest(nil), f.inspectCalls...)
}

func (f *Host) ProcessCalls() []lifecycle.ProcessRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := append([]lifecycle.ProcessRequest(nil), f.processCalls...)
	for index := range result {
		result[index].Arguments = append([]string(nil), result[index].Arguments...)
		result[index].Environment = append([]lifecycle.EnvironmentBinding(nil), result[index].Environment...)
	}
	return result
}

func (f *Host) ResourceCalls() []lifecycle.ResourceRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.ResourceRequest(nil), f.resourceCalls...)
}

func (f *Host) ReadCalls() []lifecycle.ResourceReadRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.ResourceReadRequest(nil), f.readCalls...)
}

func (f *Host) ExecutableCalls() []lifecycle.ExecutableRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.ExecutableRequest(nil), f.executableCalls...)
}

func (f *Host) PreflightCalls() []lifecycle.DiskPreflightRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.DiskPreflightRequest(nil), f.preflightCalls...)
}

func (f *Host) EnvironmentCalls() []lifecycle.EnvironmentPresenceRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lifecycle.EnvironmentPresenceRequest(nil), f.environmentCalls...)
}

var _ lifecycle.HostInspector = (*Host)(nil)
var _ lifecycle.AtomicFileWriter = (*Host)(nil)
var _ lifecycle.ProcessRunner = (*Host)(nil)
var _ lifecycle.DiskPreflighter = (*Host)(nil)
var _ lifecycle.ResourceChecker = (*Host)(nil)
var _ lifecycle.EnvironmentInspector = (*Host)(nil)
var _ lifecycle.HostResourcePolicyProvider = (*Host)(nil)
var _ lifecycle.HostServices = (*Host)(nil)
