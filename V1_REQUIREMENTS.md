# AI4J — v1 Requirements

| Field | Value |
|---|---|
| Status | Final v1 scope baseline with explicit estimation gates |
| Release | Post-MVP v1 |
| Product | AI4J |
| Executables | `ai4j` on macOS; `ai4j.exe` on Windows |
| Canonical repository identity | `github.com/alx4j/ai4j` |
| Project URL | `https://github.com/alx4j/ai4j` |
| Go module | `github.com/alx4j/ai4j` |
| Targets | Claude Code lifecycle and packages; Codex packages with native interactive installation |
| Platforms | macOS Apple Silicon and Windows x64 (`windows/amd64`), live-qualified on Windows Server 2025 |
| Source | GitHub; local checkout in development mode |
| Last revised | 2026-08-25 |

## 1. Purpose and relationship to the MVP

v1 expands the native-first AI4J MVP into a multi-toolkit Claude manager and a cross-target package authoring/build tool. It adds Codex package output, Windows x64, project scopes, selective assets, local development, advanced reconciliation, and durable rollback based on structural history plus bounded toolkit-owned package artifacts.

The [MVP requirements](./MVP_REQUIREMENTS.md) remain normative unless this document explicitly supersedes them. This document restates the important retained invariants so that v1 scope can be reviewed independently.

`ai4j` is the canonical command root on macOS, and `ai4j.exe` is the Windows executable filename. Changing either canonical name is a breaking CLI-contract change. “Toolkit” remains the generic domain term for installable content.

## 2. Normative language

- **Must / must not**: required for v1 acceptance.
- **Should**: expected unless a documented implementation constraint justifies otherwise.
- **May**: optional behavior that must not weaken a required guarantee.
- **Evaluation item**: required research and an architecture decision, but not a committed runtime feature unless its decision record selects it.

## 3. v1 outcome

A v1 user must be able to:

1. Manage multiple Claude Code toolkits independently and build the same logical selections as native Codex packages.
2. Install Claude Code toolkits at user, project-local, or project-shared scope using target-native mechanisms.
3. Select complete toolkits, named bundles, or individual assets with validated dependencies.
4. Preview every lifecycle, target-state, installation-state, or retained-history mutation and consume the same result as versioned JSON.
5. Build and validate target-native packages from the first-party default repository, another GitHub commit, or a local development checkout.
6. Reconcile selection and configuration changes without overwriting unrelated user content.
7. Inspect drift, capabilities, compatibility, and update differences.
8. Roll back successful operations using durable structural history and exact toolkit-owned native package artifacts.
9. Diagnose MCP startup only through an explicit, constrained, opt-in execution path.
10. Receive the same safety and recovery guarantees on supported macOS and Windows hosts.

## 4. Scope

### 4.1 Included

| Area | v1 decision |
|---|---|
| Targets | Claude Code for authoring, build, and lifecycle; Codex for authoring/build followed by native interactive installation |
| Hosts | macOS on Apple Silicon and Windows x64 (`windows/amd64`); the Windows profile is live-qualified on Windows Server 2025 |
| Scopes | Claude lifecycle: `user`, `project-local`, and `project-shared` |
| Installations | Multiple toolkits and multiple independent installations of a toolkit |
| Source | Built-in `github.com/alx4j/ai4j` default, another canonical GitHub source, or explicit local-development mode |
| Selection | Complete toolkit, or one or more named bundles and/or assets |
| Content | Skills, agents, instructions, references, support assets, scripts or binaries, MCP definitions, prompt templates, native hooks, and target-specific extensions |
| Management | Claude install, update, synchronize, status, diagnostics, rollback, and uninstall |
| Authoring | Initialize, validate, build, and inspect target-native output |
| Delivery | Reproducible unsigned `ai4j` and `ai4j.exe` executables with SHA-256 checksums, built from one pinned stable Go toolchain |

### 4.2 Explicitly excluded

v1 does not include:

- A single atomic operation across Claude and Codex
- Automated Codex install, enable, update, status, or uninstall; Codex currently documents only interactive plugin management through its native plugin browser
- Linux, Intel macOS, Windows 10, or Windows on ARM
- Arbitrary Git providers, arbitrary destination paths, or installation from an unsupported or noncanonical URL
- Organization-managed or administrator-enforced installation scope
- Dynamic semantic-version tag selection or dependency resolution from remote registries
- General environment profiles or conditional policy languages
- A hosted synchronization service, GUI, credential store, or general-purpose secret store
- Arbitrary repository lifecycle scripts
- Automatic Git add, commit, push, or pull-request creation for project-shared files
- Retained plaintext whole-file backups
- Apple Developer ID signing and notarization, Windows Authenticode signing, SBOM publication, and provenance attestations
- Live Claude/Codex qualification on clean supported hosts and canonical GitHub tag/release publication

## 5. Retained safety invariants

Unless an approved evaluation decision explicitly revises one of these contracts, v1 must retain the following MVP behavior:

- Every Git installation resolves to and records an exact immutable commit.
- The first-party default is source-selection shorthand only and receives no trust, validation, execution, approval, or recovery exemption.
- `--dry-run` on a modifying command is the only dry-run concept and makes no persistent toolkit-controlled change.
- GitHub source acquisition uses operation-specific ephemeral workspaces and no toolkit-managed persistent source cache is populated. A local development checkout remains user-owned and read-only.
- Repository content is active AI/code content; validation does not claim that it is benign.
- Normal commands never execute toolkit scripts, binaries, hooks, or MCP servers.
- Native package managers own their caches and registries; the toolkit uses supported public interfaces.
- Adapter-owned writes are path-contained, checksum-guarded, and per-file atomic where supported.
- Modifying operations use locks and refuse to overwrite unresolved or conflicting state.
- Shared configuration is changed structurally and only within recorded toolkit ownership.
- Secret-dependent fields contain environment-variable references, not inline values.
- Machine-readable output has a versioned schema and stable error codes.

### V1-HARD-01 — Hardening deferred from the simplified MVP

The simplified MVP deliberately proves the basic single-target workflow before adding defense in depth. v1 owns the following deferred areas through the existing requirements shown here:

| Deferred area | v1 owner |
|---|---|
| Private GitHub sources and existing Git/SSH authentication | V1-FR-11 |
| Strong cross-platform path, race, link, mount, and executable checks | V1-FR-32 through V1-FR-34 |
| Crash recovery, compensation, rollback artifacts, and retained history | V1-FR-24 through V1-FR-27 |
| Multiple-installation and shared-resource locking | V1-AR-05 and V1-NFR-04 |
| Opt-in executable or MCP startup diagnostics | V1-FR-28 and V1-FR-29 |
| Enforced resource bounds and disk preflight | V1-NFR-03 |
| Reproducible cross-host release artifacts and checksums | V1-NFR-05 |
| Persistent source-cache alternatives | V1-EVAL-01 |

These requirements define outcomes, not mandatory mechanisms. A keychain helper, shell exception, file-descriptor binding scheme, Git subprocess manifest, `xcode-select` workflow, filesystem proof ledger, Bootstrap/Bundle lifecycle, APFS-specific syscall sequence, or named resource profile requires separate evidence and an approved architecture decision before it becomes normative.

## 6. Architecture requirements

### V1-AR-01 — Core and target adapters

v1 should extend the MVP's existing target, host, source, and state contracts where they remain useful rather than replace working lifecycle logic. It may add a clock or another test seam when a concrete v1 behavior requires one, but it still does not expose a dynamic Go plugin API for management behavior.

The core must own:

- Source identity and immutable revision resolution
- Toolkit and asset identity
- Scope semantics
- Desired-state calculation
- Selection and dependency resolution
- Planning and approval
- Ownership, locking, journaling, history, and reconciliation
- Stable human and JSON results

Each target adapter must own:

- Capability discovery
- Native package rendering and strict validation
- Native scope mapping
- Native install, inspect, update, enablement, and uninstall operations
- Target-specific instruction and configuration semantics
- Target-specific drift classification and reload requirements

Core state must not contain hard-coded Claude or Codex private cache paths or undocumented schemas.

Every adapter must distinguish native declaration, current-user installation, enablement, current-session loading, reload requirement, and next-session availability when the target exposes those concepts. It must not collapse a project declaration into proof that the package is installed or loaded for the current user.

### V1-AR-02 — Capability-driven integration

At runtime, an adapter must discover the capabilities of the installed target version. A release must declare the target versions and capabilities it supports.

An operation must fail before mutation with `unsupported_capability` when its required native behavior is unavailable. The CLI must not compensate for a missing public capability by editing a target's private cache, database, registry, or undocumented state.

Unrecognized native command output must fail closed. Compatibility shims must be version-bounded and covered by adapter contract tests.

### V1-AR-03 — Native mapping principles

The canonical model must preserve intent while allowing target-native representation. It must not force all targets into a lowest-common-denominator file format.

Target overlays may express model selection, tool availability and permissions, filesystem or network access, sandbox behavior, MCP access, hook events, and other native metadata. Unsupported properties must be reported explicitly under the mapping rules below and must never be ignored silently.

| Canonical intent | Claude adapter | Codex adapter |
|---|---|---|
| Reusable skill | Claude-native plugin skill | Codex plugin or native Agent Skill |
| Specialized agent | Claude-native plugin agent | Supported Codex native agent/subagent capability |
| Persistent instructions | Dedicated Claude rules file or structurally owned target file | Supported `AGENTS.md` hierarchy or target-native plugin mechanism |
| MCP server | Claude-native plugin MCP declaration | Codex plugin or supported MCP configuration |
| Hook or platform extension | Claude-native hook when declared | Codex-native hook or extension when supported |
| Reference/support file | Native package-owned file | Native package- or skill-owned file |
| Script or binary | Native package-owned executable | Native package- or skill-owned executable |

Mappings are release-version sensitive. The adapter must report the actual mapping and resulting activation scope in every plan.

If a canonical property cannot be represented faithfully for a target, validation must report one of:

- `unsupported`: the operation cannot proceed.
- `lossy_mapping`: the operation requires explicit approval and the plan identifies the lost behavior.

Silent loss or reinterpretation is prohibited.

### V1-AR-04 — Native package ownership

Where a supported target-native plugin or package manager exists, the adapter must use it and leave cache, enablement, and dependency ownership to that manager.

Direct structural configuration is permitted only for a supported feature that lacks a native package operation, such as a documented instruction file. Such a resource must have explicit ownership boundaries, checksums, conflict handling, and a structural inverse.

A native cache may retain bytes after uninstall. Success means the toolkit is no longer declared or active at the selected scope and all adapter-owned active content is safely removed.

Every native install or update must consume the validated immutable package artifact or a supported native source pinned to the same full commit or local-source digest. A native version string is not commit provenance.

Each adapter must define a tested target-specific provenance contract. Before mutation, it must prove that the exact rendered native declaration or artifact handed to the target is immutable. After mutation, it must verify every identity, source, version, installation, and enablement fact that the supported public interface exposes. When a target does not expose the installed source revision, the adapter must not claim otherwise; it must rely on a supported immutable handoff contract, such as Claude's exact `sha` in the plugin source, and verify the observable native state. If the native manager may re-resolve a mutable source or ignore the immutable pin, the operation must fail as unsupported and reconcile any completed native action.

### V1-AR-05 — Shared-resource coordination

Multiple installations may share a native registration, configuration container, or instruction file. The CLI must maintain a resource ownership graph and reference counts where required.

A native package resource must have a deterministic key that includes the target's native identity domain, native registry or marketplace identifier when applicable, and native package or plugin identifier. For Claude, a marketplace identifier is user-wide native state even when installations use different project roots or logical scopes.

Two installations may share a native package resource only when their catalog or declaration source, exact source commit or local digest, rendered package digest, resolved selection, native identity, and enablement requirements are identical. Otherwise the adapter must allocate deterministic distinct native identifiers or fail before mutation with `native_identity_conflict`. It must never refresh, redirect, update, disable, uninstall, or roll back a native resource in a way that changes the bytes or activation required by another installation.

Marketplace registrations should be dedicated to a single compatible native package resource. A shared or unmanaged marketplace must be retained unless supported native introspection proves removal safe. Project-shared declarations and catalog references must be portable and stable for collaborators; they must not point to a private ephemeral workspace or a user-specific absolute path.

A resource may be removed only when no live installation or unmanaged native consumer still requires it. Locks must cover both the installation and every shared native-resource key or structural resource affected by an operation.

Every modifying operation must acquire a deterministic logical-identity lock derived from `(target, scope kind, canonical scope root, toolkit identifier)`. A first install or archived reactivation must hold that lock through installation-ID selection/allocation and state commit. An existing active or archived installation must additionally acquire its installation-ID lock.

The complete lock set, including logical identity, installation ID when present or proposed, and identifiers for absent shared resources being created, must be computed before mutation and acquired in deterministic lexical order. If reconciliation discovers another required lock, the CLI must release, replan, and reacquire; it must never expand the lock set after mutation starts. Ownership-graph and reference-count changes must be journaled and committed or recovered with installation state.

The v1 journal must record the logical tuple, canonical root, existing or proposed installation ID, acquired lock keys, and every ownership-graph mutation.

## 7. Canonical toolkit model

### V1-FR-01 — Versioned manifest and asset envelope

The versioned `toolkit.json` schema must provide a common envelope with:

- Schema version
- Toolkit identifier, version, display metadata, and compatibility
- Optional stable project-shared declaration identifier
- Asset identifier, type, source path, and ownership class
- Optional asset dependencies
- Optional named-bundle membership
- Target and host compatibility selectors
- Executable and environment-reference declarations
- Target-native overlays and package metadata

Asset and declaration identifiers must be unique in their respective namespaces and match `[a-z][a-z0-9-]{1,62}`.

The common envelope must not duplicate the complete native schemas of Claude or Codex. Native payloads or overlays remain target-specific and must be validated by their adapter.

### V1-FR-02 — Supported asset types

v1 must support canonical declarations for:

- Skills
- Agents
- Persistent shared instructions
- Prompt templates
- Reference documents and support assets
- Scripts and binaries
- Command-based MCP servers
- Target-native hooks
- Target-specific extension metadata

An asset may include target-specific variants. Selection must choose exactly one compatible variant or fail as ambiguous or unsupported.

Target-native hooks are content installed for the target to invoke later. The AI4J CLI must not run them as installation lifecycle scripts.

### V1-FR-03 — Dependency graph

Assets may declare dependencies on other assets in the same toolkit. The resolver must:

- Compute a deterministic transitive closure
- Reject missing dependencies and cycles
- Reject a dependency with no compatible target/host variant
- Explain why each implicit asset was selected
- Preserve stable ordering in plans and JSON

Remote package dependencies and arbitrary lifecycle dependencies remain unsupported.

### V1-FR-04 — Canonical schemas

The CLI must accept only the canonical toolkit, installation-state, history, and journal schemas defined for the first public release. Any other schema version must fail closed without rewriting source or state.

AI4J has no runtime upgrade path for unreleased development formats. Authors must regenerate an outdated toolkit manifest, and users must remove outdated local development state before retrying. A future public-format upgrade requires an explicit release requirement and compatibility design; it must not be implemented speculatively.

State and history writes remain atomic, exclusively locked where required, and recoverable for interrupted current-format operations. Unknown schema versions must block mutation without reinterpretation.

## 8. Targets, hosts, and scopes

### V1-FR-05 — Supported target and host matrix

The supported tuples are:

```text
claude / darwin-arm64
claude / windows-amd64
codex  / darwin-arm64
codex  / windows-amd64
```

Every operation that renders, installs, updates, synchronizes, diagnoses, rolls back, or removes target content must identify exactly one target directly or through one installation ID. A synthetic `both` target is prohibited. `init` may scaffold one or multiple explicitly selected targets; `list` and `version` may be target-agnostic because they do not reconcile target content.

The v1 compatibility documentation must declare contract-tested target-version ranges and required native capabilities for each tuple. It must distinguish automated contract coverage from any later live-host qualification.

### V1-FR-06 — Canonical scopes

v1 must expose exactly these writable scopes:

| Scope | Meaning |
|---|---|
| `user` | Available to the current user across projects |
| `project-local` | Available only to the current user in one project and not intended for version control |
| `project-shared` | Declared in project files suitable for version control and collaboration |

The adapter must map the canonical scope to a documented native scope or supported composition of native mechanisms. It must disclose the mapping in the plan.

`project-local` and `project-shared` require a canonical project root. By default, the CLI must use the nearest enclosing Git worktree root; `--project <path>` may select another explicit root after canonical containment validation.

For `project-local`, the adapter must use a native local scope or ensure adapter-owned files remain untracked without changing tracked project content. For `project-shared`, the CLI may modify declarative project files but must never stage or commit them.

Managed or policy-owned resources are read-only. A policy that blocks or makes the requested customization ineffective must produce `policy_blocked` before mutation.

### V1-FR-07 — Target-specific scope mapping

The Claude adapter must resolve the documented effective configuration directory, including `CLAUDE_CONFIG_DIR` when the supported Claude version documents that override. It must disclose the effective root before mutation and reject an unsupported or unusable override.

The Claude adapter must map canonical scopes to Claude's supported `user`, `local`, and `project` plugin/configuration semantics.

For Claude persistent instructions, the required mapping is:

| Canonical scope | Adapter-owned instruction mapping |
|---|---|
| `user` | `<effective-config-dir>/rules/<installation-id>.md` |
| `project-shared` | `<project-root>/.claude/rules/<declaration-id>.md` |
| `project-local` | A uniquely named file under `<project-root>/.claude/rules/` plus a toolkit-owned Git-local exclusion resolved through `git rev-parse --git-path info/exclude`, or `unsupported` when this composition cannot be proven safe |

The Claude adapter must not assume that a plugin-root `CLAUDE.md` supplies persistent instructions. A project-local rules file must already be untracked and safely excluded before activation; an existing tracked path is a conflict.

For Claude project-shared scope, status must distinguish `declared_in_project`, `installed_for_current_user`, `enabled`, and `loaded`, because a shared declaration does not prove that every collaborator has the plugin in a local native cache.

For GitHub-backed Claude user and project-local plugin installation, the adapter may register its generated exact-SHA catalog from a stable private AI4J-owned path. Project-local activation metadata must remain untracked and user-specific.

For a Claude `development_source` installation at user or project-local scope, the adapter must not fabricate a Git `sha` or leave the native registration pointing to an ephemeral snapshot. It must render the validated native units and catalog into a private, immutable, content-addressed install-backing bundle. The bundle key must be the SHA-256 digest of a canonical complete descriptor covering the installed source digest, target and host profile, canonical scope identity, resolved selection and enablement requirements, ordered native-unit identities and package digests, every payload-relative file path and content digest, exact catalog or declaration digest, native marketplace and plugin identities, and adapter contract and capability-profile versions. The descriptor must be stored as private immutable sidecar metadata associated with the bundle, outside the bundle payload; it inventories only the native-unit and catalog payload and never includes or hashes itself. The native declaration must use a documented local-source form that resolves only within that bundle. If the supported Claude version cannot install and retain that declaration through its public interface, Claude installation from `development_source` is unsupported; validation and build remain available.

The install-backing bundle is narrow native installation state, not a source cache. It must contain only manifest-selected rendered plugin units and the catalog needed by the live native registration; it must not contain the original checkout, unrelated repository bytes, mutable authoring state, or its sidecar descriptor. AI4J must create, checksum, permission, journal, reference-count, and remove the bundle and descriptor together like other private adapter-owned state. Both must remain immutable while any native registration refers to the bundle, may be used only to reconcile or remove those recorded registrations, and must not satisfy ordinary source, build, update, offline-install, or rollback requests.

For Claude project-shared plugin installation, the adapter must structurally manage only the documented inline `extraKnownMarketplaces` marketplace with `source: "settings"` in tracked `<project-root>/.claude/settings.json`. Every Git-subdirectory plugin entry must carry the exact validated commit `sha`. Tracked settings must use the stable project-shared declaration ID and portable repository-relative or GitHub identities; they must not contain private installation IDs, private catalog paths, or user-specific absolute paths.

AI4J must request plugin installation, enablement, disablement, and removal through Claude's native manager with the explicit intended scope. The native manager owns `enabledPlugins`, enablement state, cache lifecycle, and native marketplace registration; AI4J must not directly write or claim structural ownership of `enabledPlugins`. An unscoped native operation that can affect another editable scope is unsupported.

Project-shared uninstall must complete and reconcile the scoped native plugin removal and then the scoped native marketplace removal. Only after native deregistration succeeds may AI4J structurally remove its owned inline `extraKnownMarketplaces` object if the native operation leaves that object behind, and only after reference and checksum checks. If the supported Claude version cannot safely deregister the inline marketplace at the intended scope, the operation must fail before mutation as `unsupported_capability`; deleting project configuration alone must never be reported as successful native uninstall.

The Codex package adapter must map skills, instructions, MCP configuration, and plugins to documented Codex package mechanisms. AI4J must fail automated Codex lifecycle requests before source acquisition or mutation with `unsupported_capability` and direct the user to the native interactive plugin browser. It must not edit Codex private caches, databases, registries, or infer lifecycle state from package files.

The same toolkit may use different native mappings on the two targets while retaining the same logical asset identities.

## 9. Multiple installations and selection

### V1-FR-08 — Installation identity

An installation is uniquely identified by:

```text
(target, scope kind, canonical scope root, toolkit identifier)
```

Every installation must also have an immutable generated installation ID used by CLI commands and machine output.

Project-shared declarations must additionally use a stable manifest-defined `declarationId`, defaulting deterministically to the toolkit identifier when omitted. It is part of tracked ownership markers and filenames and must be identical for every collaborator. The private installation ID remains per user and canonical root. Status, ownership, and history must link both identifiers.

Once project-shared state exists, its declaration ID is immutable. A proposed change must fail as an ownership conflict rather than create a second tracked declaration.

The same identity tuple always reconciles the existing installation; “multiple installations of one toolkit” means distinct target/scope/root tuples. For user scope, the canonical scope root is the canonical current-user home and private-state domain.

While an archived tombstone exists, reactivation must use `install --installation <archived-id>`. That form must use the tombstone's stored target, scope, source mode, canonical source identity or checkout, requested reference, exact commit or local digest, and resolved selection. Source, target, scope, project, selection, and expected-revision overrides are invalid on the reactivation form. `--allow-dirty` is the only source-related exception: it is valid only when the tombstone records `development_source`, and it is required when the stored checkout currently contains dirty or untracked input. The reactivation must still reproduce the tombstone's exact stored source digest or fail before mutation.

A normal new-install invocation that resolves to a logical identity with an archived tombstone must fail before mutation and identify the archived installation ID. To change source, revision, scope, or selection, the user must reactivate the exact archived installation and then use the appropriate update or sync operation, or explicitly purge all retained history and the tombstone before performing a fresh install. After that purge, a later install generates a new installation ID.

An archived installation may be inspected, have history purged, roll back its retained uninstall, or be reactivated by the explicit archived-install flow above. `update`, `sync`, and another `uninstall` must reject an archived installation. Rolling back the uninstall or reactivating it restores the prior active state under the same installation ID.

State must be independent by installation while shared-resource ownership is coordinated globally. It must include the canonical scope root, source mode and commit/digest, selection intent and resolved closure, shared-resource ownership references, native-resource keys, history references, native identifiers, checksums, and health. These facts must never be inferred from another installation merely because toolkit IDs match.

An in-place source or reference change must preserve the toolkit identifier and installation ID. A source declaring a different toolkit identifier is a separate installation and must not inherit ownership.

### V1-FR-09 — Asset and bundle selection

New install and synchronize operations must require either:

- `--all` by itself; or
- One or more `--asset <asset-id>` and/or one or more `--bundle <bundle-id>` values.

`--asset` and `--bundle` must be repeatable and may be combined. `--all` is mutually exclusive with both.

Archived reactivation is the only install form that takes its selection from stored state instead of requiring `<selection>`.

The plan must distinguish explicitly selected assets, bundle members, and transitive dependencies.

Changing selection must use `sync`; rerunning install must not create a second installation with the same identity.

### V1-FR-10 — Named bundles

A bundle is a named set of assets in the toolkit manifest. Bundles may include other bundles only when the resulting graph is acyclic and deterministic.

Bundle expansion must be visible in human and JSON plans. Removing an asset from a bundle must not remove a resource still required by another selected asset or bundle.

Each adapter must deterministically render the resolved asset closure into one or more native installation units. A selection change replaces or reconciles complete native units through supported native interfaces; the CLI must never edit an installed Claude or Codex cache directory to simulate per-asset ownership.

For a GitHub-backed Claude installation, every native unit must map exactly to a manifest-declared, self-contained plugin subdirectory at the resolved commit. The applicable catalog must contain one Git-subdirectory entry per selected unit, with repository, path, and exact `sha` matching that unit's validated source. AI4J must reject a selection that would require byte-level filtering or dynamic assembly within one immutable Claude plugin unit. A toolkit author must split independently selectable Claude content into independently addressable plugin subdirectories.

## 10. Source and authoring workflows

### V1-FR-11 — GitHub source

v1 retains the MVP canonical `github.com` identity, exact-commit, immutable-checkout, ref ambiguity, and fast-forward rules. It extends acquisition to public and private repositories through existing system Git and SSH authentication. AI4J must not store credentials, accept credential-bearing URLs, or implement OAuth.

When validate, build, or a new-source install (including `install --dry-run`) omits both `--repo` and `--source`, it must use the built-in `github.com/alx4j/ai4j` repository. `--ref` without `--repo` applies to that repository. An explicit `--repo` selects another GitHub repository; an explicit error must never fall back to the default.

Default expansion must occur before identity, locking, state, cache, or archived-tombstone comparison. Plans, JSON, and state must carry the MVP `sourceSelection`, effective repository, requested/resolved reference, typed commit identity, and rendered digest. v1 additionally records `sourceMode` as `github` or `development_source`; local mode uses `sourceSelection: explicit`.

The `github.com/alx4j/ai4j` repository must retain its first-party toolkit and provide validated Claude and Codex variants for its declared default assets and bundles. Target-specific variants must remain within closed manifest-selected content roots and must not make Go source or repository automation installable.

Submodules, Git LFS expansion, repository hooks, external filters, unsafe transports, credential-bearing URLs, dirty source snapshots, and untracked installation bytes remain unsupported for GitHub installations.

### V1-FR-12 — Source and reference changes

An existing installation may switch to another accepted GitHub repository or reference only through an explicit update request.

Omitting `--repo` and `--ref` during update always means the installation's stored source, never the current AI4J release default. A change to the compiled-in default must not initiate or imply a source change.

The source-change plan must show:

- Old and new canonical repository identities
- Old requested reference and installed commit
- New requested reference, reference kind, and exact commit
- Source-level asset changes
- Rendered/native package changes
- Ownership transfers and resources to remove
- Compatibility, trust, and rollback implications

Changing the repository or reference requires explicit approval even when no rendered byte changes.

### V1-FR-13 — Local-development source

Authoring commands may use an explicit local checkout through `--source <path>`. Local mode must never be selected implicitly from the current directory.

Local installation and synchronization are development workflows. They must be labeled `development_source` in state and status; the canonical distributable source remains GitHub.

Local source mode must:

- Canonicalize and contain all paths within the selected checkout
- Apply the same schema, active-content, size, file-type, collision, and secret-reference validation as GitHub source
- Record the Git commit when available and a deterministic content digest for the built input
- Reject dirty or untracked input unless `--allow-dirty` is explicitly supplied
- Mark dirty builds and installations as non-reproducible and ineligible for normal remote update
- Avoid mutating the developer's checkout

`--source` is mutually exclusive with both `--repo` and `--ref`. Omitting all three selects the built-in GitHub repository only for a new-source command; local mode is never inferred.

`development_source` is unsupported with `project-shared` scope because a user-owned local checkout cannot provide a portable collaborator source. The combination must fail before planning target mutations.

The local checkout remains user-owned persistent input, not a toolkit cache. Validation, planning, build, install, update, and sync may copy its selected bytes into a private ephemeral snapshot so the operation uses one stable digest. A modifying operation must revalidate the source digest while holding its mutation locks. `--expected-source-digest <sha256>` must be supported for review/apply consistency; `--expected-commit` is GitHub-only.

A local-source installation must retain the canonical checkout path and installed source digest. Update or sync may consume the current checkout only after showing its new digest and dirty state. A dirty or untracked current tree requires `--allow-dirty`; otherwise the operation fails before mutation. A missing checkout makes source-dependent update or sync unavailable without damaging installed state.

### V1-FR-14 — Initialize

`ai4j init` must create a minimal, valid toolkit skeleton only in an explicit empty output directory. It must fail rather than overwrite or merge into a non-empty directory.

The generated skeleton must include the root manifest, selected target package scaffolds, sample assets only when requested, validation instructions, and appropriate generated-output ignore rules. A newly scaffolded repository must pass validation without manual changes.

### V1-FR-15 — Build

`ai4j build` must render deterministic target-native packages without installing them.

Build must:

- Resolve selection and dependencies
- Render one explicitly selected target/host tuple
- Validate canonical and native output strictly
- Produce a manifest of input and output checksums
- Identify lossy or unsupported mappings
- Write only to an explicit empty or toolkit-owned build directory
- Never execute built content

The same source digest, CLI version, target capability profile, and build options must produce the same output bytes except for explicitly documented non-reproducible metadata.

## 11. Planning and lifecycle operations

### V1-FR-16 — Complete plan

`--dry-run` on the corresponding modifying command is the only dry-run interface. It must be available for install, update, sync, rollback, uninstall, and history purge. A dry run returns the same complete plan that normal interactive execution displays before confirmation, but it never prompts or mutates.

In addition to MVP disclosure, a v1 plan must include:

- Installation ID, target, canonical scope, and project root when applicable
- Explicit assets, bundle expansion, dependencies, and selected variants
- Canonical-to-native mappings and target capability decisions
- Native commands as sanitized executable and argument arrays
- Shared-resource reference-count changes
- Structural configuration edits and owned instruction sections/files
- Current, desired, rollback, degraded, and conflict states
- Source and generated diffs at an appropriate bounded level
- Durable history entry that a successful operation would create
- Retained history entries and rollback material that a purge would remove

Plans must not resolve secret values or execute toolkit content.

Unless V1-EVAL-01 is approved with a revised contract, GitHub-backed planning may use only private ephemeral source workspaces and must make no persistent toolkit-controlled change. Local-backed planning treats the user-owned checkout as read-only input and may create only an ephemeral validation snapshot; it must not modify that checkout.

### V1-FR-17 — Install

Install must resolve exactly one target, scope, toolkit source, and selection. It must validate all dependencies and target mappings before mutation.

For a new installation, omitted source flags resolve to the built-in first-party repository through V1-FR-11. Archived reactivation uses its tombstone's exact stored source through `--installation`. For an existing active installation, update and sync always begin from recorded source state; they must not re-evaluate the current built-in default.

The adapter must use supported target-native package operations wherever available. Direct configuration writes must be limited to documented resources with structural ownership.

The selected asset closure must be rendered into complete native installation units before native validation. The adapter must install those units as a whole and must not edit native cache internals after installation.

The operation must use installation and shared-resource locks, a durable crash journal, per-resource checksum preconditions, post-operation reconciliation, and explicit active-content approval.

### V1-FR-18 — Synchronize selection

`sync` must reconcile an existing installation to a new asset or bundle selection. For GitHub installations it must retain the installed source commit unless update is explicitly requested. For local installations it must follow the current-checkout digest rules in V1-FR-13 and disclose any simultaneous source-content change.

Sync must:

- Add newly required assets and dependencies
- Update retained assets only when their desired representation changed
- Remove resources no longer selected and not shared by another selected asset
- Preserve unrelated content and shared resources
- Produce durable structural rollback history

### V1-FR-19 — Update and diff

Update must support:

- Fast-forward movement of a tracked branch
- Explicit change of repository, branch, tag, or commit
- Source-level asset diff
- Selected-asset and dependency diff
- Target-native rendered diff
- Compatibility warnings
- Active instruction, executable, hook, and MCP diff

No pinned reference, explicit source change, or non-fast-forward tracked reference may move without explicit new source/reference input, exact-commit disclosure, and approval. A stored tracked branch may advance automatically only by fast-forward.

`--repo` without `--ref` selects the new repository's default branch. `--ref` without `--repo` applies to the stored GitHub repository. A rewritten branch or moved tag requires explicit reference input plus a matching `--expected-commit`. Expected revision mismatch is a pre-mutation conflict. A source change must not change the toolkit identifier.

Switching an existing installation between GitHub and local source modes is not supported. For an existing local installation, update uses its stored canonical checkout; `--repo` and `--ref` are invalid, while `--allow-dirty` and `--expected-source-digest` apply. Changing that checkout path requires uninstall and install. For a GitHub installation, local-source options are invalid.

### V1-FR-20 — Status and list

`list` must enumerate installations without accessing the network or executing toolkit content.

`status` must identify and report exactly one installation:

- Logical source, exact revision, selection, target, scope, and native mapping
- Native declaration, installation, enablement, reload, policy, and health states when observable
- Adapter-owned and native-observable drift as `unchanged`, `safely_replaceable`, `modified_owned`, `conflicting_unmanaged`, `missing`, `orphaned`, `invalid`, or `unknown`
- Shared-resource ownership
- Last successful operation and rollback descriptors with current restorability
- Incomplete transaction or unresolved recovery state
- Local-development and non-reproducible markers
- Update disposition as `not_checked`, `not_installed`, `up_to_date`, `available`, `pinned`, `ref_rewritten`, or `unknown`

`status --check-updates` may access the network through the selected source-acquisition strategy but must not mutate installation state or target resources.

Plain status must be local. An attempted update check that cannot resolve or authenticate the source must report `unknown` and use the inherited source-failure exit code, not claim `up_to_date`.

### V1-FR-21 — Uninstall

Uninstall must remove only resources exclusively owned by the selected installation and decrement shared-resource ownership safely.

It must use the same plan, approval, lock, journal, checksum, structural merge, and recovery rules as install and update. Drifted resources must remain unchanged unless the plan and apply command select the same approved v1 policy for safe toolkit-owned content.

Uninstall must create a durable rollback point sufficient to restore the prior logical installation under V1-FR-25.

## 12. Conflict handling

### V1-FR-22 — Conflict classification

The CLI must distinguish:

- Unmanaged destination collision
- Unmanaged configuration-key collision
- Toolkit-owned drift
- Concurrent modification after planning
- Cross-installation ownership conflict
- Native-manager state conflict
- Policy-managed or policy-blocked state
- Source/reference rewrite
- Unsupported or lossy target mapping

Every conflict must identify the affected resource, expected ownership/checksum, observed state, allowed actions, and whether any earlier mutation requires recovery.

### V1-FR-23 — Conflict policies

The default policy is `fail` before mutation.

v1 must also support:

- `keep`: preserve drifted content already owned by the same installation, adopt its observed checksum as an explicit exception, and record the installation as degraded when the user accepts that result. It must not adopt an unmanaged collision or omit a dependency-critical resource.
- `replace-owned`: replace only content already proven to be owned by the same installation, after showing the diff and retaining a structural rollback action.
- `interactive`: choose among safe actions for each conflict in a terminal.

JSON mode must never prompt. It must receive a complete policy through flags or fail before mutation. `interactive` is valid only on an executing command in a terminal and must be rejected with `--dry-run`, JSON, or non-terminal execution.

No policy may silently replace an unrelated unmanaged file or configuration entry. A future `backup-and-replace` policy for unmanaged whole files is conditional on V1-EVAL-02 and is not part of the committed v1 scope.

For uninstall, `keep` must explicitly relinquish toolkit ownership of the preserved drifted resource and report it as an orphan; it must not claim that the preserved bytes remain rollback-managed.

## 13. Recovery and rollback

### V1-FR-24 — Crash-recovery journal

v1 introduces a versioned crash-recovery journal with explicit success, compensated-failure, terminal-outcome, and cleanup states. Every target, state, or history mutation must be crash-recoverable and per-resource atomic where the operating system and target interface allow it.

Native actions must be journaled as intent plus observed result. Recovery must inspect current native state and either roll forward, apply a documented safe compensating operation, or report `recovery_required` without guessing.

Automatic inverse actions may modify a resource only when its current checksum or native identity matches the post-operation value written by the interrupted operation.

### V1-FR-25 — Durable structural rollback history

Durable rollback history must be separate from the short-lived crash-recovery journal.

Until explicitly purged under V1-FR-26, history must retain at least the latest successful install, update, sync, and uninstall of each installation:

- Operation identifier, type, timestamp, and CLI version
- Toolkit, target, scope, source reference, and exact commit
- Selection and native identity changes
- Pre-operation and post-operation checksums
- Structural inverse actions
- Previous toolkit-owned values or managed instruction content required by those inverse actions
- A bounded, checksummed previous target-native package artifact when exact native reinstall is required

The default history format must not retain whole-file copies of unrelated JSON, TOML, YAML, Markdown, or target configuration.

The retained native package is a rollback artifact, not a general source cache: it may be used only by rollback for its recorded installation/operation, must contain only validated toolkit-owned package output, and must follow private-storage and retention rules. It must not satisfy ordinary plan, build, update, or offline-source requests.

Previous toolkit-owned values, instructions, and package artifacts are opaque and potentially sensitive. They must never be displayed in full, logged, indexed, uploaded, or exported by diagnostics.

Rollback must first produce a plan. `ai4j rollback --dry-run` returns that plan without mutation; normal interactive rollback displays it before confirmation. Rollback itself must use current locks, a new crash journal, checksum preconditions, target capability validation, and active-content disclosure.

Before install, update, sync, or uninstall mutates, it must create and checksum every prior structural value and native artifact required by the new rollback point and prove through the current supported adapter that the point can be restored. If it cannot, the operation must fail before mutation.

A rollback point may be advertised as restorable only while the adapter proves that a supported public target interface can install the recorded exact native artifact/source and restore every required structural inverse. `rollback_unsupported` is permitted only when a point proven usable at commit later loses restorability because the installed target or CLI capability changed. Status must expose that downgrade, and rollback apply must fail before mutation.

If current state no longer matches the expected post-operation state, rollback must report a conflict and must not overwrite drift automatically.

### V1-FR-26 — History retention and purge

The CLI must:

- Create and retain at least one rollback point proven usable when each successful modifying operation commits; automatic retention must not remove the last usable point
- Apply a documented bounded count- or age-based retention policy
- Report history size and expiration in status
- Support explicit safe purge of one selected operation, expired history, or all history
- Protect history with the same private-storage guarantees as installation state

Purging history must not alter current target-native or adapter-owned content. For an active installation, it must not alter the current installation record except its history references. For an archived installation, removing the final retained rollback point also removes its tombstone as a disclosed part of the same journaled state mutation.

After uninstall commits, a minimal archived installation tombstone keyed by the immutable installation ID must remain until its final rollback point is purged. `list` and `status` must distinguish archived from active installations.

Every purge request must select exactly one of `--operation <operation-id>`, `--expired`, or `--all`. Its plan must disclose the exact descriptors and package artifacts removed, any loss of rollback capability, whether an archived tombstone will be deleted, and that a future install will receive a new installation ID after tombstone deletion. Purge must acquire the logical-identity, installation, and history locks and journal its metadata and tombstone changes.

### V1-FR-27 — Short-lived preimages

v1 may create a full-file preimage only for crash recovery when a toolkit-owned resource cannot be reversed structurally. Preimages must be private, opaque, and deleted during committed or rolled-back cleanup.

They must not be promoted silently into persistent rollback snapshots.

## 14. Diagnostics and explicit execution

### V1-FR-28 — Static diagnostics

`doctor` must be static and non-executing by default. It may check:

- Host, Git, target, and adapter compatibility
- Manifest, history, and journal integrity
- Native package declaration, installation, enablement, and policy state
- Managed paths, checksums, permissions, ACLs, and drift
- Executable file type, target architecture, and declared interpreter availability
- MCP registration and whether required environment-variable names are present

It must not start a script, binary, hook, or MCP server.

### V1-FR-29 — Opt-in MCP process-startup check

An MCP process-startup check must require an explicit server selection:

```text
ai4j doctor <id> --test-mcp <server-id>
```

The adapter must prefer a supported native target diagnostic when it provides the needed startup evidence without broadening execution. Otherwise, before direct execution, the CLI must show the exact executable, argument list, documented launch context and placeholder substitutions, referenced environment-variable names, ownership, and a side-effect warning. JSON or non-interactive execution requires `--yes`.

The test must:

- Invoke the executable directly without a command shell
- Use a documented bounded startup timeout
- Terminate the complete child process tree on timeout or cancellation
- Pass a documented allowlisted baseline environment sufficient for executable, home, temporary-directory, and interpreter resolution, plus variables explicitly referenced by that MCP definition
- Never place secret values in command-line arguments
- Bound captured output and never persist raw child output in logs or diagnostics
- Report a sanitized `process_startup_check` result and never infer “healthy in Claude/Codex” from process spawning alone
- Avoid any intentional toolkit, target, or project configuration change by the CLI

On Windows, process-tree containment and termination must use Job Objects or an equivalently reliable operating-system mechanism.

The child runs with the current user's permissions and is not sandboxed by this requirement. Its side effects cannot be guaranteed absent and must be disclosed before approval.

## 15. Secret handling

### V1-FR-30 — Secret references

Secret-dependent configuration must use environment-variable references whose names match:

```text
[A-Za-z_][A-Za-z0-9_]*
```

Plan, build, install, update, sync, status, static doctor, rollback, and uninstall must not resolve secret values. Target output must preserve references rather than materialize values.

Only the explicit MCP startup test may read a referenced value, and only transiently to construct the child environment. The CLI must not persist, log, display, hash, measure, or include that value in an error.

The product must not claim that arbitrary repository files, project files, or opaque recovery content are secret-free.

### V1-FR-31 — No inline secret schema

Canonical and target-overlay schemas must not provide an inline-secret field. Every adapter must inspect all known target-native secret-bearing and MCP environment fields and must reject literal values.

If a target feature requires materializing a secret into persistent configuration, that mapping is unsupported in v1.

## 16. Cross-platform filesystem and executable safety

### V1-FR-32 — Path and file-type safety

The MVP traversal, canonical containment, symlink, special-file, collision, no-follow, checksum, and atomic-replacement requirements apply on both hosts.

Collision detection must use the effective filesystem's separator, case, and Unicode behavior. A package valid on one host may be rejected on the other.

The CLI must not follow a writable destination symlink, junction, mount point, or unexpected reparse point and must recheck every mutation immediately before it occurs. Recursive cleanup on Windows must remain inside a verified utility-owned root and must not traverse a reparse point.

### V1-FR-33 — Private storage

On macOS, private state, temporary, journal, recovery, history, and rollback directories must use mode `0700`; private files must use mode `0600`.

On Windows, private temporary source, staging, state, journal, recovery, history, and rollback paths must use owner-restricted ACLs granting access only to the current user and operating-system principals required for normal file operation. The CLI must not rely on POSIX-mode emulation or inherit broader project-directory permissions.

No operation may invoke `sudo`, request elevation, or make a destination group- or world-writable.

### V1-FR-34 — Platform executable variants

Executable assets must declare supported target tuples. The resolver must select exactly one compatible variant and reject ambiguity.

Static validation must cover:

- Regular-file status
- Declared executable intent
- Mach-O architecture or supported script shebang on macOS
- PE architecture or supported script type on Windows
- Availability of a declared external interpreter or runtime

Static validation must not claim that all transitive runtime dependencies are present. Runtime startup is tested only through an explicit operation such as V1-FR-29.

The CLI must invoke tools directly without wrapping toolkit content in a command shell. A target may later invoke a declared shell-script asset under its own documented runtime contract, but the CLI's only permitted toolkit-content execution path is the explicit V1-FR-29 check. Installation lifecycle code remains prohibited.

## 17. Command-line contract

### V1-FR-35 — Commands

v1 must provide:

```text
ai4j init --output <empty-path> --target <claude|codex> [--target <claude|codex>]...
           [--examples] [--json]
ai4j validate [--repo <github-reference>] [--ref <git-reference>] [--source <path>]
               --target <claude|codex> [--allow-dirty] [--json]
ai4j build [--repo <github-reference>] [--ref <git-reference>] [--source <path>]
            --target <claude|codex> --host <darwin-arm64|windows-amd64>
            --output <path>
            <selection>
            [--allow-dirty] [--json]

ai4j install --dry-run [--repo <github-reference>] [--ref <git-reference>] [--source <path>]
                          --target <claude|codex>
                          --scope <user|project-local|project-shared>
                          [--project <path>]
                          <selection>
                          [--allow-dirty] [--json]
ai4j install --dry-run --installation <archived-id> [--allow-dirty] [--json]
ai4j install [--repo <github-reference>] [--ref <git-reference>] [--source <path>]
              --target <claude|codex>
              --scope <user|project-local|project-shared>
              [--project <path>] <selection> [--allow-dirty]
              [--expected-commit <full-hash> | --expected-source-digest <sha256>]
              [--yes] [--json]
ai4j install --installation <archived-id> [--allow-dirty] [--yes] [--json]

ai4j update <id> --dry-run
                 [--repo <github-reference>] [--ref <git-reference>]
                 [--allow-dirty]
                 [--conflict-policy <fail|keep|replace-owned>] [--json]
ai4j update <id>
                 [--repo <github-reference>] [--ref <git-reference>]
                 [--allow-dirty]
                 [--expected-commit <full-hash> | --expected-source-digest <sha256>]
                 [--conflict-policy <fail|keep|replace-owned|interactive>]
                 [--yes] [--json]

ai4j sync <id> --dry-run
               <selection> [--allow-dirty]
               [--conflict-policy <fail|keep|replace-owned>] [--json]
ai4j sync <id>
               <selection> [--allow-dirty]
               [--expected-source-digest <sha256>]
               [--conflict-policy <fail|keep|replace-owned|interactive>]
               [--yes] [--json]

ai4j list [--target <claude|codex>] [--scope <scope>] [--json]
ai4j status [--installation <id>] [--check-updates] [--json]
ai4j doctor <id> [--test-mcp <server-id>] [--yes] [--json]

ai4j rollback <id> --dry-run [--operation <operation-id>]
                           [--conflict-policy <fail|keep|replace-owned>] [--json]
ai4j rollback <id> [--operation <operation-id>]
                   [--conflict-policy <fail|keep|replace-owned|interactive>]
                   [--yes] [--json]

ai4j uninstall <id> --dry-run
                            [--conflict-policy <fail|keep|replace-owned>] [--json]
ai4j uninstall <id>
                            [--conflict-policy <fail|keep|replace-owned|interactive>]
                            [--yes] [--json]

ai4j history <id> [--json]
ai4j history purge <id> --dry-run
                               (--operation <operation-id> | --expired | --all) [--json]
ai4j history purge <id>
                               (--operation <operation-id> | --expired | --all) [--yes] [--json]
ai4j version [--json]
```

For update, sync, doctor, rollback, uninstall, history, and history purge, `<id>` is the installation ID and must immediately follow the command or `history purge` subcommand. `--installation` is not accepted by those commands. Status retains `--installation` because selecting one installation is optional, and install retains it only for archived reactivation.

`<selection>` means either `--all` alone or one or more repeatable `--asset <id>` and/or `--bundle <id>` options. For a new-source command, omitting `--repo` and `--source` selects the built-in GitHub repository, and `--ref` alone applies to it. `--source` is mutually exclusive with `--repo` and `--ref`. `--allow-dirty` and `--expected-source-digest` are valid only for local source; `--expected-commit` is GitHub-only. Install's `--installation` is valid only for an archived ID and is mutually exclusive with every new-install source, target, scope, project, selection, and expected-revision option. With `--installation`, `--allow-dirty` is valid only for a `development_source` tombstone and has the exact-digest semantics defined by V1-FR-08.

An implementation may add ergonomic subcommand aliases, but `ai4j`/`ai4j.exe` remains the canonical executable identity. Aliases must resolve to the same plan, JSON command identity, locking, approval, and safety semantics.

### V1-FR-36 — Approval behavior

Every target, installation-state, or history mutation must return or display its recomputed plan before mutation. `init` and `build` instead use their explicit empty/toolkit-owned output contract and never overwrite existing unmanaged content.

Interactive mode must obtain confirmation when a plan introduces, changes, or reactivates active content; changes source identity; accepts a lossy mapping; applies a non-default conflict policy; purges history; or executes an MCP process-startup check. JSON mode and non-terminal input must never prompt and must require `--yes` plus every required policy argument for those operations.

The conflict policy reviewed with `--dry-run` must match the executing command. `interactive` is accepted only by an executing command in a terminal and is invalid with `--dry-run`, `--json`, or non-terminal input.

`--yes` is approval, not a force flag. It must not bypass validation, ownership, checksum, path, policy, or recovery checks.

`--dry-run` is mutually exclusive with `--yes`, `--expected-commit`, and `--expected-source-digest`. It accepts only non-interactive conflict policies. The command output and JSON `command` field retain the actual command identity, such as `install` or `history.purge`; dry-run success carries plan data, while execution success carries mutation data.

### V1-FR-37 — JSON and exit codes

v1 must preserve the MVP JSON envelope fields and their meanings. New commands and additive optional object fields may remain in JSON schema version 1; changing a field, adding a closed-enumeration value, or changing exit-code semantics requires a published schema revision.

JSON dry-run results and interactively displayed plans must use stable identifiers, deterministic ordering, and typed action, mapping, capability, conflict, drift, and history records.

The published v1 command schemas must additionally define:

| Command family | Required data |
|---|---|
| `init`, `build` | Target/host selections, output root, created artifacts, checksums, validation result, and reproducibility marker |
| `sync` | Installation, prior/desired selection, dependency closure, native-unit actions, and final state |
| `list` | Deterministically ordered active/archived installation summaries |
| `doctor` | Static checks and, when explicitly requested, sanitized process-startup-check result |
| `rollback` | Rollback descriptor, restorability, inverse/native actions, conflicts, and final state |
| `history`, `history purge` | Bounded descriptors, retention/size metadata, and purged identifiers without opaque content bodies |

Every source-consuming v1 result must also carry `sourceSelection`, `sourceMode`, effective canonical repository or local checkout identity, requested/resolved revision, exact commit or source digest, and rendered-package digest as applicable.

At minimum, v1 must add a stable `unsupported_capability` error under exit code `3`. An explicitly accepted `keep` result must use the MVP-reserved `degraded` status and exit `0`; it must not masquerade as fully converged `ok`.

### V1-FR-38 — Structural configuration formats

When a documented target feature requires direct configuration rather than a native package operation, the responsible adapter must structurally read and write JSON, TOML, YAML, and Markdown managed sections as applicable.

It must:

- Parse the complete document and modify only the installation-owned key, object, array member, or managed section
- Preserve unrelated fields, sections, comments, ordering, and formatting when safe round-trip support exists
- Fail rather than rewrite when safe preservation cannot be guaranteed
- Use `declarationId` and toolkit identifier in tracked project-shared ownership markers; those markers must not contain a private installation ID or an absolute scope root
- Use installation ID and toolkit identifier in user-scope and project-local ownership markers
- Reject duplicate or ambiguous owned entries
- Revalidate syntax and ownership after rendering
- Recheck the pre-operation checksum immediately before same-directory atomic replacement
- Record a structural inverse and durable history action

Blind string replacement is prohibited. Native package/configuration interfaces remain preferred over direct structural writes.

## 18. Non-functional requirements

### V1-NFR-01 — Reproducibility

GitHub-based build and install results must be reproducible from the CLI version and build profile, typed exact toolkit commit identity, target capability profile, host tuple, scope, and selection. CLI build provenance and installed toolkit provenance must remain distinct. Local dirty builds must be clearly marked non-reproducible.

### V1-NFR-02 — Idempotency and convergence

Install, update, sync, rollback, and uninstall must converge to their approved desired state. Repeating an operation against the same actual and desired state must make no change and create no redundant history.

### V1-NFR-03 — Bounded operations

Both host implementations must publish and enforce finite metadata, file-count, file-size, total-byte, parser-depth, output, process, lock, history, and operation time limits. Disk-space checks must cover source acquisition, target builds, journals, rollback history, and temporary recovery data.

Every external process must have a bounded timeout and complete process-tree termination on cancellation or timeout. Windows implementations must use Job Objects or an equivalently reliable mechanism; modifying native-operation timeouts must enter journal reconciliation before a final result is reported.

### V1-NFR-04 — Concurrency

Independent installations may operate concurrently only when they share no mutable native or adapter-owned resource. Lock keys include the deterministic logical identity, canonical installation ID when present, and canonical shared-resource IDs, including absent resources being created. Lock ordering and acquisition follow V1-AR-05. Concurrent project or target changes must be detected through checksums and native-state reconciliation.

Read commands must take compatible shared locks or consume only atomically committed snapshots. History purge must take the installation/history lock. Ownership-graph, history-reference, and installation-state updates must be one journaled recovery unit even when their individual files are replaced atomically.

### V1-NFR-05 — Release integrity

The v1 release-candidate bundle must provide:

- Reproducible unsigned `ai4j` `darwin/arm64` artifact plus SHA-256 checksum
- Reproducible unsigned `ai4j.exe` `windows/amd64` artifact plus SHA-256 checksum
- Locked and verified Go dependencies and reproducible-build metadata
- Formatting, static-analysis, unit, integration, compatibility, and artifact-install checks
- `ai4j version` output containing product/executable identity, CLI version, typed CLI source identity, exact Go version, build time, target tuple, and built-in default-source policy
- Release documentation that clearly identifies the executables as unsigned and explains SHA-256 verification

The CLI executable must not require Python, Node.js, Java, Homebrew, WSL, or another runtime. Toolkit assets may declare their own external runtimes, which must be disclosed and validated statically.

### V1-NFR-06 — Compatibility testing

The test suite must include:

- Fake adapters with failure injection after every state transition
- Contract tests for the supported Claude lifecycle range and Codex package format
- The four target/host tuples
- User, project-local, and project-shared scopes
- Case-sensitive and case-insensitive path collision fixtures
- Process interruption and concurrent-edit recovery
- Native policy-blocked and native-output-change fixtures

Version-bounded command/output fixtures, host-native automated tests where available, and cross-build checks satisfy the v1 compatibility gate. Live Claude/Codex execution on clean macOS and Windows hosts remains a post-v1 qualification activity and must not be implied by contract-test results.

### V1-NFR-07 — Data minimization

State, journals, history, logs, and diagnostics must contain only the data necessary for ownership, reconciliation, and support. Unrelated project or configuration content must not be copied when a structural inverse is sufficient. Opaque prior toolkit-owned content retained solely for rollback must be classified as potentially sensitive and protected by private storage, bounded retention, and non-disclosure rules.

### V1-NFR-08 — Go portability and extension fitness

v1 retains the MVP module path and exact stable-toolchain pinning. It additionally requires the reproducible release profile, repository attribution policy, and no-cgo defaults defined by this document. All release targets must be built from the same clean source commit and exact Go patch version. A platform exception requiring cgo must be approved as a documented architecture decision with new build, dependency, and platform-specific automated coverage.

The target-neutral core must compile and test independently from Claude, Codex, Darwin, and Windows implementation packages. Architecture checks must enforce acyclic dependencies and prevent adapter or host implementations from leaking private target paths, Windows registry/ACL details, or POSIX assumptions into canonical state and planning contracts.

The Go release baseline must remain on a Go-supported stable major and its latest security patch at every v1 release. Older toolchains, development toolchains, implicit toolchain downloads, workspace overrides, and local module replacements are unsupported for releases.

## 19. Required evaluation and estimation gates

These items must be estimated during v1 planning. They are not committed runtime behavior unless the resulting architecture decision explicitly selects them and the normative requirements are revised.

### V1-EVAL-01 — Source acquisition and cache strategy

The MVP's **Option A** remains the normative GitHub acquisition baseline: every consuming command uses a private ephemeral workspace and creates no persistent toolkit-managed cache.

v1 planning must first compare persistence and command-boundary policies:

| Option | Candidate behavior | Potential benefit | Principal cost or risk |
|---|---|---|---|
| A — Ephemeral baseline | Acquire the exact snapshot into an operation-specific temporary workspace | Honest no-persistence contract; simple invalidation | Repeated network/Git work; no offline planning |
| B — Plan-writable cache | A plan may create a new immutable commit-keyed entry and update separate cache metadata | Faster repeated plans; possible offline reuse | Redefines “no persistent changes”; surprising mutation, locking, eviction, and policy complexity |
| C — Explicit `fetch` or `prepare` | A separate mutating command populates immutable entries; plan remains read-only | Clear mutation boundary and controllable offline workflow | Extra lifecycle command and stale-cache UX |
| E — Install-populated, read-only-plan cache | Successful install, update, or build may populate immutable entries; read commands use an exact match or fall back to ephemeral acquisition | Keeps plan read-only while improving common repeat operations | Cache lifecycle still exists; misses remain online/ephemeral and install side effects grow |

Acquisition transport is a separate decision that may combine with A, B, C, or E:

| Acquisition option | Candidate behavior | Main questions |
|---|---|---|
| Git fetch/checkout | Use system Git object and ref semantics | Process cost, partial clone, authentication, filters, and cleanup |
| D — Verified archive/stream | Retrieve commit-bound blobs or an archive without a reusable checkout | Private authentication, commit authenticity, Git-mode fidelity, limits, partial downloads, and host parity |

The estimate must cover:

- User workflow, command surface, and a command-by-command cache-effects matrix
- Public/private Git authentication and acquisition transport
- Exact-commit and canonical-origin guarantees
- Moving-reference versus exact-commit offline semantics
- Disk, network, and latency characteristics
- Cache format version, permissions, quotas, locking, atomic promotion, corruption recovery, eviction, purge safety, and observability
- Plan-contract and JSON compatibility
- Security exposure, abandoned-entry cleanup, and source-limit enforcement
- macOS and Windows implementation effort

The output must be an architecture decision record with prototype evidence, normative/CLI/JSON changes, and adoption impact. Until it is approved, Option A remains normative and no persistent toolkit-managed source cache may be created.

If any persistent cache is selected, entries must be immutable and keyed by a hash of sanitized canonical repository identity plus exact commit, never raw path text. Incomplete entries must be ignored until atomically promoted. Origin mismatch, dirty or untracked bytes, unsafe file types, format mismatch, or checksum failure must invalidate the entry before use. Cache is disposable and must never be authoritative installation or rollback state.

An offline cached branch tip must never be represented as the current remote tip without network confirmation; it may only be reported as a previously cached exact commit with freshness `unknown`.

### V1-EVAL-02 — Optional encrypted whole-file snapshot history

The committed v1 rollback mechanism is structural configuration history plus bounded toolkit-owned native package artifacts. v1 planning must estimate whether users also need opt-in, byte-for-byte restoration of complete pre-existing user files.

If selected, the feature must:

- Be disabled by default and require an explicit warning acknowledgement
- Encrypt content before persistent storage
- Use macOS Keychain, Windows protected key storage, or an equivalently established platform mechanism
- Keep keys separate from snapshots
- Store snapshots outside repositories, installed assets, source workspaces, and native caches
- Apply bounded retention and explicit purge
- Never print, index, upload, or include snapshot content in diagnostics
- Fail closed when the decryption key is unavailable

An unavailable key must leave current installation state unchanged. Platform-protected keys used solely for this optional rollback feature are rollback-security material, not a general-purpose user secret store.

The estimate must cover threat model, key creation and loss, rotation, supportability, retention, cross-platform recovery, storage growth, and interaction with uninstall.

Plaintext retained whole-user-file backups are not an acceptable alternative. Until this evaluation is approved, v1 retains only structural configuration history and toolkit-owned package artifacts.

### V1-EVAL-03 — Cross-target fidelity gate

Before implementation freezes the v1 canonical schema, a working Claude/Codex spike must build and inspect the first-party toolkit from `github.com/alx4j/ai4j`, containing:

- One skill with references and a script
- One specialized agent
- One persistent instruction
- One MCP server with environment references
- One target-native hook or a documented unsupported mapping

The spike must produce a capability and fidelity matrix, identify every target overlay, and prove that neither adapter edits a private native cache. Unresolved lossy mappings block schema freeze.

## 20. Acceptance criteria

v1 is acceptable only when automated tests demonstrate all of the following:

1. Automated contracts cover every supported Claude/macOS and Claude/Windows tuple; Codex packages cross-build for both hosts and pass package-format validation before native interactive installation. Compatibility documentation distinguishes this evidence from deferred live-host qualification.
2. Claude user, project-local, and project-shared mappings are effective, disclosed, and isolated; project-local instructions are safely Git-locally excluded, project-shared status distinguishes declaration, current-user installation, enablement, and loading, and two independent project clones converge on the same tracked declaration identity while retaining distinct private installation IDs.
3. Project-shared operations never stage or commit files.
4. Multiple installations coexist without state, ownership-graph, reference-count, or lock collisions, including creation of a previously absent shared resource; installations of the same toolkit at different revisions or selections receive compatible distinct native identities or fail before mutation and never redirect one another.
5. Whole-toolkit, multiple-bundle, mixed bundle/asset, and per-asset selection resolve deterministically with correct dependency closures and variants, then render as complete native installation units without cache editing; every GitHub-backed Claude unit is a manifest-declared self-contained plugin subdirectory, and selection never filters bytes within a unit.
6. GitHub source retains exact-commit and fast-forward safety; local mode records stable digests, enforces expected-digest checks, and marks dirty state non-reproducible.
7. JSON, TOML, YAML, and Markdown configuration fixtures preserve unrelated content and fail safely when round-trip preservation is impossible.
8. Build output is deterministic and native validators accept it without executing toolkit content.
9. A source or reference change shows source and rendered diffs, preserves toolkit and installation identity, and requires explicit approval.
10. Every conflict class follows the same policy in plan and apply; interactive policy is terminal-only, accepted `keep` is degraded, and `--yes` never acts as force.
11. Failure and termination in every success, compensation, and cleanup journal phase leave recoverable or cleanup-pending state, or a reported checksum/native-state conflict, without overwriting concurrent changes; fully compensated failures terminate as rolled back with `changed: false`.
12. Rollback uses the recorded exact native artifact through a supported interface, restores only toolkit-owned structural state, is itself crash-recoverable, and rejects drift or unsupported native restore capability before mutation.
13. Uninstall leaves an archived installation tombstone while rollback history exists; bounded history purge takes the required lock and does not alter current target state, its plan and apply results agree, and planning never removes history or rollback material.
14. Static doctor never starts toolkit content.
15. The opt-in MCP process-startup check uses no shell, a documented allowlisted environment, bounded output/time, complete process-tree termination, and no intentional CLI configuration mutation; it does not claim native MCP health or absence of child side effects.
16. Environment-referenced secret values never appear in plans, state, journals, history, logs, JSON, or diagnostics.
17. Secrets in unrelated content are not copied into structural history; opaque prior toolkit-owned content retained only for rollback is private, bounded, and never displayed, logged, indexed, or exported.
18. Path traversal, symlinks, junctions, mount points, unexpected reparse points, special files, case/Unicode collisions, and mutation-time substitutions are rejected on both hosts.
19. Private temporary, state, recovery, history, and rollback data receive required macOS modes or Windows ACLs at creation.
20. JSON output is deterministic, schema-valid, prose-free on standard output, consistent with exit codes, and uses the reserved `degraded` status correctly.
21. Unknown installation-state, journal, and history schemas block mutation without reinterpretation; interrupted operations using the canonical formats remain recoverable.
22. CLI grammar tests cover GitHub/local expected revisions, dirty-source validation, dirty local archived reactivation with and without the required `--allow-dirty`, plan/apply policies, uninstall policy, init/build output safety, and non-interactive approval.
23. The release-candidate bundle contains `ai4j` and `ai4j.exe` built from the same clean commit and pinned Go patch; independent verification confirms their target metadata and SHA-256 checksums without requiring public release publication.
24. V1-EVAL-01, V1-EVAL-02, and V1-EVAL-03 have recorded decisions; unselected features remain disabled and Option A remains effective unless explicitly replaced.
25. Omitted source flags select `github.com/alx4j/ai4j`; the equivalent explicit repository produces the same effective identity, exact commit, rendered digest, active-content inventory, ordered actions, and desired final state on every supported target/host tuple, while `sourceSelection` differs as `built_in_default` versus `explicit`. Explicit third-party public and private repositories retain identical safety treatment.
26. `--ref` alone applies to the built-in repository, an invalid explicit repository never falls back, and `--source` is rejected with either GitHub flag. Update uses stored or explicitly supplied sources; archived reactivation requires `--installation <archived-id>` and uses only the tombstone's stored source and desired state. A dirty `development_source` tombstone can be reactivated only with `--allow-dirty` and only when the current checkout reproduces the stored digest. Neither path changes because a later release changes its default.
27. The first-party repository builds its declared Claude and Codex variants and installs its Claude variant while Go source, module files, CI/release configuration, release metadata, and unrelated files remain outside active-content inventory and native packages. Codex automated lifecycle requests fail before mutation and the built package remains available for native interactive installation.
28. `ai4j` and `ai4j.exe` build from module `github.com/alx4j/ai4j` with the same pinned supported Go patch; isolated unsigned builds are reproducible and their version metadata, VCS metadata, and checksums agree.
29. Architecture tests keep Codex package output, Windows, Claude scopes, local source, selection, and durable history behind explicit core boundaries without target or host implementation imports in the core.
30. GitHub-backed Claude user and project-local generated SHA-pinned catalogs use stable private adapter-owned storage, survive ephemeral-source cleanup, cannot redirect an incompatible installation, and are removed only after safe scoped native deregistration. A supported local-development install instead uses an immutable digest-addressed backing bundle containing only rendered native units and its catalog, backed by a canonical complete sidecar descriptor that excludes itself from the payload inventory; no registration points to an ephemeral snapshot, and the bundle cannot serve as a general source cache. Tests prove that equal source/package digests with different catalog or native identities produce different bundle keys, while exactly compatible installations share one reference-counted bundle and one installation's update or uninstall cannot remove another's live backing. Project-shared Claude declarations use a structurally owned portable inline marketplace in tracked settings, while Claude's scoped native manager exclusively owns `enabledPlugins`, enablement, cache lifecycle, and native marketplace registration. Uninstall reconciles scoped native plugin and marketplace removal before deleting any remaining owned inline declaration; local-development source with project-shared scope is rejected.
31. Repository policy checks reject a commit whose author or committer differs from `Oleksii Stupin <oleksii.stupin@gmail.com>`.

## 21. Informative platform baseline

These links identify the public native concepts that the adapters currently target. They are informative; release capability discovery and compatibility tests are authoritative.

### Claude Code

- [Claude Code plugin reference](https://code.claude.com/docs/en/plugins-reference)
- [Claude Code plugin installation](https://code.claude.com/docs/en/discover-plugins)
- [Claude Code plugin marketplaces](https://code.claude.com/docs/en/plugin-marketplaces)
- [Claude Code configuration](https://code.claude.com/docs/en/configuration)
- [Claude Code MCP](https://code.claude.com/docs/en/mcp)

### Codex

- [Codex skills](https://developers.openai.com/codex/skills)
- [Codex `AGENTS.md`](https://developers.openai.com/codex/guides/agents-md)
- [Codex MCP](https://developers.openai.com/codex/mcp)
- [OpenAI plugin architecture](https://developers.openai.com/plugins/concepts/plugins)

### Go

- [Go release history and support policy](https://go.dev/doc/devel/release)
- [Go toolchain selection](https://go.dev/doc/toolchain)
