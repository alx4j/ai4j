package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
)

func TestAgentActivationInstallIsSingletonPerTargetScope(t *testing.T) {
	harness := newLifecycleHarness(t)
	harness.validator.agentActivationBundles = map[string]bool{"default": true, "other": true}

	installBundle(t, harness, "default")
	records, err := harness.store.LoadAll()
	if err != nil || len(records) != 1 || !records[0].AgentActivation {
		t.Fatalf("activation installation state = %#v, %v", records, err)
	}
	nativeCalls := len(harness.native.commands)
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "other", "--yes")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	assertAgentActivationConflict(t, response, err)
	if records, loadErr := harness.store.LoadAll(); loadErr != nil || len(records) != 1 || len(harness.native.commands) != nativeCalls {
		t.Fatalf("blocked install mutated state: records=%#v commands=%d/%d error=%v", records, len(harness.native.commands), nativeCalls, loadErr)
	}
}

func TestAgentActivationUpdateCannotClaimAnOwnedTargetScope(t *testing.T) {
	harness := newLifecycleHarness(t)
	harness.validator.agentActivationBundles = map[string]bool{"other": true}
	installBundle(t, harness, "other")
	installBundle(t, harness, "default")

	record := recordForToolkit(t, harness, "ai4j")
	harness.validator.agentActivationBundles["default"] = true
	harness.validator.update = true
	nativeCalls := len(harness.native.commands)
	request := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--yes")
	response, err := harness.service.Update(context.Background(), request, CommandIO{})
	assertAgentActivationConflict(t, response, err)
	stored, present, loadErr := harness.store.LoadByID(record.InstallationID)
	if loadErr != nil || !present || stored.AgentActivation || stored.Source.Commit != record.Source.Commit || len(harness.native.commands) != nativeCalls {
		t.Fatalf("blocked update mutated state: record=%#v present=%t commands=%d/%d error=%v", stored, present, len(harness.native.commands), nativeCalls, loadErr)
	}
}

func TestAgentActivationReactivationAndRollbackCannotClaimAnOwnedTargetScope(t *testing.T) {
	harness := newLifecycleHarness(t)
	harness.validator.agentActivationBundles = map[string]bool{"default": true}
	installBundle(t, harness, "default")
	archivedID := recordForToolkit(t, harness, "ai4j").InstallationID
	uninstall := parseRequest[cli.UninstallRequest](t, "uninstall", archivedID, "--yes")
	if response, err := harness.service.Uninstall(context.Background(), uninstall, CommandIO{}); err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("uninstall = %#v, %v", response.Result(), err)
	}

	harness.validator.agentActivationBundles["other"] = true
	installBundle(t, harness, "other")
	nativeCalls := len(harness.native.commands)
	reactivate := parseRequest[cli.InstallRequest](t, "install", "--installation", archivedID, "--yes")
	response, err := harness.service.Install(context.Background(), reactivate, CommandIO{})
	assertAgentActivationConflict(t, response, err)
	rollback := parseRequest[cli.RollbackRequest](t, "rollback", archivedID, "--yes")
	response, err = harness.service.Rollback(context.Background(), rollback, CommandIO{})
	assertAgentActivationConflict(t, response, err)
	stored, present, loadErr := harness.store.LoadByID(archivedID)
	if loadErr != nil || !present || stored.Lifecycle != "archived" || !stored.AgentActivation || len(harness.native.commands) != nativeCalls {
		t.Fatalf("blocked restoration mutated state: record=%#v present=%t commands=%d/%d error=%v", stored, present, len(harness.native.commands), nativeCalls, loadErr)
	}
}

func TestAgentActivationCompositionInstallCannotClaimAnOwnedTargetScope(t *testing.T) {
	harness := newLifecycleHarness(t)
	harness.validator.agentActivationBundles = map[string]bool{"other": true, "common": true}
	installBundle(t, harness, "other")
	nativeCalls := len(harness.native.commands)
	request := parseRequest[cli.InstallRequest](t, "install", "--git-root", "https://github.com/oleksii", "--bundle", "common@v1", "--bundle", "everpure@v2", "--target", "claude", "--scope", "user", "--yes")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	assertAgentActivationConflict(t, response, err)
	if records, loadErr := harness.store.LoadAll(); loadErr != nil || len(records) != 1 || len(harness.native.commands) != nativeCalls {
		t.Fatalf("blocked composition mutated state: records=%#v commands=%d/%d error=%v", records, len(harness.native.commands), nativeCalls, loadErr)
	}
}

func TestAgentActivationConflictMatchesOnlyAnotherActiveInstallationInTheSameTargetScope(t *testing.T) {
	desired := installstate.Record{InstallationID: "desired", AgentActivation: true, Target: "claude", Scope: "user", ScopeRoot: filepath.Join("root", "scope"), Lifecycle: "active"}
	matching := desired
	matching.InstallationID = "matching"

	tests := []struct {
		name    string
		record  installstate.Record
		desired installstate.Record
		want    bool
	}{
		{name: "same target scope", record: matching, desired: desired, want: true},
		{name: "same installation", record: desired, desired: desired},
		{name: "archived owner", record: func() installstate.Record { record := matching; record.Lifecycle = "archived"; return record }(), desired: desired},
		{name: "different root", record: func() installstate.Record {
			record := matching
			record.ScopeRoot = filepath.Join("other", "scope")
			return record
		}(), desired: desired},
		{name: "different scope", record: func() installstate.Record { record := matching; record.Scope = "project-local"; return record }(), desired: desired},
		{name: "archived desired", record: matching, desired: func() installstate.Record { record := desired; record.Lifecycle = "archived"; return record }()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasAgentActivationConflict([]installstate.Record{test.record}, test.desired); got != test.want {
				t.Fatalf("hasAgentActivationConflict() = %t, want %t", got, test.want)
			}
		})
	}
}

func installBundle(t *testing.T, harness lifecycleHarness, bundle string) {
	t.Helper()
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", bundle, "--yes")
	response, err := harness.service.Install(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install %s = %#v, %v", bundle, response.Result(), err)
	}
}

func recordForToolkit(t *testing.T, harness lifecycleHarness, toolkitID string) installstate.Record {
	t.Helper()
	records, err := harness.store.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.ToolkitID == toolkitID {
			return record
		}
	}
	t.Fatalf("toolkit %q has no installation", toolkitID)
	return installstate.Record{}
}

func assertAgentActivationConflict(t *testing.T, response cli.Response, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	problems := response.Result().Errors()
	if response.Result().ExitCode() != result.ExitConflict || response.Result().Mutation() != result.MutationNotStarted || len(problems) != 1 || problems[0].Code() != "agent_activation_conflict" {
		t.Fatalf("agent activation conflict = %#v", response.Result())
	}
}
