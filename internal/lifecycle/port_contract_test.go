package lifecycle_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

type observerStub struct{}

func (observerStub) ObserveTarget(ctx context.Context, _ lifecycle.TargetObservationRequest) (lifecycle.TargetObservation, error) {
	return lifecycle.TargetObservation{}, ctx.Err()
}

type mutatorStub struct{}

func (mutatorStub) MutateTarget(ctx context.Context, _ lifecycle.TargetMutationRequest) (lifecycle.TargetMutationResult, error) {
	return lifecycle.TargetMutationResult{}, ctx.Err()
}

type sourceStub struct{}

func (sourceStub) AcquireSource(ctx context.Context, _ lifecycle.SourceRequest) (lifecycle.SourceSnapshot, error) {
	return nil, ctx.Err()
}

type stateReaderStub struct{}

func (stateReaderStub) ReadInstallation(ctx context.Context, _ lifecycle.InstallationKey) (lifecycle.InstallationRecord, bool, error) {
	return lifecycle.InstallationRecord{}, false, ctx.Err()
}

type stateWriterStub struct{}

func (stateWriterStub) WriteInstallation(ctx context.Context, _ lifecycle.InstallationRecord) error {
	return ctx.Err()
}
func (stateWriterStub) DeleteInstallation(ctx context.Context, _ lifecycle.InstallationKey) error {
	return ctx.Err()
}

type clockStub struct{}

func (clockStub) Now() time.Time { return time.Unix(0, 0).UTC() }

var (
	_ lifecycle.TargetObserver          = observerStub{}
	_ lifecycle.TargetMutator           = mutatorStub{}
	_ lifecycle.SourceAcquirer          = sourceStub{}
	_ lifecycle.InstallationStateReader = stateReaderStub{}
	_ lifecycle.InstallationStateWriter = stateWriterStub{}
	_ lifecycle.Clock                   = clockStub{}
)

func TestReadAndMutationPortsAreIndependent(t *testing.T) {
	t.Parallel()

	var reader lifecycle.TargetObserver = observerStub{}
	if _, ok := reader.(lifecycle.TargetMutator); ok {
		t.Fatal("read-only target port unexpectedly exposes mutation")
	}
}

func TestBlockingPortReceivesCallerCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := lifecycle.TargetObservationRequest{Target: domain.ClaudeTarget(), Scope: domain.UserScope()}
	_, err := (observerStub{}).ObserveTarget(ctx, request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ObserveTarget() error = %v", err)
	}
	if _, err := (sourceStub{}).AcquireSource(ctx, lifecycle.SourceRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireSource() error = %v", err)
	}
}

func TestProcessStreamClosedDisplaySafetyUnion(t *testing.T) {
	t.Parallel()

	text, ok := lifecycle.NewProcessStream(lifecycle.SanitizedTextOutput, []byte("line\nvalue\t\r"), false)
	if !ok {
		t.Fatal("safe sanitized text rejected")
	}
	if got, displayable := text.SanitizedText(); !displayable || got != "line\nvalue\t\r" {
		t.Fatalf("SanitizedText() = %q, %t", got, displayable)
	}
	for _, unsafe := range [][]byte{{0}, {0x1b}, {0xc2, 0x85}, {0xff}} {
		if _, ok := lifecycle.NewProcessStream(lifecycle.SanitizedTextOutput, unsafe, false); ok {
			t.Fatalf("unsafe sanitized bytes accepted: %x", unsafe)
		}
	}
	opaqueData := []byte{'a', 0, 0xff, 'b'}
	opaque, ok := lifecycle.NewProcessStream(lifecycle.OpaqueBytesOutput, opaqueData, true)
	if !ok {
		t.Fatal("opaque bytes rejected")
	}
	if _, displayable := opaque.SanitizedText(); displayable {
		t.Fatal("opaque bytes became displayable")
	}
	got, available := opaque.OpaqueBytes()
	if !available || !reflect.DeepEqual(got, opaqueData) || !opaque.Truncated() {
		t.Fatalf("OpaqueBytes() = %x, %t, truncated=%t", got, available, opaque.Truncated())
	}
	got[0] = 'X'
	again, _ := opaque.OpaqueBytes()
	if again[0] != 'a' {
		t.Fatal("opaque accessor exposed mutable internal bytes")
	}
	defaultText, ok := lifecycle.NewProcessStream("", nil, false)
	if !ok || defaultText.Mode() != lifecycle.SanitizedTextOutput {
		t.Fatal("default output mode did not normalize to sanitized text")
	}
}

func TestOpaqueProcessStreamFormattingNeverDisclosesBytes(t *testing.T) {
	t.Parallel()

	const canary = "AI4J_OPAQUE_CANARY"
	stream, ok := lifecycle.NewProcessStream(lifecycle.OpaqueBytesOutput, []byte(canary), true)
	if !ok {
		t.Fatal("opaque stream rejected")
	}
	result := lifecycle.ProcessResult{Started: true, Stdout: stream, Stderr: stream}
	for _, formatted := range []string{
		fmt.Sprintf("%v", stream), fmt.Sprintf("%+v", stream), fmt.Sprintf("%#v", stream),
		fmt.Sprintf("%s", stream), fmt.Sprintf("%q", stream), fmt.Sprintf("%x", stream),
		fmt.Sprintf("%v", result), fmt.Sprintf("%+v", result), fmt.Sprintf("%#v", result),
	} {
		if strings.Contains(formatted, canary) {
			t.Fatalf("formatted opaque stream disclosed canary: %q", formatted)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), canary) {
		t.Fatalf("JSON disclosed opaque canary: %s", encoded)
	}
	text, err := stream.MarshalText()
	if err != nil || strings.Contains(string(text), canary) {
		t.Fatalf("text marshaling = %q, %v", text, err)
	}
}

func TestFileArtifactPlanningAndOutcomeFactsAreClosed(t *testing.T) {
	t.Parallel()

	operation, _ := domain.NewOperationID("operation-1")
	token, _ := domain.NewArtifactToken("0123456789abcdef0123456789abcdef")
	plan, ok := lifecycle.PlanFileArtifacts(operation, token)
	if !ok || plan.TemporaryName != ".ai4j-operation-1-0123456789abcdef0123456789abcdef.tmp" ||
		plan.QuarantineName != ".ai4j-operation-1-0123456789abcdef0123456789abcdef.quarantine" {
		t.Fatalf("artifact plan = %+v, %t", plan, ok)
	}
	if _, ok := lifecycle.PlanFileArtifacts(domain.OperationID{}, token); ok {
		t.Fatal("invalid operation produced artifact plan")
	}
	digest, _ := domain.NewRenderedDigest("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	expectation := lifecycle.FileExpectation{
		State: lifecycle.ExpectPresent, Digest: digest,
		RootIdentity:   lifecycle.ObjectIdentity{Filesystem: 1, Object: 1},
		ParentIdentity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 2},
		Identity:       lifecycle.ObjectIdentity{Filesystem: 1, Object: 3}, Mode: 0o600, Size: 1, OwnedByCurrentUser: true,
	}
	desired := lifecycle.FileContentExpectation{Digest: digest, Mode: 0o640, Size: 1}
	if !desired.Valid() || !desired.Matches(expectation) {
		t.Fatal("desired file predicate did not accept a restrictive prepared mode")
	}
	inspection := lifecycle.FileArtifactInspectionRequest{
		OperationID: operation, ArtifactToken: token, Artifacts: plan,
		Root: lifecycle.ManagedOutputRoot, Destination: "rules.md",
		RootIdentity: expectation.RootIdentity, ParentIdentity: expectation.ParentIdentity,
		Preimage: expectation, Desired: desired, Prepared: expectation,
	}
	if !inspection.Valid() {
		t.Fatalf("valid artifact inspection rejected: %+v", inspection)
	}
	inspection.Prepared.Identity = lifecycle.ObjectIdentity{}
	if inspection.Valid() {
		t.Fatal("artifact inspection accepted an incomplete prepared predicate")
	}
	artifact := lifecycle.CleanupArtifact{
		OperationID: operation, ArtifactToken: token, Artifacts: plan,
		Root: lifecycle.ManagedOutputRoot, Path: plan.TemporaryName, Expected: expectation,
	}
	recoveryConflict := lifecycle.FileRecoveryConflict{
		Root: lifecycle.ManagedOutputRoot, Path: plan.TemporaryName, Reason: lifecycle.RecoveryPredicateMismatch,
		Kind: lifecycle.RecoveryRegularObject, Identity: expectation.Identity,
	}
	if !artifact.Valid() || !recoveryConflict.Valid() {
		t.Fatal("valid cleanup/recovery facts rejected")
	}
	mutation := lifecycle.FileMutation{
		OperationID: operation, ArtifactToken: token, Artifacts: plan,
		Root: lifecycle.ManagedOutputRoot, Destination: "rules.md", Content: []byte("x"), Mode: 0o600, Expected: expectation,
	}
	if !mutation.Valid() {
		t.Fatal("valid mutation rejected")
	}
	reservedPrefix := strings.TrimSuffix(plan.TemporaryName, ".tmp") + ".user"
	for _, destination := range []string{
		plan.TemporaryName, strings.ToUpper(plan.QuarantineName), reservedPrefix,
		".AI4J-user-file", ".Ai4j-cafe\u0301",
	} {
		mutation.Destination = destination
		if mutation.Valid() {
			t.Fatalf("mutation accepted destination/artifact alias %q", destination)
		}
		inspection.Destination = destination
		if inspection.Valid() {
			t.Fatalf("inspection accepted destination/artifact alias %q", destination)
		}
	}
	mutation.Destination = "rules.md"
	inspection.Destination = "rules.md"
	invalidArtifact := artifact
	invalidArtifact.OperationID, _ = domain.NewOperationID("other-operation")
	if invalidArtifact.Valid() {
		t.Fatal("cleanup artifact accepted a path outside its operation plan")
	}
	invalidArtifact = artifact
	invalidArtifact.Path = "ordinary-owned-file"
	if invalidArtifact.Valid() {
		t.Fatal("operation cleanup artifact accepted an ordinary destination path")
	}
	if !(lifecycle.FileCleanupResult{Cleanup: lifecycle.CleanupComplete}).Coherent() ||
		!(lifecycle.FileCleanupResult{Cleanup: lifecycle.CleanupRequired, Artifact: artifact}).Coherent() ||
		!(lifecycle.FileCleanupResult{Cleanup: lifecycle.CleanupRequired, RecoveryConflict: recoveryConflict}).Coherent() {
		t.Fatal("valid cleanup outcomes were rejected")
	}
	if (lifecycle.FileCleanupResult{Cleanup: lifecycle.CleanupRequired}).Coherent() ||
		(lifecycle.FileCleanupResult{Cleanup: lifecycle.CleanupRequired, Artifact: artifact, RecoveryConflict: recoveryConflict}).Coherent() ||
		(lifecycle.FileCleanupResult{Cleanup: lifecycle.CleanupComplete, Artifact: artifact}).Coherent() {
		t.Fatal("incoherent cleanup outcome was accepted")
	}
	if !(lifecycle.FileArtifactInspectionResult{Artifacts: []lifecycle.CleanupArtifact{artifact}}).Coherent() ||
		!(lifecycle.FileArtifactInspectionResult{Conflicts: []lifecycle.FileRecoveryConflict{recoveryConflict}}).Coherent() {
		t.Fatal("bounded artifact inspection facts rejected")
	}
	if (lifecycle.FileArtifactInspectionResult{
		Artifacts: []lifecycle.CleanupArtifact{artifact}, Conflicts: []lifecycle.FileRecoveryConflict{recoveryConflict},
	}).Coherent() {
		t.Fatal("artifact inspection accepted cleanup and conflict for one path")
	}
	for name, result := range map[string]lifecycle.FileMutationResult{
		"not applied": {Cleanup: lifecycle.CleanupNotRequired, Visibility: lifecycle.FileNotApplied, Durability: lifecycle.NamespaceNotStarted},
		"pending": {
			Cleanup: lifecycle.CleanupNotRequired, Visibility: lifecycle.FileIndeterminate,
			Durability: lifecycle.NamespacePending, VisibleExpectation: expectation,
		},
		"durable indeterminate": {
			Cleanup: lifecycle.CleanupNotRequired, Visibility: lifecycle.FileIndeterminate,
			Durability: lifecycle.NamespaceDurable, VisibleExpectation: expectation,
		},
		"applied": {
			Cleanup: lifecycle.CleanupNotRequired, Visibility: lifecycle.FileAppliedVerified,
			Durability: lifecycle.NamespaceDurable, VisibleExpectation: expectation,
		},
	} {
		if !result.Coherent() {
			t.Errorf("%s result is incoherent: %+v", name, result)
		}
	}
	if (lifecycle.FileMutationResult{
		Cleanup: "unknown", Visibility: lifecycle.FileNotApplied, Durability: lifecycle.NamespaceNotStarted,
	}).Coherent() || (lifecycle.FileMutationResult{
		Visibility: lifecycle.FileNotApplied, Durability: lifecycle.NamespaceNotStarted,
	}).Coherent() {
		t.Fatal("mutation result accepted zero or unknown cleanup disposition")
	}
	if (lifecycle.FileMutationResult{Visibility: lifecycle.FileAppliedVerified, Durability: lifecycle.NamespacePending, VisibleExpectation: expectation}).Coherent() {
		t.Fatal("verified applied result accepted pending durability")
	}
	if (lifecycle.FileMutationResult{
		Cleanup: lifecycle.CleanupRequired, CleanupArtifact: artifact, RecoveryConflict: recoveryConflict,
		Visibility: lifecycle.FileIndeterminate, Durability: lifecycle.NamespacePending, VisibleExpectation: expectation,
	}).Coherent() {
		t.Fatal("result accepted cleanup and do-not-delete facts for the same transition")
	}
	if (lifecycle.FileMutationResult{
		Cleanup: lifecycle.CleanupRequired, Visibility: lifecycle.FileIndeterminate,
		Durability: lifecycle.NamespacePending, VisibleExpectation: expectation,
	}).Coherent() {
		t.Fatal("indeterminate cleanup requirement accepted without a typed recovery fact")
	}
}
