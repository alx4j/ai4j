# MVP-ST-002 — Establish the typed MVP core and extension registry

| Field | Value |
|---|---|
| Status | Defined |
| Type | Architecture enabler |
| Wave | 0 — Foundation and risk retirement |
| Relative size | L |
| Depends on | MVP-ST-001 |
| Requirements | MVP-AR-04, MVP-NFR-07, MVP-NFR-08 |
| MVP acceptance | 32, 33 |

## User story

As an AI4J maintainer, I want lifecycle logic to depend on explicit typed contracts so that v1 can add targets, hosts, sources, scopes, and selections without replacing the MVP core.

## Outcome

The core compiles against stable ports and registers only the Claude, Darwin, GitHub, user-scope, whole-toolkit, and short-lived-journal implementations allowed by the MVP.

## Scope

- Define compile-time ports for target-native operations, host/filesystem/process behavior, immutable source acquisition, state/locking/recovery persistence, clock, and identifier generation.
- Model target, host, scope, source mode, selection mode, capability, commit identity, tree identity, and rendered digest as distinct typed values.
- Provide fake target, host, source, state, clock, and identifier implementations.
- Add dependency and cycle checks that keep target-neutral core packages independent of Claude- and Darwin-specific code.
- Reject unknown or unregistered enum, schema, adapter, host, scope, source, and selection values before mutation.
- Keep the seams internal; do not create a public dynamic plugin API.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-002.T1 — Define the closed MVP domain vocabulary

**Implementation**

- Create `internal/domain` types for target, host, scope, source mode, source selection, selection mode, capability, commit identity, tree identity, and rendered-package digest, with constructors that enforce canonical values and keep repository, tree, package, and CLI build identities distinct.
- Define closed MVP constants only for Claude, Darwin, GitHub Option A, user scope, whole-toolkit selection, SHA-1 commit identity, and short-lived recovery; preserve unknown wire values for explicit rejection rather than silently mapping them to defaults.

**Completion evidence**

- Table-driven tests cover every valid MVP value, invalid zero/unknown value, identifier invariant, and equality rule without depending on Claude- or Darwin-specific packages.
- Compile-time and serialization-boundary fixtures prove commit OIDs, tree OIDs, package digests, and CLI build commits cannot be assigned interchangeably.

### MVP-ST-002.T2 — Establish stable core error semantics

**Implementation**

- Add `internal/fault` typed categories and detail records for invalid input, unsupported capability, conflict, cancellation, timeout, and internal failure while preserving wrapped causes and excluding adapter-private or secret-bearing data.
- Keep domain validation errors independent of CLI exit codes; the CLI story will own presentation and process mapping.

**Completion evidence**

- Tests branch with `errors.Is` or `errors.As`, retain causal errors through wrapping, and verify stable category and typed-context fields.
- Redaction fixtures prove error construction cannot serialize raw credentials, unbounded child output, or Claude-private paths through the core detail types.

### MVP-ST-002.T3 — Define consumer-owned boundary ports

**Implementation**

- Define small interfaces beside their consumers in `internal/lifecycle` for target observation and mutation, host filesystem and process operations, immutable source acquisition, installation state, locking, journaling, recovery material, clock, and identifier generation.
- Put `context.Context` first on blocking operations, make cancellation and resource ownership explicit, split read and mutation capabilities, and avoid a single broad adapter or storage interface.

**Completion evidence**

- Compile-time fixtures show each port can be implemented independently and that read-only consumers cannot receive mutation methods accidentally.
- Contract tests prove cancelled contexts propagate to every blocking fake boundary and no interface stores a context or depends on a package-global singleton.

### MVP-ST-002.T4 — Build an explicit, fail-closed implementation registry

**Implementation**

- Add `internal/registry` as an immutable value assembled by composition code, with typed registrations for target, host, source, scope, selection, and recovery policy; do not expose runtime loading or global registration hooks.
- Register only the MVP values and return typed unsupported errors for unknown, unregistered, duplicate, or capability-incomplete entries before any mutation-capable dependency is returned.

**Completion evidence**

- Registration tests enumerate the exact MVP capability set and reject duplicate, missing, unknown, and post-MVP variants deterministically.
- A mutation-spy fixture proves every rejected lookup completes before any mutation port is called.

### MVP-ST-002.T5 — Provide deterministic boundary fakes

**Implementation**

- Add focused test implementations under `internal/testkit` for target, host, source, state, lock, journal, recovery, clock, and identifier ports; each fake records typed observations and supports bounded, explicit failure injection.
- Give fake clocks and identifiers caller-supplied sequences, keep fake state instance-local, and avoid wall-clock time, random IDs, shared directories, sleeps, or mutable process globals.

**Completion evidence**

- Compile-time assertions verify every fake satisfies the same consumer-owned port as its production counterpart.
- A target-neutral lifecycle fixture substitutes all fakes, propagates cancellation, and produces repeatable observations across shuffled test runs.

### MVP-ST-002.T6 — Enforce dependency direction and extension behavior

**Implementation**

- Add architecture checks that parse the Go package graph, reject cycles, and prevent `internal/domain`, `internal/fault`, and target-neutral `internal/lifecycle` packages from importing `internal/target/claude` or `internal/host/darwin`.
- Add test-only second target and host registrations to prove extension occurs through adapters and composition without adding branches to core lifecycle code.

**Completion evidence**

- Dependency fixtures fail on a forbidden import and cycle, then pass for the intended acyclic dependency graph.
- The second fake target and host complete registration and the representative orchestration fixture without changing a core lifecycle package or exposing a new MVP CLI capability.

## Story acceptance criteria

- [ ] Core lifecycle packages import no Claude- or Darwin-specific implementation package.
- [ ] The registered MVP capability set contains only the variants required by current product behavior.
- [ ] Fake target, host, source, state, clock, and identifier implementations satisfy the same compile-time ports as production adapters and can be substituted in a core orchestration fixture.
- [ ] Unknown target, host, scope, source, selection, state-schema, and capability values fail closed before mutation.
- [ ] Adding a second fake target or host requires registration and adapter code but no change to core lifecycle orchestration.

## Verification

- Run architecture dependency and cycle tests in CI.
- Run table-driven registration and unknown-value tests.
- Exercise representative typed port calls and adapter substitution entirely through fakes; downstream planner and recovery stories own their end-to-end fake orchestration tests.

## Out of scope

- Registering Codex, Windows, project scopes, local source, per-asset selection, or durable rollback.
- Runtime-loaded extensions.
