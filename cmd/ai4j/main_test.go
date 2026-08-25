package main

import (
	"bytes"
	"io"
	"reflect"
	"testing"
)

func TestRunDelegatesProcessBoundary(t *testing.T) {
	t.Parallel()

	args := []string{`C:\Program Files\AI4J\ai4j.exe`, "version", "--json"}
	stdin := bytes.NewBufferString("input")
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	const wantExitCode = 23

	called := false
	gotExitCode := run(func(gotArgs []string, gotStdin io.Reader, gotStdout, gotStderr io.Writer) int {
		called = true
		if !reflect.DeepEqual(gotArgs, args) {
			t.Errorf("args = %q, want %q", gotArgs, args)
		}
		if gotStdin != stdin {
			t.Error("stdin was not passed through")
		}
		if gotStdout != stdout {
			t.Error("stdout was not passed through")
		}
		if gotStderr != stderr {
			t.Error("stderr was not passed through")
		}
		return wantExitCode
	}, args, stdin, stdout, stderr)

	if !called {
		t.Fatal("runner was not called")
	}
	if gotExitCode != wantExitCode {
		t.Fatalf("exit code = %d, want %d", gotExitCode, wantExitCode)
	}
}
