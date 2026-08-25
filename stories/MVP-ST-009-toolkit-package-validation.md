# MVP-ST-009 — Validate the toolkit and native package contract

| Field | Value |
|---|---|
| Status | Defined |
| Type | Validation capability |
| Wave | 1 — Secure read-only validation |
| Relative size | L |
| Depends on | MVP-ST-002, MVP-ST-005 |
| Requirements | MVP-AR-03, MVP-FR-05, MVP-NFR-01, MVP-NFR-03 |
| MVP acceptance | 4, 28 |

## User story

As an AI4J user, I want malformed or incompatible toolkit packages rejected before modification so that native handoff contains only the declared self-contained plugin.

## Outcome

AI4J validates the versioned root manifest, Claude-native package metadata, identity agreement, compatibility, and deterministic inventory without executing content.

## Scope

- Parse and validate `toolkit.json`, schema version, required fields, IDs, versions, package paths, host and Claude ranges, rules source, executable declarations, and runtime dependencies.
- Require exactly one self-contained Claude plugin and no external plugin dependency.
- Validate plugin and marketplace structures through supported Claude validation interfaces.
- Cross-check duplicated identity, path, executable, and MCP facts between root and native metadata.
- Keep Claude-native metadata authoritative for native behavior.
- Enforce an explicit MVP allowlist of native component types and fields; reject hooks, LSP servers, monitors, themes, channels, interactive `userConfig`, settings payloads, dependencies, supplemental marketplace components, and every other supported or auto-discovered component outside MVP scope.
- Require fail-closed native validation of the source plugin, committed developer marketplace, and generated marketplace using the compatibility-tested strict validation operation.
- Reject source plugin or marketplace `version` fields unless a tested commit-unique scheme is explicitly enabled; the default MVP package omits them so exact Git SHA remains native update identity.
- Enforce finite root-metadata size, parser nesting, repository file-count, individual-file-size, and total validated/rendered-byte limits before unbounded parsing or allocation.
- Produce stable validation errors with code, severity, source file, field/component, and explanation.
- Produce a deterministic package inventory and rendered-input digest.

## MVP delivery rule

Validate the smallest representative toolkit package first: one manifest, one plugin, one skill, one agent, shared rules, and one MCP declaration. Do not add membership graphs, persisted snapshots, duplicate wire models, or cross-story grants until the basic `validate` result is complete and a later story demonstrates the need.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-009.T1 — Parse the root manifest within fixed limits

**Implementation**

- Define the versioned `toolkit.json` wire model separately from validated domain types, and read it through an explicit byte limit before allocating or decoding the complete document.
- Perform a bounded JSON token pass that enforces maximum nesting and collection counts, then decode with the schema's unknown-field policy and validate required fields, identifier grammar, versions, compatibility ranges, and cross-field invariants.
- Return structured validation findings instead of panicking or exposing parser internals; keep source location and field context bounded and sanitized.

**Completion evidence**

- Boundary tests cover exactly-at-limit and over-limit bytes, depth, arrays, objects, strings, unsupported schema versions, unknown fields, duplicate semantic fields, and missing requirements.
- A seeded fuzz target proves malformed input cannot panic, hang, allocate beyond the configured bound, or emit unbounded error content.

### MVP-ST-009.T2 — Parse the native package with an explicit MVP allowlist

**Implementation**

- Parse the committed marketplace and selected plugin metadata from their fixed declared locations into narrow types that represent only the native fields AI4J must cross-check.
- Require exactly one selected plugin and reject dependencies, supplemental marketplace entries, `strict: false`, interactive configuration, hooks, LSP servers, monitors, themes, channels, settings payloads, unsupported custom component paths, and default-discovered unsupported component directories.
- Reject plugin or marketplace version fields by default; permit them only behind an explicitly registered commit-unique policy whose behavior is covered by the supported Claude profile.

**Completion evidence**

- Fixture tests cover every allowed native component and each recognized, auto-discovered, dependency, supplemental, interactive, and version-precedence rejection.
- Repeated parsing of semantically identical metadata yields the same typed package description and sorted findings.

### MVP-ST-009.T3 — Reconcile toolkit and native metadata

**Implementation**

- Cross-check toolkit, marketplace, and plugin identifiers; package and shared-rule paths; executable and MCP facts; and host/Claude compatibility declarations, treating native metadata as authoritative for native behavior.
- Require the selected plugin to be self-contained and reject every external plugin dependency or duplicated fact that disagrees with native content.
- Evaluate compatibility only against the typed host and Claude capability profile from MVP-ST-005 and fail closed on an unknown range, capability, source type, or schema value.

**Completion evidence**

- Cross-contract tests cover identity, path, executable, MCP, compatibility, dependency, and selected-plugin disagreement in both directions.
- An unknown-value matrix proves each unregistered schema, component, capability, or source value fails before rendering or target mutation.

### MVP-ST-009.T4 — Build a bounded deterministic package inventory

**Implementation**

- Expand only manifest-selected tracked entries beneath declared package and content roots, consuming regular-file facts through a narrow source-reader port and excluding every repository path outside those roots.
- Enforce file-count, individual-file, and cumulative validated/rendered-byte limits before reading or retaining complete content; stream SHA-256 calculation where content need not remain resident.
- Sort inventory entries by their canonical relative path and compute the rendered-input digest from an explicit, versioned serialization rather than filesystem traversal or map iteration order.

**Completion evidence**

- Limit tests cover zero, exact-boundary, and overflow file counts and byte totals, including many small files and one oversized file, without partial inventory success.
- Golden tests show deterministic entry order and digest while proving Go source, module, CI, release, signing, and unrelated files never enter inventory or rendered input.

### MVP-ST-009.T5 — Qualify the supported Claude validators

**Implementation**

- Implement native validation behind `internal/target/claude` using only the command shape registered by the detected compatibility profile and direct executable-plus-argument invocation, never a shell or repository command.
- Validate the source plugin, committed developer marketplace, and a generated-catalog fixture through the supported strict validation operation with bounded time and captured output.
- Normalize exit and diagnostic behavior into typed adapter results; unknown output, capability drift, truncation that hides authority, or a warning that violates AI4J policy must fail closed rather than be accepted from success prose.

**Completion evidence**

- Contract fixtures for every supported Claude version cover valid, warning, invalid, timeout, truncated, and unknown validator results for all three artifact shapes.
- Argument and process sentinels prove validation uses direct argv, respects cancellation/output limits, and starts no plugin script, binary, hook, or MCP process.

### MVP-ST-009.T6 — Assemble deterministic validation results

**Implementation**

- Combine AI4J and native findings into one immutable result with stable codes, severity, bounded source/field/component context, deterministic ordering, package inventory, and rendered-input digest.
- Redact or replace raw child output at the adapter boundary and ensure the result makes only structural, compatibility, and inventory claims, never benignness or runtime-startup claims.
- Publish the narrow validation service through the lifecycle consumer boundary without exposing Claude-private paths, schemas, or mutable parser representations.

**Completion evidence**

- Golden human and JSON fixtures remain stable across repeated runs and contain no raw native output, filesystem traversal order, or secret canary.
- Package tests prove equivalent valid input yields an identical result, while each malformed, incompatible, resource-bound, or native-contract case yields its intended stable error category.

## Story acceptance criteria

- [ ] Valid minimal and representative third-party fixtures produce the same deterministic result across repeated runs; MVP-ST-012 owns qualification of the shipped first-party package.
- [ ] Malformed schema, unsupported version, invalid identifier, missing file, identity mismatch, multiple plugins, or external dependency fails before mutation.
- [ ] Native validation and AI4J validation are both applied; native warnings do not silently weaken AI4J's fail-closed rules.
- [ ] Every unsupported recognized, default-discovered, dependency, supplemental-marketplace, or interactive-configuration component is rejected even when Claude would otherwise warn or merge it.
- [ ] Source and marketplace version metadata that could outrank Git SHA is rejected unless it is commit-unique and covered by the supported-version contract.
- [ ] Oversized metadata, excessive nesting, file-count overflow, individual-file overflow, and total-byte overflow fail with bounded errors before target mutation.
- [ ] Metadata outside declared package/content roots never enters inventory or rendering.
- [ ] Static validation makes no claim that content is benign or that runtime startup will succeed.
- [ ] Error objects are stable and contain no secret values or unbounded child output.

## Verification

- Maintain valid, malformed, incompatible, identity-conflict, and external-dependency fixtures.
- Maintain unsupported-component, auto-discovery, supplemental-marketplace, version-precedence, parser-depth, count, and byte-limit fixtures.
- Contract-test supported Claude validators and fail closed on unknown outputs.
- Snapshot deterministic inventory and error JSON.

## Out of scope

- Runtime execution or startup testing.
- External plugin dependencies or more than one native package unit.
