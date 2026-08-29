// Package hostprocess runs bounded host processes with platform-native process-tree containment.
package hostprocess

import (
	"bytes"
	"context"
	"os/exec"
)

const maximumOutput = 16 << 20

// Result describes one bounded process execution.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Started  bool
	TimedOut bool
}

// OSRunner executes processes through the host operating system.
type OSRunner struct{}

func (OSRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (OSRunner) Run(ctx context.Context, directory, executable string, arguments, environment []string) (Result, error) {
	return run(ctx, directory, executable, arguments, environment, true)
}

// RunIsolated launches an explicitly approved diagnostic process with only the
// supplied allowlisted environment. It uses the same bounded capture and
// process-tree containment as other host processes.
func (OSRunner) RunIsolated(ctx context.Context, directory, executable string, arguments, environment []string) (Result, error) {
	return run(ctx, directory, executable, arguments, environment, false)
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	if b.overflow {
		return len(value), nil
	}
	remaining := maximumOutput - b.buffer.Len()
	if remaining <= 0 {
		b.overflow = true
		return len(value), nil
	}
	written := len(value)
	if len(value) > remaining {
		value = value[:remaining]
		b.overflow = true
	}
	_, _ = b.buffer.Write(value)
	return written, nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }
