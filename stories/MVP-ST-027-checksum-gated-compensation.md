# MVP-ST-027 — Apply checksum-gated compensation

| Field | Value |
|---|---|
| Status | Deferred to V1 |
| Type | Recovery capability |
| Wave | V1 — Advanced recovery and rollback |
| Relative size | L |
| Depends on | MVP-ST-005, MVP-ST-014, MVP-ST-019, MVP-ST-020, MVP-ST-021 |
| Requirements | MVP-FR-17, MVP-NFR-05, MVP-NFR-07 |
| MVP acceptance | 12, 13, 14, 18 |

## User story

As an AI4J user, I want a failed partial operation compensated only from verified post-state and prepared recovery material so that AI4J never overwrites a concurrent change while trying to recover.

## Outcome

The mutation executor applies checksum-gated structural inverses and supported exact native rollback actions, verifies the restored pre-operation state, and completes the rolled-back journal branch.

## Scope

- Consume only `compensation_required` decisions produced by MVP-ST-020.
- Require every referenced preimage, prior package, or native rollback handle to be present and checksum-verified before compensation begins.
- Apply an adapter-owned inverse only when the current resource equals the post-operation checksum written by that operation.
- Invoke native compensation only through the supported qualified Claude adapter.
- Reinspect native, adapter-owned, and management state after each compensation action.
- Mark terminal outcome `rolled_back` only after actual state matches the complete pre-operation boundary.
- Move through `rolled_back_cleanup_pending` and leave artifact/journal deletion to their owning cleanup services.
- Report `changed: false` for a fully compensated failed operation; retain a recoverable journal on unresolved conflict.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-027.T1 — Validate the compensation decision and journal branch

**Implementation**

- Implement compensation orchestration in `internal/lifecycle` and accept only a typed `compensation_required` decision or an existing `compensating` resume tied to a supported pending journal schema, operation ID, target, user scope, and immutable pre-operation/expected-post-operation boundaries.
- Reject committed, cleanup-only, complete, unknown-schema, malformed, or mismatched decisions before reading or applying an inverse.
- Derive the remaining inverse sequence from durable journal state and preserve monotonic rollback-branch transitions across restart.

**Completion evidence**

- State-machine tests prove only legal `applying -> compensating` selections and existing `compensating` resumes are accepted, and that committed or cleanup-pending branches can never enter compensation.
- Corrupt, replayed, wrong-operation, wrong-scope, and unknown-schema fixtures fail without adapter, native, management-state, or cleanup mutation.

### MVP-ST-027.T2 — Verify all recovery prerequisites before compensation

**Implementation**

- Resolve every journaled structural inverse, preimage, prior exact package, and supported native rollback handle through its owning private store.
- Verify path ownership, containment, file type, checksum, operation binding, target/scope binding, and expected post-state before the first inverse runs.
- Reinspect the complete native, adapter-owned, and management boundary; return a typed conflict when it matches neither the journaled post-state nor an already-restored pre-state.

**Completion evidence**

- Missing, corrupt, stale, substituted, wrong-operation, wrong-scope, and valid recovery-material fixtures prove all prerequisites are checked before compensation begins.
- Secret and opaque-content canaries remain absent from errors, logs, JSON, and diagnostics while checksum and identity context stays actionable.

### MVP-ST-027.T3 — Provide checksum-gated adapter and state inverse primitives

**Implementation**

- Implement one-at-a-time structural inverse primitives for same-directory atomic restore, removal, and management-state actions, immediately rechecking the requested resource against that operation's expected post-operation checksum.
- Require the caller to persist inverse intent before invocation; return a bounded result plus reobserved resource facts for durable result recording, but do not choose global ordering or advance to another inverse inside a primitive.
- On concurrent change, leave the resource untouched and return a typed conflict so the shared orchestrator retains the compensating journal and stops remaining mutation.

**Completion evidence**

- Filesystem and state-store tests cover restore, delete, already-restored idempotency, checksum drift, symlink substitution, atomic-write failure, and process death before and after each inverse.
- Concurrent-change fixtures prove no stale preimage or structural descriptor overwrites a newer value and restart never repeats a converged inverse.

### MVP-ST-027.T4 — Provide the qualified exact native rollback primitive

**Implementation**

- Invoke native compensation only through the qualified Claude adapter using the verified prior exact package or rollback handle, fully qualified identities, and explicit user scope wherever supported.
- Require journaled native intent before invocation and return a bounded machine-readable result plus reinspection facts, treating timeout, caller cancellation after possible effect, or ambiguous output as outcome unknown under a separately owned bounded recovery context; do not choose global inverse order inside the primitive.
- Preserve persistent data and unrelated native state, and keep no-execution/reload sentinels active across rollback operations.

**Completion evidence**

- Supported-Claude contract tests prove exact prior-package or handle binding, qualified identities, user scope, documented output parsing, and preservation of unrelated registrations/data.
- Timeout-after-commit, cancellation-after-effect, malformed-output, wrong-native-state, indirect-execution, and missing-capability fixtures select reconciliation or conflict without unsafe retry or branch crossover.

### MVP-ST-027.T5 — Execute the unified inverse stream and reconcile the boundary

**Implementation**

- Execute one durable journal-ordered inverse stream that may interleave adapter-owned, native, and management-state primitives according to the approved recovery plan; persist each intent and result and resume from the first non-converged action.
- After every inverse, normalize native, adapter-owned, and management observations and compare them with the complete pre-operation boundary recorded in the journal. If timeout or caller cancellation follows a possible effect, complete mandatory inspection under an owned bounded recovery context and retain the same `compensating` branch when the recovery budget expires.
- Select terminal outcome `rolled_back` only after every required fact matches; never commit the failed desired state or infer restoration from command success alone.
- Advance durably to `rolled_back_cleanup_pending` and produce canonical `changed: false`, failure classification, and remediation.

**Completion evidence**

- Reconciliation tests cover fully restored, partially restored, concurrently changed, unobservable native, cancellation-after-effect, mixed adapter/native/state ordering, and already-restored restart states.
- State and JSON assertions prove `rolled_back` cannot be selected early, failed desired state is never committed, and fully compensated results report `changed: false`.

### MVP-ST-027.T6 — Resume rollback and hand off terminal cleanup

**Implementation**

- Resume from the first non-converged inverse after restart, preserving the selected rollback branch through repeated failure, timeout, caller cancellation, and process death.
- Delegate artifact and journal deletion only to the recovery-material and journal cleanup owners after `rolled_back_cleanup_pending`; do not implement a second cleanup policy in the executor.
- Retain the minimum journal and recovery references required for a safe retry and report `cleanup_required` when owning cleanup cannot finish.

**Completion evidence**

- Failure-injection tests at every inverse, cancellation-after-possible-effect boundary, terminal transition, artifact deletion, and journal deletion prove monotonic restart behavior through `complete_rolled_back`.
- End-to-end compensation fixtures show either verified prior-state restoration and cleanup or a non-destructive recoverable conflict, with no retained material after successful cleanup.

## Story acceptance criteria

- [ ] Every structural inverse is checksum-gated and skips a concurrently changed resource with a typed conflict.
- [ ] Native compensation cannot begin without a verified exact prior package or supported rollback handle.
- [ ] Failure or termination after every compensation action resumes the same rollback branch and never switches to commit.
- [ ] `rolled_back` is selected only after native, adapter-owned, and management state match the pre-operation state.
- [ ] Fully compensated failure reaches `complete_rolled_back`, returns `changed: false`, and commits no failed desired state.
- [ ] Cleanup failure preserves only the material and journal needed for a safe retry and reports `cleanup_required`.

## Verification

- Inject failure, timeout, caller cancellation, process death, and concurrent mutation before and after every inverse and native rollback action.
- Exercise missing, corrupt, stale, and valid recovery-material fixtures.
- Prove terminal-branch monotonicity across restart and cleanup.

## Out of scope

- User-invoked rollback after a successful operation.
- Restoring unrelated content or applying an inverse after checksum drift.
