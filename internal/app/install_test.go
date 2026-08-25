package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/buildinfo"
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func TestInstallCompletesEndToEndAndRepeatIsNoChange(t *testing.T) {
	harness := newInstallHarness(t)
	request := installRequest(t, "--yes", "--json")
	unrelated := []string{
		filepath.Join(harness.installer.home, ".claude", "CLAUDE.md"),
		filepath.Join(harness.installer.home, ".claude", "rules", "other.md"),
	}
	for _, path := range unrelated {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("UNRELATED_CANARY\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	assertNoInstallTemps(t, harness.installer.home)

	response, err := harness.installer.Install(context.Background(), request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	if response.Result().ExitCode() != result.ExitSuccess || !response.Result().Changed() || !response.HasOperationID() {
		t.Fatalf("install result = exit:%d changed:%t operation:%t", response.Result().ExitCode(), response.Result().Changed(), response.HasOperationID())
	}
	wantCommands := [][]string{
		{"plugin", "marketplace", "add", harness.installer.catalogRoot(), "--scope", "user"},
		{"plugin", "install", "ai4j-default@ai4j", "--scope", "user"},
	}
	if !slices.EqualFunc(harness.native.commands, wantCommands, slices.Equal[[]string]) {
		t.Fatalf("Claude commands = %v, want %v", harness.native.commands, wantCommands)
	}
	if got, err := os.ReadFile(harness.installer.rulesPath()); err != nil || string(got) != string(harness.validator.report.Rules) {
		t.Fatalf("rules = %q, %v", got, err)
	}
	if got, err := os.ReadFile(harness.installer.catalogPath()); err != nil || !bytes.Contains(got, []byte(strings.Repeat("a", 40))) {
		t.Fatalf("catalog = %q, %v", got, err)
	}
	record, present, err := harness.store.Load()
	if err != nil || !present || record.Source.Commit != strings.Repeat("a", 40) || record.Rules.Checksum != harness.validator.report.RulesChecksum {
		t.Fatalf("state = %#v, present:%t err:%v", record, present, err)
	}
	if _, present, err := harness.store.LoadMarker(); err != nil || present {
		t.Fatalf("marker after success = present:%t err:%v", present, err)
	}
	for _, path := range unrelated {
		if got, err := os.ReadFile(path); err != nil || string(got) != "UNRELATED_CANARY\n" {
			t.Fatalf("unrelated file %s = %q, %v", path, got, err)
		}
	}

	repeat, err := harness.installer.Install(context.Background(), request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Result().Status() != result.StatusNoChange || repeat.Result().Changed() || len(harness.native.commands) != 2 {
		t.Fatalf("repeat = status:%s changed:%t commands:%d", repeat.Result().Status(), repeat.Result().Changed(), len(harness.native.commands))
	}
}

func TestCommandDispatcherRunsTheInstallVerticalSlice(t *testing.T) {
	harness := newInstallHarness(t)
	service := installDispatchService{planValidationStub: &planValidationStub{}, installer: harness.installer}
	request := installRequest(t, "--yes", "--json")
	response, err := newCommandHandler(service)(request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess || len(harness.native.commands) != 2 {
		t.Fatalf("dispatched install = exit:%d commands:%d err:%v", response.Result().ExitCode(), len(harness.native.commands), err)
	}
}

func TestInstallRequiresApprovalAndHonorsExpectedCommitBeforeMutation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want result.Failure
	}{
		{name: "JSON requires yes", args: []string{"--json"}, want: result.FailureApproval},
		{name: "expected commit mismatch", args: []string{"--yes", "--expected-commit", strings.Repeat("b", 40)}, want: result.FailureConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newInstallHarness(t)
			response, err := harness.installer.Install(context.Background(), installRequest(t, test.args...), CommandIO{})
			if err != nil {
				t.Fatal(err)
			}
			if response.Result().Failure() != test.want || response.Result().Mutation() != result.MutationNotStarted || len(harness.native.commands) != 0 {
				t.Fatalf("result = failure:%s mutation:%s commands:%d", response.Result().Failure(), response.Result().Mutation(), len(harness.native.commands))
			}
			if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
				t.Fatalf("pre-mutation marker = present:%t err:%v", present, markerErr)
			}
		})
	}
}

func TestInteractiveInstallShowsPlanAndAcceptsOrDeclines(t *testing.T) {
	t.Run("accept", func(t *testing.T) {
		harness := newInstallHarness(t)
		var output bytes.Buffer
		response, err := harness.installer.Install(context.Background(), installRequest(t), CommandIO{
			Input: strings.NewReader("yes\n"), Output: &output, Interactive: true,
		})
		if err != nil || response.Result().ExitCode() != result.ExitSuccess || !strings.Contains(output.String(), "Proceed with installation?") {
			t.Fatalf("interactive accept = exit:%d output:%q err:%v", response.Result().ExitCode(), output.String(), err)
		}
	})
	t.Run("decline", func(t *testing.T) {
		harness := newInstallHarness(t)
		var output bytes.Buffer
		response, err := harness.installer.Install(context.Background(), installRequest(t), CommandIO{
			Input: strings.NewReader("no\n"), Output: &output, Interactive: true,
		})
		if err != nil || response.Result().Status() != result.StatusCancelled || response.Result().Failure() != result.FailureCancellation || len(harness.native.commands) != 0 {
			t.Fatalf("interactive decline = failure:%s commands:%d err:%v", response.Result().Failure(), len(harness.native.commands), err)
		}
	})
}

func TestInterruptedInstallRemainsBlockedWhenStateIsNotProvablySafe(t *testing.T) {
	harness := newInstallHarness(t)
	harness.native.failAt = 2
	request := installRequest(t, "--yes", "--json")
	response, err := harness.installer.Install(context.Background(), request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	if response.Result().ExitCode() != result.ExitRecoveryRequired || response.Result().Phase() != result.PhaseApplying {
		t.Fatalf("failed install = exit:%d phase:%s", response.Result().ExitCode(), response.Result().Phase())
	}
	marker, present, err := harness.store.LoadMarker()
	if err != nil || !present || marker.Operation != "install" {
		t.Fatalf("retained marker = %#v present:%t err:%v", marker, present, err)
	}
	assertNoInstallTemps(t, harness.installer.home)
	commandCount := len(harness.native.commands)
	harness.native.failAt = 0
	retry, err := harness.installer.Install(context.Background(), request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Result().ExitCode() != result.ExitRecoveryRequired || len(harness.native.commands) != commandCount {
		t.Fatalf("retry = exit:%d commands:%d, want %d", retry.Result().ExitCode(), len(harness.native.commands), commandCount)
	}
}

func TestInstallCleansOnlyProvablySafeMarkers(t *testing.T) {
	t.Run("marker only", func(t *testing.T) {
		harness := newInstallHarness(t)
		marker, err := installstate.NewInstallMarker("operation-old", "install-aaaaaaaaaaaa", strings.Repeat("a", 40))
		if err != nil {
			t.Fatal(err)
		}
		if err := harness.store.SaveMarker(marker); err != nil {
			t.Fatal(err)
		}
		response, err := harness.installer.Install(context.Background(), installRequest(t, "--yes"), CommandIO{})
		if err != nil || response.Result().ExitCode() != result.ExitSuccess {
			t.Fatalf("install after safe marker = exit:%d err:%v", response.Result().ExitCode(), err)
		}
	})
	t.Run("committed state", func(t *testing.T) {
		harness := newInstallHarness(t)
		request := installRequest(t, "--yes")
		if response, err := harness.installer.Install(context.Background(), request, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
			t.Fatalf("initial install = %#v, %v", response.Result(), err)
		}
		record, _, err := harness.store.Load()
		if err != nil {
			t.Fatal(err)
		}
		marker, err := installstate.NewInstallMarker(record.LastOperation.ID, record.InstallationID, record.Source.Commit)
		if err != nil {
			t.Fatal(err)
		}
		if err := harness.store.SaveMarker(marker); err != nil {
			t.Fatal(err)
		}
		response, err := harness.installer.Install(context.Background(), request, CommandIO{})
		if err != nil || response.Result().Status() != result.StatusNoChange {
			t.Fatalf("reconciled committed marker = status:%s err:%v", response.Result().Status(), err)
		}
		if _, present, err := harness.store.LoadMarker(); err != nil || present {
			t.Fatalf("committed marker = present:%t err:%v", present, err)
		}
	})
}

func TestInstallBlocksOwnedDestinationAndConcurrentModifier(t *testing.T) {
	t.Run("owned destination", func(t *testing.T) {
		harness := newInstallHarness(t)
		if err := os.MkdirAll(filepath.Dir(harness.installer.rulesPath()), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(harness.installer.rulesPath(), []byte("unmanaged"), 0o600); err != nil {
			t.Fatal(err)
		}
		response, err := harness.installer.Install(context.Background(), installRequest(t, "--yes"), CommandIO{})
		if err != nil || response.Result().Failure() != result.FailureConflict || len(harness.native.commands) != 0 {
			t.Fatalf("owned conflict = failure:%s commands:%d err:%v", response.Result().Failure(), len(harness.native.commands), err)
		}
	})
	t.Run("concurrent modifier", func(t *testing.T) {
		harness := newInstallHarness(t)
		harness.installer.acquire = func(context.Context) (func() error, error) { return nil, errors.New("busy") }
		response, err := harness.installer.Install(context.Background(), installRequest(t, "--yes"), CommandIO{})
		if err != nil || response.Result().Failure() != result.FailureConflict || len(harness.native.commands) != 0 {
			t.Fatalf("lock conflict = failure:%s commands:%d err:%v", response.Result().Failure(), len(harness.native.commands), err)
		}
	})
	t.Run("state destination changes during install", func(t *testing.T) {
		harness := newInstallHarness(t)
		harness.validator.createStateDuringVerify = true
		response, err := harness.installer.Install(context.Background(), installRequest(t, "--yes"), CommandIO{})
		if err != nil || response.Result().ExitCode() != result.ExitRecoveryRequired {
			t.Fatalf("state race = exit:%d err:%v", response.Result().ExitCode(), err)
		}
		got, readErr := os.ReadFile(harness.store.Path())
		if readErr != nil || string(got) != "EXTERNAL_STATE_CANARY\n" {
			t.Fatalf("external state = %q, %v", got, readErr)
		}
	})
}

type installHarness struct {
	installer *installer
	validator *installValidator
	native    *installNativeRunner
	store     installstate.Store
}

type installDispatchService struct {
	*planValidationStub
	installer *installer
}

func (s installDispatchService) Install(ctx context.Context, request cli.InstallRequest, commandIO CommandIO) (cli.Response, error) {
	return s.installer.Install(ctx, request, commandIO)
}

func newInstallHarness(t *testing.T) installHarness {
	t.Helper()
	home := t.TempDir()
	store, err := installstate.NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	rules := []byte("# AI4J shared rules\n")
	rulesDigest := sha256.Sum256(rules)
	validator := &installValidator{home: home, report: validation.Report{
		Source: testPlanSource(t), Content: []cli.ContentItem{testPlanRules(t)}, Rules: rules,
		RulesChecksum: fmt.Sprintf("%x", rulesDigest),
	}}
	native := &installNativeRunner{}
	validator.native = native
	build := buildinfo.New(buildinfo.Inputs{Version: "v0.1.0"})
	value := newInstaller(validator, store, native, home, build, func(context.Context) (func() error, error) {
		return func() error { return nil }, nil
	})
	value.now = func() time.Time { return time.Date(2026, 8, 24, 18, 0, 0, 0, time.UTC) }
	randomValues := append(bytes.Repeat([]byte{1}, 12), bytes.Repeat([]byte{2}, 84)...)
	value.random = bytes.NewReader(randomValues)
	return installHarness{installer: value, validator: validator, native: native, store: store}
}

func installRequest(t *testing.T, options ...string) cli.InstallRequest {
	t.Helper()
	argv := append([]string{"ai4j", "install"}, options...)
	request, err := cli.NewParser("darwin").Parse(argv)
	if err != nil {
		t.Fatal(err)
	}
	install, ok := request.(cli.InstallRequest)
	if !ok {
		t.Fatalf("request = %T", request)
	}
	return install
}

type installNativeRunner struct {
	commands                     [][]string
	failAt                       int
	marketplace, plugin, enabled bool
}

func (r *installNativeRunner) LookPath(name string) (string, error) {
	if name != "claude" {
		return "", errors.New("not found")
	}
	return "/usr/bin/claude", nil
}

func (r *installNativeRunner) Run(_ context.Context, _ string, _ string, arguments, _ []string) (validation.ProcessResult, error) {
	r.commands = append(r.commands, append([]string(nil), arguments...))
	if r.failAt == len(r.commands) {
		return validation.ProcessResult{ExitCode: 1}, nil
	}
	switch {
	case len(arguments) >= 3 && slices.Equal(arguments[:3], []string{"plugin", "marketplace", "add"}):
		r.marketplace = true
	case slices.Equal(arguments, []string{"plugin", "install", "ai4j-default@ai4j", "--scope", "user"}):
		r.plugin = true
		r.enabled = true
	case slices.Equal(arguments, []string{"plugin", "enable", "ai4j-default@ai4j", "--scope", "user"}):
		r.enabled = true
	case slices.Equal(arguments, []string{"plugin", "marketplace", "update", "ai4j"}):
	case slices.Equal(arguments, []string{"plugin", "update", "ai4j-default@ai4j", "--scope", "user"}):
	case slices.Equal(arguments, []string{"plugin", "uninstall", "ai4j-default@ai4j", "--scope", "user", "--keep-data"}):
		r.plugin = false
		r.enabled = false
	case slices.Equal(arguments, []string{"plugin", "marketplace", "remove", "ai4j", "--scope", "user"}):
		r.marketplace = false
	default:
		return validation.ProcessResult{}, fmt.Errorf("unexpected Claude arguments: %v", arguments)
	}
	return validation.ProcessResult{}, nil
}

type installValidator struct {
	home                    string
	report                  validation.Report
	update                  validation.UpdateReport
	native                  *installNativeRunner
	createStateDuringVerify bool
}

func (v *installValidator) Validate(context.Context, cli.SourceOptions) validation.Report {
	return v.report
}

func (v *installValidator) ValidateUpdate(context.Context, cli.SourceOptions, domain.CommitOID) validation.UpdateReport {
	return v.update
}

func (v *installValidator) InspectNativeStatus(context.Context) (validation.NativeStatus, *result.Problem) {
	return validation.NativeStatus{
		MarketplaceRegistered: v.native.marketplace,
		PluginInstalled:       v.native.plugin,
		PluginEnabled:         v.native.enabled,
	}, nil
}

func (v *installValidator) InspectPlanInstall(context.Context) ([]cli.Conflict, *result.Problem) {
	paths := []struct {
		path, code, resource string
	}{
		{filepath.Join(v.home, "Library", "Application Support", "ai4j", "state", "installation.json"), "installation_exists", "AI4J installation state"},
		{filepath.Join(v.home, "Library", "Application Support", "ai4j", "state", "catalog", ".claude-plugin", "marketplace.json"), "catalog_destination_occupied", "AI4J marketplace catalog"},
		{filepath.Join(v.home, ".claude", "rules", "ai4j.md"), "rules_destination_occupied", "Claude user rules/ai4j.md"},
	}
	var conflicts []cli.Conflict
	for _, item := range paths {
		if _, err := os.Lstat(item.path); err == nil {
			conflicts = append(conflicts, appConflict(item.code, item.resource))
		}
	}
	if v.native.marketplace {
		conflicts = append(conflicts, appConflict("marketplace_identity_conflict", "AI4J marketplace"))
	}
	if v.native.plugin {
		conflicts = append(conflicts, appConflict("plugin_identity_conflict", "ai4j-default@ai4j"))
	}
	return conflicts, nil
}

func (v *installValidator) InspectPlanExisting(_ context.Context, catalogChecksum, rulesChecksum string) ([]cli.Conflict, *result.Problem) {
	return v.inspectExisting(catalogChecksum, rulesChecksum, true)
}

func (v *installValidator) InspectUninstall(_ context.Context, catalogChecksum, rulesChecksum string) ([]cli.Conflict, *result.Problem) {
	return v.inspectExisting(catalogChecksum, rulesChecksum, false)
}

func (v *installValidator) inspectExisting(catalogChecksum, rulesChecksum string, requireEnabled bool) ([]cli.Conflict, *result.Problem) {
	var conflicts []cli.Conflict
	if !testFileChecksum(filepath.Join(v.home, "Library", "Application Support", "ai4j", "state", "catalog", ".claude-plugin", "marketplace.json"), catalogChecksum) {
		conflicts = append(conflicts, appConflict("catalog_drift", "AI4J marketplace catalog"))
	}
	if !testFileChecksum(filepath.Join(v.home, ".claude", "rules", "ai4j.md"), rulesChecksum) {
		conflicts = append(conflicts, appConflict("rules_drift", "Claude user rules/ai4j.md"))
	}
	if !v.native.marketplace {
		conflicts = append(conflicts, appConflict("marketplace_missing", "AI4J marketplace"))
	}
	if !v.native.plugin {
		conflicts = append(conflicts, appConflict("plugin_missing", "ai4j-default@ai4j"))
	} else if requireEnabled && !v.native.enabled {
		conflicts = append(conflicts, appConflict("plugin_disabled", "ai4j-default@ai4j"))
	}
	if v.createStateDuringVerify {
		v.createStateDuringVerify = false
		path := filepath.Join(v.home, "Library", "Application Support", "ai4j", "state", "installation.json")
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		_ = os.WriteFile(path, []byte("EXTERNAL_STATE_CANARY\n"), 0o600)
	}
	return conflicts, nil
}

func appConflict(code, resource string) cli.Conflict {
	conflict, _ := cli.NewConflict(code, resource, "resource does not match the required state")
	return conflict
}

func testFileChecksum(path, want string) bool {
	contents, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(contents)
	return fmt.Sprintf("%x", digest) == want
}

func assertNoInstallTemps(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tmp") {
			return fmt.Errorf("temporary file remains: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
