//go:build darwin && arm64

package darwin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/host/darwin/resource"
)

func TestProductionBootstrapReturnsExactPolicyAndCreatesNothing(t *testing.T) {
	uid := os.Geteuid()
	account, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(account.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	if home != account.HomeDir {
		t.Fatalf("account home is not an exact no-symlink locator: %q", account.HomeDir)
	}
	protected := filepath.Join(home, fmt.Sprintf(".ai4j-bootstrap-zero-write-%d", os.Getpid()))
	if _, err := os.Lstat(protected); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("protected fixture must start absent: %v", err)
	}
	bootstrap, policy, err := NewBootstrap(t.Context(), BootstrapConfig{ProtectedBaseRoot: protected})
	if err != nil {
		t.Fatal(err)
	}
	if policy.Version().String() != string(resource.MVPPolicyVersion) ||
		policy.GitTimeoutMaximum().Duration() != 5*time.Minute ||
		policy.ClaudeTimeoutMaximum().Duration() != 2*time.Minute {
		t.Fatal("NewBootstrap returned a divergent resource policy")
	}
	if _, err := bootstrap.InspectUserHome(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(protected); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Bootstrap created protected root: %v", err)
	}
}

func TestBootstrapConfigFormattingIsRedacted(t *testing.T) {
	t.Parallel()

	config := BootstrapConfig{ProtectedBaseRoot: "/Users/bootstrap-config-secret-canary"}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	formatted := fmt.Sprintf("%v|%+v|%#v|%q|%s|%s", config, config, config, config, config, encoded)
	if strings.Contains(formatted, config.ProtectedBaseRoot) || strings.Contains(formatted, "bootstrap-config-secret-canary") {
		t.Fatal("BootstrapConfig disclosed its protected root")
	}
}
