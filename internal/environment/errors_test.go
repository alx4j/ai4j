package environment_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/environment"
)

func TestErrorCodesAreFixedUniqueAndDisclosureSafe(t *testing.T) {
	t.Parallel()

	codes := []environment.ErrorCode{
		environment.CodeInvalidOperatingSystem,
		environment.CodeInvalidArchitecture,
		environment.CodeInvalidDarwinVersion,
		environment.CodeInvalidHostTuple,
		environment.CodeInvalidTool,
		environment.CodeInvalidSemanticVersion,
		environment.CodeInvalidAppleGitRevision,
		environment.CodeInvalidToolVersion,
		environment.CodeInvalidExecutable,
		environment.CodeInvalidDirectoryRole,
		environment.CodeInvalidDirectorySource,
		environment.CodeInvalidDirectoryPresence,
		environment.CodeInvalidDirectory,
		environment.CodeInvalidProfileID,
		environment.CodeInvalidCapabilityFact,
		environment.CodeInvalidCapabilityProfile,
		environment.CodeInvalidPolicyObservation,
		environment.CodeInvalidFaultKind,
		environment.CodeInvalidFaultReason,
		environment.CodeInvalidEnvironmentFact,
		environment.CodeInvalidEnvironmentFault,
		environment.CodeInvalidObservation,
	}
	safe := regexp.MustCompile(`^environment\.[a-z_]+$`)
	seen := make(map[environment.ErrorCode]struct{}, len(codes))
	for _, code := range codes {
		if !code.Valid() || !safe.MatchString(string(code)) {
			t.Fatalf("unsafe or invalid code %q", code)
		}
		if _, duplicate := seen[code]; duplicate {
			t.Fatalf("duplicate code %q", code)
		}
		seen[code] = struct{}{}
	}
	if (environment.ErrorCode("")).Valid() || environment.ErrorCode("environment.future").Valid() {
		t.Fatal("unknown error code must be invalid")
	}
}

func TestValidationErrorsNeverRetainRejectedInputs(t *testing.T) {
	t.Parallel()

	invalidPath := "relative/" + pathCanary
	_, err := environment.NewDirectory(
		environment.ClaudeConfigurationDirectory(),
		environment.DefaultDirectorySource(),
		environment.PresentDirectory(),
		invalidPath,
	)
	requireCode(t, err, environment.CodeInvalidDirectory)
	formatted := fmt.Sprintf("%v|%+v|%#v|%q", err, err, err, err)
	encoded, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, output := range []string{err.Error(), formatted, string(encoded)} {
		if strings.Contains(output, pathCanary) || strings.Contains(output, invalidPath) || strings.Contains(output, testDigest) {
			t.Fatalf("validation error disclosure = %q", output)
		}
	}
	if string(encoded) != `{"code":"environment.invalid_directory"}` {
		t.Fatalf("MarshalJSON() = %s", encoded)
	}
}

func TestExecutableValidationErrorDoesNotExposePathOrDigest(t *testing.T) {
	t.Parallel()

	identity := validExecutable(t, environment.ClaudeTool())
	observation := identity.Observation()
	observation.ResolvedPath = "/Users/alex/" + pathCanary
	observation.Resource.ExecutableDigest = domain.ExecutableDigest{}
	_, err := environment.NewExecutableIdentity(identity.Tool(), identity.Version(), observation)
	requireCode(t, err, environment.CodeInvalidExecutable)
	formatted := fmt.Sprintf("%v|%+v|%#v|%q", err, err, err, err)
	encoded, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, output := range []string{formatted, string(encoded)} {
		if strings.Contains(output, pathCanary) || strings.Contains(output, testDigest) || strings.Contains(output, "/Users/alex") {
			t.Fatalf("executable validation error disclosure = %q", output)
		}
	}
}
