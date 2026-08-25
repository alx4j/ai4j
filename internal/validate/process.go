package validate

import (
	"bytes"
	"context"
	"io"
	"os/exec"
)

const maximumProcessOutput = 16 << 20

// ProcessRunner is the single execution boundary used by Wave 1. It exists so
// Git and Claude invocations can be acceptance-tested without network access or
// starting toolkit content.
type ProcessRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, string, []string, []string) (ProcessResult, error)
}

type ProcessResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Started  bool
	TimedOut bool
}

type OSProcessRunner struct{}

func (OSProcessRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (OSProcessRunner) Run(ctx context.Context, directory, executable string, arguments, environment []string) (ProcessResult, error) {
	return runOSProcess(ctx, directory, executable, arguments, environment, true)
}

// RunIsolated launches an explicitly approved diagnostic process with only the
// supplied allowlisted environment. It uses the same bounded capture and
// process-tree containment as target-native commands.
func (OSProcessRunner) RunIsolated(ctx context.Context, directory, executable string, arguments, environment []string) (ProcessResult, error) {
	return runOSProcess(ctx, directory, executable, arguments, environment, false)
}

type limitedBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	if b.overflow {
		return len(value), nil
	}
	remaining := maximumProcessOutput - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return len(value), nil
	}
	written := len(value)
	if len(value) > remaining {
		value = value[:remaining]
		b.overflow = true
	}
	_, _ = io.Copy(&b.Buffer, bytes.NewReader(value))
	return written, nil
}
