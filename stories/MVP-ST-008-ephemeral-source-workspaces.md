# MVP-ST-008 — Manage private ephemeral source workspaces

| Field | Value |
|---|---|
| Status | Defined |
| Type | Source lifecycle enabler |
| Wave | 1 — Secure read-only validation |
| Relative size | L |
| Depends on | MVP-ST-004, MVP-ST-006, MVP-ST-007 |
| Requirements | MVP-AR-02, MVP-FR-04, MVP-NFR-02 |
| MVP acceptance | 6, 17, 29 |

## User story

As an AI4J user, I want source-consuming commands to use private disposable snapshots so that plans and update checks leave no persistent source cache or target change.

## Outcome

The source service acquires, validates, and removes one operation-specific immutable workspace for every declared GitHub-consuming operation kind, with safe recovery for abandoned temporary state.

## Scope

- Create workspaces outside repositories, installed assets, Claude cache, and adapter-owned installed state.
- Bind each workspace to canonical repository identity and exact commit.
- Before reusing a workspace or snapshot for validation, rendering, planning, or native handoff, re-read its origin and verify that it exactly matches the effective canonical repository identity.
- Verify clean detached tracked-only state before validation or rendering.
- Mark workspace ownership using a validated marker and current-user ownership.
- Remove workspaces after normal and handled-failure completion.
- Scavenge abandoned workspaces only after marker, owner, containment, live-lock, journal, symlink, and mount checks pass.
- Ensure the source acquisition and cleanup service creates no durable AI4J target, state, history, journal, recovery, or cache change.

## MVP delivery rule

First create, use, and remove one private workspace for `validate`. Reuse the existing host interfaces where they are sufficient; add scavenging, additional operation kinds, or a distinct runtime abstraction only after the basic lease lifecycle works and a concrete consumer requires it.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-008.T1 — Define the workspace lease contract

**Implementation**

- Define an immutable workspace lease and marker model around typed operation ID, source-consuming operation kind, canonical repository identity, exact commit identity, workspace root, and marker schema version.
- Expose acquisition and release through a consumer-owned interface in `internal/lifecycle`; keep Git and Darwin implementations behind that port and reject zero, unknown, or mismatched typed values before filesystem access.
- Enumerate only `validate`, lifecycle plan/apply operations, and update checking as source-consuming; do not expose a generic reusable checkout or cache handle.

**Completion evidence**

- Table tests accept every declared source-consuming operation kind and reject unknown kinds, incomplete identities, and cross-operation lease reuse.
- Architecture tests prove the lifecycle contract does not import the Git or Darwin implementation packages.

### MVP-ST-008.T2 — Create a private rooted workspace

**Implementation**

- Create the utility-owned temporary root and each operation directory with mode `0700` through the Darwin host boundary, outside every repository, Claude asset/cache root, stable catalog path, and installed-state root.
- Open the workspace through a rooted filesystem handle and create its ownership marker with mode `0600`; include only the typed lease identity, current-user ownership evidence, and schema version, never credentials or secret values.
- Return no lease until directory placement, owner, permissions, rooted access, and marker durability have all been verified.

**Completion evidence**

- Darwin integration tests verify creation-time modes, owner, forbidden-root exclusion, unique operation directories, and marker durability without relying on a later permission tightening step.
- Fault-injection tests at directory creation, marker creation, sync, and verification leave no usable lease and clean up every safely identifiable partial directory.

### MVP-ST-008.T3 — Populate and prove the immutable snapshot

**Implementation**

- Populate the lease through the `internal/source/git` exact-commit acquisition contract using direct executable-plus-argument invocation, explicit option termination, bounded context, and the hook, submodule, LFS, and filter restrictions established by MVP-ST-007.
- Before exposing snapshot bytes, verify the sanitized origin maps exactly to the effective canonical repository, `HEAD` equals the planned commit, the root tree matches provenance, the checkout is detached and clean, and all consumable bytes are tracked.
- Bind the verified snapshot facts to the lease so later consumers cannot replace its repository or commit identity.

**Completion evidence**

- Hermetic Git integration tests cover clean detached acquisition plus wrong origin, wrong `HEAD`, dirty, untracked, filtered, submodule, LFS-only, and hook-dependent snapshots.
- An argument-capture fake proves Git is launched without a shell, option-like repository or reference data cannot become Git options, and cancellation closes the acquisition process tree.

### MVP-ST-008.T4 — Revalidate every snapshot handoff

**Implementation**

- Provide a scoped snapshot-use operation rather than an unrestricted durable path; immediately before validation, rendering, planning, or native handoff, reopen the rooted workspace and revalidate marker, owner, containment, origin, detached `HEAD`, commit, tree, and clean tracked-only state.
- Reject a changed marker, origin, root, worktree, symlink, or mount boundary as a typed source conflict and never substitute the built-in repository after a mismatch.
- Keep all reads cancellable and bounded, and never run repository-provided content as part of a revalidation.

**Completion evidence**

- Boundary tests substitute origin metadata, `HEAD`, tracked bytes, marker data, owner, symlink, and mount identity between acquisition and each handoff and observe rejection before downstream use.
- A process sentinel proves every handoff performs metadata and filesystem checks only and starts no repository executable.

### MVP-ST-008.T5 — Close the workspace on every handled path

**Implementation**

- Make lease close idempotent and anchor recursive removal to the owned private temporary root with no-follow traversal; revalidate the marker, owner, containment, and mount boundary immediately before deletion.
- Wire close into every normal, cancelled, and handled-error return path without creating a journal, history entry, stable catalog, target file, installation record, or persistent source cache.
- Return a bounded typed cleanup error when safe removal cannot finish so the caller cannot report an ordinary successful read-only result while abandoned state remains.

**Completion evidence**

- Fault injection at acquisition, checkout, validation, rendering, cancellation, and close proves that each handled result either removes the workspace or returns the typed cleanup failure with a still-valid ownership marker.
- Durable-state assertions verify that stable catalog, target, installation, history, journal, recovery, and source-cache roots are byte-for-byte unchanged after every read-only result.

### MVP-ST-008.T6 — Scavenge only proven abandoned workspaces

**Implementation**

- Enumerate only direct children of the utility-owned temporary root through rooted no-follow operations, then require a supported marker, current-user ownership, expected directory modes, root containment, unchanged filesystem/mount identity, and absence of symlinked components.
- Query the lock and journal ownership ports before deletion; an active OS lock, any journal reference, an unknown schema, unreadable evidence, or an indeterminate liveness result must make the candidate ineligible.
- Recheck every eligibility fact immediately before idempotent deletion and stop on concurrent substitution rather than following or deleting the replacement.

**Completion evidence**

- Hostile scavenging fixtures cover unmarked, wrong-owner, out-of-root, live-lock, journal-referenced, unknown-schema, symlinked, mounted, unreadable, and concurrently replaced candidates.
- Termination tests at acquisition, population, handoff, close, and scavenging show that abrupt exit leaves at most a marked private workspace and that a later eligible scan removes only that workspace.

## Story acceptance criteria

- [ ] The typed source service classifies `validate`, every lifecycle plan/apply operation, and update checking as GitHub-consuming and returns a fresh private workspace bound to one operation ID for each request.
- [ ] Normal and handled-failure completion removes the workspace and leaves no persistent source cache.
- [ ] Abrupt termination leaves at most a marked workspace under the private temporary root.
- [ ] Scavenging refuses unmarked, wrong-owner, out-of-root, live-lock, journal-referenced, symlinked, or mount-redirected paths.
- [ ] Workspace acquisition, validation handoff, and cleanup never create the stable generated catalog or cause Claude to populate its native plugin cache.
- [ ] The stable catalog for an installed plugin is classified as adapter-control state, not a reusable source workspace.
- [ ] Origin substitution or mismatch detected before any workspace reuse fails before mutation and never falls back to the built-in source.

## Verification

- Inject termination at acquisition, checkout, validation, rendering, and cleanup boundaries.
- Table-test every declared GitHub-consuming operation kind and reject cross-operation workspace reuse.
- Assert durable-state absence after each normal, handled-failure, and cancelled source-service result.
- Exercise hostile scavenging fixtures and concurrent live operations.
- Substitute or rewrite workspace origin metadata between acquisition and each reuse boundary.

## Out of scope

- Persistent source caching, offline installation, or an explicit fetch command.
- Cleanup of Git-, SSH-, credential-helper-, or Claude-owned external state.
