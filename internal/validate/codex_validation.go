package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const codexValidationTimeout = 30 * time.Second

type codexDoctorCheck struct {
	Status  string                     `json:"status"`
	Details map[string]json.RawMessage `json:"details"`
}

func (s Service) validateRenderedCodexAgents(ctx context.Context, stage, executable string) (validationErr error) {
	source := filepath.Join(stage, "configuration", ".codex", "agents")
	entries, err := os.ReadDir(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || len(entries) == 0 {
		return errNativeValidationEnvironment
	}

	home, err := os.MkdirTemp(s.config.TempRoot, "ai4j-codex-home-")
	if err != nil {
		return errNativeValidationEnvironment
	}
	defer func() {
		if os.RemoveAll(home) != nil {
			validationErr = errNativeValidationEnvironment
		}
	}()
	agents := filepath.Join(home, "agents")
	if err := os.Mkdir(agents, 0o700); err != nil {
		return errNativeValidationEnvironment
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			return errNativeValidationEnvironment
		}
		contents, readErr := os.ReadFile(filepath.Join(source, entry.Name()))
		if readErr != nil || os.WriteFile(filepath.Join(agents, entry.Name()), contents, 0o600) != nil {
			return errNativeValidationEnvironment
		}
	}

	nativeContext, cancel := context.WithTimeout(ctx, codexValidationTimeout)
	defer cancel()
	node, _ := s.config.Runner.LookPath("node")
	result, runErr := s.config.Runner.RunIsolated(
		nativeContext,
		stage,
		executable,
		[]string{"--strict-config", "doctor", "--json"},
		codexValidationEnvironment(home, executable, node),
	)
	if runErr != nil {
		return errNativeValidationEnvironment
	}
	valid, reported := codexConfigLoadResult(result.Stdout)
	if !reported {
		return errNativeValidationEnvironment
	}
	if !valid {
		return errNativeBuildValidation
	}
	return nil
}

func codexValidationEnvironment(home, codex, node string) []string {
	const blockedProxy = "http://127.0.0.1:9"
	environment := []string{
		"CODEX_HOME=" + home,
		"HTTP_PROXY=" + blockedProxy,
		"HTTPS_PROXY=" + blockedProxy,
		"ALL_PROXY=" + blockedProxy,
		"NO_PROXY=",
	}
	paths := executableDirectories(codex, node)
	if len(paths) != 0 {
		environment = append(environment, "PATH="+strings.Join(paths, string(os.PathListSeparator)))
	}
	if runtime.GOOS == "windows" {
		environment = append(environment, "PATHEXT=.COM;.EXE;.BAT;.CMD")
		if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
			environment = append(environment, "SystemRoot="+systemRoot)
		}
		if commandProcessor := os.Getenv("ComSpec"); commandProcessor != "" {
			environment = append(environment, "ComSpec="+commandProcessor)
		}
	}
	return environment
}

func executableDirectories(executables ...string) []string {
	directories := make([]string, 0, len(executables))
	for _, executable := range executables {
		if executable == "" {
			continue
		}
		directory := filepath.Clean(filepath.Dir(executable))
		duplicate := false
		for _, existing := range directories {
			if existing == directory || runtime.GOOS == "windows" && strings.EqualFold(existing, directory) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			directories = append(directories, directory)
		}
	}
	return directories
}

func validCodexConfigLoad(output []byte) bool {
	valid, _ := codexConfigLoadResult(output)
	return valid
}

func codexConfigLoadResult(output []byte) (valid, reported bool) {
	var document struct {
		Checks map[string]codexDoctorCheck `json:"checks"`
	}
	if json.Unmarshal(output, &document) != nil {
		return false, false
	}
	check, ok := document.Checks["config.load"]
	if !ok {
		return false, false
	}
	if check.Status != "ok" {
		return false, true
	}
	warning, present := check.Details["startup warning"]
	if !present {
		return true, true
	}
	warning = bytes.TrimSpace(warning)
	valid = bytes.Equal(warning, []byte("null")) || bytes.Equal(warning, []byte(`""`)) || bytes.Equal(warning, []byte("[]"))
	return valid, true
}
