package environment_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/environment"
)

func TestObservationIsOrderIndependentAndComplete(t *testing.T) {
	t.Parallel()

	host := validHost(t)
	profile := validProfile(t)
	baseline := validObservation(t)
	for seed := int64(0); seed < 64; seed++ {
		executables := []environment.ExecutableIdentity{
			validExecutable(t, environment.GitTool()),
			validExecutable(t, environment.ClaudeTool()),
		}
		directories := validDirectories(t)
		random := rand.New(rand.NewSource(seed))
		random.Shuffle(len(executables), func(i, j int) { executables[i], executables[j] = executables[j], executables[i] })
		random.Shuffle(len(directories), func(i, j int) { directories[i], directories[j] = directories[j], directories[i] })
		got, err := environment.NewObservation(host, executables, directories, profile, environment.PolicyNotObservable())
		if err != nil || got != baseline {
			t.Fatalf("seed %d observation = %v, %v", seed, got, err)
		}
	}
	if !baseline.Valid() || baseline.Host() != host || baseline.Profile() != profile {
		t.Fatal("complete observation did not retain normalized facts")
	}
	if _, ok := baseline.Executable(environment.GitTool()); !ok {
		t.Fatal("Git executable missing")
	}
	if _, ok := baseline.Executable(environment.ClaudeTool()); !ok {
		t.Fatal("Claude executable missing")
	}
	if _, ok := baseline.Directory(environment.ClaudeRulesDirectory()); !ok {
		t.Fatal("Claude rules directory missing")
	}
}

func TestObservationCopiesConstructorAndAccessorSlices(t *testing.T) {
	t.Parallel()

	executables := []environment.ExecutableIdentity{validExecutable(t, environment.GitTool()), validExecutable(t, environment.ClaudeTool())}
	directories := validDirectories(t)
	observation, err := environment.NewObservation(validHost(t), executables, directories, validProfile(t), environment.PolicyNotObservable())
	if err != nil {
		t.Fatal(err)
	}
	executables[0] = environment.ExecutableIdentity{}
	directories[0] = environment.Directory{}
	if !observation.Valid() {
		t.Fatal("mutating constructor slices changed observation")
	}
	returnedExecutables := observation.Executables()
	returnedDirectories := observation.Directories()
	returnedExecutables[0] = environment.ExecutableIdentity{}
	returnedDirectories[0] = environment.Directory{}
	if !observation.Valid() || !observation.Executables()[0].Valid() || !observation.Directories()[0].Valid() {
		t.Fatal("mutating accessor slices changed observation")
	}
}

func TestObservationRejectsIncompleteDuplicateAndIncoherentFacts(t *testing.T) {
	t.Parallel()

	host := validHost(t)
	profile := validProfile(t)
	git := validExecutable(t, environment.GitTool())
	claude := validExecutable(t, environment.ClaudeTool())
	directories := validDirectories(t)

	tests := []struct {
		name        string
		executables []environment.ExecutableIdentity
		directories []environment.Directory
	}{
		{name: "missing executable", executables: []environment.ExecutableIdentity{git}, directories: directories},
		{name: "duplicate executable", executables: []environment.ExecutableIdentity{git, git}, directories: directories},
		{name: "zero executable", executables: []environment.ExecutableIdentity{git, environment.ExecutableIdentity{}}, directories: directories},
		{name: "missing directory", executables: []environment.ExecutableIdentity{git, claude}, directories: directories[:3]},
		{name: "duplicate directory role", executables: []environment.ExecutableIdentity{git, claude}, directories: []environment.Directory{directories[0], directories[0], directories[2], directories[3]}},
		{name: "zero directory", executables: []environment.ExecutableIdentity{git, claude}, directories: []environment.Directory{directories[0], directories[1], directories[2], environment.Directory{}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := environment.NewObservation(host, test.executables, test.directories, profile, environment.PolicyNotObservable())
			requireCode(t, err, environment.CodeInvalidObservation)
		})
	}

	overrideRules := mustDirectory(t, environment.ClaudeRulesDirectory(), environment.EnvironmentOverrideDirectorySource(), environment.PresentDirectory(), "/Users/alex/.claude/rules")
	wrongRules := mustDirectory(t, environment.ClaudeRulesDirectory(), environment.DefaultDirectorySource(), environment.PresentDirectory(), "/Users/alex/.claude/not-rules")
	absentConfig := mustDirectory(t, environment.ClaudeConfigurationDirectory(), environment.DefaultDirectorySource(), environment.AbsentDirectory(), "/Users/alex/.claude")
	duplicatePathRecovery := mustDirectory(t, environment.AI4JRecoveryDirectory(), environment.PrivateRuntimeDirectorySource(), environment.AbsentDirectory(), directories[2].AbsolutePath())
	for _, candidate := range [][]environment.Directory{
		{directories[0], overrideRules, directories[2], directories[3]},
		{directories[0], wrongRules, directories[2], directories[3]},
		{absentConfig, directories[1], directories[2], directories[3]},
		{directories[0], directories[1], directories[2], duplicatePathRecovery},
	} {
		_, err := environment.NewObservation(host, []environment.ExecutableIdentity{git, claude}, candidate, profile, environment.PolicyNotObservable())
		requireCode(t, err, environment.CodeInvalidObservation)
	}
	if (environment.Observation{}).Valid() {
		t.Fatal("zero observation must be invalid")
	}
}

func TestObservationFormattingAndJSONRedactAllHostBoundFacts(t *testing.T) {
	t.Parallel()

	observation := validObservation(t)
	formatted := fmt.Sprintf("%v|%+v|%#v|%q", observation, observation, observation, observation)
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{formatted, string(encoded)} {
		for _, forbidden := range []string{"/Users/alex", "/usr/bin/git", testDigest, pathCanary, ".claude/rules"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("observation disclosure %q in %q", forbidden, output)
			}
		}
	}
	if !strings.Contains(string(encoded), `"observation":"redacted"`) {
		t.Fatalf("MarshalJSON() = %s", encoded)
	}
}
