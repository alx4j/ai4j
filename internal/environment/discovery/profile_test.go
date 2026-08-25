package discovery_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/environment/discovery"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestMVPProbeProfileBindsExactPolicyAndEnvironmentProfiles(t *testing.T) {
	t.Parallel()

	profile := validProbeProfile(t)
	if !profile.Valid() || profile.PolicyVersion().String() != "mvp_resource_v1" {
		t.Fatalf("profile = %v", profile)
	}
	if profile.GitTimeoutMaximum().Duration() != 5*time.Minute ||
		profile.ClaudeTimeoutMaximum().Duration() != 2*time.Minute {
		t.Fatal("profile did not retain exact separately typed host maxima")
	}
	if profile.GitEnvironmentProfile().String() != "isolated" ||
		profile.ClaudeEnvironmentProfile().String() != "claude_probe_v1" {
		t.Fatal("unexpected process environment profile")
	}
	gitEnvironment := profile.GitEnvironment()
	if gitEnvironment == nil || len(gitEnvironment) != 0 {
		t.Fatalf("Git environment = %#v", gitEnvironment)
	}
	wantClaude := []lifecycle.EnvironmentBinding{
		{Name: "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", Value: "1"},
		{Name: "CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL", Value: "1"},
		{Name: "DISABLE_UPDATES", Value: "1"},
	}
	if got := profile.ClaudeEnvironment(); !reflect.DeepEqual(got, wantClaude) {
		t.Fatalf("Claude environment = %#v, want %#v", got, wantClaude)
	}
	mutated := profile.ClaudeEnvironment()
	mutated[0].Value = pathCanary
	if got := profile.ClaudeEnvironment(); !reflect.DeepEqual(got, wantClaude) {
		t.Fatal("Claude environment accessor did not return a copy")
	}
	if text := fmt.Sprintf("%v|%+v|%#v", profile, profile, profile); text !=
		"<environment-probe-profile:mvp_resource_v1>|<environment-probe-profile:mvp_resource_v1>|<environment-probe-profile:mvp_resource_v1>" {
		t.Fatalf("profile formatting = %q", text)
	}
}

func TestMVPProbeProfileRejectsZeroInvalidAndWrongVersionPolicy(t *testing.T) {
	t.Parallel()

	if (discovery.ProbeProfile{}).Valid() {
		t.Fatal("zero profile must be invalid")
	}
	if _, err := discovery.NewMVPProbeProfile(lifecycle.HostResourcePolicy{}); err == nil {
		t.Fatal("zero policy accepted")
	}
	version, err := lifecycle.NewResourcePolicyVersion("other_resource_v1")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := lifecycle.NewHostResourcePolicy(version, time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := discovery.NewMVPProbeProfile(policy); err == nil {
		t.Fatal("wrong policy version accepted")
	}
	mvpVersion, err := lifecycle.NewResourcePolicyVersion("mvp_resource_v1")
	if err != nil {
		t.Fatal(err)
	}
	for name, limits := range map[string][2]time.Duration{
		"mislabeled Git maximum":    {time.Minute, 2 * time.Minute},
		"mislabeled Claude maximum": {5 * time.Minute, time.Minute},
	} {
		name, limits := name, limits
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mislabeled, policyErr := lifecycle.NewHostResourcePolicy(mvpVersion, limits[0], limits[1])
			if policyErr != nil {
				t.Fatal(policyErr)
			}
			if _, profileErr := discovery.NewMVPProbeProfile(mislabeled); profileErr == nil {
				t.Fatal("mislabeled mvp_resource_v1 policy accepted")
			}
		})
	}
}

func TestProbeTimeoutTypesRemainDistinct(t *testing.T) {
	t.Parallel()

	git := reflect.TypeOf(validProbeProfile(t).GitTimeoutMaximum())
	claude := reflect.TypeOf(validProbeProfile(t).ClaudeTimeoutMaximum())
	if git == claude || git.AssignableTo(claude) || claude.AssignableTo(git) {
		t.Fatal("Git and Claude timeout maxima crossed their typed boundary")
	}
}
