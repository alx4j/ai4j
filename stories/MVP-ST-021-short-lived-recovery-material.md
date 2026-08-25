# MVP-ST-021 — Protect and remove short-lived recovery material

| Field | Value |
|---|---|
| Status | Deferred to V1 |
| Type | Recovery and privacy capability |
| Wave | V1 — Advanced recovery and rollback |
| Relative size | M |
| Depends on | MVP-ST-004, MVP-ST-019 |
| Requirements | MVP-FR-17, MVP-FR-18, MVP-NFR-02, MVP-NFR-04 |
| MVP acceptance | 12, 13, 14, 16, 17 |

## User story

As an AI4J user, I want recovery material private, minimal, and short-lived so that crash safety does not become permanent secret-bearing backup storage.

## Outcome

AI4J creates full preimages only for exclusively owned resources that cannot be reversed structurally, protects them as opaque sensitive data, and removes them after either terminal outcome.

## Scope

- Create a preimage immediately before its corresponding mutation.
- Store it outside repositories, source workspaces, installed assets, and native caches under the private recovery root.
- Record source path and checksum durably before mutation.
- Apply the same lifecycle to short-lived prior native packages used only for current-operation compensation.
- Never inspect preimage bodies for display, indexing, logging, diagnostics, or upload.
- Clean material only in the selected terminal branch's cleanup-pending phase.
- Retain enough journal state and report `cleanup_required` when safe deletion fails.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-021.T1 — Define the recovery-material eligibility policy

**Implementation**

- Define closed recovery-material kinds for an exclusively owned file preimage and a prior validated native package or supported exact rollback handle retained for one operation.
- Prefer structural inverse metadata; permit a full-file preimage only when structural reversal is unavailable, ownership and current checksum are proven, and the corresponding planned mutation declares that need.
- Reject shared/unmanaged resources, unrelated configuration, unknown material kinds, durable rollback/history intent, and any artifact without one operation/action owner, finite size, digest algorithm, and cleanup obligation.

**Completion evidence**

- Policy tests cover structurally reversible, exclusively owned non-structural, drifted, missing, shared, unmanaged, oversized, and unsupported material requests.
- Every accepted request maps to exactly one journaled action and terminal-branch cleanup obligation; every rejected request creates no directory, file, handle, or journal reference.

### MVP-ST-021.T2 — Implement the opaque private recovery store

**Implementation**

- Implement an operation-scoped store beneath the private `0700` recovery root with current-user ownership, containment, no-follow access, randomized names, and `0600` regular files created with restrictive permissions from the first open.
- Stream-copy through bounded buffers while computing size and SHA-256, enforce declared/remaining disk limits, sync content and the parent directory, and close every source/destination handle on success, error, timeout, or cancellation.
- Expose only typed artifact references and an opaque reader for the compensation owner; prohibit body-to-string conversion, indexing, rendering, upload, generic diagnostics, or raw-byte logging.

**Completion evidence**

- Darwin integration tests verify location, mode, ownership, regular-file/no-link policy, size/disk limits, digest calculation, atomic publication, cancellation, and cleanup of unpublished temporary files.
- Logging/output spies and canary payloads prove store operations expose metadata only and never emit or retain artifact bodies outside the recovery root.

### MVP-ST-021.T3 — Capture and journal a file preimage before mutation

**Implementation**

- After ST-019 reaches `prepared`, revalidate the source as the exact exclusively owned regular file and expected checksum, then create its preimage immediately before the associated adapter-owned mutation.
- Durably publish the artifact, then persist its operation/action identity, original owned path, size, digest, and opaque reference in a new journal revision before issuing the mutation permit.
- If capture or journal persistence fails, perform no target mutation; keep the operation-scoped material discoverable through the incomplete prepared journal so bounded cleanup can retry after a crash.

**Completion evidence**

- Ordered fault tests at source recheck, create, copy, sync, publish, journal write, journal sync, and mutation-permit boundaries prove no mutation occurs before the reference survives reload.
- Source substitution, symlink, owner/mode, checksum, disk-exhaustion, timeout, and cancellation fixtures fail closed without copying unrelated bytes.

### MVP-ST-021.T4 — Stage prior native recovery material

**Implementation**

- Accept only the previous toolkit-owned native package already validated against the installed exact repository commit and rendered digest, or a capability-qualified exact native rollback handle.
- Store package bytes under the same opaque operation-scoped policy, bind their identity/digest to the planned update or uninstall action, and durably journal the reference before the native mutation is eligible.
- Treat the material as compensation-only: do not register, validate by execution, add to source caches, expose as a fetch result, or retain it after the current operation's terminal cleanup.

**Completion evidence**

- Contract tests reject mismatched commit/package/plugin/marketplace identities, moving references, unqualified handles, missing bytes, and material not owned by the installed toolkit.
- Lifecycle spies prove staging starts no plugin script, binary, hook, or MCP process and leaves no persistent source checkout or cache.

### MVP-ST-021.T5 — Clean only the selected terminal branch's material

**Implementation**

- Begin deletion only from `committed_cleanup_pending` or `rolled_back_cleanup_pending`, enumerate artifacts solely from the validated operation journal and operation-scoped directory, and never use a broad recursive path derived from untrusted input.
- Revalidate containment, current-user ownership, regular-file/no-link type, operation/action marker, and recorded digest before deletion; on mismatch, preserve the path and return a typed cleanup conflict without displaying content.
- Delete each verified artifact and empty operation directory with bounded filesystem operations, sync the recovery root, then allow ST-019 to mark completion and delete the crash journal; retain the journal and report `cleanup_required` on any incomplete cleanup.

**Completion evidence**

- Tests cover both cleanup branches, partial deletion, retry, symlink/file substitution, changed digest, wrong owner, cancellation, timeout, and directory-sync failure without crossing terminal outcomes.
- Successful cleanup leaves no preimage, prior native package, temporary file, or operation directory; failed cleanup leaves a minimal valid journal that identifies only bounded opaque metadata for retry.

### MVP-ST-021.T6 — Qualify crash privacy and artifact lifecycle

**Implementation**

- Inject process termination before and after artifact creation, publication, journal reference durability, associated mutation, terminal selection, each deletion, directory sync, journal completion, and journal deletion.
- On restart, correlate only current-user operation directories with an incomplete or cleanup-pending journal and active lock state; clean through the selected branch or report a conflict, never adopt material as rollback history.
- Scan human output, JSON, logs, state, journal metadata, diagnostics, process arguments, and non-recovery filesystem content for opaque canaries.

**Completion evidence**

- The crash matrix proves every residual artifact is private, operation-scoped, associated with a recoverable journal, and eventually deleted after either verified terminal outcome.
- Secret-canary, race, repeated-restart, resource-limit, and cleanup-retry suites pass with no body disclosure, leaked file descriptor, broad deletion, or material retained after successful completion.

## Story acceptance criteria

- [ ] Full-file preimages are created only when structural reversal is unavailable for an exclusively owned file.
- [ ] Recovery artifacts have private permissions at creation and never appear inside project or source paths.
- [ ] Artifact references and checksums reach durable journal state before the corresponding mutation.
- [ ] Success and verified compensation delete all preimages and short-lived native recovery packages.
- [ ] A crash may leave artifacts only with an incomplete or cleanup-pending journal, and the next invocation cleans or reports them safely.
- [ ] Secret canaries in opaque recovery material never appear in output, logs, JSON, state, or diagnostics.

## Verification

- Terminate before/after preimage creation, mutation, terminal selection, and deletion.
- Inject deletion failures and retry cleanup on the next invocation.
- Scan all observable outputs and persistent non-recovery state for canaries.

## Out of scope

- Retained whole-file backups, encrypted history, or manual rollback storage.
- Copying unrelated content from shared configuration.
