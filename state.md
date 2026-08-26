# AI4J MVP Implementation State

| Field | Value |
|---|---|
| Execution source | [MVP implementation plan](MVP_IMPLEMENTATION_PLAN.md) |
| Tracking scope | MVP stories `MVP-ST-001` through `MVP-ST-028` |
| Update policy | Update after each story transition; commit only after the complete wave passes its exit gate |
| Last updated | 2026-08-24 |

## Status definitions

| Status | Meaning |
|---|---|
| Pending | Dependencies or wave entry gate are not complete |
| In progress | Sequential technical substories are being implemented |
| Done | Story acceptance and completion evidence passed inside its wave |
| Blocked | A named requirement or external capability prevents safe progress |
| Deferred to V1 | Explicitly excluded from the MVP baseline |

## Story status

| Wave | Story | Status | Current evidence or next gate |
|---:|---|---|---|
| 0 | MVP-ST-001 — Go module and CI foundation | Done | Pinned Go 1.26.6 checks, unit tests, vet, Darwin/ARM64 CGO-free build, authorship policy, and two-clean-clone byte reproducibility passed |
| 0 | MVP-ST-002 — Typed core and extension registry | Done | Typed identity/fault tests, independent fail-closed registrations, all-port deterministic fakes, nested dependency guards, and second target/host extension tests passed |
| 0 | MVP-ST-003 — CLI, JSON, and exit contracts | Done | Exact grammar, typed result/exit model, JSON v1 schemas, independent renderers, adapter-free version path, and deterministic process-contract harness passed |
| 1 | MVP-ST-004 — Darwin host and private runtime | Done | The production validate path uses a private `0700` temporary workspace, direct bounded child processes, and no shell or target mutation; full tests, vet, and Darwin/ARM64 build pass |
| 1 | MVP-ST-005 — Environment and Claude capabilities | Done | Validate checks Apple Silicon macOS, Git, Claude Code, and the default user configuration directory before source use, then applies the documented `claude plugin validate` operation to the static-valid package |
| 1 | MVP-ST-006 — Canonical GitHub source | Done | Built-in and explicit public HTTPS sources are canonicalized without fallback; unsupported transport is rejected before workspace creation |
| 1 | MVP-ST-007 — Immutable Git provenance | Done | Default branch, explicit branch, tag, and full commit resolve to an exact commit and tree before validation; checkout attributes, index, and clean status are verified |
| 1 | MVP-ST-008 — Ephemeral source workspaces | Done | A fresh operation workspace is removed after successful validation and handled native/static validation failures; no persistent source cache is created |
| 1 | MVP-ST-009 — Toolkit package validation | Done | The root manifest, marketplace, plugin, bounded tracked-file inventory, MVP component allowlist, and Claude-native package validation are joined in the runnable command |
| 1 | MVP-ST-010 — Content roots and filesystem safety | Done | Only tracked regular files beneath declared roots enter the package; traversal, special modes, unsafe attributes, and case/Unicode path collisions fail before materialization or target mutation |
| 1 | MVP-ST-011 — MCP, executable, and secret validation | Done | Command-based MCP declarations, executable ownership/dependency, placeholders, and same-name environment references are validated statically without executing toolkit content or exposing values |
| 1 | MVP-ST-012 — First-party default toolkit | Done | The repository ships `toolkit.json`, one marketplace/plugin, a skill with support content, an agent, shared rules, and a documented command-based MCP declaration; unrelated Go/repository files are excluded |
| 1 | MVP-ST-013 — Active-content disclosure and no execution | Done | Production `ai4j validate` now returns deterministic human/JSON source and active-content disclosure, includes the trust warning, invokes only Git and Claude validation, and cleans its workspace |
| 2 | MVP-ST-014 — SHA-pinned catalog and native handoff | Done | Deterministic Claude catalog JSON contains one first-party plugin whose documented `git-subdir` source pins the validated full commit; renderer and checksum tests pass |
| 2 | MVP-ST-015 — Installation state and atomic reads | Done | One bounded, versioned, secret-free ownership record loads safely and is atomically replaced; absent, malformed, unknown-schema, round-trip, and cleanup tests pass |
| 2 | MVP-ST-016 — Complete lifecycle plans | Done | Production `install --dry-run`, `update --dry-run`, and `uninstall --dry-run` disclose deterministic source, actions, active content, warnings, conflicts, and expected state without mutation; pinned, no-change, fast-forward, rewritten-reference, missing-state, native-identity, and checksum cases pass |
| 3 | MVP-ST-017 — Approval and reviewed commit | Done | Install recomputes under the mutation lock, enforces `--expected-commit`, requires `--yes` for JSON/non-interactive use, and displays the plan before an interactive prompt |
| 3 | MVP-ST-018 — Mutation locking and consistent reads | Done | One bounded Darwin `flock` serializes modifying commands and releases through the OS file descriptor; contention and release fixtures are included |
| 3 | MVP-ST-019 — Minimal operation marker | Done | One bounded, private, secret-free install marker is written before external/owned mutation and removed only after state commit and cleanup |
| 3 | MVP-ST-020 — Interrupted-operation inspection | Done | Marker-only and fully committed states are safely cleaned; partial, malformed, or ambiguous states remain blocked with `recovery_required` |
| V1 | MVP-ST-021 — Short-lived recovery material | Deferred to V1 | MVP excludes preimages and rollback material |
| 3 | MVP-ST-022 — Dedicated shared rules | Done | Install atomically creates only `~/.claude/rules/ai4j.md` from the validated tracked bytes and records its raw-file checksum; occupied or drifted files conflict |
| V1 | MVP-ST-027 — Checksum-gated compensation | Deferred to V1 | MVP excludes automatic compensation and rollback |
| 3 | MVP-ST-023 — Install and idempotent reconciliation | Done | Production dispatch completes the approved exact-commit Claude install, verifies native and owned state, commits installation state, and returns `no_change` on converged reinstall |
| 4 | MVP-ST-024 — Status, drift, and update check | Done | Production `status` reports recorded exact source, documented native state, owned-file drift, recovery attention, and optional branch/tag/commit update disposition without repair or content execution |
| 5 | MVP-ST-025 — Fast-forward update | Done | Approved fast-forward updates disclose content changes, verify native/owned state, preserve installation identity, and commit only the verified exact SHA |
| 5 | MVP-ST-026 — Conservative uninstall | Done | Preflighted scoped removal preserves unrelated rules and persistent plugin data, removes owned state last, and retains a marker on ambiguous failure |
| 6 | MVP-ST-028 — Release qualification and distribution | Done | Clean-clone release bundle, checksum and metadata verification, published compatibility profile, and canonical tag-release workflow pass the Wave 6 gate |

## Wave history

| Wave | Status | Exit-gate evidence | Commit |
|---:|---|---|---|
| 0 | Done | Clean-clone quality, schema/process contracts, architecture/fakes, Darwin/ARM64 release build, byte reproducibility, and VCS/version metadata passed with Go 1.26.6 | `feat: complete MVP wave 0 foundation` |
| 1 | Done | The production validate vertical slice resolves an exact public GitHub commit, validates and discloses the first-party package without execution or persistent mutation, cleans temporary workspaces on handled paths, passes all tests and vet, and cross-builds for Darwin/ARM64. Target-host smoke qualification is tracked as release verification, not additional Wave 1 implementation. | `c8b6cb0` |
| 2 | Done | Production install/update/uninstall plans, exact-SHA catalog rendering, and atomic installation-state snapshots pass focused and full tests, vet, diff checks, and the Darwin/ARM64 CGO-free cross-build. Race execution is unavailable on this Windows host because no C compiler is installed. | `27a8e68` |
| 3 | Done | Approval, exact-commit guard, one Darwin mutation lock, minimal marker inspection, owned catalog/rules writes, documented Claude user-scope handoff, final verification, committed state, recovery-required failures, and idempotent reinstall pass focused/full tests, vet, diff checks, and the Darwin/ARM64 CGO-free cross-build. Real supported-host command qualification remains a release verification item. | `27a8e68` |
| 4 | Done | The production status vertical slice runs locally by default, renders deterministic human/JSON output, preserves unsupported native facts as `not_observable`, reports ordinary drift and recovery precisely, and uses cleaned disposable source workspaces only for explicit update checks. Focused/full tests, vet, diff checks, isolated executable smoke, and Darwin/ARM64 cross-build pass. | `d7e0b44` |
| 5 | Done | Fast-forward update and conservative uninstall complete end to end with exact command ordering, approval/conflict gates, retained recovery markers, idempotency, unrelated-file preservation, full tests, vet, and a Darwin/ARM64 cross-build. | `a9be627` |
| 6 | Done | A clean isolated clone produces the Darwin/ARM64 binary, commit/toolchain/target/version metadata, and matching SHA-256 file; independent verification and two-clone byte comparison pass, and a canonical tag workflow publishes that verified bundle after the full quality gate. | This commit |
