package installstate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestStoreRejectsUnsupportedSchemaWithoutModifyingIt(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	store, err := NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	unsupported := []byte("{\"schemaVersion\":3}\n")
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), unsupported, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Snapshot(); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("unsupported Snapshot() error = %v", err)
	}
	before, _ := os.ReadFile(store.Path())
	if !bytes.Equal(before, unsupported) {
		t.Fatal("unsupported state changed after rejected read")
	}
	if err := store.Save(testRecord()); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Save() over unsupported state error = %v", err)
	}
	after, _ := os.ReadFile(store.Path())
	if !bytes.Equal(after, before) {
		t.Fatal("unsupported state changed after rejected write")
	}
}

func TestNewStoreAtUsesAnAbsoluteCleanDataRoot(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	store, err := NewStoreAt(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dataRoot, "state"); store.Root() != want {
		t.Fatalf("Root() = %q, want %q", store.Root(), want)
	}
	if _, err := NewStoreAt("relative"); err == nil {
		t.Fatal("relative data root was accepted")
	}
	if _, err := NewStoreAt(dataRoot + string(os.PathSeparator) + "."); err == nil {
		t.Fatal("unclean data root was accepted")
	}
}

func TestStoreKeepsMultipleInstallationsIndependentAndOrdered(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second := testRecord()
	second.InstallationID = "installation-002"
	second.ToolkitID = "zeta-toolkit"
	second.Packages = []NativePackage{{ID: "zeta-plugin", Path: "plugins/zeta-plugin"}}
	second.Target = "codex"
	second.Host = "windows-amd64"
	second.Scope = "project-local"
	second.ScopeRoot = filepath.Join(second.ScopeRoot, "project")
	second.NativeResources = []string{"codex:plugin:zeta-plugin"}
	second.Catalog = OwnedFile{}
	second.Rules = OwnedFile{}
	if err := store.SaveNew(second); err != nil {
		t.Fatal(err)
	}
	first := testRecord()
	if err := store.SaveNew(first); err != nil {
		t.Fatal(err)
	}
	records, err := store.LoadAll()
	if err != nil || len(records) != 2 || records[0].InstallationID != "installation-001" || records[1].InstallationID != "installation-002" {
		t.Fatalf("LoadAll() = %#v, %v", records, err)
	}
	if _, _, err := store.Load(); !errors.Is(err, ErrInstallationSelectionRequired) {
		t.Fatalf("ambiguous Load() error = %v", err)
	}
	loaded, present, err := store.LoadByID("installation-002")
	if err != nil || !present || loaded.ToolkitID != "zeta-toolkit" {
		t.Fatalf("LoadByID() = %#v, %t, %v", loaded, present, err)
	}
	conflict := first
	conflict.InstallationID = "installation-003"
	conflict.Lifecycle = "archived"
	conflict.NativeResources = nil
	if err := store.SaveNew(conflict); !errors.Is(err, ErrStateOccupied) {
		t.Fatalf("logical identity conflict = %v", err)
	}
}

func TestStoreRoundTripsOneAtomicInstallationRecord(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := testRecord()
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, present, err := store.Load()
	if err != nil || !present || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %#v, %t, %v", got, present, err)
	}
	info, err := os.Stat(store.Path())
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Fatalf("state permissions = %v, %v", info.Mode(), err)
	}
	entries, err := os.ReadDir(filepath.Dir(store.Path()))
	if err != nil || len(entries) != 1 || entries[0].Name() != "installation.json" {
		t.Fatalf("state directory = %v, %v", entries, err)
	}
}

func TestStoreCanonicalizesFlattenedSelectionAndNativePackages(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := testRecord()
	record.Selection.ResolvedBundles = []string{"nested", "default"}
	record.Selection.ResolvedAssets = []string{"zeta-asset", "alpha-asset"}
	record.Packages = []NativePackage{
		{ID: "zeta-plugin", Path: "plugins/zeta-plugin"},
		{ID: "alpha-plugin", Path: "plugins/alpha-plugin"},
	}
	record.NativeResources = []string{
		"claude:zeta-plugin@ai4j",
		"claude:marketplace:ai4j",
		"claude:alpha-plugin@ai4j",
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	got, present, err := store.Load()
	if err != nil || !present {
		t.Fatalf("Load() = %#v, %t, %v", got, present, err)
	}
	if got.Packages[0].ID != "alpha-plugin" || got.Packages[1].ID != "zeta-plugin" ||
		got.Selection.ResolvedBundles[0] != "default" || got.Selection.ResolvedAssets[0] != "alpha-asset" ||
		got.NativeResources[0] != "claude:alpha-plugin@ai4j" {
		t.Fatalf("canonical record = %#v", got)
	}
}

func TestStoreRoundTripsCanonicalMultiSourceComposition(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := compositionRecord()
	record.Selection.ResolvedAssets = []string{"everpure-tools", "everpure-rules", "common-tools", "common-rules"}
	record.Components[0], record.Components[1] = record.Components[1], record.Components[0]
	record.Components[0].Selection.ResolvedAssets[0], record.Components[0].Selection.ResolvedAssets[1] = record.Components[0].Selection.ResolvedAssets[1], record.Components[0].Selection.ResolvedAssets[0]
	record.Packages[0], record.Packages[1] = record.Packages[1], record.Packages[0]

	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if record.Components[0].Name != "everpure" || record.Packages[0].ID != "everpure-tools" || record.Selection.ResolvedAssets[0] != "everpure-tools" {
		t.Fatalf("Save() mutated caller-owned slices: %#v", record)
	}
	got, present, err := store.Load()
	if err != nil || !present {
		t.Fatalf("Load() = %#v, %t, %v", got, present, err)
	}
	if got.ToolkitID != "composition" || got.Source != (Source{}) || len(got.Components) != 2 ||
		got.Components[0].Name != "common" || got.Components[1].Name != "everpure" ||
		got.Components[1].Source.Repository != "git.everpure.example/platform/toolkits/everpure" ||
		got.Components[1].Source.RequestedRef == nil || *got.Components[1].Source.RequestedRef != "refs/tags/v0.6.0" ||
		!reflect.DeepEqual(got.Components[1].Packages, []string{"everpure-tools"}) ||
		!reflect.DeepEqual(got.Selection.ResolvedAssets, []string{"common-rules", "common-tools", "everpure-rules", "everpure-tools"}) ||
		got.Packages[0].ID != "common-tools" || got.Packages[0].Component != "common" {
		t.Fatalf("canonical composition = %#v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("round-tripped composition is invalid: %v", err)
	}
}

func TestRecordAcceptsThreeComponentComposition(t *testing.T) {
	t.Parallel()
	record := compositionRecord()
	record.Components = append(record.Components,
		compositionComponent("team", "git.everpure.example/platform/toolkits/team", "ssh", "v0.2.0", "0.2.0", "1", "2", []string{"team"}, []string{"team-rules", "team-tools"}, []string{"team-tools"}),
	)
	record.Packages = append(record.Packages, NativePackage{ID: "team-tools", Path: "plugins/team-tools", Component: "team"})
	record.Selection.ResolvedAssets = append(record.Selection.ResolvedAssets, "team-rules", "team-tools")
	record.NativeResources = []string{
		"claude:common-tools@composition",
		"claude:everpure-tools@composition",
		"claude:marketplace:composition",
		"claude:team-tools@composition",
	}

	if err := record.Validate(); err != nil {
		t.Fatalf("three-component composition = %v", err)
	}
}

func TestRecordRejectsMalformedMultiSourceComposition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "one component", mutate: func(record *Record) {
			record.Components = record.Components[:1]
			record.Packages = record.Packages[:1]
			record.Selection.ResolvedAssets = []string{"common-rules", "common-tools"}
			record.NativeResources = []string{"claude:common-tools@composition", "claude:marketplace:composition"}
		}},
		{name: "four components", mutate: func(record *Record) {
			extra := record.Components[0]
			extra.Name = "personal"
			extra.Source.Repository = "github.com/oleksii/personal"
			extra.Selection.RequestedBundle = "personal"
			extra.Selection.ResolvedBundles = []string{"personal"}
			extra.Selection.ResolvedAssets = nil
			extra.Packages = nil
			team := extra
			team.Name = "team"
			team.Source.Repository = "github.com/oleksii/team"
			team.Selection.RequestedBundle = "team"
			team.Selection.ResolvedBundles = []string{"team"}
			record.Components = append(record.Components, extra, team)
		}},
		{name: "components are not sorted", mutate: func(record *Record) {
			record.Components[0], record.Components[1] = record.Components[1], record.Components[0]
		}},
		{name: "duplicate component", mutate: func(record *Record) {
			record.Components[1] = record.Components[0]
		}},
		{name: "record identity is not synthetic", mutate: func(record *Record) {
			record.ToolkitID = "common"
		}},
		{name: "record source is populated", mutate: func(record *Record) {
			record.Source = record.Components[0].Source
		}},
		{name: "record selection is not synthetic", mutate: func(record *Record) {
			record.Selection.ResolvedBundles = []string{"common", "composition"}
		}},
		{name: "component source is not git", mutate: func(record *Record) {
			record.Components[0].Source.Mode = "github"
		}},
		{name: "component source is not explicit", mutate: func(record *Record) {
			record.Components[0].Source.Selection = "built_in_default"
		}},
		{name: "component source is not tag pinned", mutate: func(record *Record) {
			record.Components[0].Source.RefKind = "branch"
		}},
		{name: "component requested ref differs from tag", mutate: func(record *Record) {
			requested := "refs/tags/v2.0.0"
			record.Components[0].Source.RequestedRef = &requested
		}},
		{name: "component tag is qualified", mutate: func(record *Record) {
			record.Components[0].Tag = "refs/tags/v1.3.0"
			requested := "refs/tags/refs/tags/v1.3.0"
			record.Components[0].Source.RequestedRef = &requested
		}},
		{name: "component transport is absent", mutate: func(record *Record) {
			record.Components[0].Source.Transport = ""
		}},
		{name: "components use different root namespaces", mutate: func(record *Record) {
			record.Components[1].Source.Repository = "git.other.example/platform/toolkits/everpure"
		}},
		{name: "components use different transports", mutate: func(record *Record) {
			record.Components[1].Source.Transport = "https"
		}},
		{name: "component commit is absent", mutate: func(record *Record) {
			record.Components[0].Source.Commit = ""
		}},
		{name: "component digest is absent", mutate: func(record *Record) {
			record.Components[0].Source.RenderedDigest = ""
		}},
		{name: "component version is absent", mutate: func(record *Record) {
			record.Components[0].ToolkitVersion = ""
		}},
		{name: "component selects a different bundle", mutate: func(record *Record) {
			record.Components[0].Selection.RequestedBundle = "shared"
		}},
		{name: "component repository differs from name", mutate: func(record *Record) {
			record.Components[0].Source.Repository = "github.com/oleksii/other"
		}},
		{name: "asset union is incomplete", mutate: func(record *Record) {
			record.Selection.ResolvedAssets = record.Selection.ResolvedAssets[:3]
		}},
		{name: "package owner is absent", mutate: func(record *Record) {
			record.Packages[0].Component = ""
		}},
		{name: "package owner is wrong", mutate: func(record *Record) {
			record.Packages[0].Component = "everpure"
		}},
		{name: "component package union is incomplete", mutate: func(record *Record) {
			record.Components[0].Packages = nil
		}},
		{name: "package belongs to two components", mutate: func(record *Record) {
			record.Components[1].Packages = []string{"common-tools", "everpure-tools"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := compositionRecord()
			test.mutate(&record)
			if err := record.Validate(); !errors.Is(err, ErrMalformedState) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestSaveNewNeverReplacesAnOccupiedInstallationRecord(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveNew(testRecord()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	replacement := testRecord()
	replacement.AI4JVersion = "v9.9.9"
	if err := store.SaveNew(replacement); !errors.Is(err, ErrStateOccupied) {
		t.Fatalf("second SaveNew() error = %v", err)
	}
	after, err := os.ReadFile(store.Path())
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("occupied state changed: err=%v", err)
	}
}

func TestDeleteRemovesOnlyTheExpectedInstallationRecord(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	expected := testRecord()
	if err := store.Save(expected); err != nil {
		t.Fatal(err)
	}
	changed := expected
	changed.LastOperation.ID = "operation-002"
	if err := store.Save(changed); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(expected); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("Delete(changed state) error = %v", err)
	}
	if _, present, err := store.Load(); err != nil || !present {
		t.Fatalf("changed state retained = present:%t err:%v", present, err)
	}
	if err := store.Delete(changed); err != nil {
		t.Fatal(err)
	}
	if _, present, err := store.Load(); err != nil || present {
		t.Fatalf("deleted state = present:%t err:%v", present, err)
	}
	if err := store.Delete(changed); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("Delete(absent state) error = %v", err)
	}
}

func TestStoreDistinguishesAbsentUnsupportedAndMalformedState(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := store.Load(); err != nil || present {
		t.Fatalf("absent Load() = present:%t err:%v", present, err)
	}
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte(`{"schemaVersion":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("unsupported schema error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte(`{"schemaVersion":1,"secret":"SECRET_CANARY"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); !errors.Is(err, ErrMalformedState) || strings.Contains(err.Error(), "SECRET_CANARY") {
		t.Fatalf("malformed state error = %v", err)
	}
}

func TestRecordRejectsContradictorySourceMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "built-in repository changed", mutate: func(record *Record) {
			record.Source.Selection = "built_in_default"
			record.Source.Repository = "github.com/example/other"
		}},
		{name: "default branch has requested ref", mutate: func(record *Record) {
			record.Source.RefKind = "default_branch"
		}},
		{name: "tracked branch omits requested ref", mutate: func(record *Record) {
			record.Source.RequestedRef = nil
		}},
		{name: "commit ref differs from commit", mutate: func(record *Record) {
			requested := strings.Repeat("d", 40)
			record.Source.RefKind = "commit"
			record.Source.RequestedRef = &requested
		}},
		{name: "unsupported source transport", mutate: func(record *Record) {
			record.Source.Transport = "ftp"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := testRecord()
			test.mutate(&record)
			if err := record.Validate(); !errors.Is(err, ErrMalformedState) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestRecordAcceptsEnterpriseGitSourceAndLegacyGitHubState(t *testing.T) {
	t.Parallel()

	enterprise := testRecord()
	enterprise.Source.Mode = "git"
	enterprise.Source.Repository = "gitlab.barclays.example/division/team/toolkit"
	enterprise.Source.Transport = "ssh"
	if err := enterprise.Validate(); err != nil {
		t.Fatalf("enterprise Git state = %v", err)
	}

	missingTransport := enterprise
	missingTransport.Source.Transport = ""
	if err := missingTransport.Validate(); !errors.Is(err, ErrMalformedState) {
		t.Fatalf("missing transport = %v", err)
	}

	legacy := testRecord()
	legacy.Source.Mode = "github"
	legacy.Source.Transport = ""
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy GitHub state = %v", err)
	}

	legacyCustomHost := legacy
	legacyCustomHost.Source.Repository = "gitlab.barclays.example/division/team/toolkit"
	if err := legacyCustomHost.Validate(); !errors.Is(err, ErrMalformedState) {
		t.Fatalf("legacy custom-host state = %v", err)
	}

	contradictoryBuiltIn := testRecord()
	contradictoryBuiltIn.Source.Mode = "git"
	contradictoryBuiltIn.Source.Selection = "built_in_default"
	contradictoryBuiltIn.Source.Transport = "ssh"
	if err := contradictoryBuiltIn.Validate(); !errors.Is(err, ErrMalformedState) {
		t.Fatalf("built-in SSH transport = %v", err)
	}
}

func TestRecordRejectsNoncanonicalPackageAndSelectionState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "requested bundle is not resolved", mutate: func(record *Record) {
			record.Selection.ResolvedBundles = []string{"nested"}
		}},
		{name: "resolved bundles are not sorted", mutate: func(record *Record) {
			record.Selection.ResolvedBundles = []string{"nested", "default"}
		}},
		{name: "resolved assets are duplicated", mutate: func(record *Record) {
			record.Selection.ResolvedAssets = []string{"ai4j-rules", "ai4j-rules"}
		}},
		{name: "native package set is empty", mutate: func(record *Record) {
			record.Packages = nil
		}},
		{name: "native packages are not sorted", mutate: func(record *Record) {
			record.Packages = []NativePackage{{ID: "zeta-plugin", Path: "plugins/zeta"}, {ID: "alpha-plugin", Path: "plugins/alpha"}}
		}},
		{name: "native package path escapes", mutate: func(record *Record) {
			record.Packages[0].Path = "../plugin"
		}},
		{name: "legacy native package names a component", mutate: func(record *Record) {
			record.Packages[0].Component = "common"
		}},
		{name: "active native resources omit a package", mutate: func(record *Record) {
			record.NativeResources = []string{"claude:marketplace:ai4j"}
		}},
		{name: "archived record retains active native resources", mutate: func(record *Record) {
			record.Lifecycle = "archived"
			record.Catalog = OwnedFile{}
			record.Rules = OwnedFile{}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := testRecord()
			test.mutate(&record)
			if err := record.Validate(); !errors.Is(err, ErrMalformedState) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestStateCommitRejectsAChangedPreimage(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("original\n")
	changed := []byte("same-user change\n")
	if err := os.WriteFile(store.Path(), original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), changed, 0o600); err != nil {
		t.Fatal(err)
	}
	err = store.writeState([]byte("replacement\n"), stateExpectation{contents: original, present: true})
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("writeState() error = %v", err)
	}
	if contents, readErr := os.ReadFile(store.Path()); readErr != nil || !bytes.Equal(contents, changed) {
		t.Fatalf("changed state was overwritten: %q, %v", contents, readErr)
	}
}

func testRecord() Record {
	requested := "main"
	scopeRoot, _ := filepath.Abs(".")
	return Record{
		SchemaVersion: SchemaVersion, InstallationID: "installation-001", ToolkitID: "ai4j",
		Packages: []NativePackage{{ID: "ai4j-default", Path: "plugins/ai4j-default"}}, MarketplaceID: "ai4j",
		Source: Source{Mode: "github", Selection: "explicit", Repository: "github.com/alx4j/ai4j", RequestedRef: &requested, RefKind: "branch", Commit: strings.Repeat("a", 40), RenderedDigest: strings.Repeat("d", 64)},
		Target: "claude", Host: "darwin-arm64", Scope: "user", ScopeRoot: scopeRoot, Lifecycle: "active",
		Selection:       Selection{RequestedBundle: "default", ResolvedBundles: []string{"default"}, ResolvedAssets: []string{"ai4j-rules"}},
		NativeResources: []string{"claude:ai4j-default@ai4j", "claude:marketplace:ai4j"}, Health: "healthy", AI4JVersion: "0.0.0-dev",
		Catalog:       OwnedFile{Path: "state/catalog/.claude-plugin/marketplace.json", Checksum: strings.Repeat("b", 64)},
		Rules:         OwnedFile{Path: ".claude/rules/ai4j.md", Checksum: strings.Repeat("c", 64)},
		LastOperation: LastOperation{ID: "operation-001", Timestamp: "2026-08-24T12:00:00Z"},
	}
}

func compositionRecord() Record {
	record := testRecord()
	record.ToolkitID = "composition"
	record.ToolkitVersion = ""
	record.Packages = []NativePackage{
		{ID: "common-tools", Path: "plugins/common-tools", Component: "common"},
		{ID: "everpure-tools", Path: "plugins/everpure-tools", Component: "everpure"},
	}
	record.MarketplaceID = "composition"
	record.Source = Source{}
	record.Selection = Selection{
		RequestedBundle: "composition",
		ResolvedBundles: []string{"composition"},
		ResolvedAssets:  []string{"common-rules", "common-tools", "everpure-rules", "everpure-tools"},
	}
	record.Components = []Component{
		compositionComponent("common", "git.everpure.example/platform/toolkits/common", "ssh", "v1.3.0", "1.3.0", "a", "d", []string{"common", "shared"}, []string{"common-rules", "common-tools"}, []string{"common-tools"}),
		compositionComponent("everpure", "git.everpure.example/platform/toolkits/everpure", "ssh", "v0.6.0", "0.6.0", "e", "f", []string{"everpure"}, []string{"everpure-rules", "everpure-tools"}, []string{"everpure-tools"}),
	}
	record.NativeResources = []string{
		"claude:common-tools@composition",
		"claude:everpure-tools@composition",
		"claude:marketplace:composition",
	}
	return record
}

func compositionComponent(name, repository, transport, tag, toolkitVersion, commitCharacter, digestCharacter string, bundles, assets, packages []string) Component {
	requested := "refs/tags/" + tag
	return Component{
		Name: name,
		Tag:  tag,
		Source: Source{
			Mode: "git", Selection: "explicit", Repository: repository, Transport: transport,
			RequestedRef: &requested, RefKind: "tag", Commit: strings.Repeat(commitCharacter, 40), RenderedDigest: strings.Repeat(digestCharacter, 64),
		},
		ToolkitVersion: toolkitVersion,
		Selection:      Selection{RequestedBundle: name, ResolvedBundles: bundles, ResolvedAssets: assets},
		Packages:       packages,
	}
}
