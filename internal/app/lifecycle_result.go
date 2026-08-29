package app

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/diskcapacity"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
)

func approveLifecycle(approved bool, outputMode cli.OutputMode, commandIO CommandIO, plan cli.Response, operation string) (approvalDecision, error) {
	if approved {
		return approvalGranted, nil
	}
	if outputMode == cli.OutputJSON {
		return approvalMissing, nil
	}
	return promptApproval(commandIO, plan, "Proceed with "+operation+"? [y/N]: ")
}

func lifecycleFailure(command cli.Command, failure result.Failure, code, message string, disposition result.UpdateDisposition, warnings []result.Warning) (cli.Response, error) {
	problem, err := result.NewProblem(code, message, nil)
	if err != nil {
		return cli.Response{}, err
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: failure, UpdateDisposition: disposition, Warnings: warnings, Errors: []result.Problem{problem},
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}

func lifecycleConflict(command cli.Command, conflicts []cli.Conflict, disposition result.UpdateDisposition, warnings []result.Warning) (cli.Response, error) {
	problems := make([]result.Problem, 0, len(conflicts))
	for _, conflict := range conflicts {
		item, _ := result.NewContext("resource", conflict.Resource())
		problem, _ := result.NewProblem(conflict.Code(), conflict.Message(), []result.Context{item})
		problems = append(problems, problem)
	}
	commandResult, err := result.New(result.Facts{
		Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureConflict, UpdateDisposition: disposition, Warnings: warnings, Errors: problems,
	})
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, cli.UnavailableData{})
}

func lifecycleNoChange(command cli.Command, operation cli.Operation, installationID *domain.InstallationID, final cli.FinalState, disposition result.UpdateDisposition, warnings []result.Warning) (cli.Response, error) {
	commandResult, err := result.New(result.Facts{
		Status: result.StatusNoChange, Phase: result.PhaseNone, Outcome: result.OutcomeNone,
		Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone,
		Failure: result.FailureNone, UpdateDisposition: disposition, Warnings: warnings,
	})
	if err != nil {
		return cli.Response{}, err
	}
	data, err := cli.NewMutationData(operation, commandResult, installationID, nil, final, disposition)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(command, commandResult, nil, data)
}

func mustFinalState(installation, native, owned cli.StatePresence) cli.FinalState {
	final, err := cli.NewFinalState(installation, native, owned)
	if err != nil {
		panic(err)
	}
	return final
}

func replaceOwnedMatching(home, path, expectedChecksum string, contents []byte) error {
	if err := validateOwnedPath(home, path); err != nil || inspectFileDrift(path, expectedChecksum) != cli.DriftUnchanged {
		return errors.New("owned file does not match installation state")
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
	if err := validateOwnedPath(home, path); err != nil || inspectFileDrift(path, expectedChecksum) != cli.DriftUnchanged {
		return errors.New("owned file changed during replacement")
	}
	if err := commitOwnedReplacement(temporaryPath, path); err != nil {
		return err
	}
	digest := sha256Digest(contents)
	if inspectFileDrift(path, digest) != cli.DriftUnchanged {
		return errors.New("owned-file replacement could not be verified")
	}
	return nil
}

func removeOwnedMatching(home, path, expectedChecksum string) error {
	if err := validateOwnedPath(home, path); err != nil || inspectFileDrift(path, expectedChecksum) != cli.DriftUnchanged {
		return errors.New("owned file does not match installation state")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if !ownedFileAbsent(path) {
		return errors.New("owned-file removal could not be verified")
	}
	return nil
}

func validateOwnedPath(home, path string) error {
	relative, err := filepath.Rel(home, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("owned path is outside the user home")
	}
	current := home
	for component := range strings.SplitSeq(filepath.Dir(relative), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || hostPathUnsafe(current) {
			return errors.New("owned path parent is unsafe")
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || hostPathUnsafe(path) {
		return errors.New("owned path is unsafe")
	}
	return nil
}

func ownedFileAbsent(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

func sha256Digest(contents []byte) string {
	digest := sha256.Sum256(contents)
	return fmt.Sprintf("%x", digest)
}
