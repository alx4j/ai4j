# MVP-ST-025 — Update a tracked branch

| Field | Value |
|---|---|
| Status | Done |
| Type | Modifying lifecycle capability |
| Wave | 5 — Update and uninstall |
| Relative size | M |
| Depends on | MVP-ST-024 |
| Requirements | MVP-FR-03, MVP-FR-06 through MVP-FR-10, MVP-FR-13, MVP-FR-17, MVP-FR-18, MVP-FR-20 |
| MVP acceptance | 2, 5, 7, 10 through 15 |

## User story

As an AI4J user, I want a stored branch installation updated only to a reviewed fast-forward exact commit without executing installed content.

## MVP scope

1. Acquire the existing modifying-command lock and stop when an operation marker already requires attention.
2. Load the single installation and use only its stored public GitHub repository and requested reference.
3. Return `pinned` for tag/commit installations and `no_change` when the branch has not moved.
4. Resolve and validate a fast-forward branch candidate in a disposable workspace; reject source failure or rewritten history before mutation.
5. Validate the installed exact commit only to compute deterministic added, removed, changed, and unchanged active-content disclosure.
6. Verify current catalog, rules, marketplace, plugin, and enablement state; enforce approval and any supplied `--expected-commit` before writing a marker.
7. After the marker, atomically replace the checksum-matching catalog, refresh marketplace `ai4j`, update `ai4j-default@ai4j` at user scope, replace checksum-matching rules, and verify the desired observable state.
8. Preserve the installation ID, atomically commit the new exact source and checksums, then remove the marker.
9. On an ambiguous post-marker failure, retain the marker and report recovery required; do not implement rollback or compensation.

## Acceptance criteria

- [x] A validated fast-forward branch update applies the planned exact commit and records it only after verification.
- [x] Added, removed, changed, and unchanged active content is disclosed before approval.
- [x] Tag and commit installations are pinned; unchanged branches are `no_change`; rewritten branches do not mutate.
- [x] Missing approval, expected-commit mismatch, drift, native mismatch, lock contention, and pre-existing recovery state stop before mutation.
- [x] Catalog refresh precedes the documented user-scoped plugin update, and neither starts toolkit content.
- [x] Post-marker failure retains the marker and does not record the unverified commit.
- [x] Temporary source workspaces are removed and human/JSON results match the typed exit contract.

## Verification

- Exercise available, unchanged, rewritten, pinned, source-failure, approval, drift, native-failure, and interrupted-operation fixtures.
- Assert exact command order, preserved installation ID, new catalog/rules checksums, committed exact SHA, and no unrelated file changes.
- Assert repeat update is `no_change` and handled source workspaces are empty.

## Out of scope

- Source migration, rewritten-history acceptance, automatic tag movement, private sources, SSH, or credentials.
- Automatic recovery, rollback, compensation, retained old packages, native cache management, or session activation claims.
