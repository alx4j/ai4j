package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alx4j/ai4j/internal/buildinfo"
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/host/privatepath"
	"github.com/alx4j/ai4j/internal/result"
)

const (
	commandLogRetention  = 10
	commandLogRunIDBytes = 8
	commandLogToolLimit  = 64
)

var commandLogUnsafeToolCharacters = regexp.MustCompile(`[^a-z0-9_-]+`)
var commandLogToolPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type commandLogRecord struct {
	time       time.Time
	message    string
	attributes []slog.Attr
}

type commandLogSession struct {
	dataRoot             string
	tool                 string
	command              cli.Command
	installation         domain.InstallationID
	started              time.Time
	now                  func() time.Time
	runID                string
	pending              []commandLogRecord
	awaitingInstallation bool
	closed               bool
}

func commandLogToolName(argv []string) string {
	if len(argv) == 0 {
		return buildinfo.Executable
	}
	name := path.Base(strings.ReplaceAll(argv[0], `\`, "/"))
	if strings.HasSuffix(strings.ToLower(name), ".exe") {
		name = name[:len(name)-len(".exe")]
	}
	return normalizeCommandLogTool(name)
}

func normalizeCommandLogTool(name string) string {
	name = commandLogUnsafeToolCharacters.ReplaceAllString(strings.ToLower(name), "-")
	name = strings.Trim(name, "-_")
	if len(name) > commandLogToolLimit {
		name = strings.TrimRight(name[:commandLogToolLimit], "-_")
	}
	if !commandLogToolPattern.MatchString(name) {
		return buildinfo.Executable
	}
	return name
}

func startCommandLog(dataRoot, tool string, request cli.Request) *commandLogSession {
	return startCommandLogWith(dataRoot, tool, request, time.Now, rand.Reader)
}

func startCommandLogWith(dataRoot, tool string, request cli.Request, now func() time.Time, entropy io.Reader) *commandLogSession {
	if request == nil || now == nil || entropy == nil || !filepath.IsAbs(dataRoot) || filepath.Clean(dataRoot) != dataRoot {
		return nil
	}
	command := request.Command()
	if !command.Valid() {
		return nil
	}
	var runID [commandLogRunIDBytes]byte
	if _, err := io.ReadFull(entropy, runID[:]); err != nil {
		return nil
	}
	installation := commandRequestInstallation(request)
	session := &commandLogSession{
		dataRoot: dataRoot, tool: normalizeCommandLogTool(tool), command: command,
		installation: installation, started: now().UTC(), now: now, runID: fmt.Sprintf("%x", runID),
		awaitingInstallation: command == cli.CommandInstall && !installation.Valid(),
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

func commandLogFileName(tool string, command cli.Command, installation domain.InstallationID, at time.Time) string {
	key := command.String()
	if installation.Valid() {
		key = installation.String()
	}
	return fmt.Sprintf("%s-%s-%s.log", tool, key, at.UTC().Format(time.DateOnly))
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
	s.awaitingInstallation = false
	s.flushPending()
}

func (s *commandLogSession) complete(response cli.Response, handlerErr error) {
	if s == nil || s.closed {
		return
	}
	responseMatches := handlerErr == nil && response.Valid() && response.Command() == s.command
	if !s.installation.Valid() && responseMatches {
		s.bindInstallation(commandResponseInstallation(response))
	}
	if s.awaitingInstallation {
		s.awaitingInstallation = false
		s.pending = nil
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
}

func (s *commandLogSession) write(message string, attributes ...slog.Attr) {
	if s == nil || s.closed {
		return
	}
	record := commandLogRecord{
		time: s.now().UTC(), message: message, attributes: slices.Clone(attributes),
	}
	if s.awaitingInstallation {
		s.pending = append(s.pending, record)
		s.appendRecord(record)
		return
	}
	s.appendRecord(record)
}

func (s *commandLogSession) flushPending() {
	for _, record := range s.pending {
		s.appendRecord(record)
	}
	s.pending = nil
}

func (s *commandLogSession) appendRecord(entry commandLogRecord) {
	directory := commandLogDirectory(s.dataRoot, s.command, s.installation)
	if err := privatepath.EnsureDirectory(directory); err != nil {
		return
	}
	name := commandLogFileName(s.tool, s.command, s.installation, entry.time)
	logPath := filepath.Join(directory, name)
	if info, err := os.Lstat(logPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return
	}
	contents := s.encodeRecord(entry)
	if len(contents) == 0 {
		return
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	if err = file.Chmod(0o600); err == nil {
		var written int
		written, err = file.Write(contents)
		if written != len(contents) && err == nil {
			err = io.ErrShortWrite
		}
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return
	}
	pruneCommandLogs(directory, s.tool, commandLogKey(s.command, s.installation), logPath)
}

func (s *commandLogSession) encodeRecord(entry commandLogRecord) []byte {
	var output bytes.Buffer
	record := slog.NewRecord(entry.time, slog.LevelInfo, entry.message, 0)
	record.AddAttrs(
		slog.String("command", s.command.String()),
		slog.String("run_id", s.runID),
	)
	if s.installation.Valid() {
		record.AddAttrs(slog.String("installation_id", s.installation.String()))
	}
	record.AddAttrs(entry.attributes...)
	handler := slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelInfo})
	if err := handler.Handle(context.Background(), record); err != nil {
		return nil
	}
	return output.Bytes()
}

func commandLogKey(command cli.Command, installation domain.InstallationID) string {
	if installation.Valid() {
		return installation.String()
	}
	return command.String()
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

type dailyCommandLog struct {
	date string
	name string
	path string
}

func pruneCommandLogs(directory, tool, key, currentPath string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	logs := make([]dailyCommandLog, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		date, owned := commandLogDate(entry.Name(), tool, key)
		if !owned {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		logs = append(logs, dailyCommandLog{date: date, name: entry.Name(), path: filepath.Join(directory, entry.Name())})
	}
	slices.SortFunc(logs, func(left, right dailyCommandLog) int {
		if order := strings.Compare(left.date, right.date); order != 0 {
			return order
		}
		return strings.Compare(left.name, right.name)
	})
	excess := max(len(logs)-commandLogRetention, 0)
	for _, log := range logs {
		if excess == 0 {
			break
		}
		if filepath.Clean(log.path) == filepath.Clean(currentPath) {
			continue
		}
		if err := os.Remove(log.path); err == nil || errors.Is(err, os.ErrNotExist) {
			excess--
		}
	}
}

func commandLogDate(name, tool, key string) (string, bool) {
	prefix := tool + "-" + key + "-"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".log") {
		return "", false
	}
	date := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".log")
	if len(date) != len(time.DateOnly) {
		return "", false
	}
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return "", false
	}
	return date, true
}
