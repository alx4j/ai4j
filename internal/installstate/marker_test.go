package installstate

import (
	"errors"
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
	want, err := NewInstallMarker("operation-001", "install-aaaaaaaaaaaa", strings.Repeat("a", 40))
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
	if err := os.WriteFile(store.MarkerPath(), []byte(`{"schemaVersion":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadMarker(); !errors.Is(err, ErrUnsupportedMarkerSchema) {
		t.Fatalf("unsupported marker error = %v", err)
	}
	if err := os.WriteFile(store.MarkerPath(), []byte(`{"schemaVersion":1,"secret":"SECRET_CANARY"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadMarker(); !errors.Is(err, ErrMalformedMarker) || strings.Contains(err.Error(), "SECRET_CANARY") {
		t.Fatalf("malformed marker error = %v", err)
	}
	marker, err := NewInstallMarker("operation-002", "install-bbbbbbbbbbbb", strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarker(marker); !errors.Is(err, ErrMalformedMarker) {
		t.Fatalf("replacement marker error = %v", err)
	}
}

func TestOperationMarkerSupportsEachMVPModifyingCommand(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"install", "update", "uninstall"} {
		marker, err := NewOperationMarker(operation, "operation-001", "install-aaaaaaaaaaaa", strings.Repeat("a", 40))
		if err != nil || marker.Operation != operation {
			t.Fatalf("NewOperationMarker(%q) = %#v, %v", operation, marker, err)
		}
	}
	if _, err := NewOperationMarker("repair", "operation-001", "install-aaaaaaaaaaaa", strings.Repeat("a", 40)); !errors.Is(err, ErrMalformedMarker) {
		t.Fatalf("unsupported operation error = %v", err)
	}
}
