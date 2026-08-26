package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/domain"
)

const nativeMutationTimeout = 30 * time.Second

func (s *lifecycleService) effectiveClaudeRoot() string {
	if s.claudeRoot != "" {
		return s.claudeRoot
	}
	return filepath.Join(s.home, ".claude")
}

func (s *lifecycleService) runClaudeAt(ctx context.Context, directory string, arguments []string) error {
	executable, err := s.runner.LookPath("claude")
	if err != nil {
		return err
	}
	commandContext, cancel := context.WithTimeout(ctx, nativeMutationTimeout)
	defer cancel()
	observation, err := s.runner.Run(commandContext, directory, executable, arguments, []string{
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL=1",
		"DISABLE_UPDATES=1",
	})
	if err != nil || observation.ExitCode != 0 {
		return errors.New("Claude command failed")
	}
	return nil
}

func (s *lifecycleService) host() string {
	if s.build.TargetOS() == "windows" && s.build.TargetArch() == "amd64" {
		return "windows-amd64"
	}
	return "darwin-arm64"
}

func newOperationID(source io.Reader) (domain.OperationID, error) {
	value := make([]byte, 12)
	if source == nil {
		source = rand.Reader
	}
	if _, err := io.ReadFull(source, value); err != nil {
		return domain.OperationID{}, err
	}
	return domain.NewOperationID("operation-" + hex.EncodeToString(value))
}

func writeOwnedNew(root, path string, contents []byte) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("owned path is outside its root")
	}
	current := root
	for _, component := range strings.Split(filepath.Dir(relative), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			if mkdirErr := os.Mkdir(current, 0o700); mkdirErr != nil {
				return mkdirErr
			}
		case statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || hostPathUnsafe(current):
			return errors.New("owned path parent is unsafe")
		}
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("owned destination is occupied")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if hostPathUnsafe(filepath.Dir(path)) {
		return errors.New("owned path parent is unsafe")
	}
	if err := diskcapacity.Require(filepath.Dir(path), uint64(len(contents))); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ai4j-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Link(temporaryPath, path)
}
