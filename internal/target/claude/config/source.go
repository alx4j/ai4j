package config

import (
	"context"
	"os"
	"sync"
)

const (
	homeEnvironmentName            = "HOME"
	claudeConfigDirEnvironmentName = "CLAUDE_CONFIG_DIR"
)

// StartupSource captures the two target-specific startup values. Capture takes
// caller context but no variable name, and every source instance reads each
// fixed name at most once.
type StartupSource interface {
	Capture(context.Context) (StartupInput, error)
}

type environmentStartupSource struct {
	mu       sync.Mutex
	captured bool
	lookup   func(string) (string, bool)
	input    StartupInput
	err      error
}

// NewStartupSource returns the fixed ambient HOME/CLAUDE_CONFIG_DIR source.
// Callers retain the returned immutable value and pass it through bootstrap;
// target resolution never rereads the ambient environment.
func NewStartupSource() StartupSource { return newStartupSource(os.LookupEnv) }

func newStartupSource(lookup func(string) (string, bool)) *environmentStartupSource {
	return &environmentStartupSource{lookup: lookup}
}

func (s *environmentStartupSource) Capture(ctx context.Context) (StartupInput, error) {
	if s == nil || s.lookup == nil {
		return StartupInput{}, newError(CodeInvalidStartupInput)
	}
	if ctx == nil {
		return StartupInput{}, newError(CodeInvalidContext)
	}
	if err := contextFailure(ctx, ctx.Err()); err != nil {
		return StartupInput{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.captured {
		home, homePresent := s.lookup(homeEnvironmentName)
		override, overridePresent := s.lookup(claudeConfigDirEnvironmentName)
		s.input, s.err = NewStartupInput(home, homePresent, override, overridePresent)
		s.captured = true
	}
	if err := contextFailure(ctx, ctx.Err()); err != nil {
		return StartupInput{}, err
	}
	return s.input, s.err
}

var _ StartupSource = (*environmentStartupSource)(nil)
