# MVP-ST-005 — Detect a supported environment and Claude capability profile

| Field | Value |
|---|---|
| Status | Defined |
| Type | Integration enabler and risk gate |
| Wave | 1 — Secure read-only validation |
| Relative size | L |
| Depends on | MVP-ST-002, MVP-ST-003, MVP-ST-004 |
| Requirements | MVP-AR-01, MVP-FR-01, MVP-NFR-07 |
| MVP acceptance | 11, 23, 24, 32 |

## User story

As an AI4J user, I want unsupported host or Claude environments rejected before mutation so that AI4J never guesses at native behavior.

## Outcome

AI4J detects the supported macOS/Git/Claude environment, resolves the effective Claude configuration directory, and maps the detected Claude version to a tested fail-closed capability profile.

## Scope

- Detect macOS, Apple Silicon, supported Git, and supported Claude Code executables.
- Resolve the documented effective Claude configuration directory, including `CLAUDE_CONFIG_DIR` when supported.
- Discover documented native validation, marketplace registration, plugin install, inspection, update, enablement, and uninstall capabilities without executing toolkit content.
- Normalize supported native outputs behind the Claude adapter.
- Reject unknown version ranges, commands, flags, output shapes, or incomplete capabilities as unsupported.
- Publish the tested macOS, Git, and Claude version matrix for each release.

## MVP delivery rule

First make supported-host detection, bounded version probes, default Claude configuration discovery, and one tested read-only capability profile work end to end. Add new proof types, lifecycle owners, or mutation preparation only when a later story consumes them; this story performs no installation or target mutation.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-005.T1 — Model normalized environment facts

**Implementation**

- Add `internal/environment` value types for host tuple, executable identity, semantic or tool-specific version, effective user directories, policy observations, and capability profile identity.
- Define typed unsupported and incomplete-environment faults with actionable public context while excluding raw native output, credentials, and Claude-private cache or registry facts.

**Completion evidence**

- Constructor tests reject unknown operating systems, non-Apple-Silicon Darwin, malformed versions, relative user roots, duplicate capability facts, and missing required fields.
- Error-schema fixtures expose stable public fields and prove raw probe output and private-path canaries are absent.

### MVP-ST-005.T2 — Discover and qualify Git and Claude executables

**Implementation**

- Read the trusted host tuple through the ST-004 host port and reject anything other than `darwin/arm64`, then resolve the selected Git and Claude commands to canonical regular files executable by the current user before invoking bounded version probes.
- Run probes with caller context, direct argument vectors, sanitized environment, bounded output, and typed version parsers; never use a shell or execute a path supplied by toolkit content.

**Completion evidence**

- Recording-runner tests verify supported and rejected host tuples, exact executable selection, argv construction, environment handling, cancellation, timeout, and that malformed or oversized version output fails closed.
- Filesystem fixtures reject missing, non-regular, non-executable, replaced, or unsupported binaries before capability discovery continues.

### MVP-ST-005.T3 — Resolve documented Claude user configuration

**Implementation**

- Add `internal/target/claude/config` resolution for the documented default user configuration and supported `CLAUDE_CONFIG_DIR` override, returning canonical user root and rules-directory facts through typed target observations.
- Treat relative, empty-explicit, wrong-owner, symlinked, unsupported-version, or policy-prohibited locations as distinct results and do not inspect or encode Claude-private cache or registry schemas.
- Keep resolution read-only and do not create a missing configuration or rules directory.

**Completion evidence**

- Table-driven tests cover absent, valid, invalid, and unsupported override cases plus owner, containment, and symlink changes using isolated filesystem fixtures.
- Serialization checks prove only documented effective directories and policy facts enter core results; private-path canaries never do.

### MVP-ST-005.T4 — Register candidate Claude capability profiles

**Implementation**

- Add immutable version-qualified candidate profiles under `internal/target/claude/compat` for native validation and inspection plus the documented command grammars that later qualification may use for marketplace and plugin mutation.
- For each profile, declare supported flags, output decoders, scope semantics, and activation observations, but publish mutation eligibility as `unqualified`; candidate mutation grammars are available only to the isolated MVP-ST-014 qualification harness and not to lifecycle consumers.
- Return unsupported for gaps instead of selecting a nearest-version fallback, and require a separate immutable MVP-ST-014 qualification overlay before a production mutation port can be constructed.

**Completion evidence**

- Profile tests enumerate the published candidate/read-only matrix exactly and reject unknown, prerelease, incomplete, overlapping, or capability-missing versions.
- Compile-time and runtime capability tests prove neither an unsupported nor merely candidate profile lets a lifecycle consumer obtain or call a native mutation operation.

### MVP-ST-005.T5 — Normalize native observations fail closed

**Implementation**

- Implement profile-owned bounded decoders under `internal/target/claude` that translate supported native outputs into typed registration, installation, enablement, policy, reload, next-session, and current-session facts.
- Preserve unknown and not-observable states explicitly; reject unrecognized command output, field meaning, or completion shape and never infer current-session activation from registration or enablement.

**Completion evidence**

- Golden contract fixtures for every supported profile cover success, absence, policy blocked, reload required, next session required, malformed, truncated, and previously unseen output.
- Cross-profile negative tests prove output accepted by one profile cannot be silently interpreted under another and no normalized result contains adapter-private fields.

### MVP-ST-005.T6 — Prove read-only capability discovery and publish the matrix

**Implementation**

- Compose environment detection and Claude compatibility selection as a read-only service with caller context, no mutation port, and no dependency on source or installed toolkit content.
- Maintain release documentation generated from the same profile registry, clearly distinguishing candidate/read-only support from MVP-ST-014-qualified mutation support, and add a sentinel Claude profile fixture whose plugin commands would create a marker or listener if discovery executed content.

**Completion evidence**

- The compatibility suite passes every published candidate/read-only fixture, leaves mutation eligibility `unqualified`, and fails representative unknown command, flag, capability, version, and output-shape fixtures.
- Clean-host and sentinel tests show detection works with only Git and Claude installed, starts no toolkit process or listener, performs no native mutation, and keeps policy, reload, next-session, and current-session facts distinct.

## Story acceptance criteria

- [ ] A supported clean environment returns a complete typed capability profile and resolved directories.
- [ ] Unsupported architecture, missing executable, untested Claude version, incomplete capability, and unrecognized native output fail before mutation with actionable errors.
- [ ] Capability discovery starts no repository-provided script, binary, hook, or MCP server.
- [ ] Core state and errors contain no Claude-private cache path or undocumented schema.
- [ ] Policy-blocked, reload-required, and next-session-required facts remain distinct and are not reported as current-session activation.

## Verification

- Run contract fixtures for every supported Claude version and representative unknown outputs.
- Use a sentinel toolkit to prove discovery and read-only probes start no toolkit process or network listener.
- Run clean-host detection with only Git and Claude Code installed.

## Out of scope

- Performing installation or modification.
- Claiming support for a Claude version not covered by the compatibility suite.
