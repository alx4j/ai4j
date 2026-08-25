# MVP-ST-020 — Inspect an interrupted operation

| Field | Value |
|---|---|
| Status | Done |
| Type | Interrupted-operation safety |
| Wave | 3 — First complete installation |
| Relative size | S |
| Depends on | MVP-ST-019 |
| Requirements | MVP-FR-17, MVP-FR-18 |
| MVP acceptance | 13 |

## User story

As an AI4J user, I want a prior interrupted operation inspected before another change so AI4J never guesses about ownership or completion.

## Scope

- Under the modification lock, load the marker and inspect the named Claude identities, owned catalog, rules, and committed installation state.
- Remove clearly AI4J-owned temporary files when their ownership is provable.
- Remove the marker only when observations prove the recorded operation completed or made no persistent change.
- Otherwise return `recovery_required` with bounded remediation and begin no new mutation.

## Acceptance criteria

- [x] A safe, fully observable completed/no-effect case is cleaned and may proceed.
- [x] Ambiguous, conflicting, or unknown state returns `recovery_required` and remains unchanged.
- [x] Unrelated files and Claude-private caches are never edited directly.
- [x] Repeating inspection is safe and deterministic.

## Verification

- Test no-effect, completed, partial, drifted, missing-resource, and unknown-native-state fixtures.
- Assert ambiguous fixtures make no target or committed-state mutation.

## Out of scope

- Automatic rollback, compensation, roll-forward state machines, native package backups, or retained recovery artifacts.
