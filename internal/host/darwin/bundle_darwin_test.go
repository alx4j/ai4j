//go:build darwin && arm64

package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/fault"
	"github.com/alx4j/ai4j/internal/host/darwin/filesystem"
	"github.com/alx4j/ai4j/internal/host/darwin/resource"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestProductionBundleSharesExactMVPHostPolicyAndOwnsClose(t *testing.T) {
	config := testBundleConfig(t)
	bundle, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	policy := bundle.ResourcePolicy()
	if !policy.Valid() || policy.Version().String() != "mvp_resource_v1" ||
		policy.GitTimeoutMaximum().Duration() != 5*time.Minute ||
		policy.ClaudeTimeoutMaximum().Duration() != 2*time.Minute {
		t.Fatalf("bundle policy = %#v", policy)
	}
	host, err := bundle.InspectHost(context.Background(), lifecycle.HostInspectionRequest{Host: domain.DarwinHost()})
	if err != nil || host.Host != domain.DarwinHost() || host.OS != "darwin" || host.Arch != "arm64" || host.OSVersion == "" {
		t.Fatalf("host observation = %#v, %v", host, err)
	}
	t.Setenv("AI4J_BUNDLE_PRESENCE_CANARY", "secret-value")
	request, _ := lifecycle.NewEnvironmentPresenceRequest([]string{"AI4J_BUNDLE_PRESENCE_CANARY", "AI4J_BUNDLE_MISSING"})
	presence, err := bundle.InspectEnvironment(context.Background(), request)
	if err != nil || !presence.Coherent() || len(presence.Values()) != 2 || presence.Values()[0].Present || !presence.Values()[1].Present {
		t.Fatalf("environment presence = %#v, %v", presence, err)
	}
	temporarySource := filepath.Join(config.Filesystem.BaseRoot, config.Filesystem.TemporarySourcePath)
	entries, err := os.ReadDir(temporarySource)
	if err != nil || len(entries) != 0 {
		t.Fatalf("safe cwd contains probe artifacts: %v, %v", entries, err)
	}
	if err := bundle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := bundle.CheckResource(context.Background(), lifecycle.ResourceRequest{}); !errors.Is(err, errBundleClosed) {
		t.Fatalf("post-close error = %v", err)
	}
}

func TestProductionBundleConfigAndFailuresAreRedacted(t *testing.T) {
	config := testBundleConfig(t)
	canary := "AI4J_BUNDLE_PATH_CANARY"
	config.Filesystem.BaseRoot = filepath.Join(filepath.Dir(config.Filesystem.BaseRoot), canary)
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		fmt.Sprintf("%v", config), fmt.Sprintf("%+v", config), fmt.Sprintf("%#v", config), string(encoded),
	} {
		if strings.Contains(value, canary) {
			t.Fatalf("Config formatting disclosed path: %s", value)
		}
	}
	if _, err := New(context.Background(), config); !errors.Is(err, errBundleConstruction) || strings.Contains(fmt.Sprint(err), canary) {
		t.Fatalf("constructor error = %v", err)
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	causeCanary := errors.New("AI4J_BUNDLE_CANCEL_CAUSE_CANARY")
	cancel(causeCanary)
	if _, err := New(ctx, testBundleConfig(t)); !errors.Is(err, context.Canceled) ||
		!errors.Is(err, fault.ErrCancelled) || strings.Contains(fmt.Sprint(err), causeCanary.Error()) {
		t.Fatalf("cancelled constructor error = %v", err)
	}
}

func TestDenyBaselineFailurePrecedesFilesystemActivationAndPrivateRootCreation(t *testing.T) {
	for _, test := range []struct {
		name       string
		inspectErr error
		cancel     bool
		want       error
		category   error
	}{
		{name: "missing required", inspectErr: lifecycle.ErrExecutableNotFound, want: errUnsupportedBundleHost, category: fault.ErrUnsupportedCapability},
		{name: "unsafe required", inspectErr: errors.New("AI4J_DENY_INSPECTION_CANARY"), want: errUnsupportedBundleHost, category: fault.ErrUnsupportedCapability},
		{name: "cancelled", cancel: true, want: context.Canceled, category: fault.ErrCancelled},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := testBundleConfig(t)
			successDigest, digestErr := domain.NewExecutableDigest(strings.Repeat("a", 64))
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			var ctx context.Context = context.Background()
			cancel := func() {}
			if test.cancel {
				var cancelContext context.CancelFunc
				ctx, cancelContext = context.WithCancel(ctx)
				cancel = cancelContext
			}
			filesystemCalls := 0
			_, err := newWithConstructorOperations(ctx, config, bundleConstructorOperations{
				deniedCandidates: func() []deniedExecutableCandidate {
					return []deniedExecutableCandidate{{path: "/required", required: true}}
				},
				inspectDenied: func(_ context.Context, _ string) (domain.ExecutableDigest, error) {
					if test.cancel {
						cancel()
						return successDigest, nil
					}
					return domain.ExecutableDigest{}, test.inspectErr
				},
				newFilesystem: func(context.Context, filesystem.Config) (*filesystem.Filesystem, error) {
					filesystemCalls++
					return nil, errors.New("filesystem activation must not run")
				},
			})
			if !errors.Is(err, test.want) || !errors.Is(err, test.category) ||
				strings.Contains(fmt.Sprint(err), "CANARY") {
				t.Fatalf("constructor error = %v", err)
			}
			if filesystemCalls != 0 {
				t.Fatalf("filesystem activation calls = %d", filesystemCalls)
			}
			for _, name := range []string{
				config.Filesystem.StatePath,
				config.Filesystem.RecoveryPath,
				config.Filesystem.TemporarySourcePath,
				config.Filesystem.StagingPath,
			} {
				if _, statErr := os.Lstat(filepath.Join(config.Filesystem.BaseRoot, name)); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("private root %q exists after read-only preflight failure: %v", name, statErr)
				}
			}
		})
	}
}

func TestBundleConstructionFaultMappingIsStableAndRedacted(t *testing.T) {
	canary := errors.New("AI4J_BUNDLE_FAULT_CANARY")
	unsupported := stableConstructionError(context.Background(), fault.MustNew(
		fault.UnsupportedCapability,
		mustUnsupportedDetail("host", "darwin_arm64", "test"),
		canary,
	))
	if !errors.Is(unsupported, fault.ErrUnsupportedCapability) || strings.Contains(fmt.Sprint(unsupported), canary.Error()) {
		t.Fatalf("unsupported mapping = %v", unsupported)
	}
	invalid := stableConstructionError(context.Background(), fault.MustNew(
		fault.InvalidInput,
		mustInvalidDetail("config", fault.ReasonInvalidFormat),
		canary,
	))
	if !errors.Is(invalid, fault.ErrInvalidInput) || strings.Contains(fmt.Sprint(invalid), canary.Error()) {
		t.Fatalf("invalid mapping = %v", invalid)
	}
	conflict := stableConstructionError(context.Background(), fault.MustNew(
		fault.Conflict,
		mustConflictDetail("authority", "changed"),
		canary,
	))
	if !errors.Is(conflict, fault.ErrConflict) || strings.Contains(fmt.Sprint(conflict), canary.Error()) {
		t.Fatalf("conflict mapping = %v", conflict)
	}
}

func TestBundleSharesOneExactFilesystemBudgetAcrossDenyQualificationAndActivation(t *testing.T) {
	config := testBundleConfig(t)
	digest, err := domain.NewExecutableDigest(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	var qualificationContext context.Context
	var qualificationDeadline time.Time
	filesystemCalls := 0
	_, err = newWithConstructorOperations(context.Background(), config, bundleConstructorOperations{
		deniedCandidates: func() []deniedExecutableCandidate {
			return []deniedExecutableCandidate{{path: "/required", required: true}}
		},
		inspectDenied: func(ctx context.Context, _ string) (domain.ExecutableDigest, error) {
			qualificationContext = ctx
			var present bool
			qualificationDeadline, present = ctx.Deadline()
			if !present {
				t.Fatal("deny qualifier received no filesystem-budget deadline")
			}
			return digest, nil
		},
		newFilesystem: func(ctx context.Context, _ filesystem.Config) (*filesystem.Filesystem, error) {
			filesystemCalls++
			activationDeadline, present := ctx.Deadline()
			if !present || ctx != qualificationContext || !activationDeadline.Equal(qualificationDeadline) {
				t.Fatalf("activation context/deadline differs: same=%t qualification=%v activation=%v", ctx == qualificationContext, qualificationDeadline, activationDeadline)
			}
			maximum, ok := resource.MVPPolicy().Timeout(resource.FilesystemBudget)
			if !ok || time.Until(activationDeadline) <= maximum-time.Second || time.Until(activationDeadline) > maximum {
				t.Fatalf("filesystem budget deadline = %v, maximum = %v", activationDeadline, maximum)
			}
			return nil, errors.New("injected activation stop")
		},
	})
	if !errors.Is(err, errBundleConstruction) || filesystemCalls != 1 {
		t.Fatalf("constructor error/calls = %v / %d", err, filesystemCalls)
	}
	for _, privateName := range []string{
		config.Filesystem.StatePath,
		config.Filesystem.RecoveryPath,
		config.Filesystem.TemporarySourcePath,
		config.Filesystem.StagingPath,
	} {
		if _, statErr := os.Lstat(filepath.Join(config.Filesystem.BaseRoot, privateName)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("budget seam created %q: %v", privateName, statErr)
		}
	}
}

func testBundleConfig(t *testing.T) Config {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(parent, "base")
	managed := filepath.Join(parent, "managed")
	for _, directory := range []string{base, managed} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return Config{Filesystem: filesystem.Config{
		BaseRoot: base, StatePath: "state", RecoveryPath: "recovery", TemporarySourcePath: "temporary-source",
		StagingPath: "staging", ManagedOutputRoot: managed, MaximumFileBytes: 1 << 20,
	}}
}
