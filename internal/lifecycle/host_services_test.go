package lifecycle_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestEnvironmentPresenceContractsAreBoundedImmutableAndValueFree(t *testing.T) {
	t.Parallel()

	request, err := lifecycle.NewEnvironmentPresenceRequest([]string{"SSH_AUTH_SOCK", "HOME"})
	if err != nil || !request.Valid() || !reflect.DeepEqual(request.Names(), []string{"HOME", "SSH_AUTH_SOCK"}) {
		t.Fatalf("request = %+v, %v", request.Names(), err)
	}
	names := request.Names()
	names[0] = "MUTATED"
	if request.Names()[0] != "HOME" {
		t.Fatal("request accessor exposed mutable storage")
	}
	result, err := lifecycle.NewEnvironmentPresenceResult([]lifecycle.EnvironmentPresence{
		{Name: "SSH_AUTH_SOCK", Present: false}, {Name: "HOME", Present: true},
	})
	if err != nil || !result.Coherent() || !reflect.DeepEqual(result.Values(), []lifecycle.EnvironmentPresence{
		{Name: "HOME", Present: true}, {Name: "SSH_AUTH_SOCK", Present: false},
	}) {
		t.Fatalf("result = %+v, %v", result.Values(), err)
	}
	values := result.Values()
	values[0].Present = false
	if !result.Values()[0].Present {
		t.Fatal("result accessor exposed mutable storage")
	}
	for _, names := range [][]string{nil, {"HOME", "HOME"}, {"1INVALID"}, {"BAD=NAME"}} {
		if value, constructErr := lifecycle.NewEnvironmentPresenceRequest(names); constructErr == nil || value.Valid() {
			t.Fatalf("invalid names accepted: %q", names)
		}
	}
}

func TestHostResourcePolicyUsesDistinctClosedTimeoutTypes(t *testing.T) {
	t.Parallel()

	version, err := lifecycle.NewResourcePolicyVersion("mvp_resource_v1")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := lifecycle.NewHostResourcePolicy(version, 5*time.Minute, 2*time.Minute)
	if err != nil || !policy.Valid() || policy.Version().String() != "mvp_resource_v1" ||
		policy.GitTimeoutMaximum().Duration() != 5*time.Minute ||
		policy.ClaudeTimeoutMaximum().Duration() != 2*time.Minute {
		t.Fatalf("policy = %+v, %v", policy, err)
	}
	if reflect.TypeOf(policy.GitTimeoutMaximum()) == reflect.TypeOf(policy.ClaudeTimeoutMaximum()) {
		t.Fatal("Git and Claude timeout maxima have the same static type")
	}
	for _, test := range []struct {
		version string
		git     time.Duration
		claude  time.Duration
	}{
		{version: "MVP_RESOURCE_V1", git: time.Minute, claude: time.Minute},
		{version: "1_resource", git: time.Minute, claude: time.Minute},
		{version: "mvp_resource_v1", git: 0, claude: time.Minute},
		{version: "mvp_resource_v1", git: time.Minute, claude: time.Hour + 1},
	} {
		candidate, versionErr := lifecycle.NewResourcePolicyVersion(test.version)
		policy, policyErr := lifecycle.NewHostResourcePolicy(candidate, test.git, test.claude)
		if versionErr == nil && policyErr == nil || policy.Valid() {
			t.Fatalf("invalid policy accepted: %+v", test)
		}
	}
}

func TestRootDirectoryExpectationIsPrivateRoleOnly(t *testing.T) {
	t.Parallel()

	base := lifecycle.DirectoryExpectation{
		Root: lifecycle.StagingRoot, Path: ".",
		RootIdentity:   lifecycle.ObjectIdentity{Filesystem: 1, Object: 1},
		ParentIdentity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 2},
		Identity:       lifecycle.ObjectIdentity{Filesystem: 1, Object: 1},
	}
	if !base.Valid() {
		t.Fatal("private root-directory expectation rejected")
	}
	managed := base
	managed.Root = lifecycle.ManagedOutputRoot
	if managed.Valid() {
		t.Fatal("managed output accepted as constructor safe cwd")
	}
	mismatch := base
	mismatch.Identity.Object++
	if mismatch.Valid() {
		t.Fatal("root-directory expectation accepted a distinct leaf identity")
	}
}
