# MVP-ST-023 — Install and idempotently reconcile the user toolkit

| Field | Value |
|---|---|
| Status | Done |
| Type | User lifecycle capability |
| Wave | 3 — First complete installation |
| Relative size | L |
| Depends on | MVP-ST-014 through MVP-ST-020, MVP-ST-022 |
| Requirements | MVP-FR-08, MVP-FR-10, MVP-FR-17, MVP-FR-20, MVP-NFR-05 |
| MVP acceptance | 6, 10, 11, 12, 13 |

## User story

As an AI4J user, I want one approved command to install the exact reviewed toolkit through Claude's supported interface.

## Scope

1. Acquire the single installation lock and inspect any existing operation marker.
2. Resolve and validate the source, recompute the install plan, enforce `--expected-commit`, and obtain approval.
3. Write the minimal operation marker.
4. Atomically write the exact-SHA catalog and call the documented Claude marketplace/plugin operations at user scope.
5. Atomically create the dedicated rules file.
6. Inspect the resulting native and owned state, atomically commit installation state, clean owned temporary files, and remove the marker.
7. Return `no_change` with the same installation ID when the desired state is already present.

Every write repeats its ownership/checksum precondition immediately before mutation. Any ambiguous native result is inspected; if completion cannot be proven, retain the marker and return `recovery_required`.

## Acceptance criteria

- [x] A clean install reaches the planned exact commit and records the native identities plus catalog and rules checksums.
- [x] Install uses only documented Claude commands and never edits Claude-private caches or starts toolkit content.
- [x] Approval failure, drift, identity collision, expected-commit mismatch, and validation failure occur before target mutation.
- [x] A converged reinstall returns `no_change` and preserves the installation ID.
- [x] Handled failures either prove no persistent effect and clean up, or retain the marker and report `recovery_required` without claiming rollback.
- [x] Secret values and unrelated user files remain untouched.

## Verification

- Run a clean-profile vertical install through production dispatch with documented Claude command fixtures.
- Test no-op reinstall and failure at each catalog, native, rules, state, and cleanup boundary.
- Assert the final state matches the approved plan and all unrelated-state/no-execution sentinels remain unchanged.

## Out of scope

- Automatic rollback, compensation, preimages, durable history, multiple installations, alternate targets/scopes, or execution of installed content.
