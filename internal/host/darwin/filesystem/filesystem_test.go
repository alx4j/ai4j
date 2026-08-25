//go:build darwin && arm64

package filesystem

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/fault"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"golang.org/x/sys/unix"
)

func assertFIFOHasNoReader(t *testing.T, name string) {
	t.Helper()
	fd, err := syscall.Open(name, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err == nil {
		_ = syscall.Close(fd)
		t.Fatal("FIFO unexpectedly had a reader")
	}
	if !errors.Is(err, syscall.ENXIO) {
		t.Fatalf("probe FIFO reader: %v", err)
	}
}

func TestPrivateRootsAndFilesUseRestrictiveCreationModes(t *testing.T) {
	for _, mask := range []int{0, 0o077} {
		t.Run("umask_"+strings.ReplaceAll(fs.FileMode(mask).String(), "-", "_"), func(t *testing.T) {
			config := testConfig(t)
			withUmask(t, mask, func() {
				value, err := New(context.Background(), config)
				if err != nil {
					t.Fatal(err)
				}
				defer value.Close()
				for _, name := range []string{config.StatePath, config.RecoveryPath, config.TemporarySourcePath, config.StagingPath} {
					assertMode(t, filepath.Join(config.BaseRoot, name), 0o700)
				}
				for index, role := range []lifecycle.RootRole{
					lifecycle.StateRoot, lifecycle.RecoveryRoot, lifecycle.TemporarySourceRoot, lifecycle.StagingRoot,
				} {
					expectation := absentExpectation(t, value, role, "private")
					request := mutationForRoot(t, role, "private", nil, 0o600, expectation, index+1)
					if result, err := value.ReplaceFile(context.Background(), request); err != nil || result.Visibility != lifecycle.FileAppliedVerified {
						t.Fatalf("ReplaceFile(%s) = %+v, %v", role, result, err)
					}
					assertMode(t, filepath.Join(value.roots[role].absolute, "private"), 0o600)
				}
				expectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
				request := mutationForRoot(t, lifecycle.ManagedOutputRoot, "rules.md", nil, 0o644, expectation, 5)
				if result, err := value.ReplaceFile(context.Background(), request); err != nil || result.Visibility != lifecycle.FileAppliedVerified {
					t.Fatalf("ReplaceFile(managed) = %+v, %v", result, err)
				}
				info, err := os.Stat(filepath.Join(config.ManagedOutputRoot, "rules.md"))
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm()&^fs.FileMode(0o644) != 0 {
					t.Fatalf("owned file mode = %04o", info.Mode().Perm())
				}
			})
		})
	}
}

func TestAtomicWriterEnforcesRootRoleFileModes(t *testing.T) {
	for _, mode := range []fs.FileMode{0, 0o400, 0o644} {
		t.Run(fmt.Sprintf("private_create_%04o", mode), func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			expectation := absentExpectation(t, value, lifecycle.StateRoot, "private")
			request := mutationForRoot(t, lifecycle.StateRoot, "private", []byte("new"), mode, expectation, 1)
			if result, err := value.ReplaceFile(context.Background(), request); err == nil || result.Visibility != lifecycle.FileNotApplied {
				t.Fatalf("private create mode %04o = %+v, %v", mode, result, err)
			}
			if _, err := os.Lstat(filepath.Join(value.roots[lifecycle.StateRoot].absolute, "private")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("private create mode %04o mutated destination: %v", mode, err)
			}
		})
	}

	for _, mode := range []fs.FileMode{0o400, 0o644} {
		t.Run(fmt.Sprintf("private_replace_request_%04o", mode), func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			name := filepath.Join(value.roots[lifecycle.StateRoot].absolute, "private")
			if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			expectation := presentExpectation(t, value, lifecycle.StateRoot, "private")
			request := mutationForRoot(t, lifecycle.StateRoot, "private", []byte("new"), mode, expectation, 1)
			if result, err := value.ReplaceFile(context.Background(), request); err == nil || result.Visibility != lifecycle.FileNotApplied {
				t.Fatalf("private replace mode %04o = %+v, %v", mode, result, err)
			}
			assertContent(t, name, "old")
		})
	}

	t.Run("private replacement preserves 0600 when mode omitted", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		name := filepath.Join(value.roots[lifecycle.StateRoot].absolute, "private")
		if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		expectation := presentExpectation(t, value, lifecycle.StateRoot, "private")
		request := mutationForRoot(t, lifecycle.StateRoot, "private", []byte("new"), 0, expectation, 1)
		if result, err := value.ReplaceFile(context.Background(), request); err != nil || result.Visibility != lifecycle.FileAppliedVerified {
			t.Fatalf("private preserve = %+v, %v", result, err)
		}
		assertMode(t, name, 0o600)
	})

	for _, existing := range []fs.FileMode{0o400, 0o644} {
		t.Run(fmt.Sprintf("private_replace_existing_%04o", existing), func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			name := filepath.Join(value.roots[lifecycle.StateRoot].absolute, "private")
			if err := os.WriteFile(name, []byte("old"), existing); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(name, existing); err != nil {
				t.Fatal(err)
			}
			expectation := presentExpectation(t, value, lifecycle.StateRoot, "private")
			request := mutationForRoot(t, lifecycle.StateRoot, "private", []byte("new"), 0, expectation, 1)
			if result, err := value.ReplaceFile(context.Background(), request); err == nil || result.Visibility != lifecycle.FileNotApplied {
				t.Fatalf("unsafe existing private mode %04o = %+v, %v", existing, result, err)
			}
			assertContent(t, name, "old")
		})
	}

	t.Run("managed replacement rejects umask narrowing", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		writeManaged(t, value, "rules.md", "old", 0o640)
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		withUmask(t, 0o077, func() {
			result, err := value.ReplaceFile(context.Background(), mutation(t, "rules.md", []byte("new"), expectation))
			if err == nil || result.Visibility != lifecycle.FileNotApplied {
				t.Fatalf("umask-narrowed replacement = %+v, %v", result, err)
			}
		})
		assertContent(t, filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, "rules.md"), "old")
		assertMode(t, filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, "rules.md"), 0o640)
	})
}

func TestOpenedPrivateDirectoryFactsNormalizeTypeBits(t *testing.T) {
	directory := canonicalTempDir(t)
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	facts, err := inspectOpenFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if facts.mode != 0o700 || !safePrivateDirectory(facts, uint32(os.Geteuid())) {
		t.Fatalf("opened private directory facts = %+v", facts)
	}
}

func TestPrivateRootCreationRejectsUnsafePreexistingObjects(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, config Config)
	}{
		{name: "permissive directory", setup: func(t *testing.T, config Config) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(config.BaseRoot, config.StatePath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(config.BaseRoot, config.StatePath), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", setup: func(t *testing.T, config Config) {
			t.Helper()
			if err := os.Symlink(config.ManagedOutputRoot, filepath.Join(config.BaseRoot, config.StatePath)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "fifo", setup: func(t *testing.T, config Config) {
			t.Helper()
			if err := syscall.Mkfifo(filepath.Join(config.BaseRoot, config.StatePath), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig(t)
			test.setup(t, config)
			if value, err := New(context.Background(), config); err == nil {
				_ = value.Close()
				t.Fatal("New() accepted unsafe root")
			} else if !errors.Is(err, fault.ErrConflict) {
				t.Fatalf("New() error = %v", err)
			}
		})
	}
	config := testConfig(t)
	config.StatePath = "../outside"
	if _, err := New(context.Background(), config); !errors.Is(err, fault.ErrInvalidInput) {
		t.Fatalf("outside root error = %v", err)
	}
	config = testConfig(t)
	if value, err := newForUID(config, uint32(os.Geteuid()+1)); err == nil {
		_ = value.Close()
		t.Fatal("New() accepted wrong expected owner")
	}

	config = testConfig(t)
	if err := os.Mkdir(filepath.Join(config.BaseRoot, config.StagingPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(config.BaseRoot, config.StagingPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if value, err := New(context.Background(), config); !errors.Is(err, fault.ErrConflict) {
		if value != nil {
			_ = value.Close()
		}
		t.Fatalf("late unsafe private root error = %v", err)
	}
	for _, name := range []string{config.StatePath, config.RecoveryPath, config.TemporarySourcePath} {
		if _, err := os.Lstat(filepath.Join(config.BaseRoot, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("late preflight failure created %q: %v", name, err)
		}
	}
}

func TestConfiguredAuthorityModesAreRevalidatedBeforeWrites(t *testing.T) {
	for _, safe := range []fs.FileMode{0o700, 0o750, 0o755} {
		t.Run(fmt.Sprintf("managed_safe_%04o", safe), func(t *testing.T) {
			config := testConfig(t)
			if err := os.Chmod(config.ManagedOutputRoot, safe); err != nil {
				t.Fatal(err)
			}
			value, err := New(context.Background(), config)
			if err != nil {
				t.Fatalf("New() rejected safe managed mode %04o: %v", safe, err)
			}
			_ = value.Close()
		})
	}
	for _, unsafe := range []fs.FileMode{0o775, 0o777, fs.ModeSetgid | 0o755, fs.ModeSticky | 0o755} {
		t.Run(fmt.Sprintf("managed_unsafe_%v", unsafe), func(t *testing.T) {
			config := testConfig(t)
			if err := os.Chmod(config.ManagedOutputRoot, unsafe); err != nil {
				t.Fatal(err)
			}
			if value, err := New(context.Background(), config); err == nil {
				_ = value.Close()
				t.Fatalf("New() accepted unsafe managed mode %v", unsafe)
			}
		})
	}

	value, _ := newTestFilesystem(t)
	defer value.Close()
	private := value.roots[lifecycle.StateRoot].absolute
	privateExpectation := absentExpectation(t, value, lifecycle.StateRoot, "must-not-exist")
	if err := os.Chmod(private, 0o777); err != nil {
		t.Fatal(err)
	}
	privateRequest := mutationForRoot(t, lifecycle.StateRoot, "must-not-exist", nil, 0o600, privateExpectation, 1)
	if _, err := value.ReplaceFile(context.Background(), privateRequest); !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("private mode-change error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(private, "must-not-exist")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("private write occurred: %v", err)
	}

	managed := value.roots[lifecycle.ManagedOutputRoot].absolute
	managedExpectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, "must-not-exist")
	if err := os.Chmod(managed, 0o775); err != nil {
		t.Fatal(err)
	}
	managedRequest := mutationForRoot(t, lifecycle.ManagedOutputRoot, "must-not-exist", nil, 0o600, managedExpectation, 2)
	if _, err := value.ReplaceFile(context.Background(), managedRequest); !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("managed mode-change error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(managed, "must-not-exist")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("managed write occurred: %v", err)
	}
}

func TestConfiguredRootsRejectUnsafeRenameAuthorityAncestors(t *testing.T) {
	for _, mode := range []fs.FileMode{0o775, 0o777} {
		t.Run(fmt.Sprintf("initial_%04o", mode), func(t *testing.T) {
			ancestor := canonicalTempDir(t)
			config := configUnderAncestor(t, ancestor)
			if err := os.Chmod(ancestor, mode); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(ancestor, 0o700)
			if value, err := New(context.Background(), config); err == nil {
				_ = value.Close()
				t.Fatalf("New() accepted unsafe authority ancestor mode %04o", mode)
			} else if !errors.Is(err, fault.ErrConflict) {
				t.Fatalf("unsafe authority ancestor error = %v", err)
			}
			if _, err := os.Lstat(filepath.Join(config.BaseRoot, config.StatePath)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("unsafe ancestor configuration created a private root: %v", err)
			}
		})
	}

	t.Run("post construction", func(t *testing.T) {
		ancestor := canonicalTempDir(t)
		config := configUnderAncestor(t, ancestor)
		value, err := New(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		defer value.Close()
		expectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, "must-not-exist")
		if err := os.Chmod(ancestor, 0o777); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(ancestor, 0o700)
		request := mutation(t, "must-not-exist", []byte("new"), expectation)
		result, err := value.ReplaceFile(context.Background(), request)
		if !errors.Is(err, fault.ErrConflict) || result.Visibility != lifecycle.FileNotApplied || result.Durability != lifecycle.NamespaceNotStarted {
			t.Fatalf("unsafe post-construction ancestor = %+v, %v", result, err)
		}
		if _, err := os.Lstat(filepath.Join(config.ManagedOutputRoot, "must-not-exist")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("unsafe ancestor change allowed a write: %v", err)
		}
	})
}

func TestConfiguredAuthoritiesMustNotOverlap(t *testing.T) {
	tests := []func(t *testing.T, config *Config){
		func(_ *testing.T, config *Config) { config.ManagedOutputRoot = config.BaseRoot },
		func(t *testing.T, config *Config) {
			config.ManagedOutputRoot = filepath.Join(config.BaseRoot, "managed")
			if err := os.Mkdir(config.ManagedOutputRoot, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		func(t *testing.T, config *Config) {
			base := filepath.Join(config.ManagedOutputRoot, "base")
			if err := os.Mkdir(base, 0o700); err != nil {
				t.Fatal(err)
			}
			config.BaseRoot = base
		},
	}
	for index, setup := range tests {
		t.Run(fmt.Sprintf("overlap_%d", index), func(t *testing.T) {
			config := testConfig(t)
			setup(t, &config)
			if value, err := New(context.Background(), config); !errors.Is(err, fault.ErrInvalidInput) {
				if value != nil {
					_ = value.Close()
				}
				t.Fatalf("overlap error = %v", err)
			}
		})
	}
}

func TestConfiguredAuthoritiesRejectCaseAndUnicodeAliasesByOpenedIdentity(t *testing.T) {
	aliases := []struct {
		name   string
		actual string
		alias  string
	}{
		{name: "case", actual: "AI4JAuthority", alias: "ai4jauthority"},
		{name: "unicode", actual: "ai4j-caf\u00e9", alias: "ai4j-cafe\u0301"},
	}
	for _, alias := range aliases {
		t.Run(alias.name, func(t *testing.T) {
			actual, alternate := requireDirectoryAlias(t, alias.actual, alias.alias)
			config := testConfig(t)
			config.BaseRoot = actual
			config.ManagedOutputRoot = alternate
			if value, err := New(context.Background(), config); !errors.Is(err, fault.ErrInvalidInput) {
				if value != nil {
					_ = value.Close()
				}
				t.Fatalf("authority alias error = %v", err)
			}
			if _, err := os.Lstat(filepath.Join(actual, config.StatePath)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("authority alias mutated private roots: %v", err)
			}
		})
	}
}

func TestPrivateRootRoleNamesRejectCaseAndUnicodeBeforeCreation(t *testing.T) {
	aliases := []struct {
		name   string
		first  string
		second string
	}{
		{name: "case", first: "AI4JState", second: "ai4jstate"},
		{name: "unicode", first: "ai4j-r\u00e9covery", second: "ai4j-re\u0301covery"},
	}
	for _, alias := range aliases {
		t.Run(alias.name, func(t *testing.T) {
			config := testConfig(t)
			config.StatePath = alias.first
			config.RecoveryPath = alias.second
			if value, err := New(context.Background(), config); !errors.Is(err, fault.ErrInvalidInput) {
				if value != nil {
					_ = value.Close()
				}
				t.Fatalf("private-root alias error = %v", err)
			}
			for _, name := range []string{alias.first, alias.second, config.TemporarySourcePath, config.StagingPath} {
				if _, err := os.Lstat(filepath.Join(config.BaseRoot, name)); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("invalid role names created %q: %v", name, err)
				}
			}
		})
	}
}

func TestConfiguredRootInitializationNeverFollowsSubstitutedSymlink(t *testing.T) {
	outsideFIFO := filepath.Join(canonicalTempDir(t), "outside-fifo")
	if err := syscall.Mkfifo(outsideFIFO, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"base", "managed"} {
		t.Run(role, func(t *testing.T) {
			target := filepath.Join(canonicalTempDir(t), role)
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			changed := false
			file, err := openAbsoluteDirectoryDescriptorWithHook(target, lifecycle.ObjectIdentity{}, func(classified string) {
				if changed || classified != target {
					return
				}
				changed = true
				if err := os.Rename(target, target+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outsideFIFO, target); err != nil {
					t.Fatal(err)
				}
			})
			if file != nil {
				_ = file.Close()
			}
			if err == nil || !changed {
				t.Fatalf("initialization error = %v, changed=%t", err, changed)
			}
			assertFIFOHasNoReader(t, outsideFIFO)
		})
	}

	basePath := canonicalTempDir(t)
	base, err := openAbsoluteDirectoryDescriptor(basePath, lifecycle.ObjectIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	baseFacts, err := inspectOpenFile(base)
	if err != nil {
		t.Fatal(err)
	}
	privateName := "private"
	if err := os.Mkdir(filepath.Join(basePath, privateName), 0o700); err != nil {
		t.Fatal(err)
	}
	changed := false
	root, err := createPrivateRoot(base, baseFacts.identity, basePath, privateName, uint32(os.Geteuid()), func() {
		changed = true
		privatePath := filepath.Join(basePath, privateName)
		if err := os.Rename(privatePath, privatePath+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideFIFO, privatePath); err != nil {
			t.Fatal(err)
		}
	})
	if root != nil {
		_ = root.directory.Close()
	}
	if err == nil || !changed {
		t.Fatalf("private-root initialization error = %v, changed=%t", err, changed)
	}
	assertFIFOHasNoReader(t, outsideFIFO)

	modeBasePath := canonicalTempDir(t)
	modeBase, err := openAbsoluteDirectoryDescriptor(modeBasePath, lifecycle.ObjectIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	defer modeBase.Close()
	modeBaseFacts, err := inspectOpenFile(modeBase)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(modeBasePath, "mode-race"), 0o700); err != nil {
		t.Fatal(err)
	}
	modeRoot, err := createPrivateRoot(modeBase, modeBaseFacts.identity, modeBasePath, "mode-race", uint32(os.Geteuid()), func() {
		if err := os.Chmod(filepath.Join(modeBasePath, "mode-race"), 0o777); err != nil {
			t.Fatal(err)
		}
	})
	if modeRoot != nil {
		_ = modeRoot.directory.Close()
	}
	if !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("private-root mode race error = %v", err)
	}
}

func TestMaximumFileBytesIsHardBoundedAndReadBoundaryIsExact(t *testing.T) {
	for _, invalidMaximum := range []int64{-1, 0, maximumConfiguredFileBytes + 1, int64(^uint64(0) >> 1)} {
		config := testConfig(t)
		config.MaximumFileBytes = invalidMaximum
		if value, err := New(context.Background(), config); !errors.Is(err, fault.ErrInvalidInput) {
			if value != nil {
				_ = value.Close()
			}
			t.Fatalf("maximum %d error = %v", invalidMaximum, err)
		}
	}
	value, _ := newTestFilesystem(t)
	defer value.Close()
	content := []byte("12345")
	writeManaged(t, value, "bounded", string(content), 0o600)
	request := lifecycle.ResourceRequest{Root: lifecycle.ManagedOutputRoot, Path: "bounded", Kind: lifecycle.RegularResource}
	read, err := value.ReadResource(context.Background(), lifecycle.ResourceReadRequest{Resource: request, MaxBytes: int64(len(content))})
	if err != nil || string(read.Content) != string(content) {
		t.Fatalf("exact-boundary read = %q, %v", read.Content, err)
	}
	if _, err := value.ReadResource(context.Background(), lifecycle.ResourceReadRequest{Resource: request, MaxBytes: int64(len(content) - 1)}); !errors.Is(err, fault.ErrInvalidInput) {
		t.Fatalf("over-boundary read error = %v", err)
	}
}

func TestRootedInspectionRejectsTraversalLinksAndSpecialFilesBeforeOpen(t *testing.T) {
	value, _ := newShortTestFilesystem(t)
	defer value.Close()
	state := value.roots[lifecycle.StateRoot].absolute
	if err := os.WriteFile(filepath.Join(state, "regular"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(state, "regular"), filepath.Join(state, "hard-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := value.CheckResource(context.Background(), lifecycle.ResourceRequest{
		Root: lifecycle.StateRoot, Path: "regular", Kind: lifecycle.RegularResource, RejectMultipleLinks: true,
	}); !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("hard-link error = %v", err)
	}
	outside := canonicalTempDir(t)
	if err := os.WriteFile(filepath.Join(outside, "outside"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside"), filepath.Join(state, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := value.CheckResource(context.Background(), lifecycle.ResourceRequest{
		Root: lifecycle.StateRoot, Path: "link", Kind: lifecycle.RegularResource,
	}); !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("symlink error = %v", err)
	}
	if err := syscall.Mkfifo(filepath.Join(state, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertReturnsWithoutLeafOpen(t, func() error {
		_, err := value.CheckResource(context.Background(), lifecycle.ResourceRequest{
			Root: lifecycle.StateRoot, Path: "pipe", Kind: lifecycle.RegularResource,
		})
		return err
	})
	listener, err := net.Listen("unix", filepath.Join(state, "socket"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	assertReturnsWithoutLeafOpen(t, func() error {
		_, err := value.CheckResource(context.Background(), lifecycle.ResourceRequest{
			Root: lifecycle.StateRoot, Path: "socket", Kind: lifecycle.RegularResource,
		})
		return err
	})
	for _, candidate := range []string{"../escape", "a/../escape", "control\u0085name", "/absolute"} {
		if _, err := value.CheckResource(context.Background(), lifecycle.ResourceRequest{
			Root: lifecycle.StateRoot, Path: candidate, Kind: lifecycle.RegularResource,
		}); !errors.Is(err, fault.ErrInvalidInput) {
			t.Fatalf("path %q error = %v", candidate, err)
		}
	}
	assertReturnsWithError(t, func() error {
		_, err := value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
			Candidate: "/dev/null", Authority: lifecycle.TrustedUserOrSystemAuthority,
		})
		return err
	})
}

func TestRootedReadIsBoundedAndUsesOpenedObjectIdentity(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	state := value.roots[lifecycle.StateRoot].absolute
	file := filepath.Join(state, "source")
	if err := os.WriteFile(file, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := lifecycle.ResourceRequest{
		Root: lifecycle.StateRoot, Path: "source", Kind: lifecycle.RegularResource,
		RequireCurrentOwner: true, RejectMultipleLinks: true,
	}
	first, err := value.ReadResource(context.Background(), lifecycle.ResourceReadRequest{Resource: request, MaxBytes: 5})
	if err != nil || string(first.Content) != "first" {
		t.Fatalf("first read = %q, %v", first.Content, err)
	}
	if _, err := value.ReadResource(context.Background(), lifecycle.ResourceReadRequest{Resource: request, MaxBytes: 4}); !errors.Is(err, fault.ErrInvalidInput) {
		t.Fatalf("bounded read error = %v", err)
	}
	if err := os.Rename(file, file+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := value.ReadResource(context.Background(), lifecycle.ResourceReadRequest{Resource: request, MaxBytes: 6})
	if err != nil {
		t.Fatal(err)
	}
	if first.Observation.Identity == second.Observation.Identity || string(second.Content) != "second" {
		t.Fatalf("replacement identity/content not observed: first=%v second=%v %q", first.Observation.Identity, second.Observation.Identity, second.Content)
	}
}

func TestExecutableQualificationResolvesAliasesAndEnforcesTrust(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	directory := canonicalTempDir(t)
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("native-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(directory, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	first, err := value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
		Candidate: alias, Authority: lifecycle.CurrentUserAuthority,
	})
	if err != nil || first.ResolvedPath != target || first.Resource.OwnerClass != lifecycle.CurrentUserOwner ||
		!first.Resource.ExecutableDigest.Valid() || !first.Profile.Valid() || !first.Valid() {
		t.Fatalf("qualified alias = %+v, %v", first, err)
	}
	if err := os.WriteFile(target, []byte("native-two"), 0o700); err != nil {
		t.Fatal(err)
	}
	second, err := value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
		Candidate: alias, Authority: lifecycle.CurrentUserAuthority,
	})
	if err != nil || first.Resource.Identity != second.Resource.Identity || first.Resource.Size != second.Resource.Size ||
		first.Resource.ExecutableDigest == second.Resource.ExecutableDigest {
		t.Fatalf("same-inode rewrite proof = first %+v, second %+v, %v", first, second, err)
	}

	for _, mode := range []fs.FileMode{0o720, 0o702, fs.ModeSetuid | 0o700, fs.ModeSetgid | 0o700} {
		if err := os.Chmod(target, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
			Candidate: target, Authority: lifecycle.TrustedUserOrSystemAuthority,
		}); !errors.Is(err, fault.ErrConflict) {
			t.Fatalf("unsafe executable mode %v error = %v", mode, err)
		}
	}

	loopA := filepath.Join(directory, "loop-a")
	loopB := filepath.Join(directory, "loop-b")
	if err := os.Symlink(loopB, loopA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loopA, loopB); err != nil {
		t.Fatal(err)
	}
	for _, badAlias := range []string{loopA, filepath.Join(directory, "dangling")} {
		if badAlias != loopA {
			if err := os.Symlink(filepath.Join(directory, "missing"), badAlias); err != nil {
				t.Fatal(err)
			}
		}
		_, err := value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
			Candidate: badAlias, Authority: lifecycle.TrustedUserOrSystemAuthority,
		})
		if err == nil {
			t.Fatalf("unsafe alias %q accepted", badAlias)
		}
		if badAlias == loopA && errors.Is(err, lifecycle.ErrExecutableNotFound) {
			t.Fatalf("alias loop classified as missing: %v", err)
		}
		if badAlias != loopA && !errors.Is(err, lifecycle.ErrExecutableNotFound) {
			t.Fatalf("dangling alias error = %v", err)
		}
	}
	missingCanary := filepath.Join(directory, "AI4J_MISSING_EXECUTABLE_CANARY")
	_, err = value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
		Candidate: missingCanary, Authority: lifecycle.TrustedUserOrSystemAuthority,
	})
	if !errors.Is(err, lifecycle.ErrExecutableNotFound) || strings.Contains(fmt.Sprint(err), missingCanary) ||
		strings.Contains(fmt.Sprint(err), "CANARY") {
		t.Fatalf("missing executable error was not stable and redacted: %v", err)
	}
}

func TestSystemOwnedExecutableAuthorityIsStrictAndRejectsAliases(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()

	const candidate = "/usr/bin/true"
	observation, err := value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
		Candidate: candidate, Authority: lifecycle.SystemOwnedChainAuthority,
	})
	if err != nil || !observation.Valid() || observation.ResolvedPath != candidate ||
		observation.Authority != lifecycle.SystemOwnedChainAuthority || observation.Resource.OwnerClass != lifecycle.SystemOwner {
		t.Fatalf("system executable observation = %#v, %v", observation, err)
	}

	alias := filepath.Join(canonicalTempDir(t), "system-alias")
	if err := os.Symlink(candidate, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
		Candidate: alias, Authority: lifecycle.SystemOwnedChainAuthority,
	}); !errors.Is(err, fault.ErrConflict) || errors.Is(err, lifecycle.ErrExecutableNotFound) {
		t.Fatalf("system alias error = %v", err)
	}
}

func TestExecutableAuthorityAncestorPredicatesAreClosed(t *testing.T) {
	currentUID := uint32(501)
	for _, test := range []struct {
		name      string
		facts     fileFacts
		authority lifecycle.ExecutableAuthorityClass
		want      bool
	}{
		{name: "system safe", facts: fileFacts{kind: lifecycle.DirectoryResource, mode: 0o755, uid: 0}, authority: lifecycle.SystemOwnedChainAuthority, want: true},
		{name: "system current-owned", facts: fileFacts{kind: lifecycle.DirectoryResource, mode: 0o700, uid: currentUID}, authority: lifecycle.SystemOwnedChainAuthority},
		{name: "system group-writable", facts: fileFacts{kind: lifecycle.DirectoryResource, mode: 0o775, uid: 0}, authority: lifecycle.SystemOwnedChainAuthority},
		{name: "system sticky", facts: fileFacts{kind: lifecycle.DirectoryResource, mode: fs.ModeSticky | 0o777, uid: 0}, authority: lifecycle.SystemOwnedChainAuthority},
		{name: "system setgid", facts: fileFacts{kind: lifecycle.DirectoryResource, mode: fs.ModeSetgid | 0o755, uid: 0}, authority: lifecycle.SystemOwnedChainAuthority},
		{name: "trusted current safe", facts: fileFacts{kind: lifecycle.DirectoryResource, mode: 0o700, uid: currentUID}, authority: lifecycle.TrustedUserOrSystemAuthority, want: true},
		{name: "trusted root sticky exception", facts: fileFacts{kind: lifecycle.DirectoryResource, mode: fs.ModeSticky | 0o777, uid: 0}, authority: lifecycle.TrustedUserOrSystemAuthority, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := executableAuthorityAncestorSafe(test.facts, currentUID, test.authority); got != test.want {
				t.Fatalf("ancestor safe = %t, want %t", got, test.want)
			}
		})
	}
	if _, err := walkAbsoluteFile(
		"/usr/bin/true", uint32(os.Geteuid()), lifecycle.ExecutableAuthorityClass("future_v1"), false, false, nil,
	); !errors.Is(err, fault.ErrInvalidInput) {
		t.Fatalf("unknown walker authority error = %v", err)
	}
	if _, _, _, err := inspectAbsoluteFile("/usr/bin/true", uint32(os.Geteuid())); err != nil {
		t.Fatalf("zero internal general-file authority was rejected: %v", err)
	}
}

func TestExecutableObservationOwnerBooleanFollowsOwnerClassAtRootEUID(t *testing.T) {
	t.Parallel()

	facts := fileFacts{
		kind: lifecycle.RegularResource, mode: 0o755, size: 1, uid: 0, links: 1,
		identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 2},
	}
	observation := facts.observation(
		lifecycle.ObjectIdentity{Filesystem: 1, Object: 3},
		lifecycle.ObjectIdentity{Filesystem: 1, Object: 4},
		0,
	)
	if observation.OwnerClass != lifecycle.SystemOwner || observation.OwnedByCurrentUser {
		t.Fatalf("root-EUID owner facts = %s, owned=%t", observation.OwnerClass, observation.OwnedByCurrentUser)
	}
}

func TestExecutableQualificationHonorsFinalCancellationAndNilReceiver(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	directory := canonicalTempDir(t)
	target := filepath.Join(directory, "native")
	writeExecutableBytes(t, target, syntheticMachOExecutable('c'))
	request := lifecycle.ExecutableRequest{Candidate: target, Authority: lifecycle.CurrentUserAuthority}

	ctx, cancel := context.WithCancel(context.Background())
	_, err := value.checkExecutableWithHooks(ctx, request, executableInspectionHooks{afterInspection: cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("final cancellation error = %v", err)
	}

	var absent *Filesystem
	if _, err := absent.CheckExecutable(context.Background(), request); !errors.Is(err, fault.ErrInvalidInput) {
		t.Fatalf("nil filesystem error = %v", err)
	}
}

func TestExecutableQualificationFinalAuthorityWalkRejectsAncestorDrift(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	root := canonicalTempDir(t)
	parent := filepath.Join(root, "authority")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "native")
	writeExecutableBytes(t, target, syntheticMachOExecutable('a'))
	request := lifecycle.ExecutableRequest{Candidate: target, Authority: lifecycle.CurrentUserAuthority}

	_, err := value.checkExecutableWithHooks(context.Background(), request, executableInspectionHooks{
		afterInspection: func() {
			if chmodErr := os.Chmod(parent, 0o777); chmodErr != nil {
				t.Fatal(chmodErr)
			}
		},
	})
	if !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("ancestor chmod race error = %v", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}

	moved := parent + ".old"
	_, err = value.checkExecutableWithHooks(context.Background(), request, executableInspectionHooks{
		afterInspection: func() {
			if renameErr := os.Rename(parent, moved); renameErr != nil {
				t.Fatal(renameErr)
			}
			if mkdirErr := os.Mkdir(parent, 0o700); mkdirErr != nil {
				t.Fatal(mkdirErr)
			}
		},
	})
	if !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("ancestor replacement race error = %v", err)
	}
}

func TestPrivateRootDirectoryExpectationOpensOnlyTheRevalidatedRoot(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()

	expectation, err := value.RootDirectoryExpectation(context.Background(), lifecycle.TemporarySourceRoot)
	if err != nil || !expectation.Valid() || expectation.Path != "." || expectation.Identity != expectation.RootIdentity {
		t.Fatalf("root expectation = %#v, %v", expectation, err)
	}
	file, err := value.OpenDirectory(context.Background(), expectation)
	if err != nil {
		t.Fatal(err)
	}
	facts, inspectErr := inspectOpenFile(file)
	fd := file.Fd()
	flags, flagsErr := unix.FcntlInt(fd, unix.F_GETFD, 0)
	if closeErr := file.Close(); inspectErr == nil {
		inspectErr = closeErr
	}
	if inspectErr != nil || flagsErr != nil || facts.identity != expectation.Identity ||
		!safePrivateDirectory(facts, value.currentUID) || fd < minimumAuthorityDescriptor || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("opened private root facts = %#v, fd=%d, flags=%d, errors=%v/%v", facts, fd, flags, inspectErr, flagsErr)
	}

	if _, err := value.RootDirectoryExpectation(context.Background(), lifecycle.ManagedOutputRoot); !errors.Is(err, fault.ErrInvalidInput) {
		t.Fatalf("managed root expectation error = %v", err)
	}
	changed := expectation
	changed.ParentIdentity.Object++
	if _, err := value.OpenDirectory(context.Background(), changed); !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("changed parent expectation error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := value.OpenDirectory(cancelled, expectation); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled open error = %v", err)
	}
}

func TestPrivateRootDirectoryExpectationRejectsDetachedAuthority(t *testing.T) {
	value, config := newTestFilesystem(t)
	defer value.Close()
	expectation, err := value.RootDirectoryExpectation(context.Background(), lifecycle.TemporarySourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	moved := config.BaseRoot + ".moved"
	if err := os.Rename(config.BaseRoot, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := value.OpenDirectory(context.Background(), expectation); !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("detached authority open error = %v", err)
	}
}

func TestFilesystemCloseIsIdempotent(t *testing.T) {
	value, _ := newTestFilesystem(t)
	if err := value.Close(); err != nil {
		t.Fatal(err)
	}
	if err := value.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestExecutableQualificationBindsProfileAndDigestToSameDescriptor(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	directory := canonicalTempDir(t)
	target := filepath.Join(directory, "native")
	writeExecutableBytes(t, target, syntheticMachOExecutable('a'))

	observation, err := value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
		Candidate: target, Authority: lifecycle.CurrentUserAuthority,
	})
	if err != nil || !observation.Valid() {
		t.Fatalf("qualified executable = %#v, %v", observation, err)
	}
	native, ok := observation.Profile.Native()
	if !ok || native.Layout() != lifecycle.NativeSingleImage || native.Role() != lifecycle.NativeExecutable ||
		native.Architectures() != lifecycle.ExecutableARM64 {
		t.Fatalf("native profile = %#v, %t", native, ok)
	}

	_, err = value.checkExecutableWithHooks(context.Background(), lifecycle.ExecutableRequest{
		Candidate: target, Authority: lifecycle.TrustedUserOrSystemAuthority,
	}, executableInspectionHooks{
		afterFirstDigest: func() { writeExecutableBytes(t, target, syntheticMachOExecutable('b')) },
	})
	if !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("same-profile digest race error = %v", err)
	}

	writeExecutableBytes(t, target, syntheticMachOExecutable('c'))
	_, err = value.checkExecutableWithHooks(context.Background(), lifecycle.ExecutableRequest{
		Candidate: target, Authority: lifecycle.TrustedUserOrSystemAuthority,
	}, executableInspectionHooks{
		afterFirstDigest: func() {
			script := make([]byte, len(syntheticMachOExecutable('c')))
			copy(script, []byte("#!/usr/bin/env node\n"))
			writeExecutableBytes(t, target, script)
		},
	})
	if !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("profile race error = %v", err)
	}

	writeExecutableBytes(t, target, syntheticMachOExecutable('d'))
	_, err = value.checkExecutableWithHooks(context.Background(), lifecycle.ExecutableRequest{
		Candidate: target, Authority: lifecycle.TrustedUserOrSystemAuthority,
	}, executableInspectionHooks{
		afterFirstDigest: func() {
			script := make([]byte, len(syntheticMachOExecutable('d')))
			copy(script, []byte("#!/usr/bin/env node\n"))
			writeExecutableBytes(t, target, script)
		},
		afterSecondProfile: func() { writeExecutableBytes(t, target, syntheticMachOExecutable('d')) },
	})
	if !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("second-profile A/B restoration race error = %v", err)
	}

	writeExecutableBytes(t, target, syntheticMachOExecutable('e'))
	_, err = value.checkExecutableWithHooks(context.Background(), lifecycle.ExecutableRequest{
		Candidate: target, Authority: lifecycle.TrustedUserOrSystemAuthority,
	}, executableInspectionHooks{
		afterFirstDigest: func() {
			grown := append(syntheticMachOExecutable('e'), 'x')
			writeExecutableBytes(t, target, grown)
		},
	})
	if !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("size growth race error = %v", err)
	}
}

func TestOpenExecutableReturnsTheRevalidatedQualifiedDescriptor(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	target := filepath.Join(canonicalTempDir(t), "native")
	writeExecutableBytes(t, target, syntheticMachOExecutable('q'))

	observation, err := value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
		Candidate: target, Authority: lifecycle.CurrentUserAuthority,
	})
	if err != nil {
		t.Fatal(err)
	}
	expectation := lifecycle.ExecutableExpectation{
		Identity:            observation.Resource.Identity,
		Authority:           observation.Authority,
		OwnerClass:          observation.Resource.OwnerClass,
		Mode:                observation.Resource.Mode,
		PrivilegeBearing:    observation.Resource.PrivilegeBearing,
		WritableByUntrusted: observation.Resource.WritableByUntrusted,
		Digest:              observation.Resource.ExecutableDigest,
		Profile:             observation.Profile,
	}
	file, err := value.OpenExecutable(context.Background(), observation.ResolvedPath, expectation)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := inspectOpenFile(file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil || !matchesExecutableExpectation(facts, expectation, value.currentUID) {
		t.Fatalf("opened facts = %#v, error = %v", facts, err)
	}

	changed := expectation
	changed.Identity.Object++
	if _, err := value.OpenExecutable(context.Background(), observation.ResolvedPath, changed); !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("identity mismatch error = %v", err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := value.OpenExecutable(context.Background(), observation.ResolvedPath, expectation); !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("mode mismatch error = %v", err)
	}
}

func TestOpenExecutablePostOpenAuthorityDriftClosesDescriptor(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	root := canonicalTempDir(t)
	parent := filepath.Join(root, "authority")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "native")
	writeExecutableBytes(t, target, syntheticMachOExecutable('p'))
	observation, err := value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
		Candidate: target, Authority: lifecycle.CurrentUserAuthority,
	})
	if err != nil {
		t.Fatal(err)
	}
	expectation := lifecycle.ExecutableExpectation{
		Identity: observation.Resource.Identity, Authority: observation.Authority,
		OwnerClass: observation.Resource.OwnerClass, Mode: observation.Resource.Mode,
		PrivilegeBearing: observation.Resource.PrivilegeBearing, WritableByUntrusted: observation.Resource.WritableByUntrusted,
		Digest: observation.Resource.ExecutableDigest, Profile: observation.Profile,
	}
	var opened *os.File
	_, err = value.openExecutableWithHooks(context.Background(), target, expectation, openExecutableHooks{
		afterOpen: func(file *os.File) {
			opened = file
			if chmodErr := os.Chmod(parent, 0o777); chmodErr != nil {
				t.Fatal(chmodErr)
			}
		},
	})
	if !errors.Is(err, fault.ErrConflict) || opened == nil {
		t.Fatalf("post-open ancestor drift = %v, opened=%v", err, opened)
	}
	if _, statErr := opened.Stat(); statErr == nil {
		t.Fatal("rejected post-open authority retained its descriptor")
	}
}

func TestExecutableQualificationRevalidatesModeAndLinksAtOpenedBoundaries(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	directory := canonicalTempDir(t)
	target := filepath.Join(directory, "native")
	writeExecutableBytes(t, target, syntheticMachOExecutable('a'))

	_, err := value.checkExecutableWithHooks(context.Background(), lifecycle.ExecutableRequest{
		Candidate: target, Authority: lifecycle.TrustedUserOrSystemAuthority,
	}, executableInspectionHooks{
		beforeCanonicalOpen: func() {
			if chmodErr := os.Chmod(target, 0o600); chmodErr != nil {
				t.Fatal(chmodErr)
			}
		},
	})
	if !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("pre-open chmod race error = %v", err)
	}
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err = value.checkExecutableWithHooks(context.Background(), lifecycle.ExecutableRequest{
		Candidate: target, Authority: lifecycle.TrustedUserOrSystemAuthority,
	}, executableInspectionHooks{
		afterFirstDigest: func() {
			if chmodErr := os.Chmod(target, 0o600); chmodErr != nil {
				t.Fatal(chmodErr)
			}
		},
	})
	if !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("post-hash chmod race error = %v", err)
	}
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(directory, "native-link")
	_, err = value.checkExecutableWithHooks(context.Background(), lifecycle.ExecutableRequest{
		Candidate: target, Authority: lifecycle.TrustedUserOrSystemAuthority,
	}, executableInspectionHooks{
		afterFirstDigest: func() {
			if linkErr := os.Link(target, link); linkErr != nil {
				t.Fatal(linkErr)
			}
		},
	})
	if !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("link-count race error = %v", err)
	}
}

func TestExecutableFactEqualityIncludesEveryTrustFact(t *testing.T) {
	base := fileFacts{
		kind: lifecycle.RegularResource, mode: 0o700, size: 64, uid: 501, links: 1,
		identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 2},
	}
	if !sameExecutableFacts(base, base) {
		t.Fatal("equal facts did not match")
	}
	mutations := []func(*fileFacts){
		func(facts *fileFacts) { facts.kind = lifecycle.DirectoryResource },
		func(facts *fileFacts) { facts.mode = 0o600 },
		func(facts *fileFacts) { facts.size++ },
		func(facts *fileFacts) { facts.uid++ },
		func(facts *fileFacts) { facts.links++ },
		func(facts *fileFacts) { facts.identity.Object++ },
	}
	for index, mutate := range mutations {
		changed := base
		mutate(&changed)
		if sameExecutableFacts(base, changed) {
			t.Fatalf("trust fact mutation %d was ignored", index)
		}
	}
}

func TestExecutableAuthorityChainEqualityIncludesChownModeAndReplacementFacts(t *testing.T) {
	baseFacts := []fileFacts{
		{kind: lifecycle.DirectoryResource, mode: 0o755, size: 64, uid: 0, links: 1, identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 1}},
		{kind: lifecycle.DirectoryResource, mode: 0o700, size: 128, uid: 501, links: 2, identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 2}},
	}
	base := make([]executableAuthorityAncestor, len(baseFacts))
	for index := range baseFacts {
		base[index] = executableAncestorFact(baseFacts[index])
	}
	if !sameExecutableAuthorityChain(base, append([]executableAuthorityAncestor(nil), base...)) {
		t.Fatal("equal authority chains did not match")
	}

	metadataOnly := append([]fileFacts(nil), baseFacts...)
	metadataOnly[0].size++
	metadataOnly[1].links++
	projected := make([]executableAuthorityAncestor, len(metadataOnly))
	for index := range metadataOnly {
		projected[index] = executableAncestorFact(metadataOnly[index])
	}
	if !sameExecutableAuthorityChain(base, projected) {
		t.Fatal("non-security directory metadata invalidated authority chain")
	}

	for name, mutate := range map[string]func([]executableAuthorityAncestor){
		"chown":       func(chain []executableAuthorityAncestor) { chain[1].uid = 0 },
		"chmod":       func(chain []executableAuthorityAncestor) { chain[1].mode = 0o755 },
		"replacement": func(chain []executableAuthorityAncestor) { chain[1].identity.Object++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := append([]executableAuthorityAncestor(nil), base...)
			mutate(changed)
			if sameExecutableAuthorityChain(base, changed) {
				t.Fatal("authority-chain drift was ignored")
			}
		})
	}
}

func TestDeniedExecutableFactsRequireSystemTrustButPermitSetID(t *testing.T) {
	base := fileFacts{kind: lifecycle.RegularResource, mode: 0o755, uid: 0}
	if !trustedDeniedExecutableFacts(base, 501) {
		t.Fatal("trusted system executable was rejected")
	}
	setID := base
	setID.mode |= fs.ModeSetuid
	if !trustedDeniedExecutableFacts(setID, 501) {
		t.Fatal("set-ID system executable could not be qualified for deny-only policy")
	}
	setGID := base
	setGID.mode |= fs.ModeSetgid
	if !trustedDeniedExecutableFacts(setGID, 501) {
		t.Fatal("set-GID system executable could not be qualified for deny-only policy")
	}
	for _, mutate := range []func(*fileFacts){
		func(facts *fileFacts) { facts.uid = 501 },
		func(facts *fileFacts) { facts.mode = 0o775 },
		func(facts *fileFacts) { facts.mode = 0o644 },
		func(facts *fileFacts) { facts.mode = 0o700 },
		func(facts *fileFacts) { facts.mode |= fs.ModeSticky },
		func(facts *fileFacts) { facts.kind = lifecycle.DirectoryResource },
	} {
		changed := base
		mutate(&changed)
		if trustedDeniedExecutableFacts(changed, 501) {
			t.Fatalf("unsafe deny-only facts accepted: %#v", changed)
		}
	}
}

func TestDeniedExecutableAliasPathConstructionIsClosed(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		relation deniedExecutableAliasRelation
		want     string
		valid    bool
	}{
		{
			name: "relative target and suffix",
			relation: deniedExecutableAliasRelation{
				linkPath: "/usr/bin/alias", target: "../libexec/tool", suffix: "nested/leaf",
			},
			want: "/usr/libexec/tool/nested/leaf", valid: true,
		},
		{
			name: "absolute target and suffix",
			relation: deniedExecutableAliasRelation{
				linkPath: "/usr/bin/alias", target: "/bin/tool", suffix: "nested/leaf",
			},
			want: "/bin/tool/nested/leaf", valid: true,
		},
		{name: "root target", relation: deniedExecutableAliasRelation{linkPath: "/bin/alias", target: "/"}},
		{name: "control target", relation: deniedExecutableAliasRelation{linkPath: "/bin/alias", target: "/bin/bad\nname"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := nextDeniedAliasPath(test.relation)
			if (err == nil) != test.valid || got != test.want {
				t.Fatalf("next alias path = %q, %v; want %q, valid=%t", got, err, test.want, test.valid)
			}
		})
	}
}

func TestDeniedExecutableResolverBoundsLoopsDepthAndCancellation(t *testing.T) {
	t.Parallel()

	identity := func(value uint64) lifecycle.ObjectIdentity {
		return lifecycle.ObjectIdentity{Filesystem: 1, Object: value}
	}
	loopCalls := 0
	_, err := resolveDeniedExecutableWithStep(context.Background(), "/system/alias", func(_ context.Context, path string) (deniedExecutableAliasRelation, bool, error) {
		loopCalls++
		return deniedExecutableAliasRelation{
			linkPath: path, target: "/system/alias", linkFacts: fileFacts{identity: identity(1)},
		}, true, nil
	})
	if !errors.Is(err, fault.ErrConflict) || loopCalls != 2 {
		t.Fatalf("alias loop = %v, calls=%d", err, loopCalls)
	}

	depthCalls := 0
	_, err = resolveDeniedExecutableWithStep(context.Background(), "/system/alias", func(_ context.Context, path string) (deniedExecutableAliasRelation, bool, error) {
		depthCalls++
		return deniedExecutableAliasRelation{
			linkPath: path, target: fmt.Sprintf("/system/alias-%d", depthCalls),
			linkFacts: fileFacts{identity: identity(uint64(depthCalls))},
		}, true, nil
	})
	if !errors.Is(err, fault.ErrConflict) || depthCalls != maximumDeniedExecutableAliasDepth+1 {
		t.Fatalf("alias depth = %v, calls=%d", err, depthCalls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	steps := 0
	_, err = resolveDeniedExecutableWithStep(ctx, "/system/alias", func(_ context.Context, _ string) (deniedExecutableAliasRelation, bool, error) {
		steps++
		cancel()
		return deniedExecutableAliasRelation{}, false, nil
	})
	if !errors.Is(err, context.Canceled) || steps != 1 {
		t.Fatalf("cancelled alias resolution = %v, steps=%d", err, steps)
	}
	if _, err := resolveDeniedExecutableWithStep(context.Background(), "/system/alias", nil); !errors.Is(err, fault.ErrInvalidInput) {
		t.Fatalf("nil alias step error = %v", err)
	}
}

func TestDeniedExecutableResolutionEqualityCoversAliasAndAuthorityFacts(t *testing.T) {
	t.Parallel()

	base := deniedExecutableResolution{
		resolved: "/bin/target",
		relations: []deniedExecutableAliasRelation{{
			linkPath: "/bin/alias", target: "target", suffix: "suffix",
			linkFacts: fileFacts{
				kind: lifecycle.ResourceKind("special"), mode: 0o777, uid: 0, links: 1,
				identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 2},
			},
			parentAuthority: []executableAuthorityAncestor{{
				kind: lifecycle.DirectoryResource, mode: 0o755, uid: 0,
				identity: lifecycle.ObjectIdentity{Filesystem: 1, Object: 1},
			}},
		}},
	}
	clone := func() deniedExecutableResolution {
		copyValue := base
		copyValue.relations = append([]deniedExecutableAliasRelation(nil), base.relations...)
		copyValue.relations[0].parentAuthority = append([]executableAuthorityAncestor(nil), base.relations[0].parentAuthority...)
		return copyValue
	}
	if !sameDeniedExecutableResolution(base, clone()) {
		t.Fatal("equal denied resolution did not match")
	}
	for name, mutate := range map[string]func(*deniedExecutableResolution){
		"resolved":      func(value *deniedExecutableResolution) { value.resolved = "/bin/other" },
		"target":        func(value *deniedExecutableResolution) { value.relations[0].target = "other" },
		"suffix":        func(value *deniedExecutableResolution) { value.relations[0].suffix = "other" },
		"link identity": func(value *deniedExecutableResolution) { value.relations[0].linkFacts.identity.Object++ },
		"parent mode":   func(value *deniedExecutableResolution) { value.relations[0].parentAuthority[0].mode = 0o775 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := clone()
			mutate(&changed)
			if sameDeniedExecutableResolution(base, changed) {
				t.Fatal("denied resolution drift was ignored")
			}
		})
	}
}

func TestDeniedExecutableDigestQualifiesReadableSystemLauncher(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	digest, err := value.InspectDeniedExecutableDigest(context.Background(), "/bin/sh")
	if err != nil || !digest.Valid() {
		t.Fatalf("InspectDeniedExecutableDigest(/bin/sh) = %v, %v", digest, err)
	}
	standalone, standaloneErr := InspectDeniedExecutableDigest(context.Background(), "/bin/sh")
	if standaloneErr != nil || standalone != digest {
		t.Fatalf("standalone InspectDeniedExecutableDigest(/bin/sh) = %v, %v", standalone, standaloneErr)
	}
	if _, err := value.CheckExecutable(context.Background(), lifecycle.ExecutableRequest{
		Candidate: "/usr/bin/sudo", Authority: lifecycle.TrustedUserOrSystemAuthority,
	}); !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("sudo unexpectedly became launchable: %v", err)
	}
	missing := "/usr/bin/AI4J_MISSING_DENY_CANARY"
	if _, err := value.InspectDeniedExecutableDigest(context.Background(), missing); !errors.Is(err, lifecycle.ErrExecutableNotFound) ||
		strings.Contains(fmt.Sprint(err), "CANARY") {
		t.Fatalf("missing deny candidate error = %v", err)
	}
}

func TestDeniedExecutableDigestQualifiesOnlySystemControlledAliases(t *testing.T) {
	if _, err := os.Lstat("/bin/csh"); errors.Is(err, fs.ErrNotExist) {
		t.Skip("optional csh baseline is absent")
	} else if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolveDeniedExecutable(context.Background(), "/bin/csh", nil)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat("/bin/csh")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		canonical, canonicalErr := filepath.EvalSymlinks("/bin/csh")
		if canonicalErr != nil || len(resolution.relations) == 0 || resolution.resolved != canonical {
			t.Fatalf("csh alias resolution = %#v", resolution)
		}
	}
	if digest, err := InspectDeniedExecutableDigest(context.Background(), "/bin/csh"); err != nil || !digest.Valid() {
		t.Fatalf("csh deny-only digest = %v, %v", digest, err)
	}

	canary := filepath.Join(canonicalTempDir(t), "AI4J_DENIED_ALIAS_CANARY")
	if err := os.Symlink("/bin/sh", canary); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectDeniedExecutableDigest(context.Background(), canary); !errors.Is(err, fault.ErrConflict) ||
		errors.Is(err, lifecycle.ErrExecutableNotFound) || strings.Contains(fmt.Sprint(err), "CANARY") {
		t.Fatalf("user-controlled deny alias error = %v", err)
	}
}

func TestDeniedExecutableAliasTraversalHonorsCancellationBeforeTargetOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	opened := false
	classifications := 0
	_, err := inspectDeniedExecutableDigest(ctx, "/bin/sh", uint32(os.Geteuid()), deniedExecutableInspectionHooks{
		afterAliasClassify: func(string) {
			classifications++
			cancel()
		},
		afterOpen: func(*os.File) { opened = true },
	})
	if !errors.Is(err, context.Canceled) || classifications != 1 || opened {
		t.Fatalf("cancelled alias traversal = %v, classifications=%d, opened=%t", err, classifications, opened)
	}
}

func TestReadOnlyDeniedExecutableInspectionClosesDescriptorOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var opened *os.File
	_, err := inspectDeniedExecutableDigest(ctx, "/bin/sh", uint32(os.Geteuid()), deniedExecutableInspectionHooks{
		afterOpen: func(file *os.File) {
			opened = file
			cancel()
		},
	})
	if !errors.Is(err, context.Canceled) || opened == nil {
		t.Fatalf("inspection error/opened = %v / %v", err, opened)
	}
	if _, statErr := opened.Stat(); statErr == nil {
		t.Fatal("cancelled read-only qualifier retained its descriptor")
	}
}

func syntheticMachOExecutable(payload byte) []byte {
	bytes := make([]byte, 64)
	binary.LittleEndian.PutUint32(bytes[0:4], 0xfeedfacf)
	binary.LittleEndian.PutUint32(bytes[4:8], 0x0100000c)
	binary.LittleEndian.PutUint32(bytes[12:16], 2)
	for index := 32; index < len(bytes); index++ {
		bytes[index] = payload
	}
	return bytes
}

func writeExecutableBytes(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(name, content, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestExecutableAliasRaceAndOwnerClassesFailClosed(t *testing.T) {
	currentUID := uint32(501)
	for _, test := range []struct {
		name        string
		facts       fileFacts
		authority   lifecycle.ExecutableAuthorityClass
		wantOwner   lifecycle.OwnerClass
		wantTrusted bool
	}{
		{name: "current", facts: fileFacts{kind: lifecycle.RegularResource, mode: 0o700, uid: currentUID}, authority: lifecycle.CurrentUserAuthority, wantOwner: lifecycle.CurrentUserOwner, wantTrusted: true},
		{name: "system", facts: fileFacts{kind: lifecycle.RegularResource, mode: 0o755, uid: 0}, authority: lifecycle.TrustedUserOrSystemAuthority, wantOwner: lifecycle.SystemOwner, wantTrusted: true},
		{name: "system narrowed", facts: fileFacts{kind: lifecycle.RegularResource, mode: 0o755, uid: 0}, authority: lifecycle.CurrentUserAuthority, wantOwner: lifecycle.SystemOwner},
		{name: "system strict", facts: fileFacts{kind: lifecycle.RegularResource, mode: 0o755, uid: 0}, authority: lifecycle.SystemOwnedChainAuthority, wantOwner: lifecycle.SystemOwner, wantTrusted: true},
		{name: "other", facts: fileFacts{kind: lifecycle.RegularResource, mode: 0o700, uid: currentUID + 1}, authority: lifecycle.TrustedUserOrSystemAuthority, wantOwner: lifecycle.OtherOwner},
		{name: "group writable", facts: fileFacts{kind: lifecycle.RegularResource, mode: 0o720, uid: currentUID}, authority: lifecycle.TrustedUserOrSystemAuthority, wantOwner: lifecycle.CurrentUserOwner},
		{name: "set id", facts: fileFacts{kind: lifecycle.RegularResource, mode: fs.ModeSetuid | 0o700, uid: currentUID}, authority: lifecycle.TrustedUserOrSystemAuthority, wantOwner: lifecycle.CurrentUserOwner},
		{name: "owner execute bit absent", facts: fileFacts{kind: lifecycle.RegularResource, mode: 0o001, uid: currentUID}, authority: lifecycle.CurrentUserAuthority, wantOwner: lifecycle.CurrentUserOwner},
		{name: "other execute bit absent", facts: fileFacts{kind: lifecycle.RegularResource, mode: 0o100, uid: 0}, authority: lifecycle.TrustedUserOrSystemAuthority, wantOwner: lifecycle.SystemOwner},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner, trusted := trustedExecutableFacts(test.facts, currentUID, test.authority)
			if owner != test.wantOwner || trusted != test.wantTrusted {
				t.Fatalf("trust = %s, %t", owner, trusted)
			}
		})
	}

	value, _ := newTestFilesystem(t)
	defer value.Close()
	directory := canonicalTempDir(t)
	first := filepath.Join(directory, "first")
	second := filepath.Join(directory, "second")
	alias := filepath.Join(directory, "alias")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte("native"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(file, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(first, alias); err != nil {
		t.Fatal(err)
	}
	_, err := value.checkExecutable(context.Background(), lifecycle.ExecutableRequest{
		Candidate: alias, Authority: lifecycle.TrustedUserOrSystemAuthority,
	}, func() {
		if err := os.Remove(alias); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(second, alias); err != nil {
			t.Fatal(err)
		}
	})
	if !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("alias race error = %v", err)
	}
}

func TestConfiguredRootReplacementFailsClosed(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	root := value.roots[lifecycle.StateRoot].absolute
	if err := os.Rename(root, root+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := value.CheckResource(context.Background(), lifecycle.ResourceRequest{
		Root: lifecycle.StateRoot, Path: "missing", Kind: lifecycle.RegularResource,
	}); !errors.Is(err, fault.ErrConflict) {
		t.Fatalf("root replacement error = %v", err)
	}
}

func TestAbsoluteTraversalNeverFollowsPostClassificationSymlink(t *testing.T) {
	for _, substitute := range []string{"leaf", "parent"} {
		t.Run(substitute, func(t *testing.T) {
			base := canonicalTempDir(t)
			parent := filepath.Join(base, "parent")
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			leaf := filepath.Join(parent, "candidate")
			if err := os.WriteFile(leaf, []byte("regular"), 0o700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(canonicalTempDir(t), "outside-pipe")
			if err := syscall.Mkfifo(outside, 0o600); err != nil {
				t.Fatal(err)
			}
			target := leaf
			if substitute == "parent" {
				target = parent
			}
			changed := false
			_, _, _, err := inspectAbsoluteFileWithHook(leaf, uint32(os.Geteuid()), func(classified string) {
				if changed || classified != target {
					return
				}
				changed = true
				if err := os.Rename(target, target+".old"); err != nil {
					t.Fatal(err)
				}
				linkTarget := outside
				if substitute == "parent" {
					outsideDirectory := canonicalTempDir(t)
					if err := os.Symlink(outside, filepath.Join(outsideDirectory, "candidate")); err != nil {
						t.Fatal(err)
					}
					linkTarget = outsideDirectory
				}
				if err := os.Symlink(linkTarget, target); err != nil {
					t.Fatal(err)
				}
			})
			if err == nil || !changed {
				t.Fatalf("inspection error = %v, substituted = %t", err, changed)
			}
			fd, openErr := syscall.Open(outside, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
			if openErr == nil {
				_ = syscall.Close(fd)
				t.Fatal("outside FIFO had a reader; traversal followed the raced symlink")
			}
			if !errors.Is(openErr, syscall.ENXIO) {
				t.Fatalf("probe outside FIFO: %v", openErr)
			}
		})
	}
}

func TestRootedTraversalNeverFollowsPostClassificationSymlink(t *testing.T) {
	value, _ := newTestFilesystem(t)
	defer value.Close()
	root := value.roots[lifecycle.StateRoot]
	if err := os.Mkdir(filepath.Join(root.absolute, "parent"), 0o700); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(root.absolute, "parent", "leaf")
	if err := os.WriteFile(leaf, []byte("regular"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideFIFO := filepath.Join(canonicalTempDir(t), "outside-fifo")
	if err := syscall.Mkfifo(outsideFIFO, 0o600); err != nil {
		t.Fatal(err)
	}
	parent, _, err := openParentNoFollow(root, "parent/leaf", value.currentUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	file, _, err := openLeafNoFollow(parent, "leaf", lifecycle.RegularResource, func() {
		changed = true
		if err := os.Rename(leaf, leaf+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideFIFO, leaf); err != nil {
			t.Fatal(err)
		}
	})
	_ = parent.Close()
	if file != nil {
		_ = file.Close()
	}
	if err == nil || !changed {
		t.Fatalf("leaf substitution error = %v, changed=%t", err, changed)
	}
	assertFIFOHasNoReader(t, outsideFIFO)

	if err := os.Remove(leaf); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root.absolute, "parent"), filepath.Join(root.absolute, "parent.original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root.absolute, "parent"), 0o700); err != nil {
		t.Fatal(err)
	}
	changed = false
	parent, _, err = openParentNoFollow(root, "parent/leaf", value.currentUID, func(classified string) {
		if changed || classified != "parent" {
			return
		}
		changed = true
		if err := os.Rename(filepath.Join(root.absolute, "parent"), filepath.Join(root.absolute, "parent.raced")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Dir(outsideFIFO), filepath.Join(root.absolute, "parent")); err != nil {
			t.Fatal(err)
		}
	})
	if parent != nil {
		_ = parent.Close()
	}
	if err == nil || !changed {
		t.Fatalf("parent substitution error = %v, changed=%t", err, changed)
	}
	assertFIFOHasNoReader(t, outsideFIFO)
}

func TestParentDescriptorsAreCloseOnExecUnlessExplicitlyPassed(t *testing.T) {
	if os.Getenv("AI4J_FD_HELPER") == "1" {
		fd := 0
		var expectedDevice, expectedObject uint64
		if _, err := fmt.Sscanf(os.Getenv("AI4J_TEST_FD"), "%d", &fd); err != nil {
			os.Exit(3)
		}
		if _, err := fmt.Sscanf(os.Getenv("AI4J_TEST_IDENTITY"), "%d:%d", &expectedDevice, &expectedObject); err != nil {
			os.Exit(3)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			os.Exit(0)
		}
		if uint64(stat.Dev) == expectedDevice && stat.Ino == expectedObject {
			os.Exit(2)
		}
		os.Exit(0)
	}
	value, _ := newTestFilesystem(t)
	defer value.Close()
	parent, _, err := openParentNoFollow(value.roots[lifecycle.StateRoot], "leaf", value.currentUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if parent.Fd() < minimumAuthorityDescriptor {
		t.Fatalf("authority descriptor = %d, want >= %d", parent.Fd(), minimumAuthorityDescriptor)
	}
	facts, err := inspectOpenFile(parent)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := unix.FcntlInt(parent.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("descriptor flags = %#x, %v", flags, err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestParentDescriptorsAreCloseOnExecUnlessExplicitlyPassed$")
	command.Env = append(os.Environ(), "AI4J_FD_HELPER=1", fmt.Sprintf("AI4J_TEST_FD=%d", parent.Fd()),
		fmt.Sprintf("AI4J_TEST_IDENTITY=%d:%d", facts.identity.Filesystem, facts.identity.Object))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fd helper: %v, %s", err, output)
	}
}

func TestAtomicReplacementHandlesAbsenceExistingContentAndRaces(t *testing.T) {
	t.Run("expected absence", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		expectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		result, err := value.ReplaceFile(context.Background(), mutation(t, "rules.md", []byte("new"), expectation))
		if err != nil || result.Cleanup != lifecycle.CleanupNotRequired {
			t.Fatalf("ReplaceFile() = %+v, %v", result, err)
		}
		assertContent(t, filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, "rules.md"), "new")
	})

	t.Run("expected present", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		writeManaged(t, value, "rules.md", "old", 0o640)
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		result, err := value.ReplaceFile(context.Background(), mutation(t, "rules.md", []byte("new"), expectation))
		if err != nil || result.Cleanup != lifecycle.CleanupNotRequired {
			t.Fatalf("ReplaceFile() = %+v, %v", result, err)
		}
		assertContent(t, filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, "rules.md"), "new")
		info, _ := os.Stat(filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, "rules.md"))
		if info.Mode().Perm()&^expectation.Mode != 0 {
			t.Fatalf("replacement broadened mode: %04o > %04o", info.Mode().Perm(), expectation.Mode)
		}
	})

	t.Run("absence race", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		destination := filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, "rules.md")
		expectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		ops := &faultAtomicOperations{real: realAtomicOperations{}, beforeExclusive: func() {
			if err := os.WriteFile(destination, []byte("concurrent"), 0o600); err != nil {
				t.Fatal(err)
			}
		}}
		value.ops = ops
		result, err := value.ReplaceFile(context.Background(), mutation(t, "rules.md", []byte("new"), expectation))
		if err == nil || result.Cleanup != lifecycle.CleanupComplete {
			t.Fatalf("race result = %+v, %v", result, err)
		}
		assertContent(t, destination, "concurrent")
	})

	t.Run("present checksum race preserves both complete objects", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		writeManaged(t, value, "rules.md", "old", 0o600)
		destination := filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, "rules.md")
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		value.ops = &faultAtomicOperations{real: realAtomicOperations{}, beforeSwap: func(call int) {
			if call == 1 {
				if err := os.WriteFile(destination, []byte("concurrent"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}}
		request := mutation(t, "rules.md", []byte("new"), expectation)
		result, err := value.ReplaceFile(context.Background(), request)
		if !errors.Is(err, fault.ErrConflict) || result.Cleanup != lifecycle.CleanupRequired ||
			result.Visibility != lifecycle.FileIndeterminate || result.Durability != lifecycle.NamespacePending {
			t.Fatalf("race result = %+v, %v", result, err)
		}
		assertContent(t, destination, "new")
		assertContent(t, filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, request.Artifacts.TemporaryName), "concurrent")
	})

	t.Run("prepared temporary rewrite fails before visibility", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		writeManaged(t, value, "rules.md", "old", 0o600)
		root := value.roots[lifecycle.ManagedOutputRoot].absolute
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		request := mutation(t, "rules.md", []byte("new"), expectation)
		ops := &faultAtomicOperations{real: realAtomicOperations{}, afterFinalValidation: func() {
			if err := os.WriteFile(filepath.Join(root, request.Artifacts.TemporaryName), []byte("bad"), 0o600); err != nil {
				t.Fatal(err)
			}
		}}
		value.ops = ops
		result, err := value.ReplaceFile(context.Background(), request)
		if !errors.Is(err, fault.ErrConflict) || result.Visibility != lifecycle.FileNotApplied ||
			result.Durability != lifecycle.NamespaceNotStarted || ops.swapCalls != 0 {
			t.Fatalf("prepared-temp rewrite = %+v, %v, swaps=%d", result, err, ops.swapCalls)
		}
		assertContent(t, filepath.Join(root, "rules.md"), "old")
	})
}

func TestAtomicFaultBoundariesLeaveCompleteDestinationAndReportCleanup(t *testing.T) {
	tests := []struct {
		name       string
		stage      string
		call       int
		visibility lifecycle.FileVisibility
		durability lifecycle.NamespaceDurability
		cleanup    lifecycle.CleanupDisposition
		visible    string
	}{
		{name: "create", stage: "create", visibility: lifecycle.FileNotApplied, durability: lifecycle.NamespaceNotStarted, cleanup: lifecycle.CleanupNotRequired, visible: "old"},
		{name: "write", stage: "write", visibility: lifecycle.FileNotApplied, durability: lifecycle.NamespaceNotStarted, cleanup: lifecycle.CleanupComplete, visible: "old"},
		{name: "file sync", stage: "sync_file", visibility: lifecycle.FileNotApplied, durability: lifecycle.NamespaceNotStarted, cleanup: lifecycle.CleanupComplete, visible: "old"},
		{name: "swap", stage: "swap", visibility: lifecycle.FileNotApplied, durability: lifecycle.NamespaceNotStarted, cleanup: lifecycle.CleanupComplete, visible: "old"},
		{name: "first namespace sync", stage: "sync_directory", call: 1, visibility: lifecycle.FileIndeterminate, durability: lifecycle.NamespacePending, cleanup: lifecycle.CleanupRequired, visible: "new"},
		{name: "quarantine rename", stage: "rename", call: 1, visibility: lifecycle.FileIndeterminate, durability: lifecycle.NamespaceDurable, cleanup: lifecycle.CleanupRequired, visible: "new"},
		{name: "quarantine namespace sync", stage: "sync_directory", call: 2, visibility: lifecycle.FileIndeterminate, durability: lifecycle.NamespaceDurable, cleanup: lifecycle.CleanupRequired, visible: "new"},
		{name: "quarantine remove", stage: "remove", call: 1, visibility: lifecycle.FileIndeterminate, durability: lifecycle.NamespaceDurable, cleanup: lifecycle.CleanupRequired, visible: "new"},
		{name: "removal namespace sync", stage: "sync_directory", call: 3, visibility: lifecycle.FileIndeterminate, durability: lifecycle.NamespaceDurable, cleanup: lifecycle.CleanupRequired, visible: "new"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			writeManaged(t, value, "rules.md", "old", 0o600)
			expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
			value.ops = &faultAtomicOperations{real: realAtomicOperations{}, failStage: test.stage, failCall: test.call}
			result, err := value.ReplaceFile(context.Background(), mutation(t, "rules.md", []byte("new"), expectation))
			if err == nil {
				t.Fatal("fault did not fail")
			}
			content, readErr := os.ReadFile(filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, "rules.md"))
			if readErr != nil || string(content) != test.visible {
				t.Fatalf("destination = %q, %v", content, readErr)
			}
			if result.Visibility != test.visibility || result.Durability != test.durability || result.Cleanup != test.cleanup || !result.Coherent() {
				t.Fatalf("result = %+v, want visibility=%s durability=%s cleanup=%s", result, test.visibility, test.durability, test.cleanup)
			}
			if test.cleanup == lifecycle.CleanupRequired && !result.CleanupArtifact.Valid() {
				t.Fatalf("required cleanup lacks full artifact: %+v", result)
			}
		})
	}

	t.Run("exclusive rename", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		expectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		value.ops = &faultAtomicOperations{real: realAtomicOperations{}, failStage: "rename", failCall: 1}
		result, err := value.ReplaceFile(context.Background(), mutation(t, "rules.md", []byte("new"), expectation))
		if err == nil || result.Visibility != lifecycle.FileNotApplied || result.Cleanup != lifecycle.CleanupComplete {
			t.Fatalf("exclusive rename result = %+v, %v", result, err)
		}
		if _, err := os.Lstat(filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, "rules.md")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("exclusive rename changed destination: %v", err)
		}
	})
}

func TestPrevisibilityTemporaryCleanupReportsAuthorityDetachment(t *testing.T) {
	for _, boundary := range []string{"after_final_sync", "failing_final_sync"} {
		for _, scope := range []string{"root", "parent"} {
			t.Run(boundary+"_"+scope, func(t *testing.T) {
				value, _ := newTestFilesystem(t)
				defer value.Close()
				root := value.roots[lifecycle.ManagedOutputRoot].absolute
				destination := "rules.md"
				authority := root
				if scope == "parent" {
					authority = filepath.Join(root, "nested")
					if err := os.Mkdir(authority, 0o700); err != nil {
						t.Fatal(err)
					}
					destination = "nested/rules.md"
				}
				expectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, destination)
				request := mutation(t, destination, []byte("new"), expectation)
				detached := authority + ".detached"
				move := func(call int) {
					if call != 2 {
						return
					}
					if err := os.Rename(authority, detached); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(authority, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				ops := &faultAtomicOperations{real: realAtomicOperations{}, failStage: "write"}
				if boundary == "after_final_sync" {
					ops.afterSync = move
				} else {
					ops.failStage = "write_and_sync"
					ops.failCall = 2
					ops.beforeSync = move
				}
				value.ops = ops
				result, err := value.ReplaceFile(context.Background(), request)
				if !errors.Is(err, fault.ErrConflict) || result.Visibility != lifecycle.FileNotApplied ||
					result.Durability != lifecycle.NamespaceNotStarted || result.Cleanup != lifecycle.CleanupRequired ||
					!result.CleanupArtifact.Empty() || result.RecoveryConflict.Reason != lifecycle.RecoveryAuthorityDetached || !result.Coherent() {
					t.Fatalf("detached previsibility cleanup = %+v, %v", result, err)
				}
				if _, err := os.Stat(detached); err != nil {
					t.Fatalf("detached cleanup authority missing: %v", err)
				}
			})
		}
	}
}

func TestAbsentTemporaryCleanupSyncFailureRetainsRecoveryObligation(t *testing.T) {
	value, config := newTestFilesystem(t)
	root := value.roots[lifecycle.ManagedOutputRoot].absolute
	expectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
	request := mutation(t, "rules.md", []byte("new"), expectation)
	value.ops = &faultAtomicOperations{
		real: realAtomicOperations{}, failStage: "write_and_sync", failCall: 1,
		beforeWriteFailure: func() {
			if err := os.Remove(filepath.Join(root, request.Artifacts.TemporaryName)); err != nil {
				t.Fatal(err)
			}
		},
	}
	result, err := value.ReplaceFile(context.Background(), request)
	if err == nil || result.Visibility != lifecycle.FileNotApplied || result.Cleanup != lifecycle.CleanupRequired ||
		result.RecoveryConflict.Reason != lifecycle.RecoveryObservationFailed || !result.Coherent() {
		t.Fatalf("absent-temp sync failure = %+v, %v", result, err)
	}
	if err := value.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, request.Artifacts.TemporaryName), []byte("replayed"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	inspection, inspectErr := restarted.InspectFileArtifacts(context.Background(), artifactInspectionRequest(t, request, lifecycle.FileExpectation{}))
	if inspectErr != nil || len(inspection.Artifacts) != 0 || len(inspection.Conflicts) != 1 || !inspection.Coherent() {
		t.Fatalf("replayed absent-temp inspection = %+v, %v", inspection, inspectErr)
	}
	assertContent(t, filepath.Join(root, request.Artifacts.TemporaryName), "replayed")
}

func TestAtomicPreVisibilityRacesDoNotMutateDestination(t *testing.T) {
	for _, race := range []struct {
		name   string
		mutate func(t *testing.T, value *Filesystem, root *rootedDirectory, destination string) func()
		assert func(t *testing.T, destination string)
	}{
		{name: "checksum", mutate: func(t *testing.T, _ *Filesystem, _ *rootedDirectory, destination string) func() {
			if err := os.WriteFile(destination, []byte("concurrent"), 0o600); err != nil {
				t.Fatal(err)
			}
			return func() {}
		}, assert: func(t *testing.T, destination string) { assertContent(t, destination, "concurrent") }},
		{name: "mode", mutate: func(t *testing.T, _ *Filesystem, _ *rootedDirectory, destination string) func() {
			if err := os.Chmod(destination, 0o640); err != nil {
				t.Fatal(err)
			}
			return func() {}
		}, assert: func(t *testing.T, destination string) { assertMode(t, destination, 0o640) }},
		{name: "owner policy", mutate: func(_ *testing.T, value *Filesystem, _ *rootedDirectory, _ string) func() {
			original := value.currentUID
			value.currentUID++
			return func() { value.currentUID = original }
		}, assert: func(t *testing.T, destination string) { assertContent(t, destination, "old") }},
		{name: "symlink", mutate: func(t *testing.T, _ *Filesystem, root *rootedDirectory, destination string) func() {
			outside := filepath.Join(root.absolute, "outside")
			if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(destination, destination+".saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, destination); err != nil {
				t.Fatal(err)
			}
			return func() {}
		}, assert: func(t *testing.T, destination string) {
			info, err := os.Lstat(destination)
			if err != nil || info.Mode()&fs.ModeSymlink == 0 {
				t.Fatalf("destination symlink = %v, %v", info, err)
			}
		}},
		{name: "mount identity", mutate: func(_ *testing.T, _ *Filesystem, root *rootedDirectory, _ string) func() {
			original := root.identity
			root.identity.Filesystem++
			return func() { root.identity = original }
		}, assert: func(t *testing.T, destination string) { assertContent(t, destination, "old") }},
	} {
		t.Run(race.name, func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			root := value.roots[lifecycle.ManagedOutputRoot]
			writeManaged(t, value, "rules.md", "old", 0o600)
			destination := filepath.Join(root.absolute, "rules.md")
			expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
			var restore func()
			ops := &faultAtomicOperations{real: realAtomicOperations{}}
			ops.beforeFinalValidation = func() { restore = race.mutate(t, value, root, destination) }
			ops.afterFinalValidation = func() { restore() }
			value.ops = ops
			result, err := value.ReplaceFile(context.Background(), mutation(t, "rules.md", []byte("new"), expectation))
			if !errors.Is(err, fault.ErrConflict) || result.Visibility != lifecycle.FileNotApplied ||
				result.Durability != lifecycle.NamespaceNotStarted || ops.swapCalls != 0 || !result.Coherent() {
				t.Fatalf("pre-visibility %s = %+v, %v; swaps=%d", race.name, result, err, ops.swapCalls)
			}
			race.assert(t, destination)
		})
	}

	t.Run("configured root move", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		root := value.roots[lifecycle.ManagedOutputRoot]
		writeManaged(t, value, "rules.md", "old", 0o600)
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		moved := root.absolute + ".moved"
		ops := &faultAtomicOperations{real: realAtomicOperations{}}
		ops.beforeFinalValidation = func() {
			if err := os.Rename(root.absolute, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(root.absolute, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		ops.afterFinalValidation = func() {
			if err := os.Remove(root.absolute); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(moved, root.absolute); err != nil {
				t.Fatal(err)
			}
		}
		value.ops = ops
		result, err := value.ReplaceFile(context.Background(), mutation(t, "rules.md", []byte("new"), expectation))
		if !errors.Is(err, fault.ErrConflict) || result.Visibility != lifecycle.FileNotApplied ||
			result.Durability != lifecycle.NamespaceNotStarted || ops.swapCalls != 0 {
			t.Fatalf("pre-visibility root move = %+v, %v; swaps=%d", result, err, ops.swapCalls)
		}
		assertContent(t, filepath.Join(root.absolute, "rules.md"), "old")
	})
}

func TestAtomicRacesAfterObservationPreserveCompleteObjects(t *testing.T) {
	for _, race := range []struct {
		name   string
		mutate func(t *testing.T, value *Filesystem, destination string)
	}{
		{name: "mode", mutate: func(t *testing.T, _ *Filesystem, destination string) {
			if err := os.Chmod(destination, 0o640); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "owner policy", mutate: func(_ *testing.T, value *Filesystem, _ string) {
			value.currentUID++
		}},
	} {
		t.Run(race.name, func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			writeManaged(t, value, "rules.md", "old", 0o600)
			destination := filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, "rules.md")
			expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
			originalUID := value.currentUID
			value.ops = &faultAtomicOperations{real: realAtomicOperations{}, beforeSwap: func(call int) {
				if call == 1 {
					race.mutate(t, value, destination)
				}
			}}
			request := mutation(t, "rules.md", []byte("new"), expectation)
			result, err := value.ReplaceFile(context.Background(), request)
			value.currentUID = originalUID
			if !errors.Is(err, fault.ErrConflict) || result.Visibility != lifecycle.FileIndeterminate || result.Cleanup != lifecycle.CleanupRequired {
				t.Fatalf("race result = %+v, %v", result, err)
			}
			assertContent(t, destination, "new")
			assertContent(t, filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, request.Artifacts.TemporaryName), "old")
		})
	}

	t.Run("symlink", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		writeManaged(t, value, "rules.md", "old", 0o600)
		root := value.roots[lifecycle.ManagedOutputRoot].absolute
		destination := filepath.Join(root, "rules.md")
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		outside := filepath.Join(canonicalTempDir(t), "outside")
		if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		value.ops = &faultAtomicOperations{real: realAtomicOperations{}, beforeSwap: func(call int) {
			if call != 1 {
				return
			}
			if err := os.Rename(destination, destination+".concurrent-old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, destination); err != nil {
				t.Fatal(err)
			}
		}}
		request := mutation(t, "rules.md", []byte("new"), expectation)
		result, err := value.ReplaceFile(context.Background(), request)
		if !errors.Is(err, fault.ErrConflict) || result.Cleanup != lifecycle.CleanupRequired {
			t.Fatalf("symlink race = %+v, %v", result, err)
		}
		assertContent(t, destination, "new")
		info, statErr := os.Lstat(filepath.Join(root, request.Artifacts.TemporaryName))
		if statErr != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Fatalf("concurrent symlink was not preserved: %v, %v", info, statErr)
		}
		assertContent(t, destination+".concurrent-old", "old")
	})

	t.Run("configured root substitution", func(t *testing.T) {
		value, config := newTestFilesystem(t)
		writeManaged(t, value, "rules.md", "old", 0o600)
		root := value.roots[lifecycle.ManagedOutputRoot].absolute
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		moved := root + ".moved"
		value.ops = &faultAtomicOperations{real: realAtomicOperations{}, beforeSwap: func(call int) {
			if call != 1 {
				return
			}
			if err := os.Rename(root, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
		}}
		request := mutation(t, "rules.md", []byte("new"), expectation)
		result, err := value.ReplaceFile(context.Background(), request)
		if !errors.Is(err, fault.ErrConflict) || result.Cleanup != lifecycle.CleanupRequired ||
			!result.RecoveryConflict.Valid() || result.RecoveryConflict.Reason != lifecycle.RecoveryAuthorityDetached ||
			!result.CleanupArtifact.Empty() || !result.Coherent() {
			t.Fatalf("root substitution = %+v, %v", result, err)
		}
		assertContent(t, filepath.Join(moved, "rules.md"), "new")
		assertContent(t, filepath.Join(moved, request.Artifacts.TemporaryName), "old")
		if err := value.Close(); err != nil {
			t.Fatal(err)
		}
		restarted, restartErr := New(context.Background(), config)
		if restartErr != nil {
			t.Fatal(restartErr)
		}
		defer restarted.Close()
		inspection, inspectErr := restarted.InspectFileArtifacts(context.Background(), artifactInspectionRequest(t, request, result.VisibleExpectation))
		if !errors.Is(inspectErr, fault.ErrConflict) || len(inspection.Artifacts) != 0 || len(inspection.Conflicts) != 1 ||
			inspection.Conflicts[0].Reason != lifecycle.RecoveryAuthorityDetached {
			t.Fatalf("detached restart inspection = %+v, %v", inspection, inspectErr)
		}
		stale := cleanupArtifactForMutation(request, request.Artifacts.TemporaryName, expectation)
		if cleanup, cleanupErr := restarted.CleanupFile(context.Background(), stale); !errors.Is(cleanupErr, fault.ErrConflict) ||
			cleanup.Cleanup != lifecycle.CleanupRequired || !cleanup.Artifact.Empty() ||
			cleanup.RecoveryConflict.Reason != lifecycle.RecoveryAuthorityDetached || !cleanup.Coherent() {
			t.Fatalf("detached restart cleanup = %+v, %v", cleanup, cleanupErr)
		}
		assertContent(t, filepath.Join(moved, request.Artifacts.TemporaryName), "old")
	})

	t.Run("post-swap destination rewrite", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		writeManaged(t, value, "rules.md", "old", 0o600)
		root := value.roots[lifecycle.ManagedOutputRoot].absolute
		destination := filepath.Join(root, "rules.md")
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		value.ops = &faultAtomicOperations{real: realAtomicOperations{}, afterSwap: func(call int) {
			if call == 1 {
				if err := os.WriteFile(destination, []byte("bad"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}}
		request := mutation(t, "rules.md", []byte("new"), expectation)
		result, err := value.ReplaceFile(context.Background(), request)
		if !errors.Is(err, fault.ErrConflict) || result.Cleanup != lifecycle.CleanupRequired {
			t.Fatalf("post-swap rewrite = %+v, %v", result, err)
		}
		assertContent(t, destination, "bad")
		assertContent(t, filepath.Join(root, request.Artifacts.TemporaryName), "old")
	})
}

func TestSwapWindowConflictsAreNeverCleanupArtifacts(t *testing.T) {
	for _, race := range []struct {
		name   string
		mutate func(t *testing.T, root, destination string)
		assert func(t *testing.T, temporary string)
	}{
		{name: "checksum", mutate: func(t *testing.T, _, destination string) {
			if err := os.WriteFile(destination, []byte("bad"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, assert: func(t *testing.T, temporary string) { assertContent(t, temporary, "bad") }},
		{name: "mode", mutate: func(t *testing.T, _, destination string) {
			if err := os.Chmod(destination, 0o640); err != nil {
				t.Fatal(err)
			}
		}, assert: func(t *testing.T, temporary string) { assertMode(t, temporary, 0o640) }},
		{name: "symlink", mutate: func(t *testing.T, root, destination string) {
			outside := filepath.Join(root, "outside")
			if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(destination, destination+".saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, destination); err != nil {
				t.Fatal(err)
			}
		}, assert: func(t *testing.T, temporary string) {
			info, err := os.Lstat(temporary)
			if err != nil || info.Mode()&fs.ModeSymlink == 0 {
				t.Fatalf("preserved symlink = %v, %v", info, err)
			}
		}},
	} {
		t.Run(race.name, func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			writeManaged(t, value, "rules.md", "old", 0o600)
			root := value.roots[lifecycle.ManagedOutputRoot].absolute
			destination := filepath.Join(root, "rules.md")
			expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
			request := mutation(t, "rules.md", []byte("new"), expectation)
			value.ops = &faultAtomicOperations{real: realAtomicOperations{}, beforeSwap: func(call int) {
				if call == 1 {
					race.mutate(t, root, destination)
				}
			}}
			result, err := value.ReplaceFile(context.Background(), request)
			if !errors.Is(err, fault.ErrConflict) || !result.RecoveryConflict.Valid() || !result.CleanupArtifact.Empty() || !result.Coherent() {
				t.Fatalf("swap-window result = %+v, %v", result, err)
			}
			value.ops = realAtomicOperations{}
			inspection, inspectErr := value.InspectFileArtifacts(context.Background(), artifactInspectionRequest(t, request, result.VisibleExpectation))
			if inspectErr != nil || len(inspection.Artifacts) != 0 || len(inspection.Conflicts) != 1 || !inspection.Conflicts[0].Valid() {
				t.Fatalf("swap-window inspection = %+v, %v", inspection, inspectErr)
			}
			temporary := filepath.Join(root, request.Artifacts.TemporaryName)
			stale := cleanupArtifactForMutation(request, request.Artifacts.TemporaryName, expectation)
			if cleanup, cleanupErr := value.CleanupFile(context.Background(), stale); !errors.Is(cleanupErr, fault.ErrConflict) ||
				cleanup.Cleanup != lifecycle.CleanupRequired || !cleanup.Artifact.Empty() ||
				!cleanup.RecoveryConflict.Valid() || !cleanup.Coherent() {
				t.Fatalf("stale cleanup = %+v, %v", cleanup, cleanupErr)
			}
			race.assert(t, temporary)
		})
	}
}

func TestAtomicCleanupRevalidatesFullStateImmediatelyBeforeRemoval(t *testing.T) {
	for _, race := range []struct {
		name   string
		mutate func(t *testing.T, name string)
	}{
		{name: "checksum", mutate: func(t *testing.T, name string) {
			if err := os.WriteFile(name, []byte("bad"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "mode", mutate: func(t *testing.T, name string) {
			if err := os.Chmod(name, 0o640); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(race.name, func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			writeManaged(t, value, "rules.md", "old", 0o600)
			root := value.roots[lifecycle.ManagedOutputRoot].absolute
			expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
			request := mutation(t, "rules.md", []byte("new"), expectation)
			value.ops = &faultAtomicOperations{real: realAtomicOperations{}, beforeExclusive: func() {
				race.mutate(t, filepath.Join(root, request.Artifacts.TemporaryName))
			}}
			result, err := value.ReplaceFile(context.Background(), request)
			if !errors.Is(err, fault.ErrConflict) || result.Cleanup != lifecycle.CleanupRequired ||
				!result.RecoveryConflict.Valid() || !result.CleanupArtifact.Empty() || !result.Coherent() {
				t.Fatalf("cleanup race = %+v, %v", result, err)
			}
			assertContent(t, filepath.Join(root, "rules.md"), "new")
		})
	}

	t.Run("substitution before remove validation", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		writeManaged(t, value, "rules.md", "old", 0o600)
		root := value.roots[lifecycle.ManagedOutputRoot].absolute
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		var saved string
		value.ops = &faultAtomicOperations{real: realAtomicOperations{}, beforeRemoveValidation: func(name string) {
			quarantine := filepath.Join(root, name)
			saved = quarantine + ".saved"
			if err := os.Rename(quarantine, saved); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(quarantine, []byte("concurrent"), 0o600); err != nil {
				t.Fatal(err)
			}
		}}
		result, err := value.ReplaceFile(context.Background(), mutation(t, "rules.md", []byte("new"), expectation))
		if !errors.Is(err, fault.ErrConflict) || result.Cleanup != lifecycle.CleanupRequired ||
			!result.RecoveryConflict.Valid() || !result.CleanupArtifact.Empty() {
			t.Fatalf("pre-remove substitution = %+v, %v", result, err)
		}
		assertContent(t, saved, "old")
		assertContent(t, filepath.Join(root, result.RecoveryConflict.Path), "concurrent")
	})

	t.Run("visible destination rewrite immediately before displaced removal", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		writeManaged(t, value, "rules.md", "old", 0o600)
		root := value.roots[lifecycle.ManagedOutputRoot].absolute
		destination := filepath.Join(root, "rules.md")
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		value.ops = &faultAtomicOperations{real: realAtomicOperations{}, beforeRemoveValidation: func(string) {
			if err := os.WriteFile(destination, []byte("bad"), 0o600); err != nil {
				t.Fatal(err)
			}
		}}
		result, err := value.ReplaceFile(context.Background(), mutation(t, "rules.md", []byte("new"), expectation))
		if !errors.Is(err, fault.ErrConflict) || result.Cleanup != lifecycle.CleanupRequired || !result.CleanupArtifact.Valid() {
			t.Fatalf("destination rewrite at cleanup boundary = %+v, %v", result, err)
		}
		assertContent(t, destination, "bad")
		assertContent(t, filepath.Join(root, result.CleanupArtifact.Path), "old")
	})
}

func TestDeterministicArtifactCandidatesFailClosedBeforeMutation(t *testing.T) {
	for _, candidates := range [][]string{{"temporary"}, {"quarantine"}, {"temporary", "quarantine"}} {
		t.Run(strings.Join(candidates, "_"), func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			root := value.roots[lifecycle.ManagedOutputRoot].absolute
			expectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
			request := mutation(t, "rules.md", []byte("new"), expectation)
			for _, candidate := range candidates {
				name := request.Artifacts.TemporaryName
				if candidate == "quarantine" {
					name = request.Artifacts.QuarantineName
				}
				if err := os.WriteFile(filepath.Join(root, name), []byte("stale"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := value.ReplaceFile(context.Background(), request)
			if !errors.Is(err, fault.ErrConflict) || result.Visibility != lifecycle.FileNotApplied ||
				result.Durability != lifecycle.NamespaceNotStarted || result.Cleanup != lifecycle.CleanupRequired ||
				!result.RecoveryConflict.Valid() || !result.CleanupArtifact.Empty() || !result.Coherent() {
				t.Fatalf("stale candidate result = %+v, %v", result, err)
			}
			if _, err := os.Lstat(filepath.Join(root, "rules.md")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("destination mutated before recovery: %v", err)
			}
		})
	}
}

func TestDestinationCannotAliasItsOperationArtifactNamespace(t *testing.T) {
	for _, candidate := range []string{"temporary", "quarantine", "case_alias", "reserved_case", "reserved_nfc", "reserved_nfd"} {
		t.Run(candidate, func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			seedExpectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, "placeholder")
			request := mutation(t, "placeholder", []byte("new"), seedExpectation)
			destination := request.Artifacts.TemporaryName
			if candidate == "quarantine" {
				destination = request.Artifacts.QuarantineName
			} else if candidate == "case_alias" {
				destination = strings.ToUpper(request.Artifacts.TemporaryName)
				root := value.roots[lifecycle.ManagedOutputRoot].absolute
				lower := filepath.Join(root, request.Artifacts.TemporaryName)
				if err := os.WriteFile(lower, []byte("alias-probe"), 0o600); err != nil {
					t.Fatal(err)
				}
				if sameOpenedObject(t, lower, filepath.Join(root, destination)) {
					t.Log("fixture confirms case-folded aliasing filesystem")
				}
				if err := os.Remove(lower); err != nil {
					t.Fatal(err)
				}
			} else if candidate == "reserved_case" {
				destination = ".AI4J-user-file"
			} else if candidate == "reserved_nfc" {
				destination = ".ai4j-caf\u00e9"
			} else if candidate == "reserved_nfd" {
				destination = ".ai4j-cafe\u0301"
			}
			request.Destination = destination
			request.Expected = absentExpectation(t, value, lifecycle.ManagedOutputRoot, destination)
			if request.Valid() {
				t.Fatalf("mutation accepted artifact-namespace destination %q", destination)
			}
			result, err := value.ReplaceFile(context.Background(), request)
			if err == nil || result.Visibility != lifecycle.FileNotApplied || result.Durability != lifecycle.NamespaceNotStarted {
				t.Fatalf("artifact-namespace mutation = %+v, %v", result, err)
			}
			inspectionRequest := artifactInspectionRequest(t, request, lifecycle.FileExpectation{})
			if inspectionRequest.Valid() {
				t.Fatalf("inspection accepted artifact-namespace destination %q", destination)
			}
			if inspection, inspectErr := value.InspectFileArtifacts(context.Background(), inspectionRequest); inspectErr == nil || len(inspection.Artifacts) != 0 {
				t.Fatalf("artifact-namespace inspection = %+v, %v", inspection, inspectErr)
			}
			root := value.roots[lifecycle.ManagedOutputRoot].absolute
			for _, name := range []string{request.Artifacts.TemporaryName, request.Artifacts.QuarantineName, destination} {
				if _, statErr := os.Lstat(filepath.Join(root, name)); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("artifact-namespace request created %q: %v", name, statErr)
				}
			}
		})
	}
}

func TestCrashRecoveryInspectsAndCleansOnlyDeterministicArtifacts(t *testing.T) {
	t.Run("quarantine after crash", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		writeManaged(t, value, "rules.md", "old", 0o600)
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		request := mutation(t, "rules.md", []byte("new"), expectation)
		value.ops = &faultAtomicOperations{real: realAtomicOperations{}, failStage: "sync_directory", failCall: 2}
		mutationResult, mutationErr := value.ReplaceFile(context.Background(), request)
		if mutationErr == nil {
			t.Fatal("simulated quarantine crash window did not fail")
		}
		value.ops = realAtomicOperations{}
		inspection, err := value.InspectFileArtifacts(context.Background(), artifactInspectionRequest(t, request, mutationResult.VisibleExpectation))
		if err != nil || len(inspection.Artifacts) != 1 || !strings.HasSuffix(inspection.Artifacts[0].Path, request.Artifacts.QuarantineName) {
			t.Fatalf("artifact inspection = %+v, %v", inspection, err)
		}
		cleanup, err := value.CleanupFile(context.Background(), inspection.Artifacts[0])
		if err != nil || cleanup.Cleanup != lifecycle.CleanupComplete || !cleanup.Artifact.Empty() {
			t.Fatalf("cleanup = %+v, %v", cleanup, err)
		}
	})

	t.Run("temporary before result", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		root := value.roots[lifecycle.ManagedOutputRoot].absolute
		expectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		request := mutation(t, "rules.md", []byte("new"), expectation)
		if err := os.WriteFile(filepath.Join(root, request.Artifacts.TemporaryName), []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		inspection, err := value.InspectFileArtifacts(context.Background(), artifactInspectionRequest(t, request, lifecycle.FileExpectation{}))
		if err != nil || len(inspection.Artifacts) != 1 {
			t.Fatalf("temporary inspection = %+v, %v", inspection, err)
		}
		if cleanup, err := value.CleanupFile(context.Background(), inspection.Artifacts[0]); err != nil || cleanup.Cleanup != lifecycle.CleanupComplete {
			t.Fatalf("temporary cleanup = %+v, %v", cleanup, err)
		}
	})

	t.Run("partial temporary before result is do-not-delete", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		root := value.roots[lifecycle.ManagedOutputRoot].absolute
		expectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		request := mutation(t, "rules.md", []byte("new"), expectation)
		temporary := filepath.Join(root, request.Artifacts.TemporaryName)
		if err := os.WriteFile(temporary, []byte("n"), 0o600); err != nil {
			t.Fatal(err)
		}
		inspection, err := value.InspectFileArtifacts(context.Background(), artifactInspectionRequest(t, request, lifecycle.FileExpectation{}))
		if err != nil || len(inspection.Artifacts) != 0 || len(inspection.Conflicts) != 1 || !inspection.Conflicts[0].Valid() {
			t.Fatalf("partial temporary inspection = %+v, %v", inspection, err)
		}
		assertContent(t, temporary, "n")
	})

	t.Run("unsynced unlink uses preplanned predicate", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		writeManaged(t, value, "rules.md", "old", 0o600)
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		request := mutation(t, "rules.md", []byte("new"), expectation)
		value.ops = &faultAtomicOperations{real: realAtomicOperations{}, failStage: "sync_directory", failCall: 3}
		if _, err := value.ReplaceFile(context.Background(), request); err == nil {
			t.Fatal("simulated removal-sync crash window did not fail")
		}
		value.ops = realAtomicOperations{}
		artifact := cleanupArtifactForMutation(request, request.Artifacts.QuarantineName, expectation)
		cleanup, err := value.CleanupFile(context.Background(), artifact)
		if err != nil || cleanup.Cleanup != lifecycle.CleanupComplete {
			t.Fatalf("unsynced-unlink cleanup = %+v, %v", cleanup, err)
		}
	})

	t.Run("stale temporary handle discovers quarantined sibling after restart", func(t *testing.T) {
		value, config := newTestFilesystem(t)
		root := value.roots[lifecycle.ManagedOutputRoot].absolute
		writeManaged(t, value, "rules.md", "old", 0o600)
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		request := mutation(t, "rules.md", []byte("new"), expectation)
		if err := os.Rename(filepath.Join(root, "rules.md"), filepath.Join(root, request.Artifacts.QuarantineName)); err != nil {
			t.Fatal(err)
		}
		stale := cleanupArtifactForMutation(request, request.Artifacts.TemporaryName, expectation)
		if err := value.Close(); err != nil {
			t.Fatal(err)
		}

		restarted, err := New(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		defer restarted.Close()
		cleanup, err := restarted.CleanupFile(context.Background(), stale)
		if err != nil || cleanup.Cleanup != lifecycle.CleanupComplete || !cleanup.Coherent() {
			t.Fatalf("stale temporary cleanup = %+v, %v", cleanup, err)
		}
		if _, err := os.Lstat(filepath.Join(root, request.Artifacts.QuarantineName)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("quarantine sibling survived stale-handle cleanup: %v", err)
		}
	})

	t.Run("stale quarantine handle discovers temporary sibling after restart", func(t *testing.T) {
		value, config := newTestFilesystem(t)
		root := value.roots[lifecycle.ManagedOutputRoot].absolute
		writeManaged(t, value, "rules.md", "old", 0o600)
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		request := mutation(t, "rules.md", []byte("new"), expectation)
		if err := os.Rename(filepath.Join(root, "rules.md"), filepath.Join(root, request.Artifacts.TemporaryName)); err != nil {
			t.Fatal(err)
		}
		stale := cleanupArtifactForMutation(request, request.Artifacts.QuarantineName, expectation)
		if err := value.Close(); err != nil {
			t.Fatal(err)
		}

		restarted, err := New(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		defer restarted.Close()
		cleanup, err := restarted.CleanupFile(context.Background(), stale)
		if err != nil || cleanup.Cleanup != lifecycle.CleanupComplete || !cleanup.Coherent() {
			t.Fatalf("stale quarantine cleanup = %+v, %v", cleanup, err)
		}
		for _, name := range []string{request.Artifacts.TemporaryName, request.Artifacts.QuarantineName} {
			if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("operation sibling %q survived stale-handle cleanup: %v", name, err)
			}
		}
	})
}

func TestArtifactInspectionRejectsFinalAuthorityMove(t *testing.T) {
	for _, scope := range []string{"root", "parent"} {
		t.Run(scope, func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			root := value.roots[lifecycle.ManagedOutputRoot].absolute
			destination := "rules.md"
			authority := root
			if scope == "parent" {
				authority = filepath.Join(root, "nested")
				if err := os.Mkdir(authority, 0o700); err != nil {
					t.Fatal(err)
				}
				destination = "nested/rules.md"
			}
			expectation := absentExpectation(t, value, lifecycle.ManagedOutputRoot, destination)
			request := mutation(t, destination, []byte("new"), expectation)
			detached := authority + ".detached"
			value.ops = &faultAtomicOperations{real: realAtomicOperations{}, afterArtifactInspection: func() {
				if err := os.Rename(authority, detached); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(authority, 0o700); err != nil {
					t.Fatal(err)
				}
			}}
			inspection, err := value.InspectFileArtifacts(context.Background(), artifactInspectionRequest(t, request, lifecycle.FileExpectation{}))
			if !errors.Is(err, fault.ErrConflict) || len(inspection.Artifacts) != 0 || len(inspection.Conflicts) != 1 ||
				inspection.Conflicts[0].Reason != lifecycle.RecoveryAuthorityDetached || !inspection.Coherent() {
				t.Fatalf("detached artifact inspection = %+v, %v", inspection, err)
			}
			if _, err := os.Stat(detached); err != nil {
				t.Fatalf("detached authority missing: %v", err)
			}
		})
	}
}

func TestCleanupFileRefusesAuthorityAndLeafSubstitution(t *testing.T) {
	t.Run("leaf", func(t *testing.T) {
		value, _ := newTestFilesystem(t)
		defer value.Close()
		writeManaged(t, value, "rules.md", "old", 0o600)
		expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
		request := mutation(t, "rules.md", []byte("new"), expectation)
		value.ops = &faultAtomicOperations{real: realAtomicOperations{}, failStage: "remove"}
		result, err := value.ReplaceFile(context.Background(), request)
		if err == nil || !result.CleanupArtifact.Valid() {
			t.Fatalf("cleanup fixture = %+v, %v", result, err)
		}
		value.ops = realAtomicOperations{}
		artifactPath := filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, result.CleanupArtifact.Path)
		if err := os.Remove(artifactPath); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(artifactPath, []byte("concurrent"), 0o600); err != nil {
			t.Fatal(err)
		}
		cleanup, err := value.CleanupFile(context.Background(), result.CleanupArtifact)
		if !errors.Is(err, fault.ErrConflict) || cleanup.Cleanup != lifecycle.CleanupRequired ||
			!cleanup.Artifact.Empty() || !cleanup.RecoveryConflict.Valid() || !cleanup.Coherent() {
			t.Fatalf("leaf substitution cleanup = %+v, %v", cleanup, err)
		}
		assertContent(t, artifactPath, "concurrent")
	})

	for _, kind := range []string{"symlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			root := value.roots[lifecycle.ManagedOutputRoot].absolute
			writeManaged(t, value, "rules.md", "old", 0o600)
			expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, "rules.md")
			request := mutation(t, "rules.md", []byte("new"), expectation)
			artifactPath := filepath.Join(root, request.Artifacts.TemporaryName)
			if kind == "symlink" {
				if err := os.Symlink("rules.md", artifactPath); err != nil {
					t.Fatal(err)
				}
			} else if err := syscall.Mkfifo(artifactPath, 0o600); err != nil {
				t.Fatal(err)
			}
			artifact := cleanupArtifactForMutation(request, request.Artifacts.TemporaryName, expectation)
			cleanup, err := value.CleanupFile(context.Background(), artifact)
			if !errors.Is(err, fault.ErrConflict) || cleanup.Cleanup != lifecycle.CleanupRequired ||
				!cleanup.Artifact.Empty() || cleanup.RecoveryConflict.Reason != lifecycle.RecoveryUnsafeObject || !cleanup.Coherent() {
				t.Fatalf("unsafe %s cleanup = %+v, %v", kind, cleanup, err)
			}
			if _, err := os.Lstat(artifactPath); err != nil {
				t.Fatalf("unsafe %s artifact was not preserved: %v", kind, err)
			}
		})
	}

	for _, scope := range []string{"root", "parent"} {
		t.Run(scope, func(t *testing.T) {
			value, _ := newTestFilesystem(t)
			defer value.Close()
			root := value.roots[lifecycle.ManagedOutputRoot].absolute
			destination := "rules.md"
			if scope == "parent" {
				if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Join(root, "nested"), 0o700); err != nil {
					t.Fatal(err)
				}
				destination = "nested/rules.md"
			}
			writeManaged(t, value, destination, "old", 0o600)
			expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, destination)
			request := mutation(t, destination, []byte("new"), expectation)
			value.ops = &faultAtomicOperations{real: realAtomicOperations{}, failStage: "remove"}
			result, err := value.ReplaceFile(context.Background(), request)
			if err == nil || !result.CleanupArtifact.Valid() {
				t.Fatalf("cleanup fixture = %+v, %v", result, err)
			}
			value.ops = realAtomicOperations{}
			moved := root + ".moved"
			if scope == "parent" {
				parent := filepath.Join(root, "nested")
				moved = parent + ".moved"
				if err := os.Rename(parent, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(parent, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(root, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			cleanup, err := value.CleanupFile(context.Background(), result.CleanupArtifact)
			if !errors.Is(err, fault.ErrConflict) || cleanup.Cleanup != lifecycle.CleanupRequired {
				t.Fatalf("%s substitution cleanup = %+v, %v", scope, cleanup, err)
			}
			artifactName := filepath.Base(result.CleanupArtifact.Path)
			if _, err := os.Lstat(filepath.Join(moved, artifactName)); err != nil {
				t.Fatalf("recovery artifact was not preserved: %v", err)
			}
		})
	}
}

func TestCleanupFileReportsAuthorityDetachmentAfterFinalSync(t *testing.T) {
	for _, phase := range []string{"absent_sync", "remove_sync"} {
		for _, scope := range []string{"root", "parent"} {
			t.Run(phase+"_"+scope, func(t *testing.T) {
				value, config := newTestFilesystem(t)
				root := value.roots[lifecycle.ManagedOutputRoot].absolute
				destination := "rules.md"
				parentPath := root
				artifactPrefix := ""
				if scope == "parent" {
					parentPath = filepath.Join(root, "nested")
					if err := os.Mkdir(parentPath, 0o700); err != nil {
						t.Fatal(err)
					}
					destination = "nested/rules.md"
					artifactPrefix = "nested/"
				}
				writeManaged(t, value, destination, "old", 0o600)
				expectation := presentExpectation(t, value, lifecycle.ManagedOutputRoot, destination)
				request := mutation(t, destination, []byte("new"), expectation)
				artifactPath := artifactPrefix + request.Artifacts.QuarantineName
				artifact := cleanupArtifactForMutation(request, artifactPath, expectation)
				if phase == "remove_sync" {
					if err := os.Rename(filepath.Join(root, filepath.FromSlash(destination)), filepath.Join(root, filepath.FromSlash(artifactPath))); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Remove(filepath.Join(root, filepath.FromSlash(destination))); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(parentPath, "unrelated"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}

				var detached string
				value.ops = &faultAtomicOperations{real: realAtomicOperations{}, afterSync: func(call int) {
					if call != 1 {
						return
					}
					authority := root
					if scope == "parent" {
						authority = parentPath
					}
					detached = authority + ".detached"
					if err := os.Rename(authority, detached); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(authority, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(artifactPath)), []byte("replacement"), 0o600); err != nil {
						t.Fatal(err)
					}
				}}
				cleanup, err := value.CleanupFile(context.Background(), artifact)
				if !errors.Is(err, fault.ErrConflict) || cleanup.Cleanup != lifecycle.CleanupRequired ||
					!cleanup.Artifact.Empty() || cleanup.RecoveryConflict.Reason != lifecycle.RecoveryAuthorityDetached || !cleanup.Coherent() {
					t.Fatalf("detached cleanup = %+v, %v", cleanup, err)
				}
				assertContent(t, filepath.Join(root, filepath.FromSlash(artifactPath)), "replacement")
				assertContent(t, filepath.Join(detached, "unrelated"), "keep")
				if err := value.Close(); err != nil {
					t.Fatal(err)
				}

				restarted, err := New(context.Background(), config)
				if err != nil {
					t.Fatal(err)
				}
				defer restarted.Close()
				restartedCleanup, restartErr := restarted.CleanupFile(context.Background(), artifact)
				if !errors.Is(restartErr, fault.ErrConflict) || restartedCleanup.Cleanup != lifecycle.CleanupRequired ||
					!restartedCleanup.Artifact.Empty() || restartedCleanup.RecoveryConflict.Reason != lifecycle.RecoveryAuthorityDetached || !restartedCleanup.Coherent() {
					t.Fatalf("restarted detached cleanup = %+v, %v", restartedCleanup, restartErr)
				}
				assertContent(t, filepath.Join(root, filepath.FromSlash(artifactPath)), "replacement")
				assertContent(t, filepath.Join(detached, "unrelated"), "keep")
			})
		}
	}
}

type faultAtomicOperations struct {
	real                    realAtomicOperations
	failStage               string
	failCall                int
	beforeExclusive         func()
	beforeSwap              func(int)
	afterSwap               func(int)
	swapCalls               int
	exclusiveCalls          int
	syncCalls               int
	removeCalls             int
	beforeRemoveValidation  func(string)
	beforeFinalValidation   func()
	afterFinalValidation    func()
	afterArtifactInspection func()
	afterSync               func(int)
	beforeSync              func(int)
	beforeWriteFailure      func()
}

func (o *faultAtomicOperations) BeforeFinalValidation() {
	if o.beforeFinalValidation != nil {
		o.beforeFinalValidation()
	}
}

func (o *faultAtomicOperations) AfterFinalValidation() {
	if o.afterFinalValidation != nil {
		o.afterFinalValidation()
	}
}

func (o *faultAtomicOperations) AfterArtifactInspection() {
	if o.afterArtifactInspection != nil {
		o.afterArtifactInspection()
		o.afterArtifactInspection = nil
	}
}

func (o *faultAtomicOperations) BeforeRemoveValidation(_ *os.File, name string) {
	if o.beforeRemoveValidation != nil {
		o.beforeRemoveValidation(name)
		o.beforeRemoveValidation = nil
	}
}

var errInjected = errors.New("injected fault")

func (o *faultAtomicOperations) Create(root *os.File, name string, mode fs.FileMode) (*os.File, error) {
	if o.failStage == "create" {
		return nil, errInjected
	}
	return o.real.Create(root, name, mode)
}
func (o *faultAtomicOperations) Write(file *os.File, content []byte) error {
	if o.failStage == "write" || o.failStage == "write_and_sync" {
		if o.beforeWriteFailure != nil {
			o.beforeWriteFailure()
			o.beforeWriteFailure = nil
		}
		return errInjected
	}
	return o.real.Write(file, content)
}
func (o *faultAtomicOperations) SyncFile(file *os.File) error {
	if o.failStage == "sync_file" {
		return errInjected
	}
	return o.real.SyncFile(file)
}
func (o *faultAtomicOperations) RenameExclusive(directory *os.File, old, new string) error {
	o.exclusiveCalls++
	if o.beforeExclusive != nil {
		o.beforeExclusive()
		o.beforeExclusive = nil
	}
	if o.failStage == "rename" && o.matches(o.exclusiveCalls) {
		return errInjected
	}
	return o.real.RenameExclusive(directory, old, new)
}
func (o *faultAtomicOperations) Swap(directory *os.File, first, second string) error {
	o.swapCalls++
	if o.beforeSwap != nil {
		o.beforeSwap(o.swapCalls)
	}
	if o.failStage == "swap" {
		return errInjected
	}
	if err := o.real.Swap(directory, first, second); err != nil {
		return err
	}
	if o.afterSwap != nil {
		o.afterSwap(o.swapCalls)
	}
	return nil
}
func (o *faultAtomicOperations) SyncDirectory(directory *os.File) error {
	o.syncCalls++
	if o.beforeSync != nil {
		o.beforeSync(o.syncCalls)
	}
	if (o.failStage == "sync_directory" || o.failStage == "write_and_sync") && o.matches(o.syncCalls) {
		return errInjected
	}
	if err := o.real.SyncDirectory(directory); err != nil {
		return err
	}
	if o.afterSync != nil {
		o.afterSync(o.syncCalls)
	}
	return nil
}
func (o *faultAtomicOperations) Remove(root *os.File, name string) error {
	o.removeCalls++
	if o.failStage == "remove" && o.matches(o.removeCalls) {
		return errInjected
	}
	return o.real.Remove(root, name)
}

func (o *faultAtomicOperations) matches(call int) bool {
	target := o.failCall
	if target == 0 {
		target = 1
	}
	return call == target
}

func newTestFilesystem(t *testing.T) (*Filesystem, Config) {
	t.Helper()
	config := testConfig(t)
	value, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	return value, config
}

func newShortTestFilesystem(t *testing.T) (*Filesystem, Config) {
	t.Helper()
	parent, err := os.MkdirTemp("/tmp", "a4j-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(parent); err != nil {
			t.Errorf("remove short test filesystem: %v", err)
		}
	})
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	config := configUnderAncestor(t, parent)
	value, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	return value, config
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		BaseRoot: canonicalTempDir(t), StatePath: "state", RecoveryPath: "recovery",
		TemporarySourcePath: "sources", StagingPath: "staging",
		ManagedOutputRoot: canonicalTempDir(t), MaximumFileBytes: 1 << 20,
	}
}

func configUnderAncestor(t *testing.T, ancestor string) Config {
	t.Helper()
	base := filepath.Join(ancestor, "base")
	managed := filepath.Join(ancestor, "managed")
	for _, directory := range []string{base, managed} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return Config{
		BaseRoot: base, StatePath: "state", RecoveryPath: "recovery", TemporarySourcePath: "sources",
		StagingPath: "staging", ManagedOutputRoot: managed, MaximumFileBytes: 1 << 20,
	}
}

func requireDirectoryAlias(t *testing.T, actualName, aliasName string) (string, string) {
	t.Helper()
	parent := canonicalTempDir(t)
	actual := filepath.Join(parent, actualName)
	alias := filepath.Join(parent, aliasName)
	if err := os.Mkdir(actual, 0o700); err != nil {
		t.Fatal(err)
	}
	if !sameOpenedObject(t, actual, alias) {
		t.Skip("filesystem treats fixture spellings as distinct")
	}
	return actual, alias
}

func sameOpenedObject(t *testing.T, first, second string) bool {
	t.Helper()
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(second)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(firstInfo, secondInfo)
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func withUmask(t *testing.T, mask int, run func()) {
	t.Helper()
	previous := syscall.Umask(mask)
	defer syscall.Umask(previous)
	run()
}

func assertMode(t *testing.T, name string, want fs.FileMode) {
	t.Helper()
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %04o, want %04o", name, info.Mode().Perm(), want)
	}
}

func assertReturnsWithoutLeafOpen(t *testing.T, call func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		if !errors.Is(err, fault.ErrConflict) {
			t.Fatalf("error = %v, want conflict", err)
		}
	case <-time.After(time.Second):
		t.Fatal("inspection blocked opening a special file")
	}
}

func assertReturnsWithError(t *testing.T, call func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("unsafe inspection unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("inspection blocked opening a special file")
	}
}

func absentExpectation(t *testing.T, value *Filesystem, role lifecycle.RootRole, name string) lifecycle.FileExpectation {
	t.Helper()
	observation, err := value.CheckResource(context.Background(), lifecycle.ResourceRequest{Root: role, Path: name, Kind: lifecycle.RegularResource})
	if err != nil || observation.Exists {
		t.Fatalf("observe absent = %+v, %v", observation, err)
	}
	return lifecycle.FileExpectation{
		State: lifecycle.ExpectAbsent, RootIdentity: observation.RootIdentity, ParentIdentity: observation.ParentIdentity,
	}
}

func presentExpectation(t *testing.T, value *Filesystem, role lifecycle.RootRole, name string) lifecycle.FileExpectation {
	t.Helper()
	read, err := value.ReadResource(context.Background(), lifecycle.ResourceReadRequest{
		Resource: lifecycle.ResourceRequest{Root: role, Path: name, Kind: lifecycle.RegularResource, RequireCurrentOwner: true, RejectMultipleLinks: true},
		MaxBytes: value.maximumBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := renderedDigest(read.Content)
	if err != nil {
		t.Fatal(err)
	}
	return lifecycle.FileExpectation{
		State: lifecycle.ExpectPresent, Digest: digest, RootIdentity: read.Observation.RootIdentity,
		ParentIdentity: read.Observation.ParentIdentity, Identity: read.Observation.Identity,
		Mode: read.Observation.Mode, Size: read.Observation.Size, OwnedByCurrentUser: true,
	}
}

func mutation(t *testing.T, name string, content []byte, expectation lifecycle.FileExpectation) lifecycle.FileMutation {
	mode := fs.FileMode(0o600)
	if expectation.State == lifecycle.ExpectPresent {
		mode = 0
	}
	return mutationForRoot(t, lifecycle.ManagedOutputRoot, name, content, mode, expectation, 1)
}

func mutationForRoot(t *testing.T, root lifecycle.RootRole, name string, content []byte, mode fs.FileMode, expectation lifecycle.FileExpectation, sequence int) lifecycle.FileMutation {
	t.Helper()
	operation, err := domain.NewOperationID(fmt.Sprintf("operation-%d", sequence))
	if err != nil {
		t.Fatal(err)
	}
	token, err := domain.NewArtifactToken(fmt.Sprintf("%032x", sequence))
	if err != nil {
		t.Fatal(err)
	}
	artifacts, ok := lifecycle.PlanFileArtifacts(operation, token)
	if !ok {
		t.Fatal("artifact plan rejected")
	}
	return lifecycle.FileMutation{
		OperationID: operation, ArtifactToken: token, Artifacts: artifacts, Root: root, Destination: name,
		Content: content, Mode: mode, Expected: expectation,
	}
}

func artifactInspectionRequest(t *testing.T, mutation lifecycle.FileMutation, prepared lifecycle.FileExpectation) lifecycle.FileArtifactInspectionRequest {
	t.Helper()
	digest, err := renderedDigest(mutation.Content)
	if err != nil {
		t.Fatal(err)
	}
	desiredMode := mutation.Mode
	if desiredMode == 0 {
		desiredMode = mutation.Expected.Mode
	}
	return lifecycle.FileArtifactInspectionRequest{
		OperationID: mutation.OperationID, ArtifactToken: mutation.ArtifactToken, Artifacts: mutation.Artifacts,
		Root: mutation.Root, Destination: mutation.Destination,
		RootIdentity: mutation.Expected.RootIdentity, ParentIdentity: mutation.Expected.ParentIdentity,
		Preimage: mutation.Expected,
		Desired:  lifecycle.FileContentExpectation{Digest: digest, Mode: desiredMode, Size: int64(len(mutation.Content))},
		Prepared: prepared,
	}
}

func cleanupArtifactForMutation(mutation lifecycle.FileMutation, artifactPath string, expectation lifecycle.FileExpectation) lifecycle.CleanupArtifact {
	return lifecycle.CleanupArtifact{
		OperationID: mutation.OperationID, ArtifactToken: mutation.ArtifactToken, Artifacts: mutation.Artifacts,
		Root: mutation.Root, Path: artifactPath, Expected: expectation,
	}
}

func writeManaged(t *testing.T, value *Filesystem, name, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, name), []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(value.roots[lifecycle.ManagedOutputRoot].absolute, name), mode); err != nil {
		t.Fatal(err)
	}
}

func assertContent(t *testing.T, name, want string) {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
}
