# MVP-ST-015 — Store one installation ownership record

| Field | Value |
|---|---|
| Status | Done |
| Wave | 2 — Complete lifecycle planning |
| Requirements | MVP-FR-16, MVP-NFR-01, MVP-NFR-02 |

## User story

As an AI4J user, I want one small private ownership record so update and uninstall can plan from committed AI4J state rather than Claude's private files.

## MVP scope

- Support zero or one installation record.
- Store only the schema version, installation/toolkit/plugin identities, source selection and exact commit, target/scope, AI4J version, owned catalog/rules checksums, and last successful operation.
- Reject malformed and unknown schemas.
- Replace the record atomically from a private temporary file.
- Keep secret values and Claude-private cache details out of state.

## Acceptance criteria

- [x] A valid record round-trips without information loss.
- [x] Readers see an absent or complete record, never a partial document.
- [x] Unknown schemas are distinct from absence and block lifecycle planning.
- [x] State contains no secret-value field or Claude-private cache inventory.
- [x] Temporary state files are removed after a successful commit.

## Deferred

The Wave 3 install command owns when state is committed or removed. Multiple-installation state, history, journals, and rollback descriptors are V1 work. Unreleased state formats are not compatibility targets.
