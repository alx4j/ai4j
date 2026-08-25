# MVP-ST-003 — Publish the CLI, JSON, and exit-code contracts

| Field | Value |
|---|---|
| Status | Defined |
| Type | Shared product contract |
| Wave | 0 — Foundation and risk retirement |
| Relative size | M |
| Depends on | MVP-ST-001, MVP-ST-002 |
| Requirements | MVP command-line contract §7, MVP-FR-08, MVP-NFR-04 |
| MVP acceptance | 20, 21, 31 |

## User story

As an AI4J user or automation author, I want one stable command and response contract so that human and machine workflows interpret every result consistently.

## Outcome

The canonical `ai4j` command tree, common JSON envelope, typed command data, deterministic output, and exit-code mapping are implemented and schema-tested.

## Scope

- Implement only the MVP command grammar for `validate`, `plan install`, `install`, `plan update`, `update`, `status`, `plan uninstall`, `uninstall`, and `version`.
- Reject target, scope, selection, force, generic dry-run, and alternate-executable options.
- Implement the versioned JSON envelope and canonical command identities such as `plan.install`.
- Keep human output and JSON output on separate formatting paths.
- Publish command schemas and stable error objects with code, message, and typed context.
- Map `ok`, `no_change`, `degraded`, `cancelled`, and `error` results to the normative process exit codes.
- Encode distinct committed-cleanup-pending, rolled-back-cleanup-pending, post-mutation-compensated, and pre-mutation-failure result semantics.
- Keep `version` local and prevent it from instantiating source, target-native, or installed-content adapters.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-003.T1 — Parse only the MVP command grammar

**Implementation**

- Create `internal/cli` request types and a parser for the exact MVP command tree, canonical subcommand identities, and command-specific options; parse arguments before constructing source, target, state, or lifecycle dependencies.
- Reject alternate executable names, target, scope, selection, force, generic dry-run, and command-inapplicable options with typed usage faults rather than accepting ignored flags.

**Completion evidence**

- A table-driven grammar suite covers every command, option combination, omitted value, duplicate flag, unknown flag, and post-MVP option.
- Adapter-construction spies remain untouched for all version and usage-error paths.

### MVP-ST-003.T2 — Define the command-result and exit-code model

**Implementation**

- Add `internal/result` types for status, operation phase/outcome, update disposition, warnings, errors, and committed durable change, keeping lifecycle facts distinct from their process exit-code presentation.
- Implement one total mapping from typed results to exit codes, including no change, pinned, cancellation, pre-mutation failure, post-mutation compensation, both cleanup-pending branches, and unexpected failure.

**Completion evidence**

- An exhaustive table proves every allowed result maps to one exit code and enforces the normative `changed` semantics.
- Constructor tests reject contradictory states such as compensated with `changed: true`, a committed cleanup result without a durable outcome, or an error result with no typed error.

### MVP-ST-003.T3 — Publish versioned JSON contracts

**Implementation**

- Define the schema-version-1 envelope and command-specific data objects in Go, then publish matching schemas under `schemas/v1/` with closed enumerations and documented optional-field compatibility.
- Keep error context typed and bounded, represent absent values consistently, and sort every map-derived collection before encoding.

**Completion evidence**

- Representative success and every failure-family fixture validate against the corresponding published schema.
- Round-trip and golden tests prove deterministic field semantics, collection order, null/empty behavior, and rejection of an unrecognized closed-enum value.

### MVP-ST-003.T4 — Separate human and JSON rendering

**Implementation**

- Add separate `internal/cli/human` and `internal/cli/jsonout` renderers that consume the same typed result without parsing one another's output.
- Make JSON rendering write exactly one document to standard output, send no ANSI or prose there, bound error text, and return the exit code selected from the typed result.

**Completion evidence**

- Golden fixtures cover human and JSON output independently and prove JSON standard output decodes as exactly one value with no trailing prose.
- Shuffled input collections and repeated runs produce byte-identical JSON while the emitted `exitCode` equals the process result.

### MVP-ST-003.T5 — Implement the adapter-free version path

**Implementation**

- Add a version handler that reads immutable `internal/buildinfo` facts and returns product, executable, CLI source identity, Go version, build time, target tuple, and compiled default-source policy through both renderers.
- Wire `cmd/ai4j` so the version path does not build the lifecycle composition root or instantiate source, target-native, state, or installed-content adapters.

**Completion evidence**

- Dependency-spy tests prove `version` completes with all adapter factories absent or set to fail on construction.
- Version JSON and human golden fixtures agree on the same normalized build facts and validate against the version schema.

### MVP-ST-003.T6 — Qualify the end-to-end CLI contract harness

**Implementation**

- Build a process-level test harness with deterministic fake command handlers, isolated streams, caller-supplied build facts, and no ambient environment or working-directory dependence.
- Cover every canonical command identity and result family while leaving lifecycle implementation to its owning stories.

**Completion evidence**

- The harness proves all commands emit schema-valid JSON, prose-free standard output in JSON mode, deterministic ordering, structured bounded faults, and matching process exit codes.
- Repeated and shuffled test runs produce identical artifacts, and usage/version fixtures prove no source, target, state, or installed-content operation occurs.

## Story acceptance criteria

- [ ] Every command returns exactly one schema-valid JSON document in `--json` mode, with no prose or ANSI formatting on standard output.
- [ ] Collection ordering is deterministic and `exitCode` equals the actual process exit code.
- [ ] `changed` is false for no-op, cancelled, compensated, and unresolved partial-operation results.
- [ ] A successful `complete` result returns exit code `0` and reflects only its committed durable diff; `committed_cleanup_pending` returns exit code `8` and reflects only an already committed durable diff; `rolled_back_cleanup_pending` returns exit code `8` with `changed: false`; post-mutation `complete_rolled_back` returns exit code `7` with `changed: false`; and a pre-mutation failure retains its underlying exit code with `changed: false`.
- [ ] `pinned` is represented as `no_change` plus a typed update disposition.
- [ ] Invalid or post-MVP options fail as CLI usage errors without invoking source, target, or state adapters.
- [ ] `ai4j version --json` exposes product, executable, CLI source identity, Go version, build time, target tuple, and default-source policy fields.
- [ ] `version` performs no source, target-native, or installed-content operation and cannot start toolkit content.

## Verification

- Validate golden human output and JSON output against published schemas.
- Run exhaustive result-to-exit-code-and-`changed` table tests, including both cleanup-pending branches and pre/post-mutation failure.
- Confirm standard output contains exactly one JSON value for every command and failure family.

## Out of scope

- Implementing the lifecycle behavior behind each command.
- Compatibility aliases for another executable name.
