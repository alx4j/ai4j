package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

func TestListAndSelectedStatusInspectMultipleInstallations(t *testing.T) {
	t.Parallel()
	home, store, first := prepareStatusInstallation(t, "branch", strings.Repeat("a", 40))
	second := first
	second.InstallationID = "installation-002"
	second.ToolkitID = "zeta-toolkit"
	second.Packages = []installstate.NativePackage{{ID: "zeta-plugin", Path: "plugins/zeta-plugin"}}
	second.NativeResources = []string{"claude:marketplace:ai4j", "claude:zeta-plugin@ai4j"}
	if err := store.SaveNew(second); err != nil {
		t.Fatal(err)
	}
	validator := &statusValidationStub{t: t, native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}}
	service := statusService{validation: validator, state: store, home: home}
	request, err := cli.NewParser().Parse([]string{"ai4j", "list", "--target", "claude", "--scope", "user", "--json"})
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
	selectedRequest, err := cli.NewParser().Parse([]string{"ai4j", "status", "installation-002"})
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
	validator := &statusValidationStub{t: t, native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}}
	service := statusService{validation: validator, state: store, home: home}

	response, err := service.Status(context.Background(), statusRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.StatusData)
	installation, installed := data.Installation()
	if !installed || installation.ID().String() != record.InstallationID || installation.ToolkitID() != "ai4j" || !slices.Equal(installation.NativePluginIDs(), []string{"ai4j-default"}) {
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
	if response.Result().Status() != result.StatusNoChange || response.Result().ExitCode() != result.ExitSuccess || data.UpdateDisposition() != result.UpdateUpToDate || validator.nativeCalls != 1 || validator.updateCalls != 1 {
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
	if err != nil || exitCode != result.ExitSuccess || !strings.Contains(output.String(), "Exact commit: "+record.Source.Commit) || !strings.Contains(output.String(), "[OK] "+record.Rules.Path+" - unchanged") {
		t.Fatalf("human status exit=%d error=%v output=%s", exitCode, err, output.String())
	}
}

func TestStatusReportsMissingSelectedInstallation(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	store, err := installstate.NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	validator := &statusValidationStub{t: t}
	service := statusService{validation: validator, state: store, home: home}

	response, err := service.Status(context.Background(), statusRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.StatusData)
	if _, installed := data.Installation(); installed || data.UpdateDisposition() != result.UpdateNotInstalled || data.NativeState().Installation() != cli.NativeInstallationNotObservable {
		t.Fatalf("status data = %#v", data)
	}
	if response.Result().Status() != result.StatusError || response.Result().Failure() != result.FailureConflict || response.Result().ExitCode() != result.ExitConflict || len(response.Result().Errors()) != 1 || response.Result().Errors()[0].Code() != "installation_not_found" {
		t.Fatalf("missing installation result = %s/%s/%d errors=%v", response.Result().Status(), response.Result().Failure(), response.Result().ExitCode(), response.Result().Errors())
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
			validator := &statusValidationStub{t: t, native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}}

			response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t))
			if err != nil {
				t.Fatal(err)
			}
			data := response.Data().(cli.StatusData)
			assertStatusDrift(t, data, record.Rules.Path, test.state)
			if response.Result().ExitCode() != result.ExitSuccess || response.Result().Status() != result.StatusDegraded || len(response.Result().Warnings()) == 0 {
				t.Fatalf("ordinary drift result = %s/%d", response.Result().Status(), response.Result().ExitCode())
			}
			summary, ok := data.Summary()
			if !ok || summary.Health() != "drifted" {
				t.Fatalf("current summary health = %#v, present=%t", summary, ok)
			}
			var output bytes.Buffer
			if _, err := jsonout.Render(&output, response); err != nil || !strings.Contains(output.String(), `"status":"degraded"`) || !strings.Contains(output.String(), `"health":"drifted"`) {
				t.Fatalf("JSON status does not agree with current health: error=%v output=%s", err, output.String())
			}
		})
	}
}

func TestStatusReportsPinnedSourceAndCurrentDriftTogether(t *testing.T) {
	t.Parallel()
	home, store, record := prepareStatusInstallation(t, "commit", strings.Repeat("a", 40))
	if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(record.Rules.Path)), []byte("changed rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	validator := &statusValidationStub{t: t, native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}}

	response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if response.Result().Status() != result.StatusDegraded || response.Result().ExitCode() != result.ExitSuccess || response.Data().(cli.StatusData).UpdateDisposition() != result.UpdatePinned {
		t.Fatalf("pinned drift result = %s/%d disposition=%s", response.Result().Status(), response.Result().ExitCode(), response.Data().(cli.StatusData).UpdateDisposition())
	}
}

func TestStatusTreatsArchivedInstallationAsIntentionallyInactive(t *testing.T) {
	t.Parallel()
	home, store, record := prepareStatusInstallation(t, "branch", strings.Repeat("a", 40))
	record.Lifecycle = "archived"
	record.Health = "healthy"
	record.Catalog = installstate.OwnedFile{}
	record.Rules = installstate.OwnedFile{}
	record.NativeResources = nil
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	validator := &statusValidationStub{t: t}

	response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.StatusData)
	summary, summarized := data.Summary()
	if response.Result().Status() != result.StatusNoChange || response.Result().ExitCode() != result.ExitSuccess || data.UpdateDisposition() != result.UpdateNotChecked || len(data.Drift()) != 0 || !summarized || summary.Lifecycle() != "archived" || summary.Health() != "healthy" {
		t.Fatalf("archived status = %s/%d data=%#v summary=%#v", response.Result().Status(), response.Result().ExitCode(), data, summary)
	}
	if validator.nativeCalls != 0 || validator.updateCalls != 0 || validator.selectionCalls != 0 {
		t.Fatalf("archived status performed active checks: native=%d update=%d selection=%d", validator.nativeCalls, validator.updateCalls, validator.selectionCalls)
	}
	var output bytes.Buffer
	if _, err := human.Render(&output, response); err != nil || !strings.Contains(output.String(), "is archived") || !strings.Contains(output.String(), "did not look for source updates") {
		t.Fatalf("archived human status: error=%v output=%s", err, output.String())
	}
}

func TestStatusReportsInterruptedOperationWithoutRepair(t *testing.T) {
	t.Parallel()
	home, store, record := prepareStatusInstallation(t, "branch", strings.Repeat("a", 40))
	marker, err := installstate.NewResourceMarker("install", "operation-002", record.InstallationID, record.Source.Commit, append(record.NativeResources,
		"owned:"+record.Catalog.Path,
		"owned:state/installation.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarker(marker); err != nil {
		t.Fatal(err)
	}
	stateBefore, _ := os.ReadFile(store.Path())
	markerBefore, _ := os.ReadFile(store.MarkerPath())
	validator := &statusValidationStub{t: t, native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}}

	response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t))
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
	validator := &statusValidationStub{t: t}

	response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t))
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
			validator := &statusValidationStub{t: t, native: test.observation}

			response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t))
			if err != nil {
				t.Fatal(err)
			}
			native := response.Data().(cli.StatusData).NativeState()
			if native.Installation() != test.installation || native.Enablement() != test.enablement {
				t.Fatalf("native = %s/%s", native.Installation(), native.Enablement())
			}
			if response.Result().Status() != result.StatusDegraded || len(response.Result().Warnings()) == 0 {
				t.Fatalf("native health result = %s warnings=%v", response.Result().Status(), response.Result().Warnings())
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
	validator := &statusValidationStub{t: t, nativeProblem: &problem}

	response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	data := response.Data().(cli.StatusData)
	if response.Result().Status() != result.StatusDegraded || response.Result().ExitCode() != result.ExitSuccess || len(response.Result().Warnings()) != 1 || data.NativeState().Installation() != cli.NativeInstallationUnknown || data.NativeState().Enablement() != cli.NativeEnablementUnknown {
		t.Fatalf("result=%s/%d warnings=%v native=%#v", response.Result().Status(), response.Result().ExitCode(), response.Result().Warnings(), data.NativeState())
	}
	summary, ok := data.Summary()
	if !ok || summary.Health() != "unknown" {
		t.Fatalf("unknown native state summary health = %#v, present=%t", summary, ok)
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
			validator := &statusValidationStub{t: t, native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true}, update: test.update}

			response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t))
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

func TestStatusChecksLocalDevelopmentSourceDigest(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		current     string
		disposition result.UpdateDisposition
	}{
		{name: "up to date", current: strings.Repeat("a", 64), disposition: result.UpdateUpToDate},
		{name: "update available", current: strings.Repeat("d", 64), disposition: result.UpdateAvailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, store, record := prepareStatusInstallation(t, "branch", strings.Repeat("a", 40))
			checkout := t.TempDir()
			record.Source = installstate.Source{
				Mode: "development_source", Selection: domain.ExplicitSource().String(), Checkout: checkout,
				SourceDigest: strings.Repeat("a", 64), RenderedDigest: strings.Repeat("b", 64), BundleDigest: strings.Repeat("c", 64),
			}
			if err := store.Save(record); err != nil {
				t.Fatal(err)
			}
			digest, _ := domain.NewRenderedDigest(test.current)
			rendered, _ := domain.NewRenderedDigest(strings.Repeat("e", 64))
			build, _ := domain.NewBuildCommit(strings.Repeat("f", 40))
			source, err := cli.NewDevelopmentSource(checkout, digest, rendered, build, true)
			if err != nil {
				t.Fatal(err)
			}
			validator := &statusValidationStub{
				t: t, native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true},
				selection: validation.LifecycleSelection{Source: source},
			}
			response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t))
			if err != nil {
				t.Fatal(err)
			}
			if got := response.Data().(cli.StatusData).UpdateDisposition(); got != test.disposition || validator.selectionCalls != 1 || validator.updateCalls != 0 {
				t.Fatalf("disposition=%s selection calls=%d update calls=%d", got, validator.selectionCalls, validator.updateCalls)
			}
		})
	}
}

func TestStatusPreservesSpecificUpdateCheckProblem(t *testing.T) {
	t.Parallel()
	home, store, _ := prepareStatusInstallation(t, "branch", strings.Repeat("a", 40))
	problem, err := result.NewProblem("git_executable_unavailable", "Git is required to check this toolkit source", nil)
	if err != nil {
		t.Fatal(err)
	}
	validator := &statusValidationStub{
		t: t, native: validation.NativeStatus{MarketplaceRegistered: true, PluginInstalled: true, PluginEnabled: true},
		update: validation.UpdateReport{Report: validation.Report{Failure: validation.FailureEnvironment, Problems: []result.Problem{problem}}},
	}

	response, err := (statusService{validation: validator, state: store, home: home}).Status(context.Background(), statusRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	problems := response.Result().Errors()
	if response.Result().ExitCode() != result.ExitSource || len(problems) != 1 || problems[0].Code() != problem.Code() || problems[0].Message() != problem.Message() {
		t.Fatalf("update problem was not preserved: result=%#v", response.Result())
	}
}

type statusValidationStub struct {
	t              *testing.T
	native         validation.NativeStatus
	nativeProblem  *result.Problem
	update         validation.UpdateReport
	selection      validation.LifecycleSelection
	nativeCalls    int
	updateCalls    int
	selectionCalls int
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
	if !s.update.Report.HasSource() && s.update.Report.Failure == validation.FailureNone && s.update.Disposition == "" {
		return validation.UpdateReport{Report: testLifecycleReport(s.t), Disposition: gitsource.UpdateNoChange}
	}
	return s.update
}

func (s *statusValidationStub) SelectLifecycle(context.Context, cli.SourceOptions, string) validation.LifecycleSelection {
	s.selectionCalls++
	if s.selection.HasSource() || len(s.selection.Problems) != 0 {
		return s.selection
	}
	return validation.LifecycleSelection{Source: testLifecycleReport(s.t).Source}
}

func statusRequest(t *testing.T) cli.StatusRequest {
	t.Helper()
	request, err := cli.NewParser().Parse([]string{"ai4j", "status", "installation-001"})
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
