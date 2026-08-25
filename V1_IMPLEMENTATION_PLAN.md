# AI4J — v1 Implementation Plan

| Field | Value |
|---|---|
| Status | Wave 9 complete; v1 automated scope complete |
| Scope | v1 only |
| Normative baselines | [MVP requirements](MVP_REQUIREMENTS.md) and [v1 requirements](V1_REQUIREMENTS.md) |
| Starting point | Completed MVP through Wave 6 |
| Planning model | Ten exit-gated vertical waves |
| First usable v1 checkpoint | Wave 3: complete Claude user-scope lifecycle on macOS |
| Full v1 acceptance | Wave 9 |
| Last revised | 2026-08-25 |

## 1. Purpose

This plan delivers v1 by extending the working MVP. It does not replace the existing source, target, host, state, lifecycle, CLI, or JSON seams unless a v1 acceptance criterion proves that one of them cannot support the required behavior.

The order favors usable behavior:

1. Prove Claude/Codex fidelity and ship deterministic cross-target builds.
2. Add selection and authoring.
3. Upgrade state and complete one rollback-capable lifecycle on the existing macOS/Claude path.
4. Expand that lifecycle to new sources, Windows, project scopes, and Codex.
5. Add opt-in execution diagnostics and qualify the release last.

Each wave must end in runnable behavior. Types, interfaces, state formats, or test fakes alone do not complete a wave.

## 2. Execution rules

These rules apply to every wave:

- Optimize for completed end-to-end behavior, not maximal hardening.
- Spend no more than 20% of a wave on architecture and review.
- Review only against explicit v1 requirements, numbered acceptance criteria, and regressions in retained MVP behavior.
- Record adjacent improvements in `BACKLOG.md`; do not implement them unless they block the current behavior or prevent data loss, credential exposure, or destructive mutation.
- Do not introduce a shared abstraction unless the current acceptance criterion cannot be met without it. Prefer extending an existing interface or adding target-local code.
- Keep execution tasks small enough to finish and verify in 90 minutes. Split a task before starting if it is larger.
- Within 30 minutes of starting a wave, report elapsed time, runnable behavior completed, remaining work, and estimated remaining cost.
- If 60 minutes pass without a runnable vertical slice, stop and ask for direction.
- At 80% of the wave budget, stop expanding scope and finish, test, and deliver the current slice.
- Commit only after the complete wave exit gate passes. Partial foundations are not completion.
- Do not weaken exact-commit provenance, active-content disclosure, no implicit execution, structural recovery, ownership checks, or target-native integration to make a wave pass.

Detailed technical tasks are created just in time for the active wave. This plan intentionally does not pre-create hundreds of speculative substories.

## 3. Scope control and defaults

The following defaults keep the implementation within the approved v1 scope:

- Source acquisition remains private and ephemeral. No persistent source cache is implemented unless V1-EVAL-01 explicitly selects one and the requirements are revised.
- Rollback uses structural history and bounded toolkit-owned native artifacts. Encrypted whole-file snapshots remain disabled unless V1-EVAL-02 explicitly selects them and the requirements are revised.
- No keychain, Windows credential vault, general secret store, or credential-handling subsystem is part of the default plan.
- The MCP startup check runs as the current user with the constrained process contract in V1-FR-29. The product does not claim that the child is sandboxed.
- One operation resolves one target. There is no synthetic `both` lifecycle target or cross-target transaction.
- Target adapters use documented public interfaces and never edit Claude or Codex private caches or registries.
- Project-shared operations modify only disclosed project files. AI4J never stages or commits them.
- New hardening or portability work is accepted only when V1-FR-32 through V1-FR-34, V1-NFR-03, or another explicit acceptance criterion requires it.

## 4. Delivery sequence

```text
Wave 0  Cross-target build and decisions
   |
Wave 1  Authoring and deterministic selection
   |
Wave 2  Multiple-installation state and inspection
   |
Wave 3  Complete Claude/macOS/user lifecycle with rollback  <-- first usable v1
   |
Wave 4  Private GitHub, local development, and source migration
   |
Wave 5  Windows Claude user-scope parity
   |
Wave 6  Claude project-local and project-shared scopes
   |
Wave 7  Codex package handoff across the supported matrix
   |
Wave 8  Static doctor and explicit MCP startup check
   |
Wave 9  Compatibility, reproducibility, and release qualification
```

Later-wave fixtures or research may begin early, but later behavior must not be merged into an incomplete earlier wave merely to claim progress.

## 5. Estimation model

Wave estimates are planning ranges for one focused engineer using the repository and its automated test environments. They are not calendar commitments. Post-v1 live-host qualification and public-release coordination are excluded.

| Size | Typical focused effort |
|---|---:|
| S | Up to 1 engineering day |
| M | 2–4 engineering days |
| L | 4–7 engineering days |
| XL | 7–10 engineering days |

The complete plan is approximately **36–61 focused engineering days**. The first usable v1 checkpoint is approximately **13–22 days** through Wave 3. These ranges should be revised from actual throughput after each completed wave; they must not be converted into extra scope.

## 6. Wave plan

### Wave 0 — Cross-target build and required decisions

**Goal:** prove the canonical model against both targets before freezing it, and deliver the first runnable v1 behavior: deterministic first-party builds for Claude and Codex on the existing Darwin host path.

**Estimate:** M, 2–4 days.

| Story | Outcome | Requirements |
|---|---|---|
| V1-ST-001 — Cross-target fidelity slice | A representative first-party skill, agent, instruction, MCP declaration, support content, script, and hook/unsupported mapping render for Claude and Codex without touching private native state. | V1-AR-02, V1-AR-03, V1-EVAL-03 |
| V1-ST-002 — Minimal Codex read-only adapter | Codex capability discovery, rendering, and output inspection use documented interfaces and preserve explicit unsupported/lossy results. | V1-AR-01 through V1-AR-04 |
| V1-ST-003 — First deterministic cross-target build | `ai4j build ... --all` produces validated, checksummed Claude or Codex output from the same exact first-party commit without installing or executing it. | V1-FR-05, V1-FR-15, V1-NFR-01 |
| V1-ST-004 — Required evaluation decisions | V1-EVAL-01 and V1-EVAL-02 decisions are recorded with estimates and evidence; the baseline remains ephemeral acquisition plus structural history unless a reviewed decision changes the requirements. | V1-EVAL-01, V1-EVAL-02 |

**Evaluation budgets**

| Evaluation | Decision budget | Implementation consequence |
|---|---:|---|
| V1-EVAL-01 — Source acquisition/cache | 0.5–1 day | Default decision is Option A. A selected persistent cache requires a revised plan and separate estimate before implementation. |
| V1-EVAL-02 — Encrypted whole-file snapshots | 0.5 day for the decision; 5–8 additional days if selected | Default decision is not to add snapshots. Selection adds platform key-storage, retention, recovery, and security work and therefore changes the plan. |
| V1-EVAL-03 — Cross-target fidelity | 1.5–2.5 days, included in this wave | Unresolved lossy mapping blocks schema freeze and Codex package work. |

**Exit gate**

- The first-party toolkit builds into inspectable Claude and Codex native output from one exact commit.
- Output is deterministic, validated, checksummed, and contains only selected toolkit content.
- The fidelity matrix names every target overlay and unsupported or lossy mapping.
- Tests prove neither adapter edits a private target cache or registry.
- All three evaluation decisions are recorded; unselected behavior remains disabled.

### Wave 1 — Authoring, schema, and deterministic selection

**Goal:** make the v1 toolkit format useful to authors and users before adding more mutation paths.

**Estimate:** L, 3–5 days.

| Story | Outcome | Requirements |
|---|---|---|
| V1-ST-005 — v1 manifest and selection | Versioned assets, variants, named bundles, and deterministic dependency closure validate for one target/host at a time. | V1-FR-01 through V1-FR-04, V1-FR-09, V1-FR-10 |
| V1-ST-006 — Secret-safe target overlays | Known secret-bearing fields accept environment references and reject inline values without resolving the environment. | V1-FR-30, V1-FR-31 |
| V1-ST-007 — Initialize a valid toolkit | `ai4j init` creates a minimal valid empty-directory skeleton for one or both explicitly selected targets. | V1-FR-14 |
| V1-ST-008 — Build selected native units | `ai4j build` supports `--all`, mixed asset/bundle selection, dependency explanations, compatible variants, deterministic output, and native validation. | V1-FR-02, V1-FR-03, V1-FR-10, V1-FR-15 |
| V1-ST-009 — Authoring JSON contracts | Human and versioned JSON results agree for init, validate, and build, including checksums and reproducibility. | V1-FR-35, V1-FR-37 |

**Exit gate**

- A new toolkit can be initialized, validated, and built without installation.
- Whole-toolkit, bundle, asset, and mixed selection produce stable dependency explanations and complete native units.
- Both target outputs preserve supported intent and fail explicitly on unsupported or unapproved lossy mappings.
- Inline secrets, dependency cycles, ambiguous variants, and unsafe output ownership fail before writing output.
- The first-party content roots still exclude Go source, CI, signing material, and unrelated repository files.

### Wave 2 — Multiple-installation state and local inspection

**Goal:** replace the MVP's single-installation state with a recoverable v1 state model and deliver local multi-installation inspection before expanding mutations.

**Estimate:** L, 3–5 days.

| Story | Outcome | Requirements |
|---|---|---|
| V1-ST-010 — Versioned v1 state migration | Existing MVP state is previewed and migrated under an exclusive lock and minimal recovery journal; unknown future schemas block mutation. | V1-FR-04, V1-FR-08 |
| V1-ST-011 — Installation identity and state collection | Active and archived records use immutable installation IDs plus logical target/scope/root/toolkit identity without cross-installation inference. | V1-FR-08, V1-AR-05 |
| V1-ST-012 — Offline list and precise status | `list` deterministically enumerates active/archived installations; `status` reports one installation, source, selection, native observations, drift, health, and recovery state without network or execution. | V1-FR-20 |
| V1-ST-013 — Complete v1 command parsing | The v1 grammar, mutual exclusions, installation selection, stable errors, JSON envelope, and exit semantics are executable even when later-wave capabilities return `unsupported_capability`. | V1-FR-35 through V1-FR-37 |

**Exit gate**

- A real MVP installation migrates without changing target content and remains inspectable.
- Multiple state records coexist without path or identity collisions.
- `list` is offline and deterministic; `status` distinguishes observed, unknown, drifted, recovery-required, active, and archived state.
- A migration interruption either completes, compensates, or leaves a precise recoverable state.
- Existing MVP validate, plan, install, status, update, and uninstall regressions remain green.

### Wave 3 — Complete Claude/macOS user lifecycle with rollback

**Goal:** deliver the first complete v1 lifecycle on the existing supported target/host/scope: GitHub-backed Claude, Darwin ARM64, user scope.

**Estimate:** XL, 5–8 days.

| Story | Outcome | Requirements |
|---|---|---|
| V1-ST-014 — Ownership graph and deterministic locks | Installation, logical-identity, shared native-resource, and history locks are computed before mutation; resource references commit with installation state. | V1-AR-05, V1-NFR-04 |
| V1-ST-015 — Journaled mutation and conflict policies | Every mutation records intent/result, supports fail/keep/replace-owned/interactive policy rules, and recovers or reports a precise conflict without guessing. | V1-FR-22 through V1-FR-24 |
| V1-ST-016 — Structural history and tombstones | Successful install, update, sync, uninstall, rollback, and purge retain bounded structural inverses and exact toolkit-owned artifacts; uninstall archives the installation until final purge. | V1-FR-25 through V1-FR-27 |
| V1-ST-017 — Complete Claude user lifecycle | Plan/apply install, update, sync, rollback, uninstall, history, purge, list, and status work end to end for multiple Claude user installations on macOS. | V1-FR-16 through V1-FR-21, V1-FR-35, V1-FR-36 |

**Exit gate**

- Two Claude user-scope installations coexist and cannot redirect, disable, remove, or overwrite one another.
- Every modifying operation previews the same desired state it applies and is idempotent when already converged.
- Failure injection around every durable phase proves recovery, safe compensation, or `recovery_required` without overwriting concurrent changes.
- A successful update, sync, and uninstall can be planned and rolled back using only structural history and recorded toolkit-owned artifacts.
- Purge never changes current target state; final archived-history purge removes the tombstone as disclosed.
- No lifecycle operation executes toolkit content or writes secret values.

This is the first usable v1 checkpoint. If speed is the priority, release an internal preview here before expanding the platform matrix.

### Wave 4 — Private GitHub, local development, and source migration

**Goal:** extend the complete Wave 3 lifecycle to all committed source workflows while retaining exact provenance and ephemeral acquisition.

**Estimate:** L, 3–5 days.

| Story | Outcome | Requirements |
|---|---|---|
| V1-ST-018 — Private GitHub through existing authentication | Public and private canonical GitHub sources use system Git/SSH authentication without accepting or storing credentials. | V1-FR-11 |
| V1-ST-019 — Local-development source | Explicit local checkouts support validate, build, install, update, and sync with stable digests, dirty-state approval, expected-digest checks, and immutable Claude install-backing bundles. | V1-FR-13, V1-AR-05 |
| V1-ST-020 — Source/reference migration and diff | Explicit GitHub source or reference migration preserves installation identity and shows source, rendered, ownership, compatibility, and rollback changes before approval. | V1-FR-12, V1-FR-19 |

**Exit gate**

- Private repositories work through already configured Git/SSH authentication and no credential-bearing URL is accepted.
- Local source is never inferred, never mutated, and cannot be combined with project-shared scope.
- Dirty local input requires explicit approval, is marked non-reproducible, and must match the expected digest at apply time.
- GitHub source/reference migrations never occur implicitly and preserve toolkit and installation identity.
- Claude development installs use only immutable selected-content backing bundles, not general source caches.

### Wave 5 — Windows Claude user-scope parity

**Goal:** run the complete user-scope Claude lifecycle as `ai4j.exe` on a supported Windows x64 host with equivalent safety semantics.

**Estimate:** XL, 4–7 days.

| Story | Outcome | Requirements |
|---|---|---|
| V1-ST-021 — Windows host implementation | Canonical paths, case/Unicode collisions, reparse points, private ACLs, atomic replacement, bounded processes, Job Objects, disk checks, and PE/script validation satisfy the host contracts. | V1-FR-32 through V1-FR-34, V1-NFR-03 |
| V1-ST-022 — Claude Windows lifecycle | Validate, build, plan, install, update, sync, list, status, rollback, uninstall, and history work for Claude user scope on Windows. | V1-FR-05, V1-NFR-02, V1-NFR-04 |

**Exit gate**

- `ai4j.exe` passes the complete Wave 3 and Wave 4 user journey on a clean Windows x64 host.
- Private files receive owner-restricted ACLs at creation.
- Junction, reparse-point, case/Unicode-collision, replacement-race, and out-of-root cleanup fixtures fail before destructive mutation.
- External processes are bounded and their full process trees terminate on cancellation or timeout.
- Windows and Darwin produce equivalent canonical plans and JSON where host-specific facts do not differ.

### Wave 6 — Claude project scopes and shared resources

**Goal:** add project-local and project-shared Claude behavior on both supported hosts without confusing tracked declaration with current-user installation or loading.

**Estimate:** XL, 4–7 days.

| Story | Outcome | Requirements |
|---|---|---|
| V1-ST-023 — Canonical project roots and scope mapping | Git-root discovery, explicit project selection, project-local exclusion, project-shared declaration identity, and policy checks are planned before mutation. | V1-FR-06, V1-FR-07 |
| V1-ST-024 — Structural project configuration | Owned JSON/TOML/YAML/Markdown structures preserve unrelated content and record exact structural inverses; unsafe round trips fail. | V1-FR-38 |
| V1-ST-025 — Claude scoped lifecycle | User, project-local, and project-shared installations reconcile native plugin/marketplace/rules state with shared-resource reference counts on Darwin and Windows. | V1-AR-04, V1-AR-05, V1-FR-16 through V1-FR-27 |

**Exit gate**

- Project-local content remains untracked without modifying tracked project content.
- Project-shared declarations are portable, stable across clones, and never contain private paths or installation IDs.
- Status separately reports declaration, current-user installation, enablement, loaded/reload, and next-session facts when observable.
- AI4J never stages or commits project files and never directly manages Claude `enabledPlugins`.
- Shared resources are removed only after the final compatible owner is gone; incompatible native identities fail before mutation or receive deterministic distinct identities.
- Project-shared uninstall completes native deregistration before removing any remaining owned inline declaration.

### Wave 7 — Codex package handoff across the supported matrix

**Goal:** complete faithful Codex package output and hand it off at the documented native boundary without editing private Codex state.

**Estimate:** XL, 5–8 days.

| Story | Outcome | Requirements |
|---|---|---|
| V1-ST-026 — Codex native package adapter | Native mappings, provenance, package manifests, and structural validation use documented Codex package formats. | V1-AR-01 through V1-AR-04 |
| V1-ST-027 — Codex native handoff | Codex packages build on Darwin and Windows; automated lifecycle requests fail before mutation and direct users to the native interactive plugin browser. | V1-FR-05 through V1-FR-08, V1-FR-15 |
| V1-ST-028 — Cross-target package matrix | Package contract fixtures cover Claude/Codex and both host profiles without target/host imports in the core. | V1-NFR-06, V1-NFR-08 |

**Exit gate**

- The same logical first-party selection builds through faithful target-native mappings on both targets; Claude also installs through its native lifecycle.
- Unsupported target properties fail instead of being silently omitted.
- Both host profiles pass Claude and Codex package contracts; all three Claude scopes pass lifecycle integration tests.
- Automated Codex lifecycle requests return `unsupported_capability` before source acquisition or mutation and explain the native interactive handoff.
- No Codex operation edits a private cache, database, or undocumented registry.
- Target-neutral core packages compile and test without Claude, Codex, Darwin, or Windows implementation imports.

Decision recorded 2026-08-25: the documented Codex interface provides interactive plugin management but no non-interactive lifecycle API. v1 therefore supports Codex authoring/build plus native interactive installation, not automated Codex lifecycle. Re-evaluate only when a documented scriptable interface exists.

### Wave 8 — Static doctor and explicit MCP startup check

**Goal:** finish diagnostics while keeping normal commands non-executing.

**Estimate:** L, 2–4 days.

| Story | Outcome | Requirements |
|---|---|---|
| V1-ST-029 — Static doctor | `doctor` reports host, Git, target, package, state, history, journal, permission, executable, MCP registration, and environment-reference checks without starting content. | V1-FR-28 |
| V1-ST-030 — Explicit MCP startup check | One selected MCP server can be started directly with disclosed arguments, allowlisted environment, bounded output/time, and full process-tree termination. | V1-FR-29, V1-FR-30 |
| V1-ST-031 — Bounded-operation audit | Source, build, plan, history, parser, file, byte, process, lock, disk, and operation bounds are enforced on both hosts. | V1-NFR-03, V1-NFR-07 |

**Exit gate**

- Static doctor starts no toolkit script, binary, hook, or MCP server.
- The explicit startup test requires exact server selection and approval, uses no shell, and never persists or displays a referenced secret value.
- Timeout and cancellation terminate the complete child process tree on both hosts.
- Results state only that process startup succeeded or failed; they do not claim native target health or absence of child side effects.
- Resource-limit and disk-preflight failures occur before destructive mutation.

### Wave 9 — Compatibility, integrity, and v1 release

**Goal:** complete the automated v1 acceptance suite and produce reproducible, checksum-verifiable unsigned release-candidate artifacts.

**Estimate:** M, 1–2 focused days.

| Story | Outcome | Requirements |
|---|---|---|
| V1-ST-032 — Failure and compatibility qualification | Automated contracts cover four target/host tuples, three scopes, failure injection, concurrent edits, policy blocks, structural formats, and migrations without claiming deferred live-host qualification. | V1-NFR-02, V1-NFR-04, V1-NFR-06 |
| V1-ST-033 — Reproducible unsigned artifacts | The same clean commit and exact Go patch produce reproducible macOS and Windows binaries, matching checksums, and explicit unsigned-release documentation. | V1-NFR-01, V1-NFR-05, V1-NFR-08 |
| V1-ST-034 — Release documentation and acceptance | Contract-tested compatibility ranges, capability requirements, user guide, JSON schemas, release-candidate verification, and all numbered acceptance criteria are complete. | V1-FR-37, V1-NFR-05, V1-NFR-06 |

**Exit gate**

- All 31 numbered v1 acceptance criteria pass with automated evidence at the boundary they claim to test.
- `ai4j` and `ai4j.exe` cross-build from the same clean commit and pinned Go patch, and their target metadata and checksums verify independently.
- Release metadata, VCS identity, and checksums agree for both unsigned executables.
- Compatibility documentation declares contract-tested target-version ranges and required capabilities for each tuple without claiming live-host qualification.
- Commit author and committer policy passes for the release-candidate commit.

**Completion evidence — 2026-08-25**

- The production release builder emits canonical `darwin/arm64` `ai4j` and `windows/amd64` `ai4j.exe` artifacts from one clean commit and pinned Go patch, with target-specific version metadata and SHA-256 files.
- Two isolated clean clones produce byte-identical executables, metadata, and checksum files through both PowerShell and shell verification paths.
- Automated build contracts cover Claude and Codex on Darwin and Windows profiles without executing toolkit content; the full test suite, vet, module, formatting, architecture, authorship, and cross-build gates pass.
- Compatibility documentation labels live-host execution and public publication as post-v1 work instead of claiming evidence that was not collected.

## 7. Quality gates

Use the cheapest automated test that proves the required behavior. Do not substitute unit-only evidence for filesystem, process, Git, or adapter contract behavior.

### Per task

- Format changed Go files.
- Run focused package tests for the changed behavior.
- Run the relevant schema, golden, or command-contract test.
- Inspect the diff for scope growth and accidental secret or user-content capture.

### Per wave

- Run `go test ./...` and `go vet ./...`.
- Run architecture dependency tests.
- Run relevant race tests on a host where the Go race detector is supported.
- Cross-build the currently supported release tuples.
- Exercise at least one production-dispatch vertical journey for the wave's user-visible behavior.
- Verify human and JSON results describe the same actions and outcome.
- Verify no normal command executes toolkit content.

### Native and release boundaries

- Use version-bounded documented Claude/Codex command and output fixtures for adapter contracts.
- Run host-native filesystem/process tests in available automated environments and label unexecuted live-target behavior as unqualified.
- Inject failures before and after every durable state transition for journal and rollback claims.
- Compare two isolated clean builds for reproducibility before finalizing the release-candidate bundle.
- Verify release-candidate target metadata and checksums; canonical tag creation and publication remain post-v1 work.

## 8. Requirement traceability

### Architecture, functional, and non-functional requirements

| Requirements | Primary waves |
|---|---|
| V1-AR-01 through V1-AR-04 | 0, 1, 7 |
| V1-AR-05 | 2, 3, 4, 6, 7 |
| V1-FR-01 through V1-FR-04 | 0, 1, 2 |
| V1-FR-05 through V1-FR-07 | 0, 5, 6, 7 |
| V1-FR-08 through V1-FR-10 | 1, 2, 3 |
| V1-FR-11 through V1-FR-13 | 4 |
| V1-FR-14 and V1-FR-15 | 0, 1, 4 |
| V1-FR-16 through V1-FR-21 | 3, then parity in 4 through 7 |
| V1-FR-22 through V1-FR-27 | 3, then parity in 4 through 7 |
| V1-FR-28 and V1-FR-29 | 8 |
| V1-FR-30 and V1-FR-31 | 1, 8 |
| V1-FR-32 through V1-FR-34 | 1, 5, 8 |
| V1-FR-35 through V1-FR-37 | 1 through 4, 8, 9 |
| V1-FR-38 | 3, 6, 7 |
| V1-NFR-01 and V1-NFR-02 | 0, 1, 3, 4, 9 |
| V1-NFR-03 and V1-NFR-04 | 3, 5 through 9 |
| V1-NFR-05 and V1-NFR-06 | 7, 9 |
| V1-NFR-07 and V1-NFR-08 | 0, 3, 5, 7 through 9 |
| V1-EVAL-01 through V1-EVAL-03 | 0 |

### Numbered acceptance criteria

| Acceptance criteria | Primary waves |
|---|---|
| 1–5 | 0, 1, 2, 3, 5, 6, 7, 9 |
| 6–10 | 1, 3, 4, 6, 7, 9 |
| 11–13 | 3, 7, 9 |
| 14–17 | 1, 3, 8, 9 |
| 18–23 | 2, 3, 5, 8, 9 |
| 24–27 | 0, 1, 2, 3, 4, 7, 9 |
| 28–31 | 0, 3, 4, 5, 6, 7, 9 |

No v1 architecture, functional, non-functional, evaluation, or numbered acceptance requirement is intentionally left without a delivery wave.

## 9. Decision and stop gates

| Risk or unknown | Earliest gate | Required response |
|---|---|---|
| Claude/Codex cannot faithfully represent a canonical property | Wave 0 | Mark it unsupported or explicitly lossy; unresolved loss blocks schema freeze. |
| A target cannot install or restore the exact validated native artifact through a public interface | Waves 3 and 7 | Stop the modifying path. Do not claim rollback or edit private native state. |
| Automated Windows host tests cannot prove owner-private storage, contained cleanup, or full process-tree termination | Wave 5 | Do not claim Windows contract coverage until those tests pass. |
| Claude project scope cannot be isolated through a documented scoped operation | Wave 6 | Reject the affected asset/scope combination before mutation. |
| Structural configuration cannot preserve unrelated content safely | Waves 6 and 7 | Fail with a conflict; do not rewrite the document. |
| Persistent caching or encrypted snapshots appear desirable | Wave 0 decision, revisit only with evidence | Keep the default excluded behavior and record the request in the backlog unless the requirements are explicitly revised. |
| A wave exceeds its budget without an end-to-end slice | Any wave | Stop scope expansion, report the evidence, and ask for direction under the execution rules. |

## 10. Common definition of done

A story is complete only when:

- Its user-visible outcome works through the production command dispatcher.
- Its explicit requirements and acceptance cases pass at the appropriate test boundary.
- Human and JSON results agree and use stable, bounded, secret-free data.
- Normal, invalid, conflict, cancellation, interruption, and idempotent paths relevant to the story are covered.
- Target and host code stay behind existing core contracts, with a new seam added only when the current acceptance criterion requires it.
- Documentation, schemas, compatibility declarations, and user guidance affected by the change are updated in the same wave.
- No required check is skipped or weakened.
- The working tree contains no unrelated generated artifacts, and repository attribution checks pass.

A wave is complete only when every story in that wave meets this definition and the wave's runnable exit gate passes.

## 11. Change control

- If implementation changes a normative outcome, update `V1_REQUIREMENTS.md` first and review downstream acceptance impact.
- If an evaluation selects behavior that is currently excluded, revise this plan before implementation.
- If a target version lacks a required public capability, narrow or revise the supported capability range; do not create an undocumented workaround.
- If new work does not trace to a v1 requirement, acceptance criterion, regression, or concrete safety blocker, move it to `BACKLOG.md`.
- Preserve completed MVP behavior throughout the sequence. A v1 wave may migrate state or commands, but it must not leave the existing complete lifecycle unusable between commits.
