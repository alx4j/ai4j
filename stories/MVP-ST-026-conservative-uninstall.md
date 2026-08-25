# MVP-ST-026 — Conservatively uninstall AI4J

| Field | Value |
|---|---|
| Status | Done |
| Type | Modifying lifecycle capability |
| Wave | 5 — Update and uninstall |
| Relative size | M |
| Depends on | MVP-ST-024 |
| Requirements | MVP-FR-06 through MVP-FR-09, MVP-FR-15 through MVP-FR-18, MVP-FR-20 |
| MVP acceptance | 5, 9 through 15 |

## User story

As an AI4J user, I want uninstall to remove the documented Claude registration and only checksum-proven AI4J-owned files while preserving unrelated content.

## MVP scope

1. Acquire the existing modifying-command lock and stop when an operation marker already requires attention.
2. Load the single installation, validate its stored exact public source for plan disclosure, and verify the plugin identity, marketplace identity, catalog checksum, and rules checksum before mutation.
3. Treat a disabled installed plugin as removable, but reject a missing/wrong plugin, missing marketplace, drift, unknown state, or inspection failure.
4. Recompute and disclose the complete uninstall plan; require approval before writing a marker.
5. After the marker, uninstall `ai4j-default@ai4j` from user scope with persistent data retained, then remove marketplace `ai4j` only from user scope.
6. Verify both native identities are absent, remove only checksum-matching catalog and rules files, verify their absence, and remove installation state last.
7. Remove the marker after state deletion. Repeating a completed uninstall returns `no_change`.
8. On an ambiguous post-marker failure, retain the marker and report recovery required; do not delete later resources or attempt rollback.

## Acceptance criteria

- [x] Every ownership, checksum, and native-identity check completes before the first removal.
- [x] Missing approval, drift, wrong identity, inspection failure, lock contention, and pre-existing recovery state cause zero mutation.
- [x] The documented user-scoped plugin uninstall with `--keep-data` precedes user-scoped marketplace removal.
- [x] Only matching AI4J-owned catalog, rules, installation state, and marker files are removed; unrelated files and other Claude identities remain.
- [x] Installation state is removed last, repeat uninstall is `no_change`, and Claude-owned cache retention is not treated as failure.
- [x] Post-marker failure retains the marker and stops further removal.
- [x] Human/JSON results and command ordering are deterministic and no installed content starts.

## Verification

- Exercise healthy, disabled, already absent, approval, checksum drift, missing identity, native failure, marker, and lock fixtures.
- Place unrelated Claude configuration, rules, marketplace, plugin, and persistent-data canaries around the lifecycle and assert they survive.
- Assert exact removal order, state-last behavior, repeat `no_change`, and no temporary source residue.

## Out of scope

- Purging Claude-owned caches or persistent plugin data, forced removal, reload, or current-session revocation.
- Automatic recovery, rollback, compensation, retained packages, multiple installations, scopes, or native consumer graphs.
