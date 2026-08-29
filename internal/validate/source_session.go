package validate

import (
	"context"
	"errors"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/workspace"
)

type sourceSessionStage uint8

const (
	sourceSessionWorkspace sourceSessionStage = iota + 1
	sourceSessionAcquisition
	sourceSessionValidation
	sourceSessionConstruction
)

type sourceSessionError struct {
	stage sourceSessionStage
	err   error
}

func (e *sourceSessionError) Error() string { return e.err.Error() }
func (e *sourceSessionError) Unwrap() error { return e.err }

func sourceSessionErrorStage(err error) sourceSessionStage {
	var sessionErr *sourceSessionError
	if errors.As(err, &sessionErr) {
		return sessionErr.stage
	}
	return 0
}

type sourceSession struct {
	operationContext context.Context
	cancelOperation  context.CancelFunc
	workspace        *workspace.Workspace
	workspacePath    string
	acquisition      acquisition
	validated        packageResult
	source           cli.Source
}

func (s Service) openSource(ctx context.Context, timeout time.Duration, purpose workspace.Purpose, options cli.SourceOptions) (*sourceSession, error) {
	operationContext, cancelOperation := context.WithTimeout(ctx, timeout)
	session := &sourceSession{operationContext: operationContext, cancelOperation: cancelOperation}
	operationWorkspace, err := workspace.Create(s.config.TempRoot, purpose)
	if err != nil {
		return session, &sourceSessionError{stage: sourceSessionWorkspace, err: err}
	}
	session.workspace = operationWorkspace
	session.workspacePath = operationWorkspace.Path()
	session.acquisition, err = s.acquireOptions(operationContext, session.workspacePath, options)
	if err != nil {
		return session, &sourceSessionError{stage: sourceSessionAcquisition, err: err}
	}
	return session, nil
}

func (s Service) completeSource(session *sourceSession) error {
	validated, err := validatePackage(session.workspacePath, session.acquisition.inventory)
	if err != nil {
		return &sourceSessionError{stage: sourceSessionValidation, err: err}
	}
	session.validated = validated
	session.source, err = s.newCLISource(session.acquisition, validated.digest)
	if err != nil {
		return &sourceSessionError{stage: sourceSessionConstruction, err: err}
	}
	return nil
}

func (s *sourceSession) Close() error {
	if s == nil {
		return nil
	}
	var err error
	if s.workspace != nil {
		err = s.workspace.Close()
		if err == nil {
			s.workspace = nil
		}
	}
	if s.cancelOperation != nil {
		s.cancelOperation()
		s.cancelOperation = nil
	}
	return err
}
