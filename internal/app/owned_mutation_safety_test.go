package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/result"
)

func TestOwnedDriftAfterApprovalStopsBeforeJournalOrNativeMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy string
		path   func(lifecycleHarness, string) string
		mutate func(string) error
	}{
		{
			name: "fail modified catalog",
			path: func(harness lifecycleHarness, installationID string) string {
				record, _, _ := harness.store.LoadByID(installationID)
				return harness.service.catalogPath(record)
			},
			mutate: func(path string) error { return os.WriteFile(path, []byte("external catalog\n"), 0o600) },
		},
		{
			name:   "keep modified rules",
			policy: "keep",
			path: func(harness lifecycleHarness, installationID string) string {
				record, _, _ := harness.store.LoadByID(installationID)
				return harness.service.rulesPath(record)
			},
			mutate: func(path string) error { return os.WriteFile(path, []byte("external rules\n"), 0o600) },
		},
		{
			name:   "replace-owned unsafe rules",
			policy: "replace-owned",
			path: func(harness lifecycleHarness, installationID string) string {
				record, _, _ := harness.store.LoadByID(installationID)
				return harness.service.rulesPath(record)
			},
			mutate: func(path string) error {
				if err := os.Remove(path); err != nil {
					return err
				}
				return os.Mkdir(path, 0o700)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newLifecycleHarness(t)
			install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
			response, err := harness.service.Install(context.Background(), install, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("install = %#v, %v", response.Result(), err)
			}
			record, _, _ := harness.store.Load()
			stateBefore, err := os.ReadFile(harness.store.Path())
			if err != nil {
				t.Fatal(err)
			}
			historyBefore, err := harness.store.LoadHistory(record.InstallationID)
			if err != nil {
				t.Fatal(err)
			}
			commandsBefore := len(harness.native.commands)
			harness.validator.update = true
			arguments := []string{"update", record.InstallationID}
			if test.policy != "" {
				arguments = append(arguments, "--conflict-policy", test.policy)
			}
			request := parseRequest[cli.UpdateRequest](t, arguments...)
			path := test.path(harness, record.InstallationID)
			input := &ownedApprovalHookReader{
				reader: strings.NewReader("yes\n"),
				hook:   func() error { return test.mutate(path) },
			}

			response, err = harness.service.Update(context.Background(), request, CommandIO{Input: input, Output: io.Discard, Interactive: true})

			if err != nil || input.err != nil || !input.ran || response.Result().Failure() != result.FailureConflict || response.Result().Mutation() != result.MutationNotStarted {
				t.Fatalf("post-approval drift = %#v, commandErr=%v hookErr=%v ran=%t", response.Result(), err, input.err, input.ran)
			}
			if len(harness.native.commands) != commandsBefore {
				t.Fatalf("native commands = %d, want %d", len(harness.native.commands), commandsBefore)
			}
			if _, present, markerErr := harness.store.LoadMarker(); markerErr != nil || present {
				t.Fatalf("marker = present:%t err=%v", present, markerErr)
			}
			staged, stagedErr := harness.store.LoadStagedHistory()
			if stagedErr != nil || len(staged) != 0 {
				t.Fatalf("staged history = %#v, %v", staged, stagedErr)
			}
			historyAfter, historyErr := harness.store.LoadHistory(record.InstallationID)
			if historyErr != nil || len(historyAfter) != len(historyBefore) {
				t.Fatalf("history entries = %d/%d, %v", len(historyAfter), len(historyBefore), historyErr)
			}
			stateAfter, stateErr := os.ReadFile(harness.store.Path())
			if stateErr != nil || !bytes.Equal(stateAfter, stateBefore) {
				t.Fatalf("installation state changed: %v", stateErr)
			}
		})
	}
}

func TestReplaceOwnedRepairsPostApprovalRegularOrMissingOwnedFile(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(string) error
	}{
		{name: "modified", mutate: func(path string) error { return os.WriteFile(path, []byte("external rules\n"), 0o600) }},
		{name: "missing", mutate: os.Remove},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newLifecycleHarness(t)
			install := parseRequest[cli.InstallRequest](t, "install", "--target", "claude", "--scope", "user", "--bundle", "default", "--yes")
			response, err := harness.service.Install(context.Background(), install, CommandIO{})
			if err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("install = %#v, %v", response.Result(), err)
			}
			record, _, _ := harness.store.Load()
			harness.validator.update = true
			request := parseRequest[cli.UpdateRequest](t, "update", record.InstallationID, "--conflict-policy", "replace-owned")
			path := harness.service.rulesPath(record)
			input := &ownedApprovalHookReader{reader: strings.NewReader("yes\n"), hook: func() error { return test.mutate(path) }}

			response, err = harness.service.Update(context.Background(), request, CommandIO{Input: input, Output: io.Discard, Interactive: true})

			if err != nil || input.err != nil || response.Result().ExitCode() != result.ExitSuccess {
				t.Fatalf("replace-owned update = %#v, commandErr=%v hookErr=%v", response.Result(), err, input.err)
			}
			contents, readErr := os.ReadFile(path)
			if readErr != nil || string(contents) != "rules-default\n" {
				t.Fatalf("repaired rules = %q, %v", contents, readErr)
			}
		})
	}
}

func TestMutateOwnedChecksumRaceDoesNotReplaceOrRemove(t *testing.T) {
	for _, policy := range []cli.ConflictPolicy{cli.ConflictFail, cli.ConflictKeep} {
		for _, remove := range []bool{false, true} {
			name := string(policy) + " replace"
			var desired []byte
			if remove {
				name = string(policy) + " remove"
			} else {
				desired = []byte("desired\n")
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "owned.txt")
				owned := []byte("owned\n")
				external := []byte("external\n")
				if err := os.WriteFile(path, owned, 0o600); err != nil {
					t.Fatal(err)
				}

				err := mutateOwnedAfterInspection(root, path, sha256Digest(owned), desired, policy, func() error {
					return os.WriteFile(path, external, 0o600)
				})

				if err == nil {
					t.Fatal("checksum race was accepted")
				}
				contents, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(contents, external) {
					t.Fatalf("external content changed: %q, %v", contents, readErr)
				}
			})
		}
	}
}

func TestMutateOwnedReplaceOwnedRejectsUnsafeRace(t *testing.T) {
	for _, remove := range []bool{false, true} {
		name := "replace"
		var desired []byte
		if remove {
			name = "remove"
		} else {
			desired = []byte("desired\n")
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "owned.txt")
			if err := os.WriteFile(path, []byte("modified\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			err := mutateOwnedAfterInspection(root, path, sha256Digest([]byte("owned\n")), desired, cli.ConflictReplaceOwned, func() error {
				if err := os.Remove(path); err != nil {
					return err
				}
				return os.Mkdir(path, 0o700)
			})

			if err == nil {
				t.Fatal("unsafe race was accepted")
			}
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.IsDir() {
				t.Fatalf("unsafe destination changed: %#v, %v", info, statErr)
			}
		})
	}
}

type ownedApprovalHookReader struct {
	reader io.Reader
	hook   func() error
	ran    bool
	err    error
}

func (r *ownedApprovalHookReader) Read(buffer []byte) (int, error) {
	if !r.ran {
		r.ran = true
		r.err = r.hook()
		if r.err != nil {
			return 0, r.err
		}
	}
	return r.reader.Read(buffer)
}
