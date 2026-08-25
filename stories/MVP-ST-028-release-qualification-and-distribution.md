# MVP-ST-028 — Publish the basic MVP release

| Field | Value |
|---|---|
| Status | Done |
| Type | Release capability |
| Wave | 6 — Release qualification |
| Relative size | M |
| Depends on | MVP-ST-001 through MVP-ST-026 |
| Requirements | MVP-NFR-06 through MVP-NFR-08 |
| MVP acceptance | 10, 16, 17 |

## User story

As an AI4J user, I want a verifiable Apple Silicon macOS release and a clear tested compatibility profile.

## MVP scope

1. Build `ai4j` for `darwin/arm64` from a clean commit with Go 1.26.6, locked read-only modules, `GOTOOLCHAIN=local`, `GOWORK=off`, and `CGO_ENABLED=0`.
2. Verify the binary's module, command package, exact VCS commit, target, toolchain, clean-tree flag, and trimmed paths.
3. Produce one release bundle containing `ai4j`, deterministic version/build metadata, and a SHA-256 checksum file that identifies the binary.
4. Publish the exact macOS, Git, and Claude Code versions used by the MVP contract fixtures and macOS CI profile.
5. Keep contract tests for the documented Claude validation, install, inspection, update, and uninstall commands, including safe failure on unknown output or failed commands.
6. On a valid `v*` tag, run the existing quality gates, build the bundle once, verify its checksum, and publish those three files without rebuilding.

## Acceptance criteria

- [x] A clean checkout builds the `darwin/arm64` binary with the pinned toolchain and no module-file changes.
- [x] The release bundle contains the binary, version/build metadata, and a matching SHA-256 checksum.
- [x] Published compatibility documentation names the tested macOS, Git, and Claude Code versions and the covered native commands.
- [x] Formatting, unit/integration tests, `go vet`, architecture checks, Darwin race CI, and Claude command-contract tests are release gates.
- [x] Release publication uses the already-verified bundle from the tagged canonical repository commit.
- [x] No release step executes toolkit scripts, binaries, hooks, or MCP servers.

## Verification

- Run the repository quality script and release-input policy checks.
- Build the release bundle from a clean clone and independently verify its checksum and embedded Go/VCS metadata.
- Exercise the release workflow on a test tag before the first public MVP release.

## Out of scope

- Signing, notarization, SBOMs, provenance attestations, reproducibility certification, or an evidence graph.
- Windows, Linux, Intel macOS, Rosetta, Codex, package-manager formulas, auto-update, or a GUI.
- Private-source authentication matrices, automatic recovery, rollback, compensation, or retained artifacts.
