package darwin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

type fakeHostOperations struct {
	version       string
	versionErr    error
	environment   map[string]string
	versionCalls  int
	environmentAt []string
	afterVersion  func()
	afterLookup   func()
}

func (f *fakeHostOperations) ProductVersion() (string, error) {
	f.versionCalls++
	if f.afterVersion != nil {
		f.afterVersion()
	}
	return f.version, f.versionErr
}

func (f *fakeHostOperations) EnvironmentPresent(name string) bool {
	f.environmentAt = append(f.environmentAt, name)
	_, present := f.environment[name]
	if f.afterLookup != nil {
		f.afterLookup()
	}
	return present
}

func TestInspectorReturnsOnlyCanonicalTrustedHostFacts(t *testing.T) {
	for _, version := range []string{"15.0", "15.6.1", "4294967295.0"} {
		operations := &fakeHostOperations{version: version}
		value, err := newInspector(operations)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := value.InspectHost(context.Background(), lifecycle.HostInspectionRequest{Host: domain.DarwinHost()})
		if err != nil || observation.Host != domain.DarwinHost() || observation.OS != "darwin" ||
			observation.Arch != "arm64" || observation.OSVersion != version || operations.versionCalls != 1 {
			t.Fatalf("InspectHost(%q) = %#v, %v, calls=%d", version, observation, err, operations.versionCalls)
		}
	}
}

func TestInspectorRejectsInvalidVersionAndRequestWithoutDisclosure(t *testing.T) {
	for _, version := range []string{"", "0.1", "15", "15.01", "15.1.0.1", " 15.1", "15.1\n", "4294967296.1"} {
		operations := &fakeHostOperations{version: version}
		value, _ := newInspector(operations)
		_, err := value.InspectHost(context.Background(), lifecycle.HostInspectionRequest{Host: domain.DarwinHost()})
		if !errors.Is(err, errHostInspectionFailed) || strings.Contains(fmt.Sprint(err), version) && version != "" {
			t.Fatalf("invalid version %q error = %v", version, err)
		}
	}
	operations := &fakeHostOperations{version: "15.1"}
	value, _ := newInspector(operations)
	if _, err := value.InspectHost(context.Background(), lifecycle.HostInspectionRequest{}); !errors.Is(err, errInvalidHostInspection) || operations.versionCalls != 0 {
		t.Fatalf("invalid request error = %v, calls=%d", err, operations.versionCalls)
	}
	canary := "AI4J_PRODUCT_VERSION_ERROR_CANARY"
	operations.versionErr = errors.New(canary)
	if _, err := value.InspectHost(context.Background(), lifecycle.HostInspectionRequest{Host: domain.DarwinHost()}); !errors.Is(err, errHostInspectionFailed) || strings.Contains(fmt.Sprint(err), canary) {
		t.Fatalf("sysctl error disclosed source: %v", err)
	}
}

func TestInspectorHonorsContextBeforeAndAfterHostOperation(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	operations := &fakeHostOperations{version: "15.1"}
	value, _ := newInspector(operations)
	if _, err := value.InspectHost(cancelled, lifecycle.HostInspectionRequest{Host: domain.DarwinHost()}); !errors.Is(err, context.Canceled) || operations.versionCalls != 0 {
		t.Fatalf("pre-cancel error = %v, calls=%d", err, operations.versionCalls)
	}

	ctx, cancelDuring := context.WithCancel(context.Background())
	operations.afterVersion = cancelDuring
	if _, err := value.InspectHost(ctx, lifecycle.HostInspectionRequest{Host: domain.DarwinHost()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-call cancellation error = %v", err)
	}
}

func TestEnvironmentInspectionDiscardsValuesAndIsSorted(t *testing.T) {
	canary := "AI4J_ENVIRONMENT_VALUE_CANARY"
	operations := &fakeHostOperations{environment: map[string]string{"HOME": canary, "EMPTY": ""}}
	value, _ := newInspector(operations)
	request, _ := lifecycle.NewEnvironmentPresenceRequest([]string{"MISSING", "HOME", "EMPTY"})
	result, err := value.InspectEnvironment(context.Background(), request)
	if err != nil || !result.Coherent() {
		t.Fatalf("InspectEnvironment() = %#v, %v", result, err)
	}
	want := []lifecycle.EnvironmentPresence{
		{Name: "EMPTY", Present: true}, {Name: "HOME", Present: true}, {Name: "MISSING", Present: false},
	}
	if fmt.Sprint(result.Values()) != fmt.Sprint(want) || strings.Contains(fmt.Sprintf("%#v", result), canary) {
		t.Fatalf("presence result = %#v", result)
	}
	if fmt.Sprint(operations.environmentAt) != fmt.Sprint([]string{"EMPTY", "HOME", "MISSING"}) {
		t.Fatalf("lookup order = %v", operations.environmentAt)
	}
}

func TestEnvironmentInspectionHonorsPostLookupCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	operations := &fakeHostOperations{environment: map[string]string{"HOME": "secret"}, afterLookup: cancel}
	value, _ := newInspector(operations)
	request, _ := lifecycle.NewEnvironmentPresenceRequest([]string{"HOME"})
	if _, err := value.InspectEnvironment(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-lookup cancellation error = %v", err)
	}
}
