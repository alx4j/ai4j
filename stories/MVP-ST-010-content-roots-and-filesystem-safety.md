# MVP-ST-010 — Enforce closed content roots and filesystem safety

| Field | Value |
|---|---|
| Status | Defined |
| Type | Security capability |
| Wave | 1 — Secure read-only validation |
| Relative size | L |
| Depends on | MVP-ST-004, MVP-ST-009 |
| Requirements | MVP-FR-06, MVP-NFR-02, MVP-NFR-05 |
| MVP acceptance | 4, 15, 18, 28 |

## User story

As an AI4J user, I want source and destination boundaries enforced at validation and mutation time so that malicious paths or concurrent substitutions cannot escape ownership.

## Outcome

AI4J inventories only regular tracked bytes under closed roots and uses normalization-, ownership-, checksum-, and no-follow checks before every adapter-owned write.

## Scope

- Resolve every source and adapter-owned destination against its declared canonical root.
- Reject absolute, empty, dot, parent, control, traversal, embedded-separator, and out-of-root paths.
- Reject source symlinks, hard links, sockets, FIFOs, devices, special files, unsafe modes, and symlinked writable destinations or parents.
- Detect output collisions after separator, Unicode, and case normalization.
- Safely encode validated identifiers before using them in filesystem paths.
- Recheck containment, file type, ownership, and expected pre-operation checksum immediately before mutation.
- Use no-follow operations and same-directory atomic replacement where supported.

## MVP delivery rule

Deliver canonical relative paths, deterministic collision checks, and rooted containment for the first validated package. Add a new source-reader abstraction only when the package-validation integration demonstrates that the existing rooted host boundary cannot satisfy it safely.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-010.T1 — Define canonical relative paths and collision keys

**Implementation**

- Introduce validated relative-path and filesystem-safe identifier types that reject absolute, empty, dot, parent, control, NUL, alternate-separator, embedded-separator, and platform-device components before joining paths.
- Define one versioned collision-key algorithm for separator normalization, canonical Unicode equivalence, and locale-independent case folding, regardless of the current volume's case sensitivity; use a pinned reviewed normalization implementation rather than locale-dependent comparison.
- Keep display paths distinct from canonical comparison keys so errors remain readable without using untrusted text as a filesystem authority.

**Completion evidence**

- Table and fuzz tests cover traversal, mixed separators, normalization forms, case variants, control characters, device-like names, and identifier-to-path encoding.
- Collision fixtures prove distinct source spellings that map to the same output key are rejected deterministically before any file is opened for writing.

### MVP-ST-010.T2 — Inventory source files through a closed rooted boundary

**Implementation**

- Consume rooted source-reading operations from MVP-ST-004. The Darwin implementation must prevent symlink traversal and revalidate opened identities, while target-neutral inventory code must never import Darwin or authorize access from a cleaned string path alone.
- Intersect manifest-selected paths with the immutable tracked-file set and require every opened leaf to remain beneath its declared closed root and be one regular file with an acceptable link count and source mode.
- Reject symlinks, hard links, sockets, FIFOs, devices, mounts that redirect traversal, and any file identity that changes between classification and rooted open.

**Completion evidence**

- Darwin integration fixtures exercise regular files, every rejected special type, hard links, symlinks at each component, mount/device changes, and source substitution during open.
- Inventory assertions prove only tracked, manifest-selected regular bytes beneath closed roots are returned, regardless of unrelated repository layout.

### MVP-ST-010.T3 — Capture destination ownership and write preconditions

**Implementation**

- Resolve each adapter-owned destination from its canonical user root through rooted, component-by-component no-follow inspection and reject any symlinked, wrong-owner, wrong-type, or mount-redirected parent.
- Capture an immutable precondition containing root identity, parent and leaf file identities, ownership, mode, existence, and expected checksum; represent intended absence explicitly rather than with an empty checksum.
- Require the destination name to come from the validated identifier encoder and reject every collision before producing a mutation precondition.

**Completion evidence**

- Tests cover absent, owned, unmanaged, drifted, wrong-owner, symlinked-parent, mounted-parent, directory-at-leaf, and collision cases with stable conflict categories.
- Repeated observation of an unchanged destination produces byte-for-byte identical preconditions without modifying access or change times through a write.

### MVP-ST-010.T4 — Revalidate at the mutation boundary

**Implementation**

- Immediately before each adapter-owned mutation, reopen the trusted root and recheck containment, component identities, filesystem/mount identity, file type, owner, mode, and expected checksum against the immutable precondition.
- Invoke the MVP-ST-004 final open or replacement primitive relative to its rooted handle with no-follow semantics so an attacker cannot win a check-then-open race by substituting a path.
- Return a typed conflict without mutation when any fact changed, including changes that leave the canonical path string unchanged.

**Completion evidence**

- Deterministic race fixtures substitute a symlink, mount, parent, owner, mode, file type, inode, or bytes after planning and before the final operation; each case is rejected without touching the replacement.
- A concurrent-writer integration test proves one writer succeeds and the stale writer reports conflict rather than silently overwriting newer content.

### MVP-ST-010.T5 — Apply atomic replacement policy idempotently

**Implementation**

- Compare the desired digest before writing, return `no_change` for a converged resource, and otherwise pass the verified precondition, desired bytes, and allowed owner/mode policy to MVP-ST-004's same-directory atomic writer.
- Require the host result to prove restrictive temporary-file creation, file and directory sync, atomic rename where supported, temporary cleanup, and preservation of the existing owned file's owner without broadening permissions.
- Translate a failed final revalidation into conflict and a host write/cleanup failure into its typed operational result; never retry against an unobserved destination or follow a substituted link.

**Completion evidence**

- Darwin fault-injection tests cover create, write, sync, metadata, rename, directory-sync, and cleanup failures and observe either the complete old bytes or complete new bytes, never a partial file.
- Permission and idempotency tests prove new non-executable files are no more permissive than `0644`, replacements preserve owner and do not broaden mode, and a converged replacement performs no write.

## Story acceptance criteria

- [ ] Every hostile path and special-file fixture is rejected before target mutation.
- [ ] Go source, module files, CI configuration, signing material, and unrelated repository bytes remain outside the installable inventory.
- [ ] A symlink, mount, owner, mode, or file substitution between plan and apply is detected at the mutation boundary.
- [ ] Case- or Unicode-equivalent outputs are rejected regardless of current ownership.
- [ ] A concurrent destination change produces a conflict and is never overwritten.
- [ ] Repeating a converged adapter-owned replacement produces no write.

## Verification

- Run table-driven lexical and canonical path tests.
- Use mutation-time race fixtures for symlink, file-type, owner, and checksum substitution.
- Run collision fixtures on case-sensitive and case-insensitive filesystems where available.

## Out of scope

- Direct control over Claude-owned native destinations.
- Automatic conflict resolution or force overwrite.
