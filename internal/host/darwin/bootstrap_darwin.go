//go:build darwin && arm64

package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/alx4j/ai4j/internal/host/darwin/filesystem"
	"github.com/alx4j/ai4j/internal/host/darwin/resource"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

var errBootstrapConstruction = errors.New("construct Darwin read-only bootstrap")

// BootstrapConfig names only the AI4J base-root candidate that read-only
// Claude directory qualification must reject as protected. Formatting never
// discloses it.
type BootstrapConfig struct{ ProtectedBaseRoot string }

func (BootstrapConfig) String() string {
	return "<darwin-read-only-bootstrap-config:redacted>"
}

func (BootstrapConfig) GoString() string {
	return "<darwin-read-only-bootstrap-config:redacted>"
}

func (c BootstrapConfig) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, c.String())
}

func (c BootstrapConfig) MarshalText() ([]byte, error) {
	return []byte(c.String()), nil
}

func (BootstrapConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"bootstrap_config": "redacted"})
}

// NewBootstrap constructs only zero-write user-directory authority and
// returns the exact production resource policy alongside it. Bootstrap does
// not implement HostResourcePolicyProvider.
func NewBootstrap(
	ctx context.Context,
	config BootstrapConfig,
) (_ *Bootstrap, _ lifecycle.HostResourcePolicy, resultErr error) {
	if ctx == nil {
		return nil, lifecycle.HostResourcePolicy{}, errInvalidBootstrap
	}
	if err := ctx.Err(); err != nil {
		return nil, lifecycle.HostResourcePolicy{}, err
	}
	policy, err := lifecyclePolicy(resource.MVPPolicy())
	if err != nil {
		return nil, lifecycle.HostResourcePolicy{}, errBootstrapConstruction
	}
	directories, err := constructUserDirectoryProofAuthority(ctx, func(bounded context.Context) (userDirectoryProofAuthority, error) {
		return filesystem.NewUserDirectoryAuthority(bounded, config.ProtectedBaseRoot)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, lifecycle.ErrDirectoryQualification) {
			return nil, lifecycle.HostResourcePolicy{}, err
		}
		return nil, lifecycle.HostResourcePolicy{}, errBootstrapConstruction
	}
	defer func() {
		if resultErr != nil {
			_ = directories.Close()
		}
	}()
	bootstrap, err := newBootstrap(directories)
	if err != nil {
		return nil, lifecycle.HostResourcePolicy{}, errBootstrapConstruction
	}
	return bootstrap, policy, nil
}
