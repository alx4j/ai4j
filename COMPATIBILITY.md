# AI4J compatibility

AI4J supports Claude user, project-local, and project-shared profiles on Apple Silicon macOS and Windows x64. It builds Codex packages for both host profiles and hands installation to Codex's native interactive plugin browser. v1 distinguishes contract-tested profiles from live-host qualification evidence.

## Contract-tested profile

| Component | Contract profile |
|---|---|
| macOS | macOS 15 Apple Silicon (`darwin/arm64`); live-qualified on macOS 15.7.7 build 24G720 |
| Windows | Windows x64 (`windows/amd64`); live-qualified on GitHub's Windows Server 2025 Datacenter build 26100 runner |
| Git | 2.55.0 in the macOS live qualification; 2.55.0.windows.4 in the Windows live qualification |
| Claude Code | 2.1.211 version-bounded command/output contract; live-qualified on macOS and hosted Windows |
| Codex CLI | 0.149.1 package and native-handoff qualification on hosted Windows; interactive installation remains manual |
| Go release toolchain | 1.26.6 |

Exact-version fixtures cover host and prerequisite discovery. These are automated contract-support claims, not evidence that each selected target was exercised on a clean host. Other versions are not claimed to be compatible until they pass the same contract suite.

## Live Claude/macOS qualification

The `Claude macOS qualification` GitHub Actions workflow runs when its workflow or live Claude inputs change on `main` and can also be triggered manually. It uses the hosted `macos-15` ARM64 image with Claude Code 2.1.211. It builds and verifies the real Darwin executable, validates the canonical repository at the workflow commit through the real Claude CLI, exercises user and project scopes, refreshes and updates the native plugin, uninstalls it, runs the Darwin lock tests, and performs static and explicitly approved MCP diagnostics.

The workflow uploads its JSON and command evidence for 14 days, including the exact macOS product and build versions assigned by GitHub. The workflow definition alone is not a compatibility result: a successful canonical run is required before its profile is treated as live-qualified.

The canonical run for commit `b7eb58d6b375ac544053b90a10324ec5c1386669` passed on August 25, 2026, using Claude Code 2.1.211 on GitHub's macOS 15.7.7 build 24G720 ARM64 runner. The retained evidence confirms successful user, project-local, and project-shared install, status, doctor, and uninstall journeys; exact-SHA project-shared declaration; native registration, installation, and enablement; and clean project worktrees after uninstall. See [GitHub Actions run 32893857897](https://github.com/alx4j/ai4j/actions/runs/32893857897).

## Live Windows qualification

The `Windows qualification` GitHub Actions workflow uses fresh `windows-2025` x64 runners and two independent jobs. The Claude job installs Claude Code 2.1.211 and exercises user, project-local, and project-shared install, status, doctor, and uninstall journeys, including native refresh, exact-SHA project-shared settings, clean project worktrees after uninstall, and one explicitly approved MCP startup check. The Codex job installs Codex CLI 0.149.1, builds and verifies the complete reproducible `windows-amd64` package from the workflow commit, validates every artifact checksum, and confirms that automated installation stops without mutation at the documented `/plugins` handoff.

The canonical run for commit `b7eb58d6b375ac544053b90a10324ec5c1386669` passed on August 25, 2026, using Windows Server 2025 Datacenter version 10.0.26100 build 26100, Git 2.55.0.windows.4, Go 1.26.6, Claude Code 2.1.211, and Codex CLI 0.149.1. Both jobs retained their evidence for 14 days. See [GitHub Actions run 32893857875](https://github.com/alx4j/ai4j/actions/runs/32893857875).

The hosted Windows Server 2025 run is the accepted live qualification for the `windows/amd64` profile. Codex's interactive plugin-browser journey remains a separate manual check because the documented interface is interactive.

## Claude command contract

AI4J tests the following documented Claude Code operations with exact identities and explicit `user`, `local`, or `project` scope where the command supports it:

- `claude plugin validate . --strict`
- `claude plugin marketplace add <catalog-root> --scope user`
- `claude plugin install ai4j-default@ai4j --scope user`
- `claude plugin enable ai4j-default@ai4j --scope user`
- `claude plugin marketplace list --json`
- `claude plugin list --json`
- `claude plugin marketplace update ai4j`
- `claude plugin update ai4j-default@ai4j --scope user`
- `claude plugin uninstall ai4j-default@ai4j --scope user --keep-data`
- `claude plugin marketplace remove ai4j --scope user`

Project-local and project-shared contract journeys use the same scoped install, enable, update, uninstall, and marketplace-removal commands from the canonical Git root. Project-shared declarations use the documented inline `extraKnownMarketplaces` settings source with an exact-SHA `git-subdir` plugin source; AI4J does not write `enabledPlugins`.

The contract tests reject failed commands, malformed JSON, missing identities, and unknown required state. Lifecycle operations do not start toolkit scripts, hooks, binaries, or MCP servers.

Claude owns its plugin cache and persistent plugin data. A successful uninstall removes the scoped plugin and marketplace registration plus checksum-matching AI4J-owned files; it does not claim to purge Claude-owned cache bytes or revoke content already loaded in a running session.

## Codex package contract

AI4J renders the selected content under a native `.codex-plugin/plugin.json` package with its declared skills, agents, instructions, MCP configuration, references, and support files. The package and build manifest are covered by deterministic checksums on both supported host profiles.

Codex currently exposes install, enable, update, and removal through the interactive `/plugins` browser in Codex CLI or the desktop Plugins tab. AI4J therefore stops automated Codex lifecycle requests before source acquisition or mutation and directs the user to that native browser. No AI4J operation writes Codex private caches, databases, or undocumented registries.

The successful Windows qualification run verifies this boundary with Codex CLI 0.149.1: AI4J builds the full first-party package and checks its manifest and artifact digests, then the attempted automated lifecycle request returns `unsupported_capability` without creating AI4J installation state. It does not claim that a user completed the interactive Codex installation.

## Diagnostic process contract

Static `doctor` reads AI4J state, structural history, operation journals, owned-file checksums, retained native package declarations, executable availability, native status, and environment-variable presence without starting toolkit content.

An explicitly selected and approved MCP startup check runs without a command shell for at most five seconds. Windows uses a kill-on-close Job Object and Darwin/Linux use an isolated process group so cancellation and timeout terminate descendants. The child receives an allowlisted environment, output capture is bounded and discarded after classification, and reports contain variable names but never values or raw child output.
