package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/host/privatepath"
	"github.com/alx4j/ai4j/internal/result"
)

const (
	commandLogRetention  = 10
	commandLogTimeLayout = "20060102T150405.000000000Z"
	commandLogStaleAfter = 7 * 24 * time.Hour
)

var commandLogNamePattern = regexp.MustCompile(`^ai4j-[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-f]{16}\.log(\.interrupted)?$`)
var commandLogActiveNamePattern = regexp.MustCompile(`^ai4j-[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-f]{16}\.log\.active$`)

type commandLogSession struct {
	dataRoot     string
	directory    string
	path         string
	command      cli.Command
	installation domain.InstallationID
	started      time.Time
	now          func() time.Time
	file         *os.File
	handler      slog.Handler
	closed       bool
}

func startCommandLog(dataRoot string, request cli.Request) *commandLogSession {
	return startCommandLogWith(dataRoot, request, time.Now, rand.Reader)
}

func startCommandLogWith(dataRoot string, request cli.Request, now func() time.Time, entropy io.Reader) *commandLogSession {
	if request == nil || now == nil || entropy == nil || !filepath.IsAbs(dataRoot) || filepath.Clean(dataRoot) != dataRoot {
		return nil
	}
	command := request.Command()
	if !command.Valid() {
		return nil
	}
	installation := commandRequestInstallation(request)
	directory := commandLogDirectory(dataRoot, command, installation)
	if err := privatepath.EnsureDirectory(directory); err != nil {
		return nil
	}
	started := now().UTC()
	pruneCommandLogs(directory, started)
	var nonce [8]byte
	if _, err := io.ReadFull(entropy, nonce[:]); err != nil {
		return nil
	}
	name := fmt.Sprintf("ai4j-%s-%x.log.active", started.Format(commandLogTimeLayout), nonce)
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil
	}
	session := &commandLogSession{
		dataRoot: dataRoot, directory: directory, path: path, command: command,
		installation: installation, started: started, now: now, file: file,
		handler: slog.NewTextHandler(file, &slog.HandlerOptions{Level: slog.LevelInfo}),
	}
	session.write("command started")
	session.progress(commandLogInitialStage(command))
	return session
}

func commandRequestInstallation(request cli.Request) domain.InstallationID {
	withInstallation, ok := request.(interface {
		InstallationID() domain.InstallationID
	})
	if !ok || !withInstallation.InstallationID().Valid() {
		return domain.InstallationID{}
	}
	return withInstallation.InstallationID()
}

func commandLogDirectory(dataRoot string, command cli.Command, installation domain.InstallationID) string {
	if installation.Valid() {
		return filepath.Join(dataRoot, "logs", "installations", installation.String())
	}
	return filepath.Join(dataRoot, "logs", "commands", command.String())
}

func commandLogInitialStage(command cli.Command) string {
	switch command {
	case cli.CommandInit:
		return "initialize"
	case cli.CommandValidate, cli.CommandBuild, cli.CommandInstall, cli.CommandUpdate, cli.CommandSync,
		cli.CommandRollback, cli.CommandUninstall, cli.CommandHistoryPurge:
		return "prepare"
	case cli.CommandList, cli.CommandHistory:
		return "load"
	case cli.CommandStatus:
		return "inspect"
	case cli.CommandDoctor:
		return "diagnose"
	default:
		return "dispatch"
	}
}

func (s *commandLogSession) progress(stage string) {
	if s == nil || s.closed || stage == "" {
		return
	}
	s.write("command progress", slog.String("stage", stage))
}

func (s *commandLogSession) bindInstallation(installation domain.InstallationID) {
	if s == nil || s.closed || s.installation.Valid() || !installation.Valid() {
		return
	}
	s.installation = installation
}

func (s *commandLogSession) complete(response cli.Response, handlerErr error) {
	if s == nil || s.closed {
		return
	}
	responseMatches := handlerErr == nil && response.Valid() && response.Command() == s.command
	if !s.installation.Valid() && responseMatches {
		s.installation = commandResponseInstallation(response)
	}
	attributes := []slog.Attr{slog.Int64("duration_ms", max(s.now().UTC().Sub(s.started).Milliseconds(), 0))}
	if !responseMatches {
		status := result.StatusError
		failure := result.FailureInternal
		exitCode := result.ExitUnexpectedInternal
		if isContextCancellation(handlerErr) {
			status = result.StatusCancelled
			failure = result.FailureCancellation
			exitCode = result.ExitCancelled
		}
		attributes = append(attributes,
			slog.String("status", status.String()),
			slog.String("phase", result.PhaseNone.String()),
			slog.String("outcome", result.OutcomeNone.String()),
			slog.String("mutation", result.MutationNotStarted.String()),
			slog.String("durable_change", result.DurableChangeNone.String()),
			slog.String("failure", failure.String()),
			slog.String("update_disposition", result.UpdateNotChecked.String()),
			slog.Int("result_exit_code", exitCode.Int()),
		)
	} else {
		commandResult := response.Result()
		attributes = append(attributes,
			slog.String("status", commandResult.Status().String()),
			slog.String("phase", commandResult.Phase().String()),
			slog.String("outcome", commandResult.Outcome().String()),
			slog.String("mutation", commandResult.Mutation().String()),
			slog.String("durable_change", commandResult.DurableChange().String()),
			slog.String("failure", commandResult.Failure().String()),
			slog.String("update_disposition", commandResult.UpdateDisposition().String()),
			slog.Int("result_exit_code", commandResult.ExitCode().Int()),
		)
		if response.HasOperationID() {
			attributes = append(attributes, slog.String("operation_id", response.OperationID().String()))
		}
		if codes := warningCodes(commandResult.Warnings()); len(codes) != 0 {
			attributes = append(attributes, slog.Any("warning_codes", codes))
		}
		if codes := problemCodes(commandResult.Errors()); len(codes) != 0 {
			attributes = append(attributes, slog.Any("problem_codes", codes))
		}
	}
	s.write("command completed", attributes...)
	s.closed = true
	_ = s.file.Close()

	originalDirectory := s.directory
	targetDirectory := commandLogDirectory(s.dataRoot, s.command, s.installation)
	if privatepath.EnsureDirectory(targetDirectory) == nil {
		name := filepath.Base(s.path)
		name = name[:len(name)-len(".active")]
		target := filepath.Join(targetDirectory, name)
		if os.Rename(s.path, target) == nil {
			s.directory = targetDirectory
			s.path = target
		}
	}
	pruneCommandLogs(originalDirectory, s.now().UTC())
	if s.directory != originalDirectory {
		pruneCommandLogs(s.directory, s.now().UTC())
	}
}

func (s *commandLogSession) write(message string, attributes ...slog.Attr) {
	if s == nil || s.closed || s.handler == nil {
		return
	}
	record := slog.NewRecord(s.now().UTC(), slog.LevelInfo, message, 0)
	record.AddAttrs(slog.String("command", s.command.String()))
	if s.installation.Valid() {
		record.AddAttrs(slog.String("installation_id", s.installation.String()))
	}
	record.AddAttrs(attributes...)
	_ = s.handler.Handle(context.Background(), record)
}

func commandResponseInstallation(response cli.Response) domain.InstallationID {
	if !response.Valid() {
		return domain.InstallationID{}
	}
	switch data := response.Data().(type) {
	case cli.PlanData:
		return data.InstallationID()
	case cli.MutationData:
		if data.HasInstallationID() {
			return data.InstallationID()
		}
	}
	return domain.InstallationID{}
}

func warningCodes(warnings []result.Warning) []string {
	codes := make([]string, len(warnings))
	for index, warning := range warnings {
		codes[index] = warning.Code()
	}
	return codes
}

func problemCodes(problems []result.Problem) []string {
	codes := make([]string, len(problems))
	for index, problem := range problems {
		codes[index] = problem.Code()
	}
	return codes
}

func pruneCommandLogs(directory string, now time.Time) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		completedName := commandLogNamePattern.MatchString(name)
		activeName := commandLogActiveNamePattern.MatchString(name)
		if (!completedName && !activeName) || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(directory, name)
		if activeName {
			if info.ModTime().After(now.Add(-commandLogStaleAfter)) {
				continue
			}
			interrupted := strings.TrimSuffix(path, ".active") + ".interrupted"
			if os.Rename(path, interrupted) != nil {
				continue
			}
			path = interrupted
		}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths[:max(len(paths)-commandLogRetention, 0)] {
		_ = os.Remove(path)
	}
}
