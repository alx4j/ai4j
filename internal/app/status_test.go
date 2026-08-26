package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/human"
	"github.com/alx4j/ai4j/internal/cli/jsonout"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func TestListAndSelectedStatusInspectMultipleInstallationsOffline(t *testing.T) {
	t.Parallel()
	home, store, first := prepareStatusInstallation(t, "branch", strings.Repeat("a", 40))
	second := first
	second.InstallationID = "installation-002"
	second.ToolkitID = "zeta-toolkit"
	second.PluginID = "zeta-plugin"
	if err := store.SaveNew(second); err != nil {
		t.Fatal(err)
	}
	validator := &statusValidationStub{native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}}
	service := statusService{validation: validator, state: store, home: home}
	request, err := cli.NewParser("darwin").Parse([]string{"ai4j", "list", "--target", "claude", "--scope", "user", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.List(context.Background(), request.(cli.ListRequest))
	if err != nil {
		t.Fatal(err)
	}
	items := response.Data().(cli.ListData).Installations()
	if len(items) != 2 || items[0].ID().String() != "installation-001" || items[1].ID().String() != "installation-002" || validator.nativeCalls != 0 || validator.updateCalls != 0 {
		t.Fatalf("list = %#v, calls=%d/%d", items, validator.nativeCalls, validator.updateCalls)
	}
	var output bytes.Buffer
	if exit, err := jsonout.Render(&output, response); err != nil || exit != result.ExitSuccess || !strings.Contains(output.String(), `"installationId":"installation-002"`) {
		t.Fatalf("list JSON exit=%d error=%v output=%s", exit, err, output.String())
	}
	selectedRequest, err := cli.NewParser("darwin").Parse([]string{"ai4j", "status", "--installation", "installation-002"})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := service.Status(context.Background(), selectedRequest.(cli.StatusRequest))
	if err != nil {
		t.Fatal(err)
	}
	installation, present := selected.Data().(cli.StatusData).Installation()
	summary, summarized := selected.Data().(cli.StatusData).Summary()
	if !present || installation.ID().String() != "installation-002" || installation.ToolkitID() != "zeta-toolkit" || !summarized || summary.Target() != cli.BuildTargetClaude || summary.Scope() != cli.ScopeUser || summary.Lifecycle() != "active" || summary.Health() != "healthy" {
		t.Fatalf("selected installation = %#v, %t", installation, present)
	}
}

func TestStatusReportsInstalledSourceNativeAndOwnedStateWithoutMutation(t *testing.T) {
	t.Parallel()
	home, store, record := prepareStatusInstallation(t, "branch", strings.Repeat("a", 40))
	stateBefore, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	validator := &statusValidationStub{native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}}
	service := statusService{validation: validator, state: store, home: home}

	response, err := service.Status(context.Background(), statusRequest(t, false))
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.StatusData)
	installation, installed := data.Installation()
	if !installed || installation.ID().String() != record.InstallationID || installation.ToolkitID() != "ai4j" || installation.NativePluginID() != "ai4j-default" {
		t.Fatalf("installation = %#v, installed=%t", installation, installed)
	}
	if installation.Source().Repository().String() != record.Source.Repository || installation.Source().RequestedRef() != *record.Source.RequestedRef || installation.Source().RefKind() != cli.RefBranch || installation.Source().Commit().String() != record.Source.Commit {
		t.Fatalf("recorded source = %#v", installation.Source())
	}
	if data.NativeState().Registration() != cli.NativeRegistered || data.NativeState().Installation() != cli.NativeInstalled || data.NativeState().Enablement() != cli.NativeEnabled || data.NativeState().Activation() != cli.NativeActivationNotObservable {
		t.Fatalf("native state = %#v", data.NativeState())
	}
	assertStatusDrift(t, data, record.Catalog.Path, cli.DriftUnchanged)
	assertStatusDrift(t, data, record.Rules.Path, cli.DriftUnchanged)
	if response.Result().Status() != result.StatusOK || response.Result().ExitCode() != result.ExitSuccess || data.UpdateDisposition() != result.UpdateNotChecked || validator.nativeCalls != 1 || validator.updateCalls != 0 {
		t.Fatalf("result=%s/%d disposition=%s calls=%d/%d", response.Result().Status(), response.Result().ExitCode(), data.UpdateDisposition(), validator.nativeCalls, validator.updateCalls)
	}
	stateAfter, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Fatal("plain status changed installation state")
	}
	var output bytes.Buffer
	exitCode, err := jsonout.Render(&output, response)
	if err != nil || exitCode != result.ExitSuccess || !bytes.Contains(output.Bytes(), []byte(`"oid":"`+record.Source.Commit+`"`)) {
		t.Fatalf("JSON status exit=%d error=%v output=%s", exitCode, err, output.String())
	}
	output.Reset()
	exitCode, err = human.Render(&output, response)
	if err != nil || exitCode != result.ExitSuccess || !strings.Contains(output.String(), "commit: sha1:"+record.Source.Commit) || !strings.Contains(output.String(), "state=unchanged") {
		t.Fatalf("human status exit=%d error=%v output=%s", exitCode, err, output.String())
	}
}

func TestStatusReportsNotInstalledWithoutNativeOrSourceCalls(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	store, err := installstate.NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	validator := &statusValidationStub{}
	service := statusService{validation: validator, state: store, home: home}

	response, err := service.Status(context.Background(), statusRequest(t, true))
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.StatusData)
	if _, installed := data.Installation(); installed || data.UpdateDisposition() != result.UpdateNotInstalled || data.NativeState().Installation() != cli.NativeInstallationNotObservable {
		t.Fatalf("status data = %#v", data)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 || validator.nativeCalls != 0 || validator.updateCalls != 0 {
		t.Fatalf("home=%v error=%v calls=%d/%d", entries, err, validator.nativeCalls, validator.updateCalls)
	}
}

func TestStatusClassifiesOrdinaryOwnedFileDrift(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		state cli.DriftState
		apply func(*testing.T, string)
	}{
		{name: "modified", state: cli.DriftModified, apply: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("changed rules"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing", state: cli.DriftMissing, apply: func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "conflicting", state: cli.DriftConflicting, apply: func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home, store, record := prepareStatusInstallation(t, "branch", strings.Repeat("a", 40))
			test.apply(t, filepath.Join(home, filepath.FromSlash(record.Rules.Path)))
			validator := &statusValidationStub{native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}}

			response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t, false))
			if err != nil {
				t.Fatal(err)
			}
			data := response.Data().(cli.StatusData)
			assertStatusDrift(t, data, record.Rules.Path, test.state)
			if response.Result().ExitCode() != result.ExitSuccess || response.Result().Status() != result.StatusOK {
				t.Fatalf("ordinary drift result = %s/%d", response.Result().Status(), response.Result().ExitCode())
			}
		})
	}
}

func TestStatusReportsInterruptedOperationWithoutRepair(t *testing.T) {
	t.Parallel()
	home, store, record := prepareStatusInstallation(t, "branch", strings.Repeat("a", 40))
	marker, err := installstate.NewInstallMarker("operation-002", record.InstallationID, record.Source.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarker(marker); err != nil {
		t.Fatal(err)
	}
	stateBefore, _ := os.ReadFile(store.Path())
	markerBefore, _ := os.ReadFile(store.MarkerPath())
	validator := &statusValidationStub{native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}}

	response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t, true))
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.StatusData)
	if data.RecoveryState().State() != cli.RecoveryIncompleteJournal || data.RecoveryState().HasPhase() || response.Result().Failure() != result.FailureRecovery || response.Result().ExitCode() != result.ExitRecoveryRequired || validator.updateCalls != 0 {
		t.Fatalf("recovery=%#v result=%s/%d update calls=%d", data.RecoveryState(), response.Result().Failure(), response.Result().ExitCode(), validator.updateCalls)
	}
	stateAfter, _ := os.ReadFile(store.Path())
	markerAfter, _ := os.ReadFile(store.MarkerPath())
	if !bytes.Equal(stateBefore, stateAfter) || !bytes.Equal(markerBefore, markerAfter) {
		t.Fatal("status repaired or changed interrupted operation state")
	}
}

func TestStatusReportsUnsupportedStateSchemaWithoutRepair(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	store, err := installstate.NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	state := []byte("{\"schemaVersion\":3}\n")
	if err := os.WriteFile(store.Path(), state, 0o600); err != nil {
		t.Fatal(err)
	}
	validator := &statusValidationStub{}

	response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t, true))
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.StatusData)
	contents, readErr := os.ReadFile(store.Path())
	if data.RecoveryState().State() != cli.RecoveryUnsupportedSchema || response.Result().ExitCode() != result.ExitRecoveryRequired || validator.nativeCalls != 0 || validator.updateCalls != 0 || readErr != nil || !bytes.Equal(contents, state) {
		t.Fatalf("recovery=%s exit=%d calls=%d/%d read=%v changed=%t", data.RecoveryState().State(), response.Result().ExitCode(), validator.nativeCalls, validator.updateCalls, readErr, !bytes.Equal(contents, state))
	}
}

func TestStatusDistinguishesDisabledAndMissingNativePlugin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		observation  validation.NativeStatus
		installation cli.NativeInstallation
		enablement   cli.NativeEnablement
	}{
		{name: "disabled", observation: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true}, installation: cli.NativeInstalled, enablement: cli.NativeDisabled},
		{name: "missing", observation: validation.NativeStatus{MarketplaceRegistered: true}, installation: cli.NativeNotInstalled, enablement: cli.NativeEnablementNotObservable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home, store, _ := prepareStatusInstallation(t, "branch", strings.Repeat("a", 40))
			validator := &statusValidationStub{native: test.observation}

			response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t, false))
			if err != nil {
				t.Fatal(err)
			}
			native := response.Data().(cli.StatusData).NativeState()
			if native.Installation() != test.installation || native.Enablement() != test.enablement {
				t.Fatalf("native = %s/%s", native.Installation(), native.Enablement())
			}
		})
	}
}

func TestStatusWarnsWhenNativeStateCannotBeObserved(t *testing.T) {
	t.Parallel()
	home, store, _ := prepareStatusInstallation(t, "branch", strings.Repeat("a", 40))
	problem, err := result.NewProblem("native_inspection_failed", "Claude plugin state could not be inspected", nil)
	if err != nil {
		t.Fatal(err)
	}
	validator := &statusValidationStub{nativeProblem: &problem}

	response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t, false))
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.StatusData)
	if response.Result().Status() != result.StatusOK || response.Result().ExitCode() != result.ExitSuccess || len(response.Result().Warnings()) != 1 || data.NativeState().Installation() != cli.NativeInstallationUnknown || data.NativeState().Enablement() != cli.NativeEnablementUnknown {
		t.Fatalf("result=%s/%d warnings=%v native=%#v", response.Result().Status(), response.Result().ExitCode(), response.Result().Warnings(), data.NativeState())
	}
}

func TestStatusCheckUpdatesClassifiesStoredReferences(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		refKind   string
		commit    string
		update    validation.UpdateReport
		want      result.UpdateDisposition
		wantExit  result.ExitCode
		wantCalls int
	}{
		{name: "branch up to date", refKind: "branch", commit: strings.Repeat("a", 40), update: validation.UpdateReport{Report: testLifecycleReport(t), Disposition: gitsource.UpdateNoChange}, want: result.UpdateUpToDate, wantExit: result.ExitSuccess, wantCalls: 1},
		{name: "branch available", refKind: "branch", commit: strings.Repeat("1", 40), update: validation.UpdateReport{Report: testLifecycleReport(t), Disposition: gitsource.UpdateAvailable}, want: result.UpdateAvailable, wantExit: result.ExitSuccess, wantCalls: 1},
		{name: "branch rewritten", refKind: "branch", commit: strings.Repeat("1", 40), update: validation.UpdateReport{Report: testLifecycleReport(t), Disposition: gitsource.UpdateRefRewritten}, want: result.UpdateRefRewritten, wantExit: result.ExitSuccess, wantCalls: 1},
		{name: "tag pinned", refKind: "tag", commit: strings.Repeat("a", 40), update: validation.UpdateReport{Report: testLifecycleReport(t)}, want: result.UpdatePinned, wantExit: result.ExitSuccess, wantCalls: 1},
		{name: "tag moved", refKind: "tag", commit: strings.Repeat("1", 40), update: validation.UpdateReport{Report: testLifecycleReport(t)}, want: result.UpdateRefRewritten, wantExit: result.ExitSuccess, wantCalls: 1},
		{name: "commit pinned", refKind: "commit", commit: strings.Repeat("a", 40), want: result.UpdatePinned, wantExit: result.ExitSuccess, wantCalls: 0},
		{name: "source failure", refKind: "branch", commit: strings.Repeat("a", 40), update: validation.UpdateReport{Report: validation.Report{Failure: validation.FailureSource}}, want: result.UpdateUnknown, wantExit: result.ExitSource, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home, store, _ := prepareStatusInstallation(t, test.refKind, test.commit)
			stateBefore, err := os.ReadFile(store.Path())
			if err != nil {
				t.Fatal(err)
			}
			validator := &statusValidationStub{native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}, update: test.update}

			response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t, true))
			if err != nil {
				t.Fatal(err)
			}
			data := response.Data().(cli.StatusData)
			if data.UpdateDisposition() != test.want || response.Result().ExitCode() != test.wantExit || validator.updateCalls != test.wantCalls {
				t.Fatalf("disposition=%s exit=%d calls=%d", data.UpdateDisposition(), response.Result().ExitCode(), validator.updateCalls)
			}
			stateAfter, err := os.ReadFile(store.Path())
			if err != nil || !bytes.Equal(stateBefore, stateAfter) {
				t.Fatalf("update check changed installation state: error=%v", err)
			}
		})
	}
}

type statusValidationStub struct {
	native        validation.NativeStatus
	nativeProblem *result.Problem
	update        validation.UpdateReport
	nativeCalls   int
	updateCalls   int
}

func (s *statusValidationStub) InspectNativeStatus(context.Context) (validation.NativeStatus, *result.Problem) {
	s.nativeCalls++
	return s.native, s.nativeProblem
}

func (s *statusValidationStub) InspectNativeStatusAt(context.Context, string, string, string) (validation.NativeStatus, *result.Problem) {
	s.nativeCalls++
	return s.native, s.nativeProblem
}

func (s *statusValidationStub) ValidateUpdate(context.Context, cli.SourceOptions, domain.CommitOID) validation.UpdateReport {
	s.updateCalls++
	return s.update
}

func statusRequest(t *testing.T, checkUpdates bool) cli.StatusRequest {
	t.Helper()
	arguments := []string{"ai4j", "status"}
	if checkUpdates {
		arguments = append(arguments, "--check-updates")
	}
	request, err := cli.NewParser("darwin").Parse(arguments)
	if err != nil {
		t.Fatal(err)
	}
	return request.(cli.StatusRequest)
}

func prepareStatusInstallation(t *testing.T, refKind, commit string) (string, installstate.Store, installstate.Record) {
	t.Helper()
	home := t.TempDir()
	record := testInstallationRecord(refKind, commit)
	if refKind == "default_branch" {
		record.Source.RequestedRef = nil
	}
	if refKind == "commit" {
		reference := commit
		record.Source.RequestedRef = &reference
	}
	catalog := []byte("catalog fixture")
	rules := []byte("rules fixture")
	catalogDigest := sha256.Sum256(catalog)
	rulesDigest := sha256.Sum256(rules)
	record.Catalog.Checksum = fmt.Sprintf("%x", catalogDigest)
	record.Rules.Checksum = fmt.Sprintf("%x", rulesDigest)
	catalogPath := filepath.Join(home, "Library", "Application Support", "ai4j", filepath.FromSlash(record.Catalog.Path))
	rulesPath := filepath.Join(home, filepath.FromSlash(record.Rules.Path))
	for path, contents := range map[string][]byte{catalogPath: catalog, rulesPath: rules} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := installstate.NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	return home, store, record
}

func assertStatusDrift(t *testing.T, data cli.StatusData, resource string, want cli.DriftState) {
	t.Helper()
	for _, item := range data.Drift() {
		if item.Resource() == resource {
			if item.State() != want {
				t.Fatalf("drift for %s = %s, want %s", resource, item.State(), want)
			}
			return
		}
	}
	t.Fatalf("drift for %s was not reported", resource)
}
