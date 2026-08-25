# MVP-ST-018 — Serialize modifying commands

| Field | Value |
|---|---|
| Status | Done |
| Type | Concurrency safety |
| Wave | 3 — First complete installation |
| Relative size | S |
| Depends on | MVP-ST-015 |
| Requirements | MVP-FR-17, MVP-NFR-05 |
| MVP acceptance | 12 |

## User story

As an AI4J user, I want only one modifying command at a time so concurrent runs cannot silently overwrite each other.

## Scope

- Use one operating-system-backed lock for the single Claude user installation.
- Acquire it before rechecking mutable preconditions and hold it through the modification.
- Apply a bounded wait and return without target mutation on timeout or cancellation.
- Rely on operating-system release when a process exits; PID or timestamp metadata is not lock ownership.
- Let read-only commands consume atomically committed state snapshots without a shared lock.
- Keep checksum checks at mutation time because the lock does not control external writers.

## Acceptance criteria

- [x] Two modifying commands serialize, or one returns without mutation.
- [x] Process exit releases the lock without manual cleanup.
- [x] A timeout or cancellation performs no target mutation.
- [x] An externally changed owned file still causes a conflict while the lock is held.

## Verification

- Test two-process contention, timeout, process exit, and external file change.
- Run applicable in-process coordination tests with the race detector.

## Out of scope

- Shared locks, distributed locks, multiple installation domains, lock metadata protocols, or hostile same-user namespace hardening.
