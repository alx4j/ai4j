// Package darwin composes the supported macOS/ARM64 host adapter.
package darwin

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

var (
	errInvalidHostInspection = errors.New("invalid Darwin host inspection request")
	errHostInspectionFailed  = errors.New("Darwin host inspection failed")
)

type hostOperations interface {
	ProductVersion() (string, error)
	EnvironmentPresent(string) bool
}

type inspector struct{ operations hostOperations }

var (
	_ lifecycle.HostInspector        = (*inspector)(nil)
	_ lifecycle.EnvironmentInspector = (*inspector)(nil)
)

func newInspector(operations hostOperations) (*inspector, error) {
	if operations == nil {
		return nil, errInvalidHostInspection
	}
	return &inspector{operations: operations}, nil
}

func (i *inspector) InspectHost(
	ctx context.Context,
	request lifecycle.HostInspectionRequest,
) (lifecycle.HostObservation, error) {
	if i == nil || i.operations == nil || ctx == nil || request.Host != domain.DarwinHost() {
		return lifecycle.HostObservation{}, errInvalidHostInspection
	}
	if err := ctx.Err(); err != nil {
		return lifecycle.HostObservation{}, err
	}
	text, err := i.operations.ProductVersion()
	if err != nil {
		return lifecycle.HostObservation{}, errHostInspectionFailed
	}
	if err := ctx.Err(); err != nil {
		return lifecycle.HostObservation{}, err
	}
	version, ok := canonicalDarwinProductVersion(text)
	if !ok {
		return lifecycle.HostObservation{}, errHostInspectionFailed
	}
	return lifecycle.HostObservation{
		Host:      domain.DarwinHost(),
		OS:        "darwin",
		Arch:      "arm64",
		OSVersion: version,
	}, nil
}

func (i *inspector) InspectEnvironment(
	ctx context.Context,
	request lifecycle.EnvironmentPresenceRequest,
) (lifecycle.EnvironmentPresenceResult, error) {
	if i == nil || i.operations == nil || ctx == nil || !request.Valid() {
		return lifecycle.EnvironmentPresenceResult{}, errInvalidHostInspection
	}
	values := make([]lifecycle.EnvironmentPresence, 0, len(request.Names()))
	for _, name := range request.Names() {
		if err := ctx.Err(); err != nil {
			return lifecycle.EnvironmentPresenceResult{}, err
		}
		present := i.operations.EnvironmentPresent(name)
		if err := ctx.Err(); err != nil {
			return lifecycle.EnvironmentPresenceResult{}, err
		}
		values = append(values, lifecycle.EnvironmentPresence{Name: name, Present: present})
	}
	result, err := lifecycle.NewEnvironmentPresenceResult(values)
	if err != nil {
		return lifecycle.EnvironmentPresenceResult{}, errHostInspectionFailed
	}
	return result, nil
}

func canonicalDarwinProductVersion(value string) (string, bool) {
	if value == "" || len(value) > 128 {
		return "", false
	}
	parts := strings.Split(value, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return "", false
	}
	for index, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return "", false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return "", false
			}
		}
		parsed, err := strconv.ParseUint(part, 10, 32)
		if err != nil || index == 0 && parsed == 0 {
			return "", false
		}
	}
	return value, true
}
