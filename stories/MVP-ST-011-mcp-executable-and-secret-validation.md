# MVP-ST-011 — Validate MCP, executable, and secret declarations statically

| Field | Value |
|---|---|
| Status | Defined |
| Type | Security capability |
| Wave | 1 — Secure read-only validation |
| Relative size | M |
| Depends on | MVP-ST-004, MVP-ST-009, MVP-ST-010 |
| Requirements | MVP-FR-05, MVP-FR-08, MVP-FR-12, MVP-FR-19, MVP-NFR-04 |
| MVP acceptance | 4, 10, 11, 16, 27 |

## User story

As an AI4J user, I want MCP, executable, runtime, and secret references checked without startup so that unsafe declarations fail before they become active.

## Outcome

AI4J statically validates declared commands, arguments, placeholders, environment references, file modes, architecture, and required host dependencies while never resolving secret values.

## Scope

- Validate command-based MCP definitions, argument arrays, environment-reference maps, and supported Claude path placeholders.
- Resolve toolkit-owned executables only within the selected package and host executables without a shell.
- Require executable files to be regular, declared, safely permissioned, and compatible by supported static evidence.
- Classify declared host runtimes and executables as required or optional.
- Accept only environment-variable names matching `[A-Za-z_][A-Za-z0-9_]*`.
- Reject literal values in every known native secret-bearing field.
- Preserve the exact environment-variable reference through validated native rendering; reject a target mapping that would strip the reference or require materializing its value.
- Never resolve, hash, derive, persist, or print secret values.

## MVP delivery rule

Validate the first-party MCP shape, environment names rather than values, and one explicitly selected executable mode. Defer versioned descriptor profiles, cross-story parser ownership, and additional executable forms until a second supported package or runtime requires them.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-011.T1 — Parse the command-based MCP contract

**Implementation**

- Define narrow wire and domain types for command-based MCP entries, executable command, ordered argument vector, environment-reference map, and the supported `${CLAUDE_PLUGIN_ROOT}` and `${CLAUDE_PROJECT_DIR}` placeholders.
- Require an executable plus argument array rather than a shell command, reject unsupported MCP transports or fields, and validate placeholder placement without expanding it against the current machine.
- Treat every known secret-bearing native field as reference-only and accept an environment name only when it matches `[A-Za-z_][A-Za-z0-9_]*`; reject literal values and ambiguous reference syntax.

**Completion evidence**

- Table tests cover valid definitions plus shell strings, unknown transports/fields, empty commands, option-like executable ambiguity, malformed argument types, unknown placeholders, literal secrets, and environment-name boundaries.
- A seeded parser fuzz target proves malformed MCP metadata cannot panic, hang, exceed configured input bounds, or leak its raw payload through an error.

### MVP-ST-011.T2 — Isolate environment presence from secret values

**Implementation**

- Expose a host port that returns only a referenced environment-variable name and a presence boolean; do not allow an environment value, length, hash, prefix, suffix, or derivative to cross the port.
- Keep presence checks optional and separate from configuration rendering so no lifecycle path needs a value to validate, plan, install, update, inspect, or uninstall the toolkit.
- Centralize sanitization for errors and diagnostics at this boundary and prohibit environment snapshots or raw process environments in structured results.

**Completion evidence**

- Interface and serializer tests prove the typed result has no field capable of carrying an environment value and that absent and present references render identically except for the boolean.
- Secret-canary tests cover present values, inherited process environment, invalid references, and error paths without finding the canary or any derived form in JSON, human output, or logs.

### MVP-ST-011.T3 — Resolve executable ownership without execution

**Implementation**

- Model toolkit-owned and host-resolved executables as distinct typed variants; resolve toolkit-owned paths only through the closed package root from MVP-ST-010.
- Resolve a host executable through the Darwin host boundary without a shell and return only a sanitized absolute identity plus required/optional classification, never child output or credentials.
- Require every executable and runtime dependency to be declared, reject an undeclared or ambiguous command, and make a missing required dependency an error while retaining a bounded warning for a missing optional dependency.

**Completion evidence**

- Fixtures cover bundled, host-resolved, undeclared, missing-required, missing-optional, out-of-root, symlinked, and option-like executable declarations.
- An execution sentinel proves resolution performs lookup and filesystem inspection only and never starts the resolved file or an interpreter.

### MVP-ST-011.T4 — Inspect executable compatibility statically

**Implementation**

- Require a declared executable to be a regular file with an allowed source mode and no setuid, setgid, sticky, ACL, or extended-permission semantics that would be propagated into rendered content.
- Inspect bounded file headers with standard Go parsers where possible: validate supported Mach-O architecture for binaries and parse a bounded shebang into one declared host interpreter plus fixed arguments for scripts.
- Return only static compatibility facts and explicitly retain `unknown` for transitive libraries, runtime startup, protocol behavior, and other evidence that would require execution.

**Completion evidence**

- Static fixtures cover supported and wrong-architecture Mach-O, malformed and oversized headers, valid and invalid shebangs, missing interpreters, unsafe modes, special files, and polyglot/unknown inputs.
- Tests assert every accepted result identifies the evidence used and never claims startup success or transitive dependency completeness.

### MVP-ST-011.T5 — Render references deterministically

**Implementation**

- Render native MCP configuration from the validated domain model with stable field and map ordering, preserving ordered arguments, supported placeholders, and each environment-variable reference exactly.
- Reject any target mapping that drops a reference, converts it to an inline value, requires reading the environment value, or changes command/executable ownership semantics.
- Emit a sanitized active-code inventory containing MCP ID, command, arguments, placeholder facts, environment names, executable ownership, dependency classification, and checksums, but no content body or value.

**Completion evidence**

- Round-trip and golden tests prove identical inputs yield identical native bytes and inventory, with environment references unchanged and values absent.
- A deliberately incapable target mapping fails before mutation, while map-order and locale variations do not change rendered bytes or digest.

### MVP-ST-011.T6 — Qualify the static trust boundary

**Implementation**

- Join MCP parsing, presence-only reporting, executable resolution, static inspection, deterministic rendering, and structured validation findings without introducing an execution path.
- Apply bounded child-output sanitization to any host or Claude validation result before it reaches domain errors, JSON, human output, or logs.
- Publish reusable valid, invalid, secret-canary, executable, and no-execution fixtures for MVP-ST-012 through MVP-ST-014 and later lifecycle stories.

**Completion evidence**

- End-to-end validation fixtures demonstrate required failures, optional warnings, exact reference preservation, bounded errors, and deterministic inventory while process and network sentinels remain untouched.
- A surface scan of validation JSON, human output, logs, rendered fixtures, and captured errors finds no secret canary, raw child output, or claim that MCP startup was tested.

## Story acceptance criteria

- [ ] Valid MCP and executable fixtures produce a deterministic sanitized inventory without starting a process.
- [ ] Undeclared, out-of-root, unsafe-mode, wrong-architecture, malformed-placeholder, literal-secret, and missing-required-runtime cases fail before mutation.
- [ ] Missing optional dependencies produce bounded warnings only.
- [ ] Plans and diagnostics expose environment-variable names and presence only, never value, length, hash, prefix, suffix, or derivative.
- [ ] Secret canaries never appear in validation errors, validation JSON, human validation output, or validation logs.
- [ ] The rendered native package retains each declared environment-variable reference unchanged and contains no resolved value; an incapable mapping fails before mutation.
- [ ] Validation does not claim knowledge of transitive runtime dependencies or startup behavior.

## Verification

- Use Mach-O, script, wrong-architecture, mode, placeholder, and runtime fixtures.
- Place secret canaries in known secret fields, unrelated files, error paths, and child output.
- Inspect rendered native package fixtures to verify exact reference preservation; MVP-ST-014 verifies the final handoff boundary.
- Use sentinel commands to prove no validation path starts MCP or executable content.

## Out of scope

- MCP health checks, startup tests, or live connection status.
- A general-purpose secret store.
