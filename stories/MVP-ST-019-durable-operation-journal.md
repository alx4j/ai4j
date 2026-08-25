# MVP-ST-019 — Record an in-progress operation

| Field | Value |
|---|---|
| Status | Done |
| Type | Interrupted-operation safety |
| Wave | 3 — First complete installation |
| Relative size | S |
| Depends on | MVP-ST-015, MVP-ST-018 |
| Requirements | MVP-FR-17, MVP-FR-18 |
| MVP acceptance | 11, 13 |

## User story

As an AI4J user, I want an interrupted modification to be visible so another command does not guess or overwrite uncertain state.

## Scope

- Before the first mutation, atomically write one private marker containing schema version, operation type, operation ID, installation ID when known, exact commit when applicable, and the intended owned/native resource identities.
- Store no secret values, content bodies, preimages, inverse actions, or native cache details.
- Remove the marker only after successful state commit and owned temporary-file cleanup.
- Treat an unknown marker schema as `recovery_required`.

## Acceptance criteria

- [x] No target mutation occurs before the marker is durably available.
- [x] An existing or unknown marker blocks a new modifying operation.
- [x] Successful completion removes the marker; handled failure removes it only when observations prove that no uncertain mutation remains.
- [x] Marker bytes are bounded, private, deterministic, and secret-free.

## Verification

- Test marker write failure, interruption before and after the first mutation, successful removal, malformed/unknown schema, and secret canaries.

## Out of scope

- A phase journal, automatic roll-forward, rollback branches, compensation, retained history, or preimages. Those are v1 capabilities.
