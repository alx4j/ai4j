package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/jsonout"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	validation "github.com/alx4j/ai4j/internal/validate"
)

type lifecycleDispatchService struct {
	*planValidationStub
	lifecycle *lifecycleService
}

func (s lifecycleDispatchService) Update(ctx context.Context, request cli.UpdateRequest, commandIO CommandIO) (cli.Response, error) {
	return s.lifecycle.Update(ctx, request, commandIO)
}

func (s lifecycleDispatchService) Uninstall(ctx context.Context, request cli.UninstallRequest, commandIO CommandIO) (cli.Response, error) {
	return s.lifecycle.Uninstall(ctx, request, commandIO)
}

func TestUpdateCompletesEndToEndAndRepeatIsNoChange(t *testing.T) {
	harness := installedLifecycleHarness(t)
	original, present, err := harness.store.Load()
	if err != nil || !present {
		t.Fatalf("installed state = present:%t err:%v", present, err)
	}
	unrelated := filepath.Join(harness.installer.home, ".claude", "rules", "other.md")
	if err := os.WriteFile(unrelated, []byte("UNRELATED_CANARY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := testUpdateReport(t, strings.Repeat("2", 40))
	harness.validator.update = validation.UpdateReport{Report: candidate, Disposition: gitsource.UpdateAvailable}
	service := newLifecycleDispatch(harness)
	response := runLifecycleCommand(t, service, "update", "--yes", "--json", "--expected-commit", strings.Repeat("2", 40))
	if response.Result().ExitCode() != result.ExitSuccess || !response.Result().Changed() || !response.HasOperationID() || response.Result().UpdateDisposition() != result.UpdateAvailable {
		t.Fatalf("update result = exit:%d changed:%t operation:%t disposition:%s", response.Result().ExitCode(), response.Result().Changed(), response.HasOperationID(), response.Result().UpdateDisposition())
	}
	wantCommands := [][]string{
		{"plugin", "marketplace", "update", "ai4j"},
		{"plugin", "update", "ai4j-default@ai4j", "--scope", "user"},
	}
	if !slices.EqualFunc(harness.native.commands, wantCommands, slices.Equal[[]string]) {
		t.Fatalf("Claude commands = %v", harness.native.commands)
	}
	updated, present, err := harness.store.Load()
	if err != nil || !present || updated.InstallationID != original.InstallationID || updated.Source.Commit != strings.Repeat("2", 40) || updated.LastOperation.ID == original.LastOperation.ID {
		t.Fatalf("updated state = %#v, present:%t err:%v", updated, present, err)
	}
	if !testFileChecksum(harness.installer.rulesPath(), candidate.RulesChecksum) {
		t.Fatal("updated rules were not committed")
	}
	if got, err := os.ReadFile(unrelated); err != nil || string(got) != "UNRELATED_CANARY\n" {
		t.Fatalf("unrelated file = %q, %v", got, err)
	}
	if _, present, err := harness.store.LoadMarker(); err != nil || present {
		t.Fatalf("update marker = present:%t err:%v", present, err)
	}
	assertNoInstallTemps(t, harness.installer.home)
	assertJSONResponse(t, response)

	harness.native.commands = nil
	harness.validator.update = validation.UpdateReport{Report: candidate, Disposition: gitsource.UpdateNoChange}
	repeat := runLifecycleCommand(t, service, "update", "--yes", "--json")
	if repeat.Result().Status() != result.StatusNoChange || repeat.Result().UpdateDisposition() != result.UpdateUpToDate || len(harness.native.commands) != 0 {
		t.Fatalf("repeat update = status:%s disposition:%s commands:%v", repeat.Result().Status(), repeat.Result().UpdateDisposition(), harness.native.commands)
	}
}

func TestUpdateStopsBeforeMutationOnApprovalExpectedCommitAndDrift(t *testing.T) {
	t.Run("approval", func(t *testing.T) {
		harness := installedLifecycleHarness(t)
		harness.validator.update = validation.UpdateReport{Report: testUpdateReport(t, strings.Repeat("2", 40)), Disposition: gitsource.UpdateAvailable}
		response := runLifecycleCommand(t, newLifecycleDispatch(harness), "update", "--json")
		assertPreMutationFailure(t, harness, response, result.ExitUsageOrApproval)
	})
	t.Run("expected commit", func(t *testing.T) {
		harness := installedLifecycleHarness(t)
		harness.validator.update = validation.UpdateReport{Report: testUpdateReport(t, strings.Repeat("2", 40)), Disposition: gitsource.UpdateAvailable}
		response := runLifecycleCommand(t, newLifecycleDispatch(harness), "update", "--yes", "--expected-commit", strings.Repeat("3", 40))
		assertPreMutationFailure(t, harness, response, result.ExitConflict)
	})
	t.Run("owned drift", func(t *testing.T) {
		harness := installedLifecycleHarness(t)
		if err := os.WriteFile(harness.installer.rulesPath(), []byte("USER_EDIT\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		harness.validator.update = validation.UpdateReport{Report: testUpdateReport(t, strings.Repeat("2", 40)), Disposition: gitsource.UpdateAvailable}
		response := runLifecycleCommand(t, newLifecycleDispatch(harness), "update", "--yes")
		assertPreMutationFailure(t, harness, response, result.ExitConflict)
	})
}

func TestUpdateRetainsRecoveryMarkerAfterMutationFailure(t *testing.T) {
	harness := installedLifecycleHarness(t)
	original, _, _ := harness.store.Load()
	harness.validator.update = validation.UpdateReport{Report: testUpdateReport(t, strings.Repeat("2", 40)), Disposition: gitsource.UpdateAvailable}
	harness.native.failAt = 1
	response := runLifecycleCommand(t, newLifecycleDispatch(harness), "update", "--yes")
	if response.Result().ExitCode() != result.ExitRecoveryRequired {
		t.Fatalf("update failure exit = %d", response.Result().ExitCode())
	}
	marker, present, err := harness.store.LoadMarker()
	if err != nil || !present || marker.Operation != "update" {
		t.Fatalf("update marker = %#v, present:%t err:%v", marker, present, err)
	}
	current, present, err := harness.store.Load()
	if err != nil || !present || current.Source.Commit != original.Source.Commit {
		t.Fatalf("state after failed update = %#v, present:%t err:%v", current, present, err)
	}
}

func TestUninstallCompletesEndToEndPreservingUnrelatedAndPersistentData(t *testing.T) {
	harness := installedLifecycleHarness(t)
	harness.native.enabled = false
	unrelated := filepath.Join(harness.installer.home, ".claude", "rules", "other.md")
	persistent := filepath.Join(harness.installer.home, ".claude", "plugins", "data", "ai4j-default", "settings.json")
	for _, path := range []string{unrelated, persistent} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("PRESERVE_CANARY\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := newLifecycleDispatch(harness)
	response := runLifecycleCommand(t, service, "uninstall", "--yes", "--json")
	if response.Result().ExitCode() != result.ExitSuccess || !response.Result().Changed() || !response.HasOperationID() {
		t.Fatalf("uninstall result = exit:%d changed:%t operation:%t", response.Result().ExitCode(), response.Result().Changed(), response.HasOperationID())
	}
	wantCommands := [][]string{
		{"plugin", "uninstall", "ai4j-default@ai4j", "--scope", "user", "--keep-data"},
		{"plugin", "marketplace", "remove", "ai4j", "--scope", "user"},
	}
	if !slices.EqualFunc(harness.native.commands, wantCommands, slices.Equal[[]string]) {
		t.Fatalf("Claude commands = %v", harness.native.commands)
	}
	if _, present, err := harness.store.Load(); err != nil || present {
		t.Fatalf("uninstalled state = present:%t err:%v", present, err)
	}
	if _, present, err := harness.store.LoadMarker(); err != nil || present {
		t.Fatalf("uninstall marker = present:%t err:%v", present, err)
	}
	for _, path := range []string{harness.installer.catalogPath(), harness.installer.rulesPath()} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("owned path remains: %s (%v)", path, err)
		}
	}
	for _, path := range []string{unrelated, persistent} {
		if got, err := os.ReadFile(path); err != nil || string(got) != "PRESERVE_CANARY\n" {
			t.Fatalf("preserved file %s = %q, %v", path, got, err)
		}
	}
	assertJSONResponse(t, response)

	harness.native.commands = nil
	repeat := runLifecycleCommand(t, service, "uninstall", "--yes", "--json")
	if repeat.Result().Status() != result.StatusNoChange || len(harness.native.commands) != 0 {
		t.Fatalf("repeat uninstall = status:%s commands:%v", repeat.Result().Status(), harness.native.commands)
	}
}

func TestUninstallStopsOnDriftAndRetainsMarkerAfterMutationFailure(t *testing.T) {
	t.Run("drift", func(t *testing.T) {
		harness := installedLifecycleHarness(t)
		if err := os.WriteFile(harness.installer.rulesPath(), []byte("USER_EDIT\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		response := runLifecycleCommand(t, newLifecycleDispatch(harness), "uninstall", "--yes")
		assertPreMutationFailure(t, harness, response, result.ExitConflict)
	})
	t.Run("native failure", func(t *testing.T) {
		harness := installedLifecycleHarness(t)
		harness.native.failAt = 1
		response := runLifecycleCommand(t, newLifecycleDispatch(harness), "uninstall", "--yes")
		if response.Result().ExitCode() != result.ExitRecoveryRequired {
			t.Fatalf("uninstall failure exit = %d", response.Result().ExitCode())
		}
		marker, present, err := harness.store.LoadMarker()
		if err != nil || !present || marker.Operation != "uninstall" {
			t.Fatalf("uninstall marker = %#v, present:%t err:%v", marker, present, err)
		}
		if _, present, err := harness.store.Load(); err != nil || !present {
			t.Fatalf("state after failed uninstall = present:%t err:%v", present, err)
		}
	})
}

func installedLifecycleHarness(t *testing.T) installHarness {
	t.Helper()
	harness := newInstallHarness(t)
	response, err := harness.installer.Install(context.Background(), installRequest(t, "--yes"), CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install setup = exit:%d err:%v", response.Result().ExitCode(), err)
	}
	harness.native.commands = nil
	return harness
}

func newLifecycleDispatch(harness installHarness) lifecycleDispatchService {
	return lifecycleDispatchService{
		planValidationStub: &planValidationStub{},
		lifecycle:          newLifecycleService(harness.installer, harness.validator),
	}
}

func runLifecycleCommand(t *testing.T, service commandService, arguments ...string) cli.Response {
	t.Helper()
	request, err := cli.NewParser("darwin").Parse(append([]string{"ai4j"}, arguments...))
	if err != nil {
		t.Fatal(err)
	}
	response, err := newCommandHandler(service)(request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func testUpdateReport(t *testing.T, commit string) validation.Report {
	t.Helper()
	rules := []byte("# Updated AI4J shared rules\n")
	digest := sha256.Sum256(rules)
	skill, err := cli.NewContentItem(cli.ComponentSkill, "repository-review", "plugins/ai4j-default/skills/repository-review", strings.Repeat("e", 64), cli.ContentAdded, nil)
	if err != nil {
		t.Fatal(err)
	}
	return validation.Report{
		Source: testPlanSourceAt(t, commit), Content: []cli.ContentItem{testPlanRules(t), skill}, Rules: rules,
		RulesChecksum: fmt.Sprintf("%x", digest),
	}
}

func assertPreMutationFailure(t *testing.T, harness installHarness, response cli.Response, exit result.ExitCode) {
	t.Helper()
	if response.Result().ExitCode() != exit || response.Result().Mutation() != result.MutationNotStarted || len(harness.native.commands) != 0 {
		t.Fatalf("pre-mutation result = exit:%d mutation:%s commands:%v", response.Result().ExitCode(), response.Result().Mutation(), harness.native.commands)
	}
	if _, present, err := harness.store.LoadMarker(); err != nil || present {
		t.Fatalf("pre-mutation marker = present:%t err:%v", present, err)
	}
}

func assertJSONResponse(t *testing.T, response cli.Response) {
	t.Helper()
	var output bytes.Buffer
	if _, err := jsonout.Render(&output, response); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(output.Bytes(), &document); err != nil || document["schemaVersion"] != float64(1) {
		t.Fatalf("JSON response = %q, %v", output.String(), err)
	}
}
