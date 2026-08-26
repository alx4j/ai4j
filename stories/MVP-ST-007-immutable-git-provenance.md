# MVP-ST-007 — Resolve immutable Git references and provenance

| Field | Value |
|---|---|
| Status | Defined |
| Type | Source capability |
| Wave | 1 — Secure read-only validation |
| Relative size | L |
| Depends on | MVP-ST-004, MVP-ST-006 |
| Requirements | MVP-FR-03, MVP-NFR-01 |
| MVP acceptance | 2, 3, 9 |

## User story

As an AI4J user, I want every branch, tag, or commit resolved to immutable typed provenance so that validation and installation cannot race a moving source.

## Outcome

AI4J resolves supported references to a proven commit, records distinct commit/tree/package identities, and enforces tracked-branch and pinned-reference behavior.

## Scope

- Resolve default branch, explicit branch, tag, full commit, and fully qualified ambiguous references.
- Peel annotated tags and prove the object is a commit.
- Represent commit identity as canonical repository, `sha1`, and a 40-character lower-case OID.
- Record requested and resolved references, root-tree OID, and independent rendered-package SHA-256 digest as distinct facts.
- Require detached `HEAD` exactly at the resolved commit with tracked immutable bytes only.
- Track only fast-forward branch descendants; keep tags and commits pinned.
- Report deleted, moved-tag, ambiguous, and non-fast-forward cases without automatic update.
- Disable or reject hooks, submodules, Git LFS expansion, and external filters.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-007.T1 — Model Git reference and provenance results

**Implementation**

- Add `internal/source/git` request and result types for requested reference, resolved reference kind and name, exact commit identity, root-tree identity, tracking policy, and update disposition, reusing the non-interchangeable ST-002 domain types.
- Define a source-provenance aggregate that contains repository/ref/commit/tree facts and a separate rendered-provenance constructor that requires a caller-supplied SHA-256 package digest; do not render package content in this story.

**Completion evidence**

- Constructor tests reject malformed full OIDs, unsupported object formats, missing resolved names, contradictory tracking policies, and commit/tree/package substitutions.
- Schema and golden fixtures keep requested ref, resolved ref, commit OID, tree OID, rendered digest, and CLI build commit in distinct stable fields.

### MVP-ST-007.T2 — Harden the Git command boundary

**Implementation**

- Implement a small Git executor consumer in `internal/source/git` over the ST-004 context-aware process port, accepting only structured operations and generating direct executable plus argument vectors with explicit option termination.
- Apply an operation-local Git configuration and environment that disable hooks, external clean or smudge filters, Git LFS expansion, submodule recursion, interactive prompting, repository-provided execution paths, and endpoint-changing ambient configuration such as `url.*.insteadOf` and `core.sshCommand`, while bounding time and output.
- Admit only an authentication projection needed by the chosen transport: validated credential-helper behavior for HTTPS or agent/default-key behavior for SSH. Pin SSH `HostName` to `github.com`, disable `ProxyCommand` and `ProxyJump`, and do not consume arbitrary SSH host aliases or Git configuration that can redirect the endpoint.

**Completion evidence**

- Recording-runner tests verify argv, environment, option termination, working directory, deadlines, and sanitized typed errors for every Git operation.
- Malicious repository/config fixtures prove hooks, filters, LFS handlers, submodule commands, repository-controlled configuration, shell metacharacters, Git URL rewrites, SSH `HostName`, `ProxyCommand`, `ProxyJump`, and `core.sshCommand` cannot start a sentinel process or redirect the accepted GitHub endpoint; interactive prompts cannot block the operation.

### MVP-ST-007.T3 — Resolve remote references to a proven commit

**Implementation**

- Resolve the default branch, explicit branch, tag, full commit hash, and fully qualified ref by enumerating the relevant remote namespace, preserving the remote default branch name, and rejecting ambiguous short names.
- Fetch only the selected objects into an operation-local repository, peel annotated tags, and use local Git object inspection to prove the final 40-character lower-case SHA-1 OID names a commit.

**Completion evidence**

- Local bare-repository fixtures cover every supported reference kind, annotated and lightweight tags, ambiguous branch/tag names, deleted refs, malformed OIDs, non-commit objects, and unavailable commits.
- Repeated resolution returns identical typed facts, and cancellation, timeout, authentication, and bounded-output failures preserve their typed causes without partial provenance.

### MVP-ST-007.T4 — Materialize and verify an immutable detached snapshot

**Implementation**

- Materialize the proven commit into a caller-owned empty private workspace, check out by exact OID in detached mode, and reject submodules, LFS pointer-only requirements, external filters, hooks, untracked files, dirty bytes, and non-regular tracked entries.
- Reinspect canonical origin, the effective transport endpoint, detached `HEAD`, clean status, tracked-file set, and root-tree OID after materialization; return no snapshot if any fact differs from the resolved request or the hardened transport audit detects redirection.

**Completion evidence**

- Snapshot fixtures assert exact `HEAD`, root tree, tracked bytes, clean status, canonical origin, and actual GitHub endpoint for valid repositories and reject every prohibited repository mechanism, ambient redirect, or dirty-state variant.
- A synchronization-controlled test moves the branch after resolution but before and after checkout and proves the detached snapshot remains at the recorded commit or fails without substituting newer bytes.

### MVP-ST-007.T5 — Calculate tracked and pinned update dispositions

**Implementation**

- Implement ancestry and ref-state evaluation for an installed source: default and explicit branches track only a proven fast-forward descendant, while tags and commits remain pinned.
- Return distinct `no_change`, `available`, `pinned`, `ref_rewritten`, ambiguous, deleted, and source-error results without mutating a workspace, installation state, or target adapter.

**Completion evidence**

- Graph fixtures cover unchanged, one- and multi-commit fast-forward, non-fast-forward, force-moved, deleted, and ambiguous branches plus unchanged and moved tags and pinned commits.
- Mutation spies remain untouched for every disposition, and repeated evaluation against the same refs produces byte-identical typed and JSON results.

### MVP-ST-007.T6 — Qualify immutable provenance end to end

**Implementation**

- Build deterministic Git fixture builders and a source contract harness that joins canonical GitHub identity, hardened resolution, detached materialization, tree inspection, and caller-supplied rendered digest assembly.
- Keep workspace allocation and scavenging outside this story's production boundary so MVP-ST-008 can own ephemeral-workspace lifecycle without changing the resolver or provenance contracts.

**Completion evidence**

- The harness covers default, branch, tag, commit, fully qualified, ambiguous, deleted, moved, rewritten, HTTPS-authenticated, SSH-authenticated, and hostile ambient-redirect scenarios and proves ref movement or local configuration cannot change returned snapshot bytes, identity, or endpoint.
- Provenance checks show repository commit, root tree, rendered digest, and CLI build commit remain independently typed and serialized, while every temporary test repository is removed after the fixture completes.

## Story acceptance criteria

- [ ] Every supported reference kind resolves deterministically to a proven exact commit before content validation.
- [ ] Commit OID, root-tree OID, package digest, and CLI build commit are distinct in the typed provenance result and its JSON schema; downstream state uses those same types.
- [ ] Once resolution returns an exact commit identity and detached snapshot, later movement of the requested ref cannot change that snapshot's bytes or typed identity.
- [ ] Tags and commits return `pinned`; an unchanged branch returns `no_change`.
- [ ] Deleted branches, moved tags, ambiguous refs, and rewritten branches return their required conflict or disposition without mutation.
- [ ] A checkout containing untracked, dirty, filtered, submodule, LFS-only, or hook-dependent bytes is rejected.

## Verification

- Build local Git fixtures for every reference and rewrite scenario.
- Test tag peeling, non-commit objects, OID normalization, and immutable snapshot identity after ref movement.
- Race branch movement between resolution and later native handoff.

## Out of scope

- SHA-256 Git object format.
- In-place repository or requested-reference changes.
