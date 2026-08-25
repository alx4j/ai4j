# MVP-ST-001 — Establish the Go module and CI foundation

| Field | Value |
|---|---|
| Status | Defined |
| Type | Enabler |
| Wave | 0 — Foundation and risk retirement |
| Relative size | M |
| Depends on | None |
| Requirements | MVP-NFR-06, MVP-NFR-08 |
| MVP acceptance | 30, 31, 34 |

## User story

As an AI4J maintainer, I want a pinned, reproducible Go repository foundation so that every later story builds and tests against the same product identity and toolchain.

## Outcome

The repository builds a minimal `ai4j` executable from the canonical module and runs deterministic baseline checks in CI.

## Scope

- Create `go.mod` and `go.sum` for module `github.com/alx4j/ai4j`.
- Declare `go 1.26.0` and `toolchain go1.26.6`.
- Add the `cmd/ai4j` entry point and private implementation roots under `internal/`.
- Build with `GOTOOLCHAIN=local`, `GOWORK=off`, read-only module resolution, trimmed paths, embedded VCS metadata, and `CGO_ENABLED=0`.
- Add baseline formatting, module-tidiness, module-verification, unit-test, vet, and authorship-policy checks.
- Reject bot, assistant, generated-by, and `Co-authored-by` attribution in repository commits and release automation.
- Keep signing, notarization, SBOM, and attestation work outside MVP and v1 core delivery; track it as post-v1 release hardening.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-001.T1 — Pin the module and toolchain contract

**Implementation**

- Create `go.mod` and `go.sum` for `github.com/alx4j/ai4j`, with `go 1.26.0`, `toolchain go1.26.6`, and no local-directory `replace` directive.
- Establish only the required repository-private roots: `cmd/ai4j` for the executable and `internal/` for implementation packages; do not introduce another module or a public `pkg/` API.

**Completion evidence**

- A deterministic module-policy check asserts the module path, Go language version, exact toolchain, absence of local replacements, and presence of committed dependency checksums.
- `GOTOOLCHAIN=local GOWORK=off go mod verify` and a read-only module graph check succeed from a clean checkout using the pinned toolchain.

### MVP-ST-001.T2 — Create the thin executable and build identity boundary

**Implementation**

- Add a minimal `cmd/ai4j` entry point that delegates process arguments, standard streams, and exit-code selection to an injected internal application runner; keep command parsing and lifecycle behavior out of `main` until their owning stories.
- Add `internal/buildinfo` as the single owner of product identity and compiled build facts, with immutable inputs and no package-level mutable state.

**Completion evidence**

- Package tests prove the application runner can be substituted without invoking adapters and that `main` contains wiring only.
- A `darwin/arm64` cross-build with `CGO_ENABLED=0` succeeds and binary metadata identifies the canonical module and executable.

### MVP-ST-001.T3 — Define one reproducible build profile

**Implementation**

- Provide one repository-owned build entry point that fixes `GOTOOLCHAIN=local`, `GOWORK=off`, `CGO_ENABLED=0`, `GOOS=darwin`, `GOARCH=arm64`, read-only modules, `-trimpath`, and embedded VCS metadata.
- Reject a dirty tree, missing VCS facts, an unexpected toolchain, or a workspace/module override when the release-profile build is selected.

**Completion evidence**

- Two isolated clean workspaces produce binaries with equal bytes and equal normalized `go version -m` output when all declared build inputs are identical.
- Negative fixtures prove the release profile rejects dirty-tree, wrong-toolchain, writable-module, local-replacement, and active-workspace conditions.

### MVP-ST-001.T4 — Install the baseline CI quality gates

**Implementation**

- Add a GitHub Actions workflow pinned to the repository toolchain and invoke the same repository-owned checks used locally for formatting, module tidiness, module verification, unit tests, `go vet`, and a separate race-test job where the runner supports its native toolchain requirements.
- Keep every check non-interactive, fail-fast, and independent of a developer `go.work` or globally selected Go toolchain; keep the release-profile build independent of a C compiler even when the separate race job needs one.

**Completion evidence**

- Workflow validation and local CI-command fixtures show that formatting drift, `go.mod` or `go.sum` drift, a failing test, a race-test failure, and a vet finding each fail the corresponding gate.
- A clean baseline run records the exact Go version and target tuple and completes without modifying tracked files.

### MVP-ST-001.T5 — Enforce repository attribution policy

**Implementation**

- Add a repository-owned Go policy checker under `internal/repocheck` that validates every in-scope commit's author and committer against the exact required identity and inspects commit messages and release automation for forbidden attribution.
- Wire the checker into CI without changing Git history or adding bot, assistant, generated-by, or co-author metadata to generated release commits.

**Completion evidence**

- Table-driven commit fixtures accept only `Oleksii Stupin <oleksii.stupin@gmail.com>` in both identity fields and reject mismatched names, emails, authors, or committers.
- Message and workflow fixtures reject bot, assistant, generated-by, and `Co-authored-by` attribution while a compliant clean history passes deterministically.

## Story acceptance criteria

- [ ] A clean checkout builds `ai4j` for `darwin/arm64` with the pinned Go patch and no C toolchain.
- [ ] `go.mod` uses the canonical module path and contains no local-directory replacement.
- [ ] CI fails when formatting, module tidiness, module verification, unit tests, vet, or repository attribution checks fail.
- [ ] Embedded Go VCS metadata identifies the current repository commit and reports an unmodified tree for a release-profile build.
- [ ] The repository-local Git identity and CI policy require both author and committer to be `Oleksii Stupin <oleksii.stupin@gmail.com>`.
- [ ] Repository policy rejects bot, assistant, generated-by, and `Co-authored-by` attribution even when author and committer fields are otherwise correct.

## Verification

- Build the binary twice from isolated clean workspaces and compare baseline build metadata.
- Run module, formatting, test, and vet checks with the pinned local toolchain.
- Exercise the authorship policy with accepted and rejected commit fixtures.
- Include rejected message-trailer and generated-attribution fixtures.

## Out of scope

- Production CLI behavior beyond a minimal executable.
- Platform signing, notarization, SBOM generation, and release publication.
