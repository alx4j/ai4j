package discovery_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/environment/discovery"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestDiscoveryErrorSchemaIsClosedAndSafe(t *testing.T) {
	t.Parallel()

	service, host, _, _, _ := newFixture(t)
	host.response.err = errors.New(pathCanary)
	_, err := service.DiscoverPrerequisites(context.Background())
	requireDiscoveryCode(t, err, discovery.CodeHostInspectionFailed)
	encoded, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	want := `{"code":"environment.discovery.host_inspection_failed"}`
	if string(encoded) != want {
		t.Fatalf("MarshalJSON() = %s, want %s", encoded, want)
	}
	formatted := fmt.Sprintf("%v|%+v|%#v|%q", err, err, err, err)
	if strings.Contains(formatted, pathCanary) || strings.Contains(string(encoded), pathCanary) {
		t.Fatalf("error disclosed canary: %s / %s", formatted, encoded)
	}
}

func TestDiscoveryErrorCodesRejectZeroAndPreserveCategories(t *testing.T) {
	t.Parallel()

	if (discovery.ErrorCode("")).Valid() {
		t.Fatal("zero code must be invalid")
	}
	service, _, _, runner, _ := newFixture(t)
	runner.responses[0] = result[lifecycle.ProcessResult]{value: lifecycle.ProcessResult{Started: true, TimedOut: true}, err: context.DeadlineExceeded}
	_, err := service.DiscoverPrerequisites(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("timeout categories = %v", err)
	}
}
