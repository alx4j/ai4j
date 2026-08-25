# AI4J — MVP Requirements

| Field | Value |
|---|---|
| Status | Simplified MVP baseline |
| Release | MVP |
| Product | AI4J |
| Executable | `ai4j` |
| Canonical repository identity | `github.com/alx4j/ai4j` |
| Project URL | `https://github.com/alx4j/ai4j` |
| Go module | `github.com/alx4j/ai4j` |
| Target | Claude Code |
| Platform | macOS on Apple Silicon (`darwin/arm64`) |
| Source | Public GitHub repository |
| Installation scope | Current user |
| Last revised | 2026-08-21 |

## 1. Purpose

The MVP is a small command-line utility that validates and manages one personal AI toolkit for Claude Code.

The product must prove the basic workflow before adding broader platform support or defense-in-depth mechanisms:

1. Select a public GitHub repository and resolve an exact commit.
2. Validate and disclose the toolkit without running toolkit content.
3. Preview and approve a change.
4. Install, inspect, update, and uninstall one toolkit for the current user.

`ai4j` is the normative executable name. “Toolkit” remains the domain term for installable AI content and toolkit-owned state.

## 2. Normative language

- **Must / must not**: required for MVP acceptance.
- **Should**: expected unless a documented limitation prevents it.
- **May**: optional behavior that must not weaken a required MVP guarantee.

## 3. Delivery principles

### 3.1 KISS

The MVP supports one target, one host platform, one scope, one installation, one package unit, and one source provider. The implementation must use direct, understandable workflows instead of general frameworks.

### 3.2 YAGNI

The MVP must not add an abstraction, policy language, compatibility profile, cache, recovery protocol, or extension mechanism solely for a possible future use. A boundary is required only when the MVP has a concrete consumer or when it isolates Claude-, Git-, or macOS-specific behavior from product logic.

### 3.3 Basic safety, not defense in depth

The MVP must validate input, keep its state private, avoid executing toolkit content, preserve secret references, and refuse to overwrite content it does not own. Advanced adversarial hardening is deferred to v1.

Implementation mechanisms are not product requirements. The MVP does not require a particular keychain helper, shell exception, file-descriptor binding scheme, Git subprocess manifest, `xcode-select` workflow, filesystem proof ledger, Bootstrap/Bundle lifecycle, APFS syscall algorithm, or named resource profile.

## 4. MVP outcome

An MVP user must be able to:

1. Use the built-in first-party repository or another public GitHub repository.
2. Validate its toolkit package without executing toolkit content.
3. Preview install, update, and uninstall operations.
4. Install the complete toolkit for the current user through Claude Code's documented interface.
5. Inspect the installed source commit, plugin state, managed rules, and ordinary drift.
6. Update a tracked branch or keep a tag or commit pinned.
7. Uninstall only the plugin and files owned by AI4J.

## 5. Scope

### 5.1 Included

| Area | MVP decision |
|---|---|
| Target | Claude Code only |
| Host | One release-tested Apple Silicon macOS range |
| Scope | Current user only |
| Installations | Zero or one installed toolkit |
| Source | Built-in or explicit public `github.com` repository |
| References | Default branch, branch, tag, or full commit |
| Package | One self-contained Claude plugin plus optional dedicated shared rules |
| Content | Skills, agents, support files, scripts or binaries, and command-based MCP declarations |
| Interface | Human-readable CLI and schema-versioned JSON |
| Prerequisites | Git and Claude Code |

### 5.2 Explicitly excluded

The MVP does not include:

- Private repositories, SSH transport, credential-helper integration, OAuth, or credential storage
- Codex or another target
- Windows, Linux, Intel macOS, or Rosetta support
- Custom Claude configuration roots, including `CLAUDE_CONFIG_DIR`
- Project scopes, multiple installations, per-asset selection, or named bundles
- Local-development sources, persistent source caches, offline operation, or a fetch command
- Hooks, LSP servers, monitors, or installation lifecycle scripts
- Live MCP startup tests or execution-oriented diagnostics
- Automatic rollback to a prior successful installation
- Durable rollback history, whole-file backups, or automatic multi-resource compensation
- Force overwrite or interactive conflict resolution
- A GUI, hosted service, or general-purpose secret store
- Claims that validated instructions or executables are benign

## 6. Architecture and ownership

### MVP-AR-01 — Claude adapter

Claude-specific behavior must be isolated behind a small adapter that uses documented Claude Code validation and plugin-management interfaces.

The adapter must not edit Claude's private plugin cache, internal registry, or undocumented settings. If the supported Claude version cannot perform a required operation through a documented interface, that operation is unsupported.

### MVP-AR-02 — Ownership

| Resource | Owner | AI4J responsibility |
|---|---|---|
| Temporary source workspace | AI4J during one command | Create, use, and remove it |
| Claude plugin and native cache | Claude Code | Request and inspect operations through the adapter |
| Minimal catalog or declaration required by Claude | AI4J | Create, record, and remove only its own file |
| Dedicated shared-rules file | AI4J | Create, checksum, update, and remove only its own file |
| Installation record and operation marker | AI4J | Store privately and update atomically |
| Existing Claude configuration and unrelated files | User or Claude Code | Never modify |

AI4J must not claim that uninstall erases Claude-owned cache bytes or immediately revokes content already loaded by a running Claude session.

### MVP-AR-03 — Repository package contract

The repository must contain:

- A versioned root manifest named `toolkit.json`
- One Claude marketplace definition
- Exactly one selected Claude plugin
- Optional shared-rules content declared by `toolkit.json`

The first-party repository must use toolkit identifier `ai4j` and plugin identifier `ai4j-default`. It must contain at least one useful skill with support material, one agent, non-empty shared rules, and one command-based MCP declaration.

The manifest must declare closed package roots and enough metadata to identify the toolkit, plugin, optional rules, declared executables, required or optional runtime dependencies, and supported host/Claude versions.

Go source, module files, CI, release files, signing files, stories, and unrelated repository content must not become installable merely because they share the repository.

### MVP-AR-04 — Minimal internal boundaries

Product logic may depend on small compile-time interfaces for:

- Claude validation and lifecycle operations
- Public GitHub source acquisition
- Host filesystem and process operations
- Installation state and locking

The MVP must not expose a dynamic extension API or implement unused variants for future targets, hosts, scopes, or sources.

## 7. Functional requirements

### MVP-FR-01 — Environment detection

Before a source or modifying operation, AI4J must verify:

- A supported Apple Silicon macOS host
- A usable Git executable
- A supported Claude Code executable and required plugin capabilities
- Claude's documented default current-user configuration directory
- Access to its private application-state location when state is needed

Unsupported environments must fail before target mutation and identify the missing prerequisite. Environment detection is read-only and must not create a missing Claude configuration directory.

The CLI itself must not require Python, Node.js, Java, Homebrew, or another runtime.

### MVP-FR-02 — Public GitHub source

The MVP must accept:

```text
owner/repository
https://github.com/owner/repository.git
```

It must reject other hosts, embedded credentials, local paths, alternate protocols, query strings, fragments, control characters, and option-like repository or reference values.

The canonical serialized identity is lower-case `github.com/<owner>/<repository>` without `.git`.

The built-in default is `github.com/alx4j/ai4j`. Omitting `--repo` selects it. `--ref` without `--repo` applies to it. An invalid explicit repository must fail and must never fall back to the default.

Git must be invoked directly without constructing a shell command. Private-repository authentication and SSH support are deferred to v1.

### MVP-FR-03 — Exact source revision

AI4J must support the default branch, an explicit branch, an explicit tag, or a full commit hash. It must resolve the requested reference to a full commit before package validation.

The operation must use a clean detached checkout of that commit. Plans, JSON, and installation state must keep the canonical repository, requested reference, resolved reference kind/name, and full commit distinct.

Default and explicit branches may update only to a fast-forward descendant of the installed commit. Tags and explicit commits remain pinned. Changing repository or requested reference in place is out of scope; the user must uninstall and install again.

Git hooks, submodules, Git LFS expansion, and external clean or smudge filters must be disabled or rejected.

### MVP-FR-04 — Temporary source workspace

Every source-consuming command must use one private operation-specific workspace outside the project, target content, and AI4J durable state.

The workspace must contain only the selected tracked content, be removed after normal or handled failure, and never become a persistent source cache. Read-only commands must leave no durable AI4J or target change.

An abandoned workspace may be removed later only when it is inside AI4J's temporary root and is clearly AI4J-owned. Advanced liveness, mount, and journal-aware scavenging is deferred to v1.

### MVP-FR-05 — Package validation

Before install or update, AI4J must validate:

- The root manifest and supported schema version
- Toolkit, marketplace, and plugin identity consistency
- Exactly one selected plugin and no external plugin dependency
- Claude-native package structure using the supported Claude validator
- Manifest-selected files under declared package roots
- MCP commands, arguments, placeholders, and environment references
- Optional shared-rules content
- Declared executables and required or optional host dependencies
- Reasonable file-count, file-size, and total-size limits

Only tracked regular files may be considered. Symlinks, traversal, special files, unsafe executable declarations, and destination-name collisions must fail validation.

Executable validation is static. It must not claim startup success or complete knowledge of transitive runtime dependencies.

Validation errors must include a stable code and a useful sanitized explanation.

### MVP-FR-06 — File and destination safety

Every AI4J-owned path must be resolved beneath its declared root. Relative paths must reject traversal, control characters, absolute forms, and unsafe identifiers.

AI4J must not follow a symlink when writing an owned destination. Before replacement or removal, it must verify ownership and the checksum recorded in installation state. A mismatch is a conflict and must not be overwritten.

Owned file replacement should use a same-directory temporary file and atomic rename. AI4J must never invoke `sudo` or request elevated privileges.

Defense against hostile same-user namespace races, mount substitution, hard-link attacks, and platform-specific filesystem edge cases is deferred to v1.

### MVP-FR-07 — Active-content disclosure

Validation and plans must disclose:

- Built-in or explicit source selection
- Canonical repository, requested reference, and exact commit
- Skills, agents, shared instructions, scripts, binaries, and support content
- MCP commands, arguments, placeholders, and referenced environment-variable names
- Whether an executable is toolkit-owned or host-resolved
- Added, removed, and changed active content for update

Disclosure must identify content by type, identifier, path, and checksum without printing complete instruction bodies, binary bytes, secret values, or raw child output.

The CLI must warn that validated instructions can influence AI behavior and installed executables may later run with the user's permissions.

### MVP-FR-08 — No implicit execution

AI4J must not execute repository-provided or installed scripts, binaries, hooks, or MCP commands during validate, plan, install, update, status, uninstall, or version operations.

Claude management commands are permitted only when the supported command is known not to start toolkit content. Otherwise the operation is unsupported.

### MVP-FR-09 — Planning and approval

`ai4j plan install`, `ai4j plan update`, and `ai4j plan uninstall` must show the intended source, exact commit, actions, active content, warnings, conflicts, and expected result without persistent changes.

A modifying command must recompute its plan after taking the installation lock. A non-empty plan requires approval. Interactive use prompts; JSON or non-interactive use requires `--yes`. `--yes` approves the plan but never bypasses validation or conflicts.

`--expected-commit` must prevent mutation when the reviewed and recomputed commits differ.

### MVP-FR-10 — Install

Install must:

1. Acquire the single installation lock.
2. Verify that no installation or unmanaged conflicting Claude identity already exists.
3. Resolve and validate the exact source commit.
4. Show and approve the recomputed plan.
5. Write an operation marker before the first external or owned mutation.
6. Install and enable the plugin through the Claude adapter.
7. Write the dedicated shared-rules file when declared.
8. Verify the observable plugin and owned-file state.
9. Atomically commit installation state and clear the operation marker.

The Claude handoff must use content derived from the validated exact commit. AI4J must not copy individual plugin components into Claude's private directories.

Repeating install for the same desired state must return `no_change`.

### MVP-FR-11 — Dedicated shared rules

Shared always-on instructions must use one AI4J-owned file under Claude's documented current-user rules directory.

AI4J must not edit an existing `CLAUDE.md` or unrelated rules file. An unmanaged destination or checksum mismatch is a conflict.

### MVP-FR-12 — MCP and executable declarations

The plugin may declare command-based MCP entries with an executable, ordered arguments, supported path placeholders, and environment-variable references.

AI4J must validate and disclose those declarations without starting the MCP server. Toolkit executables must remain inside the validated package; host executables must be declared and identified as external.

### MVP-FR-13 — Update

Update must use the repository and requested reference stored at install.

For a tracked branch, it must resolve a fast-forward exact commit, validate and disclose the package, obtain approval, apply the new plugin and rules through the same basic operation flow, verify the result, and then commit the new state.

For a tag or commit, update must report `pinned`. When no desired-state change exists, it must report `no_change`.

### MVP-FR-14 — Status

Status must report:

- Installed or not installed
- Toolkit and plugin identities
- Canonical repository, requested reference, and installed commit
- Observable plugin installation and enablement state
- Dedicated rules-file path and checksum state
- Ordinary drift as unchanged, modified, missing, or conflicting
- Whether an operation marker requires attention

Status is local and non-executing by default. `status --check-updates` may access the public GitHub source and report available, up-to-date, pinned, rewritten, or unknown.

### MVP-FR-15 — Uninstall

Before mutation, uninstall must verify the plugin identity, AI4J-owned catalog or declaration, rules file, and installation state.

It must remove the plugin through Claude's documented interface, remove only matching AI4J-owned files, and remove installation state last. Existing drift or an ownership conflict must stop uninstall without force.

Uninstall must preserve unrelated Claude configuration and user files. Claude-owned cache retention is not an AI4J failure.

### MVP-FR-16 — Installation state

The MVP supports zero or one installation. Private state must contain only what is needed to manage it:

- State schema version and installation identifier
- Toolkit and plugin identifiers
- Source-selection mode, canonical repository, requested reference, and exact commit
- Claude target and current-user scope
- AI4J version
- AI4J-owned catalog/rules paths and checksums
- Last successful operation identifier and timestamp

State must contain no secret values or Claude-private cache details. Unknown state schemas must block mutation.

### MVP-FR-17 — Locking and interrupted operations

Modifying commands must use one operating-system-backed installation lock with a bounded wait.

Before the first mutation, AI4J must write a small private operation marker containing the operation type and intended resources. After success, it must atomically commit installation state and remove the marker.

If a command starts while a marker exists, it must inspect the observable Claude and AI4J-owned state. It may complete safe cleanup or report `recovery_required`; it must not guess, overwrite conflicting content, or begin another mutation.

The MVP does not promise automatic rollback or transactional atomicity across Claude Code and AI4J-owned files.

### MVP-FR-18 — Failed-operation cleanup

Temporary source, staging, and operation files must be removed after normal or handled failure when AI4J can prove ownership.

The MVP must not persist whole-file preimages, previous native packages, or rollback history. When cleanup cannot be completed safely, it must retain the operation marker and report remediation.

### MVP-FR-19 — Secret references

The toolkit schema must not define inline secret-value fields. Secret-dependent settings may contain only environment-variable references matching:

```text
[A-Za-z_][A-Za-z0-9_]*
```

AI4J must not resolve, hash, persist, or print referenced values. It may report the variable name and whether it is present. If Claude's supported representation cannot preserve a reference, validation must fail.

### MVP-FR-20 — Confirmation and non-interactive use

Install, update, and uninstall require approval when they would change state. Declining approval makes no mutation and reports `cancelled`.

JSON mode and non-interactive use require `--yes` for a non-empty plan. Conflict handling remains conservative; the MVP has no force mode.

## 8. Command-line contract

### 8.1 Commands

```text
ai4j validate [--repo <github-reference>] [--ref <git-reference>] [--json]

ai4j plan install [--repo <github-reference>] [--ref <git-reference>] [--json]
ai4j install [--repo <github-reference>] [--ref <git-reference>]
              [--expected-commit <full-hash>] [--yes] [--json]

ai4j plan update [--json]
ai4j update [--expected-commit <full-hash>] [--yes] [--json]

ai4j status [--check-updates] [--json]

ai4j plan uninstall [--json]
ai4j uninstall [--yes] [--json]

ai4j version [--json]
```

The MVP has no target, scope, asset-selection, force, generic dry-run, SSH, or credential flags.

### 8.2 JSON output

Every command must support a versioned JSON response with this envelope:

```json
{
  "schemaVersion": 1,
  "command": "status",
  "status": "ok",
  "changed": false,
  "data": {},
  "warnings": [],
  "errors": []
}
```

`status` must be `ok`, `no_change`, `cancelled`, or `error`. `changed` is true only when the final committed desired state differs from the prior committed state.

JSON standard output must contain exactly one JSON document with deterministic collection ordering and no human prose or ANSI formatting. Errors must include a stable `code` and sanitized `message`.

### 8.3 Exit codes

| Code | Meaning |
|---:|---|
| `0` | Success, including `no_change` and `pinned` |
| `1` | User cancelled before mutation |
| `2` | Invalid CLI usage or missing approval |
| `3` | Unsupported or incomplete environment |
| `4` | Source or Git failure |
| `5` | Toolkit or Claude-package validation failure |
| `6` | Ownership, drift, concurrency, or expected-commit conflict |
| `7` | Operation, cleanup, or recovery failure |
| `8` | Unexpected internal failure |

## 9. Non-functional requirements

### MVP-NFR-01 — Reproducibility

Every operation must record and use an exact toolkit commit. The same supported AI4J build, exact commit, and Claude version should produce the same intended plugin and AI4J-owned rules bytes.

### MVP-NFR-02 — Least privilege

AI4J must run as the current user and never require elevation. Private directories must be current-user-only; private state and operation files must not be readable by other users. AI4J-owned active files must not be made more permissive than required.

### MVP-NFR-03 — Bounded work

The implementation must define and test reasonable finite limits for metadata, file count, individual and total bytes, child output, process duration, and lock wait.

External processes must honor cancellation and timeouts. AI4J should terminate the complete child process group when supported. Resource exhaustion must fail without overwriting unrelated content.

The MVP does not require hard disk quotas, APFS-specific capacity algorithms, filesystem proof objects, or fixed resource-profile identifiers.

### MVP-NFR-04 — Safe diagnostics

Human and JSON output must identify the operation and useful remediation without exposing credentials, secret values, complete active-content bodies, binary bytes, or raw child output.

### MVP-NFR-05 — Idempotency and concurrency

Repeating an operation against the same desired state must make no change. The single installation lock and recorded checksums must prevent silent concurrent overwrite.

### MVP-NFR-06 — Basic release integrity

The repository must build `ai4j` for `darwin/arm64` from locked Go dependencies using the pinned stable toolchain. The release must publish the binary, its version information, and a SHA-256 checksum.

Reproducible unsigned macOS and Windows artifacts with SHA-256 checksums are v1 requirements. Code signing, notarization, SBOMs, and provenance attestations are post-v1 release hardening.

### MVP-NFR-07 — Compatibility

Each release must publish the tested macOS, Git, and Claude Code versions. Contract tests must cover the supported Claude validation, install, inspect, update, and uninstall commands. Unknown output or missing required capabilities must fail safely.

### MVP-NFR-08 — Go repository contract

The implementation must remain an idiomatic Go module at `github.com/alx4j/ai4j`, with `cmd/ai4j` as the executable entry point and implementation packages under `internal/`.

The repository must pin one supported stable Go patch version and run formatting, unit tests, integration tests, `go vet`, and architecture checks in CI. Release builds must not use local module replacements.

## 10. Acceptance criteria

The MVP is complete when automated tests demonstrate all of the following:

1. The built-in public repository and an explicit public GitHub repository resolve to exact commits without source fallback.
2. Default branch, branch, tag, commit, pinned, and non-fast-forward update cases behave as specified.
3. Validation rejects malformed metadata, identity disagreement, unsupported components, path traversal, unsafe file types, and package-root escape.
4. `validate` produces deterministic human or JSON disclosure and leaves no persistent AI4J or target change.
5. Plans show exact source, active content, actions, conflicts, and expected result without persistent changes.
6. Install uses Claude's documented interface, writes only AI4J-owned rules and state, and is idempotent at the same commit.
7. Update applies only a validated fast-forward branch commit; tag and commit installations remain pinned.
8. Status reports installed source, observable plugin state, rules drift, and interrupted-operation state without starting toolkit content.
9. Conflict-free uninstall removes the Claude registration and matching AI4J-owned files while preserving unrelated content.
10. No command starts a repository script, binary, hook, or MCP server.
11. Secret values do not appear in state, markers, output, logs, or errors.
12. A changed owned file, conflicting native identity, concurrent modifier, or expected-commit mismatch prevents mutation.
13. A simulated interrupted modifying operation blocks new mutation until safe cleanup succeeds or `recovery_required` is reported.
14. Temporary workspaces are removed after normal and handled-failure completion.
15. Every command's JSON is schema-valid, deterministic, prose-free on standard output, and consistent with its exit code.
16. The first-party toolkit contains its required skill, support content, agent, rules, and MCP declaration while excluding Go source, CI, release, signing, and unrelated repository files.
17. A clean checkout builds the `darwin/arm64` executable with the pinned Go toolchain and no uncommitted dependency changes.

## 11. Deferred to v1

The following are explicitly not MVP work:

- Private GitHub repositories, SSH, existing credential helpers, and authentication compatibility matrices
- Codex, Windows, project scopes, multiple installations, and asset selection
- Local sources, persistent caches, offline workflows, and cache invalidation
- Strong same-user race, mount, hard-link, reparse-point, and cross-platform filesystem hardening
- Automatic crash recovery, compensation, rollback, durable history, and retained package artifacts
- Hard storage quotas and platform-specific resource-enforcement algorithms
- Opt-in executable or MCP startup diagnostics
- Reproducible macOS and Windows release artifacts with SHA-256 checksums
- Advanced target-version profiles and implementation-specific process allowlists

These items are owned by [V1_REQUIREMENTS.md](./V1_REQUIREMENTS.md) when they are already specified there. Release signing, notarization, SBOMs, and provenance attestations remain in the post-v1 backlog. An implementation detail not required by either document must not be added without a concrete need and a reviewed decision.

## 12. Informative platform baseline

These links identify the public integration points. Release contract tests remain authoritative.

- [Claude Code plugin reference](https://code.claude.com/docs/en/plugins-reference)
- [Claude Code plugin discovery and installation](https://code.claude.com/docs/en/discover-plugins)
- [Claude Code plugin marketplaces](https://code.claude.com/docs/en/plugin-marketplaces)
- [Claude Code configuration](https://code.claude.com/docs/en/configuration)
- [Claude Code MCP](https://code.claude.com/docs/en/mcp)
- [Go release history and support policy](https://go.dev/doc/devel/release)
- [Go toolchain selection](https://go.dev/doc/toolchain)
