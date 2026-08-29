package hostprocess

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const processTestTimeout = 10 * time.Second

type processOutcome struct {
	result Result
	err    error
}

type testProcessInvocation struct {
	arguments   []string
	environment []string
}

func TestCancellationBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := (OSRunner{}).Run(ctx, "", os.Args[0], []string{"-test.run=^$"}, nil)
	if !errors.Is(err, context.Canceled) || result.Started || result.TimedOut || result.ExitCode != 0 || len(result.Stdout) != 0 || len(result.Stderr) != 0 {
		t.Fatalf("Run() = %#v, %v", result, err)
	}
}

func TestBoundedOutputRejectsOverflow(t *testing.T) {
	if os.Getenv("AI4J_OUTPUT_TEST_ROLE") == "child" {
		chunk := bytes.Repeat([]byte{'x'}, 64<<10)
		remaining := maximumOutput + 1
		for remaining > 0 {
			written := min(remaining, len(chunk))
			if _, err := os.Stdout.Write(chunk[:written]); err != nil {
				os.Exit(5)
			}
			remaining -= written
		}
		return
	}

	invocation := newTestProcessInvocation(t, "TestBoundedOutputRejectsOverflow", "AI4J_OUTPUT_TEST_ROLE=child")
	result, err := (OSRunner{}).RunIsolated(context.Background(), "", os.Args[0], invocation.arguments, invocation.environment)
	if err == nil || err.Error() != "process output limit exceeded" || result.Started || result.TimedOut || result.ExitCode != 0 || len(result.Stdout) != 0 || len(result.Stderr) != 0 {
		t.Fatalf("RunIsolated() = started:%t timedOut:%t exit:%d stdout:%d stderr:%d, %v", result.Started, result.TimedOut, result.ExitCode, len(result.Stdout), len(result.Stderr), err)
	}
}

func newTestProcessInvocation(t *testing.T, testName string, environment ...string) testProcessInvocation {
	t.Helper()
	coverageDirectory := t.TempDir()
	return testProcessInvocation{
		arguments:   testProcessArguments(testName, coverageDirectory),
		environment: append([]string{"GOCOVERDIR=" + coverageDirectory}, environment...),
	}
}

func childTestProcessArguments(testName string) []string {
	return testProcessArguments(testName, os.Getenv("GOCOVERDIR"))
}

func testProcessArguments(testName, coverageDirectory string) []string {
	arguments := []string{"-test.run=^" + testName + "$"}
	if testing.CoverMode() != "" && coverageDirectory != "" {
		arguments = append(arguments, "-test.gocoverdir="+coverageDirectory)
	}
	return arguments
}

func startProcess(run func() (Result, error)) <-chan processOutcome {
	outcomes := make(chan processOutcome, 1)
	go func() {
		result, err := run()
		outcomes <- processOutcome{result: result, err: err}
	}()
	return outcomes
}

func publishProcessID(path string, pid int) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func waitForProcessID(path string, outcomes <-chan processOutcome) (int, *processOutcome, error) {
	return waitForProcessIDWithReader(path, outcomes, os.ReadFile)
}

func waitForProcessIDWithReader(path string, outcomes <-chan processOutcome, readFile func(string) ([]byte, error)) (int, *processOutcome, error) {
	timer := time.NewTimer(processTestTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		contents, err := readFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(contents)))
			if parseErr != nil || pid <= 0 {
				return 0, nil, errors.New("invalid process readiness signal")
			}
			return pid, nil, nil
		}
		if !errors.Is(err, os.ErrNotExist) && !transientProcessIDReadError(err) {
			return 0, nil, fmt.Errorf("read process readiness signal: %w", err)
		}
		select {
		case outcome := <-outcomes:
			return 0, &outcome, errors.New("runner exited before process readiness")
		case <-ticker.C:
		case <-timer.C:
			return 0, nil, errors.New("timed out waiting for process readiness")
		}
	}
}

func TestWaitForProcessIDRejectsMalformedSignal(t *testing.T) {
	reads := 0
	pid, outcome, err := waitForProcessIDWithReader("child.pid", make(chan processOutcome), func(string) ([]byte, error) {
		reads++
		return []byte("not-a-process-id"), nil
	})

	if err == nil || err.Error() != "invalid process readiness signal" || pid != 0 || outcome != nil || reads != 1 {
		t.Fatalf("waitForProcessIDWithReader() = %d, %#v, %v after %d reads", pid, outcome, err, reads)
	}
}

func TestWaitForProcessIDRejectsNonTransientReadError(t *testing.T) {
	reads := 0
	readErr := &os.PathError{Op: "open", Path: "child.pid", Err: os.ErrPermission}
	pid, outcome, err := waitForProcessIDWithReader("child.pid", make(chan processOutcome), func(string) ([]byte, error) {
		reads++
		return nil, readErr
	})

	if !errors.Is(err, os.ErrPermission) || pid != 0 || outcome != nil || reads != 1 {
		t.Fatalf("waitForProcessIDWithReader() = %d, %#v, %v after %d reads", pid, outcome, err, reads)
	}
}

func waitForProcessOutcome(outcomes <-chan processOutcome) (processOutcome, error) {
	timer := time.NewTimer(processTestTimeout)
	defer timer.Stop()
	select {
	case outcome := <-outcomes:
		return outcome, nil
	case <-timer.C:
		return processOutcome{}, errors.New("timed out waiting for runner completion")
	}
}

func processIDFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "child.pid")
}
