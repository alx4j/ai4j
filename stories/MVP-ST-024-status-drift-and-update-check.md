# MVP-ST-024 — Inspect installation, drift, and updates

| Field | Value |
|---|---|
| Status | Done |
| Type | Read-only lifecycle capability |
| Wave | 4 — Inspection and update awareness |
| Relative size | M |
| Depends on | MVP-ST-023 |
| Requirements | MVP-FR-08, MVP-FR-14, MVP-FR-16, MVP-FR-17, MVP-FR-18 |
| MVP acceptance | 8, 10, 11, 14, 15, 17 |

## User story

As an AI4J user, I want to see the recorded installation, observable Claude plugin state, owned-file drift, interrupted-operation state, and optional update availability without changing or executing anything.

## MVP scope

1. Read the committed installation record and minimal operation marker without writing or repairing them.
2. Report installed/not-installed, toolkit and plugin IDs, recorded source selection/repository/reference/exact commit, AI4J version, and owned paths.
3. Inspect only the documented JSON marketplace/plugin lists and report marketplace registration, plugin installation, and enablement. Facts not exposed there remain `not_observable`.
4. Classify the owned catalog and rules files as `unchanged`, `modified`, `missing`, or `conflicting` from their recorded checksums and file types.
5. Report any valid, malformed, or unknown-schema operation marker as recovery required; do not clean or repair it from `status`.
6. Without `--check-updates`, perform no Git/network source work and report `not_checked` or `not_installed`.
7. With `--check-updates`, use the stored public GitHub source in a disposable workspace: branches become `up_to_date`, `available`, or `ref_rewritten`; unchanged tags and commits are `pinned`; moved tags are `ref_rewritten`; source failure is `unknown`.

## Acceptance criteria

- [x] Plain status is local, non-mutating, non-executing, and ordinary drift exits successfully.
- [x] Installed source intent, exact commit, native registration/installation/enablement, catalog drift, and rules drift remain distinct.
- [x] An operation marker or unreadable state reports recovery required and starts no repair.
- [x] `--check-updates` uses and removes a disposable source workspace and leaves durable AI4J and Claude state unchanged.
- [x] Branch, tag, commit, rewritten, missing-source, drifted, and malformed-native fixtures produce the exact typed dispositions.
- [x] Human and JSON responses remain deterministic, schema-valid, secret-free, and consistent with exit codes.

## Verification

- Exercise not-installed, healthy, disabled, missing, modified, conflicting, native-inspection failure, unknown schema, and operation-marker fixtures.
- Exercise branch up-to-date/available/rewritten, tag pinned/moved, commit pinned, and source-failure update checks.
- Assert plain status performs no source call, all status paths perform no writes or repair, and no installed content starts.

## Out of scope

- Repair, refresh, rollback, live MCP health checks, or starting installed content.
- Claude activation, reload, next-session, policy, or native-version claims not exposed by the documented list output.
- Compatibility matrices, private repositories, SSH, credential helpers, persistent caches, or retained source workspaces.
