# MVP-ST-013 — Disclose active content and guarantee no implicit execution

| Field | Value |
|---|---|
| Status | Defined |
| Type | Trust-boundary capability |
| Wave | 1 — Secure read-only validation |
| Relative size | M |
| Depends on | MVP-ST-003, MVP-ST-005, MVP-ST-008, MVP-ST-009, MVP-ST-011, MVP-ST-012 |
| Requirements | MVP-FR-07, MVP-FR-08, MVP-NFR-04 |
| MVP acceptance | 9, 10, 16, 27 |

## User story

As an AI4J user, I want every active instruction and executable disclosed before approval without being run so that I can make an informed trust decision.

## Outcome

The active-content inventory and change-classification model is reusable by later plans, while `ai4j validate` exposes sanitized inventory and preserves the read-only no-execution boundary.

## Scope

- Inventory skills, agents, shared instructions, scripts, binaries, MCP commands, arguments, placeholders, and environment-variable names.
- Show component type, identifier, source path, checksum, change class, and executable ownership.
- Show source-selection mode, canonical repository, requested ref, and exact commit.
- Warn that instructions influence AI behavior and installed code may later run with user authority.
- Do not print complete instruction bodies, binary bytes, secrets, or opaque recovery content.
- Block any native management capability that is not compatibility-tested to avoid executing repository content.
- Expose a reusable policy that later lifecycle stories must apply before invoking any native command.
- Complete the user-visible `ai4j validate` flow by joining canonical source acquisition, package validation, sanitized inventory, human output, JSON output, and no-execution enforcement.

## MVP delivery rule

First render the current validated package through `ai4j validate`, with one bounded disclosure entry per active component and no execution. Defer persisted snapshots, uninstall reconstruction, member aggregation, and grant objects until later lifecycle stories consume them.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-013.T1 — Define the active-content disclosure model

**Implementation**

- Introduce immutable typed entries for skills, agents, shared instructions, scripts, binaries, and MCP commands, with stable component identity, canonical source path, SHA-256 checksum, executable ownership, and change classification.
- Model MCP arguments, supported placeholders, environment-variable names, and dependency classification explicitly; provide no field for a secret value, full instruction body, binary bytes, or opaque recovery content.
- Define deterministic ordering and closed enumerations for component type, ownership, and `added`, `removed`, `changed`, and `unchanged` classifications.

**Completion evidence**

- Type and serialization tests reject unknown enumeration values, incomplete entries, duplicate identities, unstable paths, and any attempted value-bearing secret field.
- Golden fixtures demonstrate deterministic ordering for a mixed first-party inventory independent of source traversal and map order.

### MVP-ST-013.T2 — Build sanitized disclosure entries from validation

**Implementation**

- Convert only the validated package inventory from MVP-ST-009 and the MCP/executable facts from MVP-ST-011 into disclosure entries; do not rescan the repository through a broader path.
- Preserve checksums, canonical paths, toolkit-owned versus host-resolved executable identity, ordered arguments, placeholders, environment names, and required/optional dependency facts.
- Attach source-selection mode, sanitized canonical repository, requested/resolved reference, and exact commit identity as distinct top-level disclosure context.

**Completion evidence**

- Package tests prove each allowed component produces one stable entry and every repository file outside the validated inventory remains undisclosed and inactive rather than being silently imported.
- Secret and body canaries in environment values, instruction bodies, binaries, unrelated files, and native diagnostics never enter the disclosure model.

### MVP-ST-013.T3 — Classify active-content changes deterministically

**Implementation**

- Match prior and proposed entries by stable component type and identity, then classify additions, removals, checksum or executable-metadata changes, and unchanged components without trusting display names alone.
- Treat a changed command, argument, placeholder, environment-reference name, executable ownership, or dependency requirement as an active-content change even when the source path is unchanged.
- Produce a stable sorted change set for both an empty prior installation and exact-commit-to-exact-commit comparison.

**Completion evidence**

- Table tests cover rename, type change, path change, body checksum change, command/argument/reference change, executable ownership change, and duplicate-identity conflicts.
- Reversing input order or repeating the comparison yields the same classified output and digest.

### MVP-ST-013.T4 — Render bounded human and JSON disclosure

**Implementation**

- Render the reusable disclosure through the canonical human and schema-v1 result boundaries, including the source facts, component metadata, change class, and mandatory trust warning.
- Bound collection and text output, escape control characters, and show checksums and command metadata without printing complete instruction bodies, binary bytes, secret values, raw child output, or recovery content.
- Apply the same warning and detail policy to built-in-default and explicit sources; only the typed source-selection field may differ for otherwise equivalent input.

**Completion evidence**

- Human and JSON golden tests cover initial and update disclosures, output truncation policy, control characters, deterministic collection order, and schema validation.
- Canary scans prove both renderers omit full content bodies, binary bytes, environment values and derivatives, raw native output, and opaque recovery text.

### MVP-ST-013.T5 — Enforce a qualified no-execution policy

**Implementation**

- Define a reusable lifecycle policy that permits only adapter operations explicitly marked non-executing by the detected Claude compatibility profile and rejects an unknown operation, profile, output contract, or reload/startup behavior.
- Require every allowed external call to originate from a trusted host/adapter executable descriptor and direct argument vector; repository paths may be data arguments but can never be selected as the executable.
- Keep reload commands, plugin activation probes, MCP startup, lifecycle hooks, and repository-provided commands outside the permitted MVP operation set.

**Completion evidence**

- Policy-matrix tests allow only the qualified read-only validation operations available in this wave and reject missing, unknown, drifted, reload-capable, and execution-capable profiles.
- Argument-capture plus process/network sentinels prove no repository script, binary, hook, MCP server, or indirect child starts through an allowed read-only adapter call.

### MVP-ST-013.T6 — Complete the read-only validate orchestration

**Implementation**

- Orchestrate `ai4j validate` through typed source selection, immutable workspace acquisition, package validation, sanitized disclosure, canonical result rendering, and guaranteed workspace close using the lifecycle consumer ports.
- Apply the no-execution policy before every Claude child invocation and map source, validation, unsupported-capability, and cleanup failures to the established stable result and exit contracts.
- Assert at the orchestration boundary that no stable catalog, target resource, installation record, history, journal, recovery artifact, or persistent source cache is created.

**Completion evidence**

- End-to-end fixtures for omitted-default, explicit-default, representative third-party, invalid, unsupported, cancelled, and cleanup-failure cases produce deterministic human or schema-valid JSON and the required exit result.
- Filesystem, process, and network sentinels prove each normal or handled validate result removes its workspace, leaves all durable product/target roots unchanged, and starts no toolkit content.

## Story acceptance criteria

- [ ] The disclosure model deterministically represents first-install and commit-to-commit added, removed, changed, and unchanged component fixtures.
- [ ] Disclosure identifies toolkit-owned versus host-resolved executables without exposing secret values.
- [ ] A default first-party source receives the same warning, disclosure, validation, and no-execution treatment as an explicit source.
- [ ] Sentinel scripts, binaries, hooks, and MCP servers remain unstarted through `validate` and every qualified read-only Claude validation child process available in this wave.
- [ ] Unsupported native commands that might reload or execute content fail before mutation.
- [ ] Human and JSON output remain bounded and omit full content bodies.
- [ ] `ai4j validate` returns deterministic human or schema-valid JSON results, removes its ephemeral workspace, and leaves no durable product or target state.

## Verification

- Maintain process and network sentinels around `validate` and supported Claude validation operations.
- Golden-test the reusable disclosure model for first-install and commit-to-commit change sets.
- Test canaries in content bodies, binaries, environment values, and validation errors.

## Out of scope

- Malware analysis or a claim that validated content is benign.
- Live MCP startup testing or an execution-oriented `doctor` command.
