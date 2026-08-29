package installstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestStoreRoundTripsAndDeletesOperationMarker(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want, err := NewResourceMarker("install", "operation-001", "install-aaaaaaaaaaaa", strings.Repeat("a", 40), testMarkerResources())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarker(want); err != nil {
		t.Fatal(err)
	}
	got, present, err := store.LoadMarker()
	if err != nil || !present || !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadMarker() = %#v, %t, %v", got, present, err)
	}
	info, err := os.Stat(store.MarkerPath())
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("marker permissions = %v, %v", info.Mode(), err)
	}
	if err := store.DeleteMarker(); err != nil {
		t.Fatal(err)
	}
	if _, present, err := store.LoadMarker(); err != nil || present {
		t.Fatalf("deleted marker = present:%t err:%v", present, err)
	}
}

func TestStoreRejectsUnsupportedMalformedAndReplacementMarkers(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(store.MarkerPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.MarkerPath(), []byte(fmt.Sprintf(`{"schemaVersion":%d}`, MarkerSchemaVersion+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadMarker(); !errors.Is(err, ErrUnsupportedMarkerSchema) {
		t.Fatalf("unsupported marker error = %v", err)
	}
	if err := os.WriteFile(store.MarkerPath(), []byte(fmt.Sprintf(`{"schemaVersion":%d,"secret":"SECRET_CANARY"}`, MarkerSchemaVersion)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadMarker(); !errors.Is(err, ErrMalformedMarker) || strings.Contains(err.Error(), "SECRET_CANARY") {
		t.Fatalf("malformed marker error = %v", err)
	}
	marker, err := NewResourceMarker("install", "operation-002", "install-bbbbbbbbbbbb", strings.Repeat("b", 40), testMarkerResources())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarker(marker); !errors.Is(err, ErrMalformedMarker) {
		t.Fatalf("replacement marker error = %v", err)
	}
}

func TestStoreRoundTripsRecoverableHistoryPurgeMarker(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before := testRecord()
	before.History = []string{"operation-001", "operation-002"}
	desired := cloneRecord(before)
	desired.History = []string{"operation-002"}
	marker, err := NewHistoryPurgeMarker(
		"operation-purge", before.Source.Commit,
		[]string{"history:" + before.InstallationID, "owned:state/installation.json"},
		[]string{"operation-001"}, before, &desired,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarker(marker); err != nil {
		t.Fatal(err)
	}
	loaded, present, err := store.LoadMarker()
	if err != nil || !present || !reflect.DeepEqual(loaded, marker) {
		t.Fatalf("LoadMarker() = %#v, %t, %v", loaded, present, err)
	}
	changed := cloneRecord(desired)
	changed.Health = "drifted"
	if _, err := NewHistoryPurgeMarker(
		"operation-invalid-purge", before.Source.Commit,
		[]string{"history:" + before.InstallationID, "owned:state/installation.json"},
		[]string{"operation-001"}, before, &changed,
	); !errors.Is(err, ErrMalformedMarker) {
		t.Fatalf("mismatched desired record error = %v", err)
	}
}

func TestOperationMarkerSupportsEachModifyingCommand(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"install", "update", "uninstall"} {
		marker, err := NewResourceMarker(operation, "operation-001", "install-aaaaaaaaaaaa", strings.Repeat("a", 40), testMarkerResources())
		if err != nil || marker.Operation != operation {
			t.Fatalf("NewResourceMarker(%q) = %#v, %v", operation, marker, err)
		}
	}
	if _, err := NewResourceMarker("repair", "operation-001", "install-aaaaaaaaaaaa", strings.Repeat("a", 40), testMarkerResources()); !errors.Is(err, ErrMalformedMarker) {
		t.Fatalf("unsupported operation error = %v", err)
	}
}

func testMarkerResources() []string {
	return []string{
		"claude:ai4j-review@ai4j",
		"claude:ai4j-tools@ai4j",
		"claude:marketplace:ai4j",
		"owned:.claude/rules/ai4j.md",
		"owned:state/catalog/.claude-plugin/marketplace.json",
		"owned:state/installation.json",
	}
}

func TestOperationMarkerBoundsMaximumPackageSetTransitionResources(t *testing.T) {
	t.Parallel()
	resources := make([]string, maximumMarkerResources)
	for index := range resources {
		resources[index] = fmt.Sprintf("claude:package-%04d@marketplace", index)
	}
	marker, err := NewResourceMarker("sync", "operation-001", "install-aaaaaaaaaaaa", strings.Repeat("a", 40), resources)
	if err != nil || len(marker.Resources) != maximumMarkerResources {
		t.Fatalf("maximum marker = %#v, %v", marker, err)
	}
	resources = append(resources, "owned:state/installation.json")
	if _, err := NewResourceMarker("sync", "operation-002", "install-aaaaaaaaaaaa", strings.Repeat("a", 40), resources); !errors.Is(err, ErrMalformedMarker) {
		t.Fatalf("oversized marker error = %v", err)
	}
}
