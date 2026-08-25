package discovery_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/environment/discovery"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestPrerequisiteObservationIsImmutableAndRedactsHostProof(t *testing.T) {
	t.Parallel()

	service, _, _, _, _ := newFixture(t)
	observation, err := service.DiscoverPrerequisites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := observation.Executable(environment.Tool{}); ok {
		t.Fatal("unknown tool returned an executable")
	}
	formatted := fmt.Sprintf("%v|%+v|%#v|%q", observation, observation, observation, observation)
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"/usr/bin/git", ".local/share/claude", gitDigest, claudeDigest} {
		if strings.Contains(formatted, canary) || strings.Contains(string(encoded), canary) {
			t.Fatalf("observation disclosed %q: %s / %s", canary, formatted, encoded)
		}
	}
	if !strings.Contains(string(encoded), `"identity":"redacted"`) ||
		!strings.Contains(string(encoded), `"version":"2.39.5 (Apple Git-154.3)"`) {
		t.Fatalf("observation JSON = %s", encoded)
	}
	if (discovery.PrerequisiteObservation{}).Valid() {
		t.Fatal("zero observation must be invalid")
	}
}

func TestPrerequisiteObservationRejectsAliasedTools(t *testing.T) {
	t.Parallel()

	gitObservation := executableObservation(t, environment.GitTool(), nativeProfile(t, lifecycle.NativeSingleImage, lifecycle.ExecutableARM64), 100)
	claudeObservation := executableObservation(t, environment.ClaudeTool(), nativeProfile(t, lifecycle.NativeSingleImage, lifecycle.ExecutableARM64), 200)
	git := mustExecutableIdentity(t, environment.GitTool(), gitObservation)
	claude := mustExecutableIdentity(t, environment.ClaudeTool(), claudeObservation)
	version, err := environment.NewDarwinVersion("15.6.1")
	if err != nil {
		t.Fatal(err)
	}
	host, err := environment.NewHostTuple(domain.DarwinHost(), environment.DarwinOperatingSystem(), environment.ARM64Architecture(), version)
	if err != nil {
		t.Fatal(err)
	}
	claudeObservation.Resource.Identity = gitObservation.Resource.Identity
	claude = mustExecutableIdentity(t, environment.ClaudeTool(), claudeObservation)
	if _, err := discovery.NewPrerequisiteObservation(host, git, claude); err == nil {
		t.Fatal("same executable object accepted for both tools")
	}
}
