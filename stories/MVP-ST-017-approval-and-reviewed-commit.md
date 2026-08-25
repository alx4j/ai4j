# MVP-ST-017 — Apply only an approved, reviewed plan

| Field | Value |
|---|---|
| Status | Done |
| Type | Shared lifecycle capability |
| Wave | 3 — First complete installation |
| Relative size | S |
| Depends on | MVP-ST-016, MVP-ST-018 |
| Requirements | MVP-FR-09, MVP-FR-20 |
| MVP acceptance | 5, 12, 15 |

## User story

As an AI4J user, I want a fresh plan before approving a change so that approval applies to the commit and state that will actually be used.

## Scope

- Recompute the plan after acquiring the modification lock.
- Reject a supplied `--expected-commit` when it differs from the recomputed full commit.
- Prompt only for a non-empty plan on an interactive terminal.
- Require `--yes` for a non-empty plan in JSON or non-interactive mode.
- Return `cancelled` without mutation when approval is declined.
- Never treat approval as permission to bypass a conflict or validation error.

## Acceptance criteria

- [x] A changed plan is displayed before approval and is the plan passed to the installer.
- [x] Expected-commit mismatch, missing non-interactive approval, and declined approval make no mutation.
- [x] `no_change` and `pinned` require no approval.
- [x] Human and JSON results follow the existing command and exit-code contracts.

## Verification

- Test interactive accept/decline, non-interactive `--yes`, JSON mode, no-op, pinned, and expected-commit mismatch.
- Assert the mutation adapter is never called for an unapproved or conflicting plan.

## Out of scope

- Persisted approval grants, plan digests, or interactive conflict resolution.
