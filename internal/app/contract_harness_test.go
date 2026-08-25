package app_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/app"
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/human"
	"github.com/alx4j/ai4j/internal/cli/jsonout"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	githubsource "github.com/alx4j/ai4j/internal/source/github"
)

const (
	contractSecretCanary  = "AI4J_RAW_SECRET_CANARY_DO_NOT_DISCLOSE"
	contractHelperEnv     = "AI4J_CONTRACT_HELPER"
	contractIrrelevantEnv = "AI4J_CONTRACT_IRRELEVANT"
)

func TestContractHarnessEveryCanonicalCommandIsSchemaValidAndDeterministic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		arguments  []string
		command    cli.Command
		schemaName string
		usesFake   bool
		changed    bool
	}{
		{name: "init", arguments: []string{"ai4j", "init", "--target", "claude", "--target", "codex", "--output", "new-toolkit", "--json"}, command: cli.CommandInit, schemaName: "init.json", usesFake: true, changed: true},
		{name: "validate", arguments: []string{"ai4j", "validate", "--json"}, command: cli.CommandValidate, schemaName: "validate.json", usesFake: true},
		{name: "build", arguments: []string{"ai4j", "build", "--target", "codex", "--host", "darwin-arm64", "--output", "dist", "--all", "--json"}, command: cli.CommandBuild, schemaName: "build.json", usesFake: true, changed: true},
		{name: "plan install", arguments: []string{"ai4j", "plan", "install", "--json"}, command: cli.CommandPlanInstall, schemaName: "plan.install.json", usesFake: true},
		{name: "install", arguments: []string{"ai4j", "install", "--yes", "--json"}, command: cli.CommandInstall, schemaName: "install.json", usesFake: true, changed: true},
		{name: "plan update", arguments: []string{"ai4j", "plan", "update", "--json"}, command: cli.CommandPlanUpdate, schemaName: "plan.update.json", usesFake: true},
		{name: "update", arguments: []string{"ai4j", "update", "--yes", "--json"}, command: cli.CommandUpdate, schemaName: "update.json", usesFake: true, changed: true},
		{name: "status", arguments: []string{"ai4j", "status", "--json"}, command: cli.CommandStatus, schemaName: "status.json", usesFake: true},
		{name: "plan uninstall", arguments: []string{"ai4j", "plan", "uninstall", "--json"}, command: cli.CommandPlanUninstall, schemaName: "plan.uninstall.json", usesFake: true},
		{name: "uninstall", arguments: []string{"ai4j", "uninstall", "--yes", "--json"}, command: cli.CommandUninstall, schemaName: "uninstall.json", usesFake: true, changed: true},
		{name: "version", arguments: []string{"ai4j", "version", "--json"}, command: cli.CommandVersion, schemaName: "version.json"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &deterministicCommands{t: t, secret: contractSecretCanary}
			factory := app.OtherCommandsFactory(func() (app.CommandHandler, error) {
				panic("version constructed non-version command dependencies")
			})
			if test.usesFake {
				factory = fake.factory(commandResponse)
			}
			application := newTestApplication(t, validBuild(), factory, human.Render, jsonout.Render)
			var want []byte
			for run := 0; run < 20; run++ {
				stdout := new(bytes.Buffer)
				stderr := new(bytes.Buffer)
				exitCode := application.Run(test.arguments, panicReader{}, stdout, stderr)
				if exitCode != result.ExitSuccess.Int() {
					t.Fatalf("run %d exit = %d, want 0; stderr=%q", run, exitCode, stderr.String())
				}
				if stderr.Len() != 0 {
					t.Fatalf("run %d stderr = %q, want empty", run, stderr.String())
				}
				document := assertSingleJSONDocument(t, stdout.Bytes(), test.command.String(), result.ExitSuccess, test.changed)
				if document.SchemaVersion != 1 {
					t.Fatalf("schemaVersion = %d, want 1", document.SchemaVersion)
				}
				if test.command == cli.CommandVersion {
					assertInjectedVersionFacts(t, document.Data)
				}
				if run == 0 {
					validateSchema(t, test.schemaName, stdout.Bytes())
					want = append([]byte(nil), stdout.Bytes()...)
				} else if !bytes.Equal(stdout.Bytes(), want) {
					t.Fatalf("run %d differs after shuffled fixture input\ngot:  %q\nwant: %q", run, stdout.Bytes(), want)
				}
			}
			if test.usesFake {
				if fake.factoryCalls != 20 || fake.handlerCalls != 20 {
					t.Fatalf("fake calls = factory %d handler %d, want 20 each", fake.factoryCalls, fake.handlerCalls)
				}
			} else if fake.factoryCalls != 0 || fake.handlerCalls != 0 {
				t.Fatalf("version touched fake commands: factory %d handler %d", fake.factoryCalls, fake.handlerCalls)
			}
		})
	}
}

func TestContractHarnessResultFamiliesMatchProcessAndEnvelope(t *testing.T) {
	t.Parallel()

	tests := []resultScenario{
		{name: "committed success", arguments: []string{"ai4j", "install", "--yes", "--json"}, command: cli.CommandInstall, schemaName: "install.json", scenario: scenarioCommitted, wantExit: result.ExitSuccess, wantStatus: result.StatusOK, wantChanged: true, wantPhase: result.PhaseComplete},
		{name: "pinned no change", arguments: []string{"ai4j", "plan", "update", "--json"}, command: cli.CommandPlanUpdate, schemaName: "plan.update.json", scenario: scenarioPinned, wantExit: result.ExitSuccess, wantStatus: result.StatusNoChange},
		{name: "degraded warning", arguments: []string{"ai4j", "status", "--json"}, command: cli.CommandStatus, schemaName: "status.json", scenario: scenarioDegraded, wantExit: result.ExitSuccess, wantStatus: result.StatusDegraded},
		{name: "cancelled", arguments: []string{"ai4j", "validate", "--json"}, command: cli.CommandValidate, schemaName: "validate.json", scenario: scenarioCancelled, wantExit: result.ExitCancelled, wantStatus: result.StatusCancelled},
		{name: "usage", arguments: []string{"ai4j", "validate", "--credential=" + contractSecretCanary, "--json"}, schemaName: "usage.json", scenario: scenarioUsage, wantExit: result.ExitUsageOrApproval, wantStatus: result.StatusError, noFactory: true},
		{name: "approval required", arguments: []string{"ai4j", "install", "--json"}, command: cli.CommandInstall, schemaName: "install.json", scenario: scenarioApproval, wantExit: result.ExitUsageOrApproval, wantStatus: result.StatusError},
		{name: "environment", arguments: []string{"ai4j", "validate", "--json"}, command: cli.CommandValidate, schemaName: "validate.json", scenario: scenarioEnvironment, wantExit: result.ExitEnvironment, wantStatus: result.StatusError},
		{name: "source update check", arguments: []string{"ai4j", "status", "--check-updates", "--json"}, command: cli.CommandStatus, schemaName: "status.json", scenario: scenarioSource, wantExit: result.ExitSource, wantStatus: result.StatusError},
		{name: "validation", arguments: []string{"ai4j", "validate", "--json"}, command: cli.CommandValidate, schemaName: "validate.json", scenario: scenarioValidation, wantExit: result.ExitValidation, wantStatus: result.StatusError},
		{name: "conflict", arguments: []string{"ai4j", "plan", "install", "--json"}, command: cli.CommandPlanInstall, schemaName: "plan.install.json", scenario: scenarioConflict, wantExit: result.ExitConflict, wantStatus: result.StatusError},
		{name: "pre-mutation empty recovery", arguments: []string{"ai4j", "install", "--yes", "--json"}, command: cli.CommandInstall, schemaName: "install.json", scenario: scenarioPreMutationRolledBack, wantExit: result.ExitValidation, wantStatus: result.StatusError, wantPhase: result.PhaseCompleteRolledBack},
		{name: "post mutation compensated", arguments: []string{"ai4j", "install", "--yes", "--json"}, command: cli.CommandInstall, schemaName: "install.json", scenario: scenarioCompensated, wantExit: result.ExitCompensated, wantStatus: result.StatusError, wantPhase: result.PhaseCompleteRolledBack},
		{name: "committed cleanup pending", arguments: []string{"ai4j", "install", "--yes", "--json"}, command: cli.CommandInstall, schemaName: "install.json", scenario: scenarioCommittedCleanup, wantExit: result.ExitRecoveryRequired, wantStatus: result.StatusError, wantChanged: true, wantPhase: result.PhaseCommittedCleanupPending},
		{name: "rolled back cleanup pending", arguments: []string{"ai4j", "install", "--yes", "--json"}, command: cli.CommandInstall, schemaName: "install.json", scenario: scenarioRolledBackCleanup, wantExit: result.ExitRecoveryRequired, wantStatus: result.StatusError, wantPhase: result.PhaseRolledBackCleanupPending},
		{name: "prepared recovery required", arguments: []string{"ai4j", "install", "--yes", "--json"}, command: cli.CommandInstall, schemaName: "install.json", scenario: scenarioPreparedRecovery, wantExit: result.ExitRecoveryRequired, wantStatus: result.StatusError, wantPhase: result.PhasePrepared},
		{name: "unexpected handler failure", arguments: []string{"ai4j", "validate", "--json"}, command: cli.CommandValidate, schemaName: "validate.json", scenario: scenarioInternal, wantExit: result.ExitUnexpectedInternal, wantStatus: result.StatusError},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			factoryCalls := 0
			handlerCalls := 0
			factory := app.OtherCommandsFactory(func() (app.CommandHandler, error) {
				factoryCalls++
				return func(request cli.Request, _ app.CommandIO) (cli.Response, error) {
					handlerCalls++
					if test.scenario == scenarioInternal {
						return cli.Response{}, errors.New(contractSecretCanary)
					}
					return scenarioResponse(t, test.scenario, request.Command(), handlerCalls%2 == 0), nil
				}, nil
			})
			if test.noFactory {
				factory = func() (app.CommandHandler, error) {
					factoryCalls++
					panic("usage constructed non-version command dependencies")
				}
			}
			application := newTestApplication(t, validBuild(), factory, human.Render, jsonout.Render)
			var wantOutput []byte
			const runs = 4
			for run := 0; run < runs; run++ {
				stdout := new(bytes.Buffer)
				stderr := new(bytes.Buffer)
				exitCode := application.Run(test.arguments, panicReader{}, stdout, stderr)
				if exitCode != test.wantExit.Int() {
					t.Fatalf("run %d Application.Run() exit = %d, want %d; stderr=%q", run, exitCode, test.wantExit, stderr.String())
				}
				if stderr.Len() != 0 {
					t.Fatalf("run %d stderr = %q, want empty", run, stderr.String())
				}
				command := test.command.String()
				if test.noFactory {
					command = ""
				}
				document := assertSingleJSONDocument(t, stdout.Bytes(), command, test.wantExit, test.wantChanged)
				if document.Status != test.wantStatus.String() {
					t.Fatalf("run %d status = %q, want %q", run, document.Status, test.wantStatus)
				}
				if test.wantPhase != "" {
					assertOperationPhase(t, document.Data, test.wantPhase)
				}
				if test.scenario == scenarioPinned {
					assertDataString(t, document.Data, "updateDisposition", result.UpdatePinned.String())
				}
				assertDiagnosticsBounded(t, document.Warnings)
				assertDiagnosticsBounded(t, document.Errors)
				if strings.Contains(stdout.String(), contractSecretCanary) || strings.Contains(stderr.String(), contractSecretCanary) {
					t.Fatalf("run %d raw secret canary was disclosed: stdout=%q stderr=%q", run, stdout.String(), stderr.String())
				}
				if run == 0 {
					validateSchema(t, test.schemaName, stdout.Bytes())
					wantOutput = append([]byte(nil), stdout.Bytes()...)
				} else if !bytes.Equal(stdout.Bytes(), wantOutput) {
					t.Fatalf("run %d differs after reversed fixture input\ngot:  %q\nwant: %q", run, stdout.Bytes(), wantOutput)
				}
			}
			if test.noFactory {
				if factoryCalls != 0 || handlerCalls != 0 {
					t.Fatalf("usage touched fake commands: factory %d handler %d", factoryCalls, handlerCalls)
				}
			} else if factoryCalls != runs || handlerCalls != runs {
				t.Fatalf("fake calls = factory %d handler %d, want %d each", factoryCalls, handlerCalls, runs)
			}
		})
	}
}

func TestContractHarnessRepresentativeHumanOutputIsDeterministic(t *testing.T) {
	t.Parallel()

	fake := &deterministicCommands{t: t, secret: contractSecretCanary}
	application := newTestApplication(t, validBuild(), fake.factory(commandResponse), human.Render, jsonout.Render)
	var want []byte
	for run := 0; run < 20; run++ {
		stdout := new(bytes.Buffer)
		stderr := new(bytes.Buffer)
		exitCode := application.Run([]string{"ai4j", "plan", "install"}, panicReader{}, stdout, stderr)
		if exitCode != result.ExitSuccess.Int() {
			t.Fatalf("run %d exit = %d, want 0", run, exitCode)
		}
		if stderr.Len() != 0 {
			t.Fatalf("run %d stderr = %q", run, stderr.String())
		}
		for _, required := range []string{"AI4J\n", "command: plan.install\n", "actions: 2\n", "active-content: 2\n", "warnings: 2\n"} {
			if !strings.Contains(stdout.String(), required) {
				t.Fatalf("run %d output lacks %q:\n%s", run, required, stdout.String())
			}
		}
		if bytes.Contains(stdout.Bytes(), []byte{0x1b}) || strings.Contains(stdout.String(), contractSecretCanary) {
			t.Fatalf("run %d human output contains ANSI or secret: %q", run, stdout.String())
		}
		if run == 0 {
			want = append([]byte(nil), stdout.Bytes()...)
		} else if !bytes.Equal(stdout.Bytes(), want) {
			t.Fatalf("run %d human output differs after shuffled input\ngot:  %q\nwant: %q", run, stdout.Bytes(), want)
		}
	}
	if fake.factoryCalls != 20 || fake.handlerCalls != 20 {
		t.Fatalf("fake calls = factory %d handler %d, want 20 each", fake.factoryCalls, fake.handlerCalls)
	}
}

func TestContractHarnessUsageAndVersionNeverConstructCommandsOrReadStdin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		arguments  []string
		schemaName string
		wantExit   result.ExitCode
	}{
		{name: "usage", arguments: []string{"ai4j", "unknown", "--json"}, schemaName: "usage.json", wantExit: result.ExitUsageOrApproval},
		{name: "version", arguments: []string{"ai4j", "version", "--json"}, schemaName: "version.json", wantExit: result.ExitSuccess},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			factoryCalls := 0
			application := newTestApplication(t, validBuild(), func() (app.CommandHandler, error) {
				factoryCalls++
				panic("usage/version constructed source, target, state, or installed-content commands")
			}, human.Render, jsonout.Render)
			stdout := new(bytes.Buffer)
			stderr := new(bytes.Buffer)
			exitCode := application.Run(test.arguments, panicReader{}, stdout, stderr)
			if exitCode != test.wantExit.Int() {
				t.Fatalf("exit = %d, want %d", exitCode, test.wantExit)
			}
			if factoryCalls != 0 {
				t.Fatalf("factory calls = %d, want 0", factoryCalls)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			validateSchema(t, test.schemaName, stdout.Bytes())
		})
	}
}

func TestContractHarnessIsIndependentOfWorkingDirectoryAndIrrelevantEnvironment(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	type processResult struct {
		stdout []byte
		stderr []byte
		exit   int
	}
	run := func(directory, irrelevant string) processResult {
		t.Helper()
		command := exec.Command(executable, "-test.run=^TestContractHarnessSubprocessHelper$")
		command.Dir = directory
		command.Env = isolatedEnvironment(map[string]string{
			contractHelperEnv:     "1",
			contractIrrelevantEnv: irrelevant,
		})
		stdout := new(bytes.Buffer)
		stderr := new(bytes.Buffer)
		command.Stdout = stdout
		command.Stderr = stderr
		runErr := command.Run()
		exitCode := command.ProcessState.ExitCode()
		if runErr != nil || exitCode != result.ExitSuccess.Int() {
			t.Fatalf("helper in %s failed: error=%v exit=%d stdout=%q stderr=%q", directory, runErr, exitCode, stdout.String(), stderr.String())
		}
		return processResult{stdout: append([]byte(nil), stdout.Bytes()...), stderr: append([]byte(nil), stderr.Bytes()...), exit: exitCode}
	}

	first := run(t.TempDir(), "first-value")
	second := run(t.TempDir(), "different-second-value")
	if first.exit != second.exit || !bytes.Equal(first.stdout, second.stdout) || !bytes.Equal(first.stderr, second.stderr) {
		t.Fatalf("process results depend on cwd/env\nfirst:  exit=%d stdout=%q stderr=%q\nsecond: exit=%d stdout=%q stderr=%q", first.exit, first.stdout, first.stderr, second.exit, second.stdout, second.stderr)
	}
	if len(first.stderr) != 0 {
		t.Fatalf("helper stderr = %q, want empty", first.stderr)
	}
	assertSingleJSONDocument(t, first.stdout, cli.CommandPlanInstall.String(), result.ExitSuccess, false)
}

func TestContractHarnessSubprocessHelper(t *testing.T) {
	if os.Getenv(contractHelperEnv) != "1" {
		return
	}
	fake := &deterministicCommands{t: t, secret: contractSecretCanary}
	application := newTestApplication(t, validBuild(), fake.factory(commandResponse), human.Render, jsonout.Render)
	exitCode := application.Run([]string{"ai4j", "plan", "install", "--json"}, panicReader{}, os.Stdout, os.Stderr)
	if fake.factoryCalls != 1 || fake.handlerCalls != 1 {
		os.Exit(result.ExitUnexpectedInternal.Int())
	}
	os.Exit(exitCode)
}

type deterministicCommands struct {
	t            *testing.T
	secret       string
	factoryCalls int
	handlerCalls int
}

type commandResponder func(*testing.T, cli.Command, bool) cli.Response

func (f *deterministicCommands) factory(responder commandResponder) app.OtherCommandsFactory {
	return func() (app.CommandHandler, error) {
		f.factoryCalls++
		return func(request cli.Request, _ app.CommandIO) (cli.Response, error) {
			f.handlerCalls++
			if f.secret == "" {
				return cli.Response{}, errors.New("fake canary is required")
			}
			return responder(f.t, request.Command(), f.handlerCalls%2 == 0), nil
		}, nil
	}
}

type scenario string

const (
	scenarioCommitted             scenario = "committed"
	scenarioPinned                scenario = "pinned"
	scenarioDegraded              scenario = "degraded"
	scenarioCancelled             scenario = "cancelled"
	scenarioUsage                 scenario = "usage"
	scenarioApproval              scenario = "approval"
	scenarioEnvironment           scenario = "environment"
	scenarioSource                scenario = "source"
	scenarioValidation            scenario = "validation"
	scenarioConflict              scenario = "conflict"
	scenarioPreMutationRolledBack scenario = "pre_mutation_rolled_back"
	scenarioCompensated           scenario = "compensated"
	scenarioCommittedCleanup      scenario = "committed_cleanup"
	scenarioRolledBackCleanup     scenario = "rolled_back_cleanup"
	scenarioPreparedRecovery      scenario = "prepared_recovery"
	scenarioInternal              scenario = "internal"
)

type resultScenario struct {
	name        string
	arguments   []string
	command     cli.Command
	schemaName  string
	scenario    scenario
	wantExit    result.ExitCode
	wantStatus  result.Status
	wantChanged bool
	wantPhase   result.Phase
	noFactory   bool
}

func commandResponse(t *testing.T, command cli.Command, reverse bool) cli.Response {
	t.Helper()
	fixture := newContractFixture(t, reverse)
	warnings, _ := fixtureDiagnostics(t, reverse, false)

	switch command {
	case cli.CommandInit:
		commandResult := makeResult(t, result.StatusOK, result.PhaseComplete, result.OutcomeCommitted, result.MutationStarted, result.DurableCommittedWithDiff, result.FailureNone, result.UpdateNotChecked, warnings, nil)
		artifact := mustFixture(cli.NewBuildArtifact("toolkit.json", strings.Repeat("e", 64), 42))
		data := mustFixture(cli.NewInitData([]cli.BuildTarget{cli.BuildTargetCodex, cli.BuildTargetClaude}, "new-toolkit", []cli.BuildArtifact{artifact}))
		return mustFixture(cli.NewResponse(command, commandResult, nil, data))
	case cli.CommandValidate:
		commandResult := makeResult(t, result.StatusOK, result.PhaseNone, result.OutcomeNone, result.MutationNotStarted, result.DurableChangeNone, result.FailureNone, result.UpdateNotChecked, warnings, nil)
		data := mustFixture(cli.NewValidateData(fixture.source, true, 0, len(warnings), fixture.content))
		return mustFixture(cli.NewResponse(command, commandResult, nil, data))
	case cli.CommandBuild:
		commandResult := makeResult(t, result.StatusOK, result.PhaseComplete, result.OutcomeCommitted, result.MutationStarted, result.DurableCommittedWithDiff, result.FailureNone, result.UpdateNotChecked, warnings, nil)
		artifact := mustFixture(cli.NewBuildArtifact("plugin/skills/alpha-review/SKILL.md", strings.Repeat("e", 64), 42))
		data := mustFixture(cli.NewBuildData(fixture.source, cli.BuildTargetCodex, cli.BuildHostDarwinARM64, "dist", true, []cli.BuildArtifact{artifact}, fixture.content))
		return mustFixture(cli.NewResponse(command, commandResult, nil, data))
	case cli.CommandPlanInstall, cli.CommandPlanUpdate, cli.CommandPlanUninstall:
		operation := operationForCommand(t, command)
		final := fixture.finalPresent
		if operation == cli.OperationUninstall {
			final = fixture.finalAbsent
		}
		commandResult := makeResult(t, result.StatusOK, result.PhaseNone, result.OutcomeNone, result.MutationNotStarted, result.DurableChangeNone, result.FailureNone, result.UpdateNotChecked, warnings, nil)
		data := mustFixture(cli.NewPlanData(operation, fixture.source, fixture.installationID, fixture.actions, fixture.content, nil, final, result.UpdateNotChecked))
		return mustFixture(cli.NewResponse(command, commandResult, nil, data))
	case cli.CommandInstall, cli.CommandUpdate, cli.CommandUninstall:
		operation := operationForCommand(t, command)
		final := fixture.finalPresent
		installationID := &fixture.installationID
		if operation == cli.OperationUninstall {
			final = fixture.finalAbsent
			installationID = nil
		}
		commandResult := makeResult(t, result.StatusOK, result.PhaseComplete, result.OutcomeCommitted, result.MutationStarted, result.DurableCommittedWithDiff, result.FailureNone, result.UpdateNotChecked, warnings, nil)
		data := mustFixture(cli.NewMutationData(operation, commandResult, installationID, fixture.actions, final, result.UpdateNotChecked))
		return mustFixture(cli.NewResponse(command, commandResult, &fixture.operationID, data))
	case cli.CommandStatus:
		commandResult := makeResult(t, result.StatusOK, result.PhaseNone, result.OutcomeNone, result.MutationNotStarted, result.DurableChangeNone, result.FailureNone, result.UpdateUpToDate, warnings, nil)
		data := mustFixture(cli.NewStatusData(&fixture.installation, fixture.nativePresent, fixture.drift, fixture.recoveryNone, result.UpdateUpToDate))
		return mustFixture(cli.NewResponse(command, commandResult, nil, data))
	default:
		t.Fatalf("fake handler received unsupported command %q", command)
		return cli.Response{}
	}
}

func scenarioResponse(t *testing.T, selected scenario, command cli.Command, reverse bool) cli.Response {
	t.Helper()
	fixture := newContractFixture(t, reverse)
	warnings, problems := fixtureDiagnostics(t, reverse, true)

	switch selected {
	case scenarioCommitted:
		commandResult := makeResult(t, result.StatusOK, result.PhaseComplete, result.OutcomeCommitted, result.MutationStarted, result.DurableCommittedWithDiff, result.FailureNone, result.UpdateNotChecked, warnings, nil)
		data := mustFixture(cli.NewMutationData(cli.OperationInstall, commandResult, &fixture.installationID, fixture.actions, fixture.finalPresent, result.UpdateNotChecked))
		return mustFixture(cli.NewResponse(command, commandResult, &fixture.operationID, data))
	case scenarioPinned:
		commandResult := makeResult(t, result.StatusNoChange, result.PhaseNone, result.OutcomeNone, result.MutationNotStarted, result.DurableChangeNone, result.FailureNone, result.UpdatePinned, warnings, nil)
		data := mustFixture(cli.NewPlanData(cli.OperationUpdate, fixture.source, fixture.installationID, nil, fixture.content, nil, fixture.finalPresent, result.UpdatePinned))
		return mustFixture(cli.NewResponse(command, commandResult, nil, data))
	case scenarioDegraded:
		commandResult := makeResult(t, result.StatusDegraded, result.PhaseNone, result.OutcomeNone, result.MutationNotStarted, result.DurableChangeNone, result.FailureNone, result.UpdateNotChecked, warnings, nil)
		data := mustFixture(cli.NewStatusData(&fixture.installation, fixture.nativePresent, fixture.drift, fixture.recoveryNone, result.UpdateNotChecked))
		return mustFixture(cli.NewResponse(command, commandResult, nil, data))
	case scenarioCancelled:
		commandResult := makeResult(t, result.StatusCancelled, result.PhaseNone, result.OutcomeNone, result.MutationNotStarted, result.DurableChangeNone, result.FailureCancellation, result.UpdateNotChecked, warnings, problems)
		return mustFixture(cli.NewResponse(command, commandResult, nil, cli.UnavailableData{}))
	case scenarioApproval:
		commandResult := makeResult(t, result.StatusError, result.PhaseNone, result.OutcomeNone, result.MutationNotStarted, result.DurableChangeNone, result.FailureApproval, result.UpdateNotChecked, warnings, problems)
		return mustFixture(cli.NewResponse(command, commandResult, nil, cli.UnavailableData{}))
	case scenarioEnvironment:
		commandResult := makeResult(t, result.StatusError, result.PhaseNone, result.OutcomeNone, result.MutationNotStarted, result.DurableChangeNone, result.FailureEnvironment, result.UpdateNotChecked, warnings, problems)
		return mustFixture(cli.NewResponse(command, commandResult, nil, cli.UnavailableData{}))
	case scenarioSource:
		commandResult := makeResult(t, result.StatusError, result.PhaseNone, result.OutcomeNone, result.MutationNotStarted, result.DurableChangeNone, result.FailureSource, result.UpdateUnknown, warnings, problems)
		data := mustFixture(cli.NewStatusData(&fixture.installation, fixture.nativePresent, fixture.drift, fixture.recoveryNone, result.UpdateUnknown))
		return mustFixture(cli.NewResponse(command, commandResult, nil, data))
	case scenarioValidation:
		commandResult := makeResult(t, result.StatusError, result.PhaseNone, result.OutcomeNone, result.MutationNotStarted, result.DurableChangeNone, result.FailureValidation, result.UpdateNotChecked, warnings, problems)
		data := mustFixture(cli.NewValidateData(fixture.source, false, len(problems), len(warnings), fixture.content))
		return mustFixture(cli.NewResponse(command, commandResult, nil, data))
	case scenarioConflict:
		commandResult := makeResult(t, result.StatusError, result.PhaseNone, result.OutcomeNone, result.MutationNotStarted, result.DurableChangeNone, result.FailureConflict, result.UpdateNotChecked, warnings, problems)
		data := mustFixture(cli.NewPlanData(cli.OperationInstall, fixture.source, fixture.installationID, fixture.actions, fixture.content, fixture.conflicts, fixture.finalPresent, result.UpdateNotChecked))
		return mustFixture(cli.NewResponse(command, commandResult, nil, data))
	case scenarioPreMutationRolledBack:
		commandResult := makeResult(t, result.StatusError, result.PhaseCompleteRolledBack, result.OutcomeRolledBack, result.MutationNotStarted, result.DurableChangeNone, result.FailureValidation, result.UpdateNotChecked, warnings, problems)
		data := mustFixture(cli.NewMutationData(cli.OperationInstall, commandResult, &fixture.installationID, nil, fixture.finalAbsent, result.UpdateNotChecked))
		return mustFixture(cli.NewResponse(command, commandResult, &fixture.operationID, data))
	case scenarioCompensated:
		commandResult := makeResult(t, result.StatusError, result.PhaseCompleteRolledBack, result.OutcomeRolledBack, result.MutationStarted, result.DurableChangeNone, result.FailureValidation, result.UpdateNotChecked, warnings, problems)
		data := mustFixture(cli.NewMutationData(cli.OperationInstall, commandResult, &fixture.installationID, fixture.actions, fixture.finalAbsent, result.UpdateNotChecked))
		return mustFixture(cli.NewResponse(command, commandResult, &fixture.operationID, data))
	case scenarioCommittedCleanup:
		commandResult := makeResult(t, result.StatusError, result.PhaseCommittedCleanupPending, result.OutcomeCommitted, result.MutationStarted, result.DurableCommittedWithDiff, result.FailureRecovery, result.UpdateNotChecked, warnings, problems)
		data := mustFixture(cli.NewMutationData(cli.OperationInstall, commandResult, &fixture.installationID, fixture.actions, fixture.finalPresent, result.UpdateNotChecked))
		return mustFixture(cli.NewResponse(command, commandResult, &fixture.operationID, data))
	case scenarioRolledBackCleanup:
		commandResult := makeResult(t, result.StatusError, result.PhaseRolledBackCleanupPending, result.OutcomeRolledBack, result.MutationStarted, result.DurableChangeNone, result.FailureRecovery, result.UpdateNotChecked, warnings, problems)
		data := mustFixture(cli.NewMutationData(cli.OperationInstall, commandResult, &fixture.installationID, fixture.actions, fixture.finalAbsent, result.UpdateNotChecked))
		return mustFixture(cli.NewResponse(command, commandResult, &fixture.operationID, data))
	case scenarioPreparedRecovery:
		commandResult := makeResult(t, result.StatusError, result.PhasePrepared, result.OutcomePending, result.MutationNotStarted, result.DurableChangeNone, result.FailureRecovery, result.UpdateNotChecked, warnings, problems)
		data := mustFixture(cli.NewMutationData(cli.OperationInstall, commandResult, &fixture.installationID, nil, fixture.finalAbsent, result.UpdateNotChecked))
		return mustFixture(cli.NewResponse(command, commandResult, &fixture.operationID, data))
	default:
		t.Fatalf("unsupported result scenario %q", selected)
		return cli.Response{}
	}
}

type contractFixture struct {
	source         cli.Source
	installationID domain.InstallationID
	operationID    domain.OperationID
	content        []cli.ContentItem
	actions        []cli.Action
	conflicts      []cli.Conflict
	finalPresent   cli.FinalState
	finalAbsent    cli.FinalState
	installation   cli.Installation
	nativePresent  cli.NativeState
	drift          []cli.Drift
	recoveryNone   cli.RecoveryState
}

func newContractFixture(t *testing.T, reverse bool) contractFixture {
	t.Helper()
	input := mustFixture(githubsource.NewSelectionInput("", false, "", false))
	effective := mustFixture(githubsource.Resolve(input))
	request := mustFixture(gitsource.NewResolutionRequest(effective))
	commitOID := strings.Repeat("a", 40)
	advertisement := mustFixture(gitsource.ParseRemoteAdvertisement(request, []byte(
		"ref: refs/heads/main\tHEAD\n"+commitOID+"\tHEAD\n"+commitOID+"\trefs/heads/main\n",
	)))
	resolution := mustFixture(gitsource.ResolveReference(request, advertisement))
	selected := mustFixture(gitsource.NewSelectedObjectProof(resolution, []byte("commit\n")))
	commit := mustFixture(gitsource.NewDirectProvenCommit(selected))
	proof := mustFixture(gitsource.NewCommitTreeProof(commit, []byte(strings.Repeat("b", 40)+"\n")))
	provenance := mustFixture(gitsource.NewSourceProvenance(proof))
	digest := mustFixture(domain.NewRenderedDigest(strings.Repeat("c", 64)))
	build := mustFixture(domain.NewBuildCommit(strings.Repeat("d", 40)))
	rendered := mustFixture(gitsource.NewRenderedProvenance(provenance, digest, build))
	source := mustFixture(cli.NewSource(rendered))
	installationID := mustFixture(domain.NewInstallationID("installation_001"))
	operationID := mustFixture(domain.NewOperationID("operation_001"))

	skill := mustFixture(cli.NewContentItem(cli.ComponentSkill, "alpha-review", "plugins/ai4j-default/skills/alpha-review", strings.Repeat("a", 64), cli.ContentAdded, nil))
	execution := mustFixture(cli.NewExecution(cli.ExecutionHostResolved, cli.DependencyRequired, "go", []string{"run", "./bridge"}, "", []cli.Placeholder{cli.PlaceholderProjectDir, cli.PlaceholderPluginRoot}, []string{"AI4J_TOKEN", "PATH"}))
	mcp := mustFixture(cli.NewContentItem(cli.ComponentMCP, "zulu-bridge", "plugins/ai4j-default/.mcp.json", strings.Repeat("b", 64), cli.ContentChanged, &execution))
	content := []cli.ContentItem{skill, mcp}

	absent := mustFixture(cli.NewCondition(cli.ConditionAbsent, ""))
	present := mustFixture(cli.NewCondition(cli.ConditionPresent, ""))
	writeCatalog := mustFixture(cli.NewAction(2, cli.ActionOwnerAI4J, cli.ActionWriteCatalog, "catalog", absent, present, cli.RecoveryStructuralInverse))
	installPlugin := mustFixture(cli.NewAction(1, cli.ActionOwnerClaude, cli.ActionInstallPlugin, "plugin", absent, present, cli.RecoveryNativeArtifact))
	actions := []cli.Action{writeCatalog, installPlugin}

	conflictA := mustFixture(cli.NewConflict("a_conflict", "catalog", "catalog ownership conflicts"))
	conflictZ := mustFixture(cli.NewConflict("z_conflict", "plugin", "plugin ownership conflicts"))
	conflicts := []cli.Conflict{conflictZ, conflictA}
	driftA := mustFixture(cli.NewDrift("catalog", cli.DriftUnchanged))
	driftZ := mustFixture(cli.NewDrift("rules", cli.DriftModified))
	drift := []cli.Drift{driftZ, driftA}

	if reverse {
		reverseValues(content)
		reverseValues(actions)
		reverseValues(conflicts)
		reverseValues(drift)
	}

	finalPresent := mustFixture(cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent))
	finalAbsent := mustFixture(cli.NewFinalState(cli.StateAbsent, cli.StateAbsent, cli.StateAbsent))
	recordedSource := mustFixture(cli.NewRecordedSource(source.Selection(), source.Repository(), source.RequestedRef(), source.HasRequestedRef(), source.ResolvedRefKind(), source.Commit().OID()))
	installation := mustFixture(cli.NewInstallation(installationID, "ai4j", "ai4j-default", recordedSource, "1.0.0", "0.0.0-dev", "2.0.0"))
	nativePresent := mustFixture(cli.NewNativeState(cli.NativeRegistered, cli.NativeInstalled, cli.NativeEnabled, cli.NativeInactive, cli.NativeReloadRequired, cli.NativeNextSessionRequired, cli.NativePolicyAllowed, "2.0.0", cli.NativeVersionMatches))
	recoveryNone := mustFixture(cli.NewRecoveryState(cli.RecoveryStateNone, ""))

	return contractFixture{
		source: source, installationID: installationID, operationID: operationID,
		content: content, actions: actions, conflicts: conflicts,
		finalPresent: finalPresent, finalAbsent: finalAbsent,
		installation: installation, nativePresent: nativePresent, drift: drift, recoveryNone: recoveryNone,
	}
}

func fixtureDiagnostics(t *testing.T, reverse, withProblems bool) ([]result.Warning, []result.Problem) {
	t.Helper()
	contextA := mustFixture(result.NewContext("resource", "catalog"))
	contextZ := mustFixture(result.NewContext("resource", "plugin"))
	warningA := mustFixture(result.NewWarning("a_warning", "catalog requires review", []result.Context{contextA}))
	warningZ := mustFixture(result.NewWarning("z_warning", "plugin requires review", []result.Context{contextZ}))
	warnings := []result.Warning{warningZ, warningA}
	var problems []result.Problem
	if withProblems {
		problemA := mustFixture(result.NewProblem("a_failure", "catalog operation failed", []result.Context{contextA}))
		problemZ := mustFixture(result.NewProblem("z_failure", "plugin operation failed", []result.Context{contextZ}))
		problems = []result.Problem{problemZ, problemA}
	}
	if reverse {
		reverseValues(warnings)
		reverseValues(problems)
	}
	return warnings, problems
}

func makeResult(t *testing.T, status result.Status, phase result.Phase, outcome result.Outcome, mutation result.MutationState, durable result.DurableChange, failure result.Failure, disposition result.UpdateDisposition, warnings []result.Warning, problems []result.Problem) result.Result {
	t.Helper()
	return mustFixture(result.New(result.Facts{
		Status: status, Phase: phase, Outcome: outcome, Mutation: mutation,
		DurableChange: durable, Failure: failure, UpdateDisposition: disposition,
		Warnings: warnings, Errors: problems,
	}))
}

func operationForCommand(t *testing.T, command cli.Command) cli.Operation {
	t.Helper()
	switch command {
	case cli.CommandPlanInstall, cli.CommandInstall:
		return cli.OperationInstall
	case cli.CommandPlanUpdate, cli.CommandUpdate:
		return cli.OperationUpdate
	case cli.CommandPlanUninstall, cli.CommandUninstall:
		return cli.OperationUninstall
	default:
		t.Fatalf("command %q has no operation", command)
		return ""
	}
}

type contractDocument struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Command       *string              `json:"command"`
	Status        string               `json:"status"`
	Changed       bool                 `json:"changed"`
	ExitCode      int                  `json:"exitCode"`
	Data          map[string]any       `json:"data"`
	Warnings      []contractDiagnostic `json:"warnings"`
	Errors        []contractDiagnostic `json:"errors"`
}

type contractDiagnostic struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Context []contractContext `json:"context"`
}

type contractContext struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

func assertSingleJSONDocument(t *testing.T, encoded []byte, command string, exitCode result.ExitCode, changed bool) contractDocument {
	t.Helper()
	if bytes.Contains(encoded, []byte{0x1b}) || bytes.Contains(encoded, []byte("AI4J\n")) {
		t.Fatalf("JSON stdout contains ANSI or human prose: %q", encoded)
	}
	if !bytes.HasSuffix(encoded, []byte("\n")) || bytes.Count(encoded, []byte("\n")) != 1 {
		t.Fatalf("JSON stdout must contain exactly one trailing LF: %q", encoded)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var document contractDocument
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("Decode(first) error = %v; output=%q", err, encoded)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("Decode(second) error = %v, want EOF", err)
	}
	if command == "" {
		if document.Command != nil {
			t.Fatalf("command = %v, want null", document.Command)
		}
	} else if document.Command == nil || *document.Command != command {
		t.Fatalf("command = %v, want %q", document.Command, command)
	}
	if document.ExitCode != exitCode.Int() {
		t.Fatalf("envelope exitCode = %d, want %d", document.ExitCode, exitCode)
	}
	if document.Changed != changed {
		t.Fatalf("envelope changed = %t, want %t", document.Changed, changed)
	}
	return document
}

func assertDiagnosticsBounded(t *testing.T, values []contractDiagnostic) {
	t.Helper()
	if len(values) > 64 {
		t.Fatalf("diagnostic count = %d, want <= 64", len(values))
	}
	for _, diagnostic := range values {
		if diagnostic.Code == "" || len(diagnostic.Code) > 64 {
			t.Fatalf("diagnostic code is not bounded: %q", diagnostic.Code)
		}
		if diagnostic.Message == "" || utf8.RuneCountInString(diagnostic.Message) > 512 || containsControl(diagnostic.Message) {
			t.Fatalf("diagnostic message is not bounded safe text: %q", diagnostic.Message)
		}
		if len(diagnostic.Context) > 16 {
			t.Fatalf("diagnostic context count = %d, want <= 16", len(diagnostic.Context))
		}
		for _, context := range diagnostic.Context {
			if context.Field == "" || len(context.Field) > 64 || context.Value == "" || utf8.RuneCountInString(context.Value) > 256 || containsControl(context.Value) {
				t.Fatalf("diagnostic context is not bounded safe text: %#v", context)
			}
		}
	}
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func assertOperationPhase(t *testing.T, data map[string]any, phase result.Phase) {
	t.Helper()
	operationResult, ok := data["operationResult"].(map[string]any)
	if !ok {
		t.Fatalf("data.operationResult = %#v, want object", data["operationResult"])
	}
	if operationResult["phase"] != phase.String() {
		t.Fatalf("operationResult.phase = %#v, want %q", operationResult["phase"], phase)
	}
}

func assertDataString(t *testing.T, data map[string]any, field, want string) {
	t.Helper()
	if data[field] != want {
		t.Fatalf("data.%s = %#v, want %q", field, data[field], want)
	}
}

func assertInjectedVersionFacts(t *testing.T, data map[string]any) {
	t.Helper()
	for field, want := range map[string]string{
		"product":    "AI4J",
		"executable": "ai4j",
		"cliVersion": "0.0.0-dev",
		"goVersion":  "go1.26.6",
		"buildTime":  "2026-08-18T10:00:00Z",
	} {
		assertDataString(t, data, field, want)
	}
	for field, test := range map[string]struct {
		values map[string]any
		want   map[string]any
	}{
		"cliSource": {
			values: nestedObject(t, data, "cliSource"),
			want: map[string]any{
				"repository":   "github.com/alx4j/ai4j",
				"objectFormat": "sha1",
				"oid":          strings.Repeat("b", 40),
			},
		},
		"target": {
			values: nestedObject(t, data, "target"),
			want:   map[string]any{"os": "darwin", "arch": "arm64"},
		},
		"defaultSource": {
			values: nestedObject(t, data, "defaultSource"),
			want: map[string]any{
				"repository": "github.com/alx4j/ai4j",
				"reference":  nil,
				"refPolicy":  "repository_default_branch",
			},
		},
	} {
		for nestedField, want := range test.want {
			if got := test.values[nestedField]; got != want {
				t.Fatalf("data.%s.%s = %#v, want %#v", field, nestedField, got, want)
			}
		}
	}
}

func nestedObject(t *testing.T, data map[string]any, field string) map[string]any {
	t.Helper()
	value, ok := data[field].(map[string]any)
	if !ok {
		t.Fatalf("data.%s = %#v, want object", field, data[field])
	}
	return value
}

func isolatedEnvironment(overrides map[string]string) []string {
	blocked := make(map[string]struct{}, len(overrides))
	for key := range overrides {
		blocked[strings.ToUpper(key)] = struct{}{}
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, replaced := blocked[strings.ToUpper(key)]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func mustFixture[T any](value T, err error) T {
	if err != nil {
		panic("contract fixture construction failed: " + err.Error())
	}
	return value
}

func reverseValues[T any](values []T) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
