# MVP-ST-004 — Build Darwin host and private runtime primitives

| Field | Value |
|---|---|
| Status | Defined |
| Type | Platform enabler |
| Wave | 1 — Secure read-only validation |
| Relative size | L |
| Depends on | MVP-ST-002 |
| Requirements | MVP-FR-06, MVP-NFR-02, MVP-NFR-03 |
| MVP acceptance | 15, 17, 22 |

## User story

As an AI4J user, I want filesystem and child-process operations to use least privilege and bounded behavior so that validation and lifecycle commands cannot escape AI4J's intended authority.

## Outcome

The Darwin host adapter provides private storage, canonical path inspection, safe atomic file primitives, direct process execution, bounded output, timeout, cancellation, and disk preflight.

## Scope

- Create private state, recovery, temporary-source, and staging roots with mode `0700` and private files with mode `0600` at creation.
- Create new non-private, non-executable adapter-owned files with a mode no more permissive than `0644` at creation.
- Provide canonical containment, file-type, owner, mode, no-follow, and same-directory atomic-replacement primitives.
- Preserve owner and avoid broadening permissions when replacing owned files.
- Invoke external commands directly without a shell.
- Bound stdout/stderr capture, execution time, lock waits, and filesystem waits.
- Terminate the complete child process tree on cancellation or timeout.
- Preflight disk space before any target mutation.

## MVP delivery rule

Implement only the host primitives required by the first working `ai4j validate` path. Keep Git-, Claude-, and mutation-specific policy in their owning stories; defer additional authority models, platform profiles, and recovery states until a concrete consumer and acceptance test require them.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-004.T1 — Create private Darwin runtime roots safely

**Implementation**

- Implement `internal/host/darwin/filesystem` root creation for state, recovery, temporary-source, and staging roles, using explicit configuration rather than ambient package globals.
- Create private directories as `0700`, private files as `0600`, and new non-private non-executable adapter-owned files no broader than `0644`; verify owner, type, and effective mode on the opened object before returning it.

**Completion evidence**

- Darwin integration tests create every root and file under `t.TempDir()` with permissive and restrictive umasks and assert the required mode exists from creation, not after a corrective chmod.
- Negative fixtures reject pre-existing wrong-owner objects, special files, symlinks, and paths that resolve outside the configured root.

### MVP-ST-004.T2 — Provide no-follow inspection and containment primitives

**Implementation**

- Add a descriptor-rooted Darwin layer with no-follow inspection for canonical containment, current-user ownership, file type, mode, device or mount identity, and parent traversal without following symlinks.
- Return typed observations and conflicts through the ST-002 host ports; do not expose raw syscall details or accept a previously canonicalized path as proof that a later open is safe.

**Completion evidence**

- Hostile fixtures cover source and parent symlinks, hard links, special files, path traversal, case variants, mount substitution, and replacement between inspection steps.
- Contract tests prove each observation is rooted in the opened object and that containment failure returns before any write-capable primitive is called.

### MVP-ST-004.T3 — Implement checksum-gated atomic file replacement

**Implementation**

- Add a same-directory atomic writer that reopens and revalidates the destination immediately before mutation, creates the temporary file with its final restrictive mode, writes and syncs bytes, then renames and syncs the containing directory.
- Preserve the verified owner and never broaden an existing owned file's permissions; require an expected absence or pre-operation checksum rather than overwriting an unobserved destination.

**Completion evidence**

- Fault-injection tests at create, write, sync, rename, and directory-sync boundaries leave either the old complete file or the new complete file and report cleanup state for any owned temporary artifact.
- Race fixtures prove changed checksums, owners, modes, symlinks, and mount identities fail as conflicts without replacing the concurrent user's object.

### MVP-ST-004.T4 — Run bounded child processes without a shell

**Implementation**

- Implement `internal/host/darwin/process` as a context-aware direct-argv runner with an explicit executable, argument vector, minimal environment allowlist, working directory, timeout, and output limit; reject shell and privilege-escalation executables such as `sudo` at the host boundary.
- Place each child in an owned process group, terminate the group on cancellation or timeout, wait for reaping, and return typed exit facts plus bounded sanitized stdout and stderr without logging raw output.

**Completion evidence**

- Process fixtures cover successful exit, missing executable, nonzero exit, cancellation, timeout, descendant forking, ignored graceful termination, excessive output, and invalid working directories.
- Tests prove shell and `sudo` requests are rejected, no shell process is introduced, all descendants are gone before return, output truncation is deterministic, and cancellation remains discoverable with `errors.Is`.

### MVP-ST-004.T5 — Bound waits and preflight filesystem capacity

**Implementation**

- Add `internal/host/darwin/resource` policies for Git, Claude, filesystem, and lock-operation budgets, with caller contexts taking precedence over configured maximums and no stored request context.
- Calculate required temporary-source, staged-output, journal, and recovery headroom from bounded declared inputs, inspect available space on each affected filesystem, and expose a pre-mutation disk decision through the host port.

**Completion evidence**

- Deterministic budget tests cover caller cancellation, earlier deadlines, configured timeouts, overflow-safe byte arithmetic, unknown capacity, and insufficient space.
- Filesystem fixtures prove an insufficient or uninspectable destination fails before any target-mutation spy and that bounded waits release all acquired resources.

### MVP-ST-004.T6 — Qualify the Darwin host contract

**Implementation**

- Assemble the filesystem, process, and resource implementations behind the small ST-002 host interfaces in `internal/host/darwin`, with constructors that validate required roots and limits and return concrete adapters.
- Keep Darwin-specific imports behind the adapter boundary and provide build-tagged contract tests for the documented Apple Silicon macOS baseline.

**Completion evidence**

- Compile-time assertions prove the Darwin components satisfy their consumer-owned ports while target-neutral packages remain free of Darwin imports.
- The supported-host contract suite combines permission, hostile-path, atomic-write, process-tree, timeout, cancellation, and disk-preflight fixtures and leaves no child process or owned temporary artifact behind.

## Story acceptance criteria

- [ ] Private roots and files have the required permissions from creation time.
- [ ] A new adapter-owned rules or other non-private non-executable file is never more permissive than `0644` at creation.
- [ ] No operation invokes `sudo`, a command shell, or a path outside its canonical declared root.
- [ ] Atomic replacement preserves owner and does not broaden permissions.
- [ ] Timeout and cancellation terminate the complete child process tree and return bounded sanitized output.
- [ ] Insufficient disk space and filesystem timeout fail before unsafe target mutation.
- [ ] Symlink or mount substitution is detectable immediately before a mutation.

## Verification

- Run integration tests on a supported Apple Silicon macOS host.
- Use hostile filesystem fixtures for symlinks, special files, permission changes, and replacement races.
- Use child-process fixtures that fork, hang, overproduce output, and ignore ordinary termination.

## Out of scope

- Windows filesystem and process behavior.
- Direct mutation of Claude-owned cache or registry paths.
