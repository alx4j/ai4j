package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/jsonwire"
	"github.com/alx4j/ai4j/internal/result"
	validation "github.com/alx4j/ai4j/internal/validate"
)

func TestDoctorIsStaticByDefaultAndMCPStartupIsExplicit(t *testing.T) {
	t.Setenv("AI4J_TOKEN", "SECRET_CANARY")
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--all", "--yes")
	installed, err := harness.service.Install(context.Background(), install, CommandIO{})
	if err != nil || installed.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("install = %#v, %v", installed.Result(), err)
	}
	record, present, err := harness.store.Load()
	if err != nil || !present {
		t.Fatalf("load installation = %t, %v", present, err)
	}
	runner := &doctorRunnerStub{paths: map[string]string{"git": "/usr/bin/git", "claude": "/usr/bin/claude"}}
	service := newDoctorService(harness.store, statusService{validation: harness.validator, state: harness.store, home: harness.home}, harness.validator, runner)

	staticRequest := parseRequest[cli.DoctorRequest](t, "doctor", record.InstallationID, "--json")
	staticResponse, err := service.Doctor(context.Background(), staticRequest, CommandIO{})
	if err != nil || staticResponse.Result().ExitCode() != result.ExitSuccess || runner.calls != 0 {
		t.Fatalf("static doctor = %#v, calls=%d, err=%v", staticResponse.Result(), runner.calls, err)
	}
	staticData := staticResponse.Data().(cli.DoctorData)
	if _, present := staticData.StartupCheck(); present || !hasCheck(staticData.Checks(), "mcp_registration", cli.DoctorCheckOK) {
		t.Fatalf("static doctor data = %#v", staticData)
	}

	previewRequest := parseRequest[cli.DoctorRequest](t, "doctor", record.InstallationID, "--test-mcp", "claude-tools", "--json")
	prompt := new(bytes.Buffer)
	preview, err := service.Doctor(context.Background(), previewRequest, CommandIO{Input: strings.NewReader("yes\n"), Output: prompt, Interactive: true})
	if err != nil || preview.Result().Failure() != result.FailureApproval || preview.Result().Mutation() != result.MutationNotStarted || runner.calls != 0 || prompt.Len() != 0 {
		t.Fatalf("startup preview = %#v, calls=%d, err=%v", preview.Result(), runner.calls, err)
	}
	encoded, err := jsonwire.Marshal(preview)
	if err != nil || strings.Contains(string(encoded), "SECRET_CANARY") || !strings.Contains(string(encoded), `"environment":["AI4J_TOKEN"]`) {
		t.Fatalf("startup preview JSON = %s, err=%v", encoded, err)
	}
	interactiveRequest := parseRequest[cli.DoctorRequest](t, "doctor", record.InstallationID, "--test-mcp", "claude-tools")
	if _, err := service.Doctor(context.Background(), interactiveRequest, CommandIO{Input: strings.NewReader("yes\n"), Output: doctorFailingWriter{}, Interactive: true}); err == nil {
		t.Fatal("doctor discarded preview output failure")
	}
	if _, err := service.Doctor(context.Background(), interactiveRequest, CommandIO{Input: doctorFailingReader{}, Output: new(bytes.Buffer), Interactive: true}); err == nil {
		t.Fatal("doctor discarded approval input failure")
	}
	if runner.calls != 0 {
		t.Fatalf("failed approval I/O started MCP process: calls=%d", runner.calls)
	}

	runner.result = validation.ProcessResult{Started: true, TimedOut: true}
	runner.err = context.DeadlineExceeded
	approvedRequest := parseRequest[cli.DoctorRequest](t, "doctor", record.InstallationID, "--test-mcp", "claude-tools", "--yes", "--json")
	checked, err := service.Doctor(context.Background(), approvedRequest, CommandIO{})
	if err != nil || checked.Result().ExitCode() != result.ExitSuccess || checked.Result().Mutation() != result.MutationNotStarted || runner.calls != 1 {
		t.Fatalf("startup check = %#v, calls=%d, err=%v", checked.Result(), runner.calls, err)
	}
	startup, present := checked.Data().(cli.DoctorData).StartupCheck()
	if !present || startup.Result() != "timed_out" || startup.Executable() != "/usr/bin/claude" || !slices.Equal(startup.Arguments(), []string{"mcp", "serve"}) || !slices.Equal(startup.Environment(), []string{"AI4J_TOKEN"}) {
		t.Fatalf("startup result = %#v", startup)
	}
}

func TestDoctorRejectsUnknownMCPBeforeExecution(t *testing.T) {
	harness := newLifecycleHarness(t)
	install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--all", "--yes")
	_, _ = harness.service.Install(context.Background(), install, CommandIO{})
	record, _, _ := harness.store.Load()
	runner := &doctorRunnerStub{paths: map[string]string{"git": "/usr/bin/git", "claude": "/usr/bin/claude"}}
	service := newDoctorService(harness.store, statusService{validation: harness.validator, state: harness.store, home: harness.home}, harness.validator, runner)
	request := parseRequest[cli.DoctorRequest](t, "doctor", record.InstallationID, "--test-mcp", "missing", "--yes", "--json")
	response, err := service.Doctor(context.Background(), request, CommandIO{})
	if err != nil || response.Result().ExitCode() != result.ExitValidation || runner.calls != 0 || response.Result().Errors()[0].Code() != "mcp_not_found" {
		t.Fatalf("unknown MCP = %#v, calls=%d, err=%v", response.Result(), runner.calls, err)
	}
}

type doctorRunnerStub struct {
	paths       map[string]string
	result      validation.ProcessResult
	err         error
	calls       int
	environment []string
}

type doctorFailingReader struct{}

func (doctorFailingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

type doctorFailingWriter struct{}

func (doctorFailingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func (r *doctorRunnerStub) LookPath(name string) (string, error) {
	if value, ok := r.paths[name]; ok {
		return value, nil
	}
	return "", errors.New("not found")
}

func (r *doctorRunnerStub) RunIsolated(_ context.Context, _ string, _ string, _ []string, environment []string) (validation.ProcessResult, error) {
	r.calls++
	r.environment = slices.Clone(environment)
	return r.result, r.err
}

func hasCheck(checks []cli.DoctorCheck, id string, status cli.DoctorCheckStatus) bool {
	for _, check := range checks {
		if check.ID() == id && check.Status() == status {
			return true
		}
	}
	return false
}
