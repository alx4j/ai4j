package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
)

func TestCommandLogToolNameUsesRuntimeExecutable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		argv []string
		want string
	}{
		{name: "canonical Windows", argv: []string{`C:\Program Files\AI4J\ai4j.exe`}, want: "ai4j"},
		{name: "renamed Windows", argv: []string{`C:\Tools\My Toolkit.EXE`}, want: "my-toolkit"},
		{name: "renamed Unix", argv: []string{"/usr/local/bin/work-ai"}, want: "work-ai"},
		{name: "missing", want: "ai4j"},
		{name: "unsafe", argv: []string{"/...exe"}, want: "ai4j"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := commandLogToolName(test.argv); got != test.want {
				t.Fatalf("commandLogToolName(%q) = %q, want %q", test.argv, got, test.want)
			}
		})
	}
}

func TestCommandLogUsesToolInstallationAndUTCDate(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	request := parseRequest[cli.StatusRequest](t, "status", "7c2fd86a")
	fixed := time.Date(2026, 8, 29, 12, 34, 56, 123456789, time.FixedZone("test", 2*60*60))
	session := startCommandLogWith(dataRoot, "renamed-tool", request, func() time.Time { return fixed }, bytes.NewReader(bytes.Repeat([]byte{0xab}, commandLogRunIDBytes)))
	if session == nil {
		t.Fatal("startCommandLogWith returned nil")
	}

	var progress bytes.Buffer
	reportProgress(CommandIO{Progress: &progress, logSession: session}, "verify", "SECRET_CANARY")
	session.complete(cli.Response{}, errors.New("SECRET_CANARY"))

	wantPath := filepath.Join(dataRoot, "logs", "installations", "7c2fd86a", "renamed-tool-7c2fd86a-2026-08-29.log")
	contents, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, expected := range []string{
		`msg="command started"`, `command=status`, `run_id=abababababababab`, `installation_id=7c2fd86a`,
		`msg="command progress"`, `stage=inspect`, `stage=verify`, `msg="command completed"`,
		`status=error`, `failure=internal`, `result_exit_code=9`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("log does not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "SECRET_CANARY") {
		t.Fatalf("log disclosed progress or error text:\n%s", text)
	}
	if progress.String() != "ai4j: SECRET_CANARY\n" {
		t.Fatalf("progress output = %q", progress.String())
	}
	if runtime.GOOS != "windows" {
		assertPrivatePathPermissions(t, wantPath)
	}
}

func TestCommandLogAppendsSameDayAndRollsOverAtUTCMidnight(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	request := parseRequest[cli.StatusRequest](t, "status", "7c2fd86a")
	current := time.Date(2026, 8, 29, 23, 59, 59, 0, time.UTC)
	clock := func() time.Time { return current }

	first := startCommandLogWith(dataRoot, "ai4j", request, clock, bytes.NewReader(bytes.Repeat([]byte{1}, commandLogRunIDBytes)))
	second := startCommandLogWith(dataRoot, "ai4j", request, clock, bytes.NewReader(bytes.Repeat([]byte{2}, commandLogRunIDBytes)))
	if first == nil || second == nil {
		t.Fatal("startCommandLogWith returned nil")
	}
	first.complete(cli.Response{}, errors.New("failed"))
	second.complete(cli.Response{}, errors.New("failed"))

	firstPath := filepath.Join(dataRoot, "logs", "installations", "7c2fd86a", "ai4j-7c2fd86a-2026-08-29.log")
	firstContents, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(firstContents, []byte("\n")); got != 6 {
		t.Fatalf("same-day record count = %d, want 6:\n%s", got, firstContents)
	}
	for _, runID := range []string{"0101010101010101", "0202020202020202"} {
		if got := bytes.Count(firstContents, []byte("run_id="+runID)); got != 3 {
			t.Fatalf("records for run %s = %d, want 3", runID, got)
		}
	}

	crossing := startCommandLogWith(dataRoot, "ai4j", request, clock, bytes.NewReader(bytes.Repeat([]byte{3}, commandLogRunIDBytes)))
	if crossing == nil {
		t.Fatal("startCommandLogWith returned nil before midnight")
	}
	current = time.Date(2026, 8, 30, 0, 0, 1, 0, time.UTC)
	crossing.progress("after-midnight")
	crossing.complete(cli.Response{}, errors.New("failed"))
	secondPath := filepath.Join(dataRoot, "logs", "installations", "7c2fd86a", "ai4j-7c2fd86a-2026-08-30.log")
	secondContents, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(secondContents, []byte("stage=after-midnight")) {
		t.Fatalf("post-midnight progress did not roll over:\n%s", secondContents)
	}
}

func TestCommandLogPersistsAndReplaysAutomaticInstallWhenIDIsKnown(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--dry-run")
	fixed := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	session := startCommandLogWith(dataRoot, "ai4j", request, func() time.Time { return fixed }, bytes.NewReader(bytes.Repeat([]byte{4}, commandLogRunIDBytes)))
	if session == nil {
		t.Fatal("startCommandLogWith returned nil")
	}
	commandPath := filepath.Join(dataRoot, "logs", "commands", "install", "ai4j-install-2026-08-29.log")
	commandContents, err := os.ReadFile(commandPath)
	if err != nil || bytes.Count(commandContents, []byte("\n")) != 2 {
		t.Fatalf("pre-ID records were not persisted: %q, %v", commandContents, err)
	}

	installationID := mustInstallationID(t, "7c2fd86a")
	session.bindInstallation(installationID)
	session.complete(cli.Response{}, errors.New("plan failed"))

	wantPath := filepath.Join(dataRoot, "logs", "installations", installationID.String(), "ai4j-7c2fd86a-2026-08-29.log")
	contents, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(contents, []byte("installation_id=7c2fd86a")) != 3 || !bytes.Contains(contents, []byte(`msg="command started"`)) {
		t.Fatalf("buffered records did not follow the resolved installation:\n%s", contents)
	}
	commandContents, err = os.ReadFile(commandPath)
	if err != nil || bytes.Count(commandContents, []byte("\n")) != 2 {
		t.Fatalf("pre-ID crash trace changed after binding: %q, %v", commandContents, err)
	}
}

func TestCommandLogUsesCommandBucketWhenAutomaticInstallFailsEarly(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--dry-run")
	fixed := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	session := startCommandLogWith(dataRoot, "ai4j", request, func() time.Time { return fixed }, bytes.NewReader(bytes.Repeat([]byte{5}, commandLogRunIDBytes)))
	if session == nil {
		t.Fatal("startCommandLogWith returned nil")
	}
	session.complete(cli.Response{}, errors.New("selection failed"))

	wantPath := filepath.Join(dataRoot, "logs", "commands", "install", "ai4j-install-2026-08-29.log")
	contents, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(contents, []byte("\n")); got != 3 {
		t.Fatalf("fallback record count = %d, want 3:\n%s", got, contents)
	}
}

func TestCommandLogRetentionKeepsTenDaysAndUnknownEntries(t *testing.T) {
	directory := t.TempDir()
	const key = "7c2fd86a"
	var paths []string
	for index := range commandLogRetention + 1 {
		day := time.Date(2026, 8, 1+index, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
		logPath := filepath.Join(directory, fmt.Sprintf("ai4j-%s-%s.log", key, day))
		paths = append(paths, logPath)
		if err := os.WriteFile(logPath, []byte("log\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unknown := filepath.Join(directory, "keep-me.log")
	otherID := filepath.Join(directory, "ai4j-deadbeef-2026-08-01.log")
	otherTool := filepath.Join(directory, "old-name-7c2fd86a-2026-07-30.log")
	matchingDirectory := filepath.Join(directory, "ai4j-7c2fd86a-2026-07-31.log")
	for _, candidate := range []string{unknown, otherID, otherTool} {
		if err := os.WriteFile(candidate, []byte("user-owned\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(matchingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}

	pruneCommandLogs(directory, "ai4j", key, paths[len(paths)-1])

	if _, err := os.Stat(paths[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest daily log was not pruned: %v", err)
	}
	for _, candidate := range append(paths[1:], unknown, otherID, otherTool, matchingDirectory) {
		if _, err := os.Stat(candidate); err != nil {
			t.Fatalf("retained entry %q: %v", candidate, err)
		}
	}
}

func TestCommandHandlerCreatesDailyLog(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	state, err := installstate.NewStoreAt(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	handler := newCommandHandler(commandRouter{status: statusService{state: state}}, dataRoot, "ai4j")
	request := parseRequest[cli.ListRequest](t, "list")

	response, err := handler(context.Background(), request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Valid() || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("list response = %#v", response)
	}
	logs, err := filepath.Glob(filepath.Join(dataRoot, "logs", "commands", "list", "ai4j-list-*.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("daily logs = %v, error = %v", logs, err)
	}
	contents, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`msg="command started"`, `stage=load`, `msg="command completed"`, `status=ok`, `result_exit_code=0`} {
		if !bytes.Contains(contents, []byte(expected)) {
			t.Errorf("completed log does not contain %q:\n%s", expected, contents)
		}
	}
}

func TestCommandLogFailureDoesNotChangeCommandResponse(t *testing.T) {
	temporary := t.TempDir()
	state, err := installstate.NewStoreAt(filepath.Join(temporary, "state-data"))
	if err != nil {
		t.Fatal(err)
	}
	blockedDataRoot := filepath.Join(temporary, "not-a-directory")
	if err := os.WriteFile(blockedDataRoot, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newCommandHandler(commandRouter{status: statusService{state: state}}, blockedDataRoot, "ai4j")
	request := parseRequest[cli.ListRequest](t, "list")

	response, err := handler(context.Background(), request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Valid() || response.Result().Status() != result.StatusOK || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("response changed after logging failure: %#v", response)
	}
	if contents, err := os.ReadFile(blockedDataRoot); err != nil || string(contents) != "occupied" {
		t.Fatalf("blocked path changed: %q, %v", contents, err)
	}
}

func TestCommandLogConcurrentProcessAppendsAreComplete(t *testing.T) {
	const childVariable = "AI4J_TEST_COMMAND_LOG_CHILD"
	if value := os.Getenv(childVariable); value != "" {
		index, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
		dataRoot := os.Getenv("AI4J_TEST_COMMAND_LOG_ROOT")
		request := parseRequest[cli.StatusRequest](t, "status", "7c2fd86a")
		fixed := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
		session := startCommandLogWith(dataRoot, "ai4j", request, func() time.Time { return fixed }, bytes.NewReader(bytes.Repeat([]byte{byte(index)}, commandLogRunIDBytes)))
		if session == nil {
			t.Fatal("child logger did not start")
		}
		session.complete(cli.Response{}, errors.New("expected child failure"))
		return
	}

	dataRoot := filepath.Join(t.TempDir(), "data")
	const children = 8
	type childProcess struct {
		command *exec.Cmd
		output  bytes.Buffer
	}
	processes := make([]*childProcess, 0, children)
	for index := 1; index <= children; index++ {
		command := exec.Command(os.Args[0], "-test.run=^TestCommandLogConcurrentProcessAppendsAreComplete$")
		command.Env = append(os.Environ(), childVariable+"="+strconv.Itoa(index), "AI4J_TEST_COMMAND_LOG_ROOT="+dataRoot)
		process := &childProcess{command: command}
		command.Stdout = &process.output
		command.Stderr = &process.output
		processes = append(processes, process)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, process := range processes {
		if err := process.command.Wait(); err != nil {
			t.Fatalf("child append failed: %v\n%s", err, process.output.Bytes())
		}
	}

	logPath := filepath.Join(dataRoot, "logs", "installations", "7c2fd86a", "ai4j-7c2fd86a-2026-08-29.log")
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(contents, []byte("\n")); got != children*3 {
		t.Fatalf("concurrent record count = %d, want %d:\n%s", got, children*3, contents)
	}
	for index := 1; index <= children; index++ {
		runID := strings.Repeat(fmt.Sprintf("%02x", index), commandLogRunIDBytes)
		if got := bytes.Count(contents, []byte("run_id="+runID)); got != 3 {
			t.Fatalf("records for child %d = %d, want 3", index, got)
		}
	}
}

func assertPrivatePathPermissions(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("log permissions = %o, want 600", got)
	}
	info, err = os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("log directory permissions = %o, want 700", got)
	}
}

func mustInstallationID(t *testing.T, value string) domain.InstallationID {
	t.Helper()
	id, err := domain.NewInstallationID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
