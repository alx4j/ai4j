package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/result"
)

func TestCommandLogRoutesExistingInstallationAndLogsOnlySafeProgress(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	request := parseRequest[cli.StatusRequest](t, "status", "installation_001")
	fixed := time.Date(2026, 8, 29, 12, 34, 56, 123456789, time.UTC)
	session := startCommandLogWith(dataRoot, request, func() time.Time { return fixed }, bytes.NewReader(bytes.Repeat([]byte{0xab}, 8)))
	if session == nil {
		t.Fatal("startCommandLogWith returned nil")
	}

	var progress bytes.Buffer
	reportProgress(CommandIO{Progress: &progress, logSession: session}, "verify", "SECRET_CANARY")
	session.complete(cli.Response{}, errors.New("SECRET_CANARY"))

	wantPath := filepath.Join(dataRoot, "logs", "installations", "installation_001", "ai4j-20260829T123456.123456789Z-abababababababab.log")
	if session.path != wantPath {
		t.Fatalf("log path = %q, want %q", session.path, wantPath)
	}
	contents, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, expected := range []string{
		`msg="command started"`, `command=status`, `installation_id=installation_001`,
		`msg="command progress"`, `stage=inspect`, `stage=verify`, `msg="command completed"`,
		`status=error`, `phase=none`, `outcome=none`, `mutation=not_started`, `durable_change=none`,
		`failure=internal`, `update_disposition=not_checked`, `result_exit_code=9`,
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
		info, err := os.Stat(wantPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("log permissions = %o, want 600", got)
		}
		info, err = os.Stat(filepath.Dir(wantPath))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("log directory permissions = %o, want 700", got)
		}
	}
}

func TestCommandLogRoutesIDLessCommandAndClassifiesCancellation(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	request := parseRequest[cli.ListRequest](t, "list")
	fixed := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	session := startCommandLogWith(dataRoot, request, func() time.Time { return fixed }, bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7, 8}))
	if session == nil {
		t.Fatal("startCommandLogWith returned nil")
	}
	session.complete(cli.Response{}, context.Canceled)

	wantDirectory := filepath.Join(dataRoot, "logs", "commands", "list")
	if session.directory != wantDirectory || filepath.Dir(session.path) != wantDirectory || strings.HasSuffix(session.path, ".active") {
		t.Fatalf("final log path = %q, want completed log under %q", session.path, wantDirectory)
	}
	contents, err := os.ReadFile(session.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"command=list", "status=cancelled", "phase=none", "outcome=none", "mutation=not_started",
		"durable_change=none", "failure=cancellation", "update_disposition=not_checked", "result_exit_code=1",
	} {
		if !bytes.Contains(contents, []byte(expected)) {
			t.Errorf("cancelled completion does not contain %q:\n%s", expected, contents)
		}
	}
}

func TestCommandLogKeepsPreparedInstallationOnLaterFailure(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--dry-run")
	fixed := time.Date(2026, 8, 29, 12, 30, 0, 0, time.UTC)
	session := startCommandLogWith(dataRoot, request, func() time.Time { return fixed }, bytes.NewReader(bytes.Repeat([]byte{0xcd}, 8)))
	if session == nil {
		t.Fatal("startCommandLogWith returned nil")
	}
	installationID, err := domain.NewInstallationID("installation_001")
	if err != nil {
		t.Fatal(err)
	}
	session.bindInstallation(installationID)
	session.complete(cli.Response{}, errors.New("plan failed"))

	wantDirectory := filepath.Join(dataRoot, "logs", "installations", installationID.String())
	if session.directory != wantDirectory || filepath.Dir(session.path) != wantDirectory {
		t.Fatalf("final log path = %q, want directory %q", session.path, wantDirectory)
	}
	contents, err := os.ReadFile(session.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contents, []byte("installation_id="+installationID.String())) {
		t.Fatalf("prepared installation ID is missing:\n%s", contents)
	}
}

func TestCommandLogMovesAutomaticInstallIntoResolvedInstallationBucket(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	request := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--dry-run")
	fixed := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	session := startCommandLogWith(dataRoot, request, func() time.Time { return fixed }, bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7}))
	if session == nil {
		t.Fatal("startCommandLogWith returned nil")
	}
	originalPath := session.path
	if !strings.HasSuffix(originalPath, ".log.active") {
		t.Fatalf("active log path = %q", originalPath)
	}
	if !strings.Contains(originalPath, filepath.Join("logs", "commands", "install")) {
		t.Fatalf("automatic install started at %q", originalPath)
	}

	installationID, err := domain.NewInstallationID("installation_001")
	if err != nil {
		t.Fatal(err)
	}
	final, err := cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	if err != nil {
		t.Fatal(err)
	}
	source := testPlanSourceFrom(t, request.Source(), strings.Repeat("a", 40))
	commandResult, err := neutralResult(result.StatusOK, result.FailureNone, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := cli.NewPlanData(cli.OperationInstall, source, installationID, nil, nil, nil, final, result.UpdateNotChecked)
	if err != nil {
		t.Fatal(err)
	}
	response, err := cli.NewResponse(cli.CommandInstall, commandResult, nil, data)
	if err != nil {
		t.Fatal(err)
	}
	session.complete(response, nil)

	wantDirectory := filepath.Join(dataRoot, "logs", "installations", installationID.String())
	if session.directory != wantDirectory || filepath.Dir(session.path) != wantDirectory {
		t.Fatalf("final log path = %q, want directory %q", session.path, wantDirectory)
	}
	if _, err := os.Stat(originalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unscoped log still exists: %v", err)
	}
	contents, err := os.ReadFile(session.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"installation_id=" + installationID.String(), "status=ok", "phase=none", "outcome=none",
		"mutation=not_started", "durable_change=none", "failure=none", "update_disposition=not_checked", "result_exit_code=0",
	} {
		if !bytes.Contains(contents, []byte(expected)) {
			t.Errorf("completion does not contain %q:\n%s", expected, contents)
		}
	}
}

func TestCommandLogRetentionIsIsolatedAndLeavesUnknownEntries(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "installation_001")
	second := filepath.Join(root, "installation_002")
	for _, directory := range []string{first, second} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for index := range commandLogRetention + 1 {
		name := fmt.Sprintf("ai4j-20260829T1200%02d.000000000Z-%016x.log", index, index)
		names = append(names, name)
		if err := os.WriteFile(filepath.Join(first, name), []byte("log\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(second, name), []byte("log\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unknown := filepath.Join(first, "keep-me.log")
	if err := os.WriteFile(unknown, []byte("user-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ownedDirectory := filepath.Join(first, "ai4j-20260829T115959.000000000Z-0000000000000000.log")
	if err := os.Mkdir(ownedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(first, "ai4j-20260829T115958.000000000Z-0000000000000000.log.active")
	if err := os.WriteFile(active, []byte("active\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pruneCommandLogs(first, time.Now())

	if _, err := os.Stat(filepath.Join(first, names[0])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest owned log was not pruned: %v", err)
	}
	entries, err := os.ReadDir(first)
	if err != nil {
		t.Fatal(err)
	}
	var retained []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && commandLogNamePattern.MatchString(entry.Name()) {
			retained = append(retained, entry.Name())
		}
	}
	if len(retained) != commandLogRetention || !slices.Equal(retained, names[1:]) {
		t.Fatalf("retained logs = %v, want %v", retained, names[1:])
	}
	if contents, err := os.ReadFile(unknown); err != nil || string(contents) != "user-owned\n" {
		t.Fatalf("unknown file changed: %q, %v", contents, err)
	}
	if info, err := os.Stat(ownedDirectory); err != nil || !info.IsDir() {
		t.Fatalf("matching directory changed: %v, %v", info, err)
	}
	if contents, err := os.ReadFile(active); err != nil || string(contents) != "active\n" {
		t.Fatalf("active log changed: %q, %v", contents, err)
	}
	secondEntries, err := os.ReadDir(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondEntries) != commandLogRetention+1 {
		t.Fatalf("other installation retained %d logs, want %d", len(secondEntries), commandLogRetention+1)
	}
}

func TestCommandLogFinalizesStaleCrashLogBeforeRetention(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	active := filepath.Join(directory, "ai4j-20260820T120000.000000000Z-0000000000000000.log.active")
	if err := os.WriteFile(active, []byte("interrupted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := now.Add(-commandLogStaleAfter)
	if err := os.Chtimes(active, stale, stale); err != nil {
		t.Fatal(err)
	}

	pruneCommandLogs(directory, now)

	interrupted := strings.TrimSuffix(active, ".active") + ".interrupted"
	if _, err := os.Stat(active); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale active log still exists: %v", err)
	}
	if contents, err := os.ReadFile(interrupted); err != nil || string(contents) != "interrupted\n" {
		t.Fatalf("interrupted crash log = %q, %v", contents, err)
	}
}

func TestCommandHandlerCreatesCompletedLog(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	state, err := installstate.NewStoreAt(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	handler := newCommandHandler(commandRouter{status: statusService{state: state}}, dataRoot)
	request := parseRequest[cli.ListRequest](t, "list")

	response, err := handler(context.Background(), request, CommandIO{})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Valid() || response.Result().ExitCode() != result.ExitSuccess {
		t.Fatalf("list response = %#v", response)
	}
	logs, err := filepath.Glob(filepath.Join(dataRoot, "logs", "commands", "list", "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("completed logs = %v, want one", logs)
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
	handler := newCommandHandler(commandRouter{status: statusService{state: state}}, blockedDataRoot)
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
