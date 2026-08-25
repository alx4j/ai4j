# AI4J

[![CI](https://github.com/alx4j/ai4j/actions/workflows/ci.yml/badge.svg)](https://github.com/alx4j/ai4j/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/alx4j/ai4j)](https://github.com/alx4j/ai4j/releases/latest)

AI4J is a command-line tool for creating, validating, building, and managing AI
toolkits for Claude Code and Codex.

It turns a versioned toolkit repository into target-native packages, shows the
exact content and Git commit that will become active, and keeps Claude Code
installations manageable through planning, status checks, updates, rollback,
and uninstall.

AI4J does not silently execute toolkit content. It uses documented target
interfaces and leaves final Codex installation to Codex's native plugin flow.

A toolkit is a versioned collection of installable AI content: skills, agents,
instructions, references, scripts, and MCP declarations. Its `toolkit.json`
manifest defines what the collection contains, which targets and hosts it
supports, and how its assets belong together.

## What you can do

- Create a new Claude Code, Codex, or cross-target toolkit.
- Validate toolkit structure and compatibility without installing anything.
- Build deterministic target-native packages for macOS or Windows.
- Plan every Claude Code lifecycle change before applying it.
- Install multiple independent Claude Code toolkits at user or project scope.
- Inspect drift, update installations, change selected content, and roll back.
- Build Codex packages for installation through Codex's plugin browser.
- Use stable JSON output and exit codes in automation.

## Support at a glance

| Host | Build Claude packages | Claude lifecycle | Build Codex packages | Codex installation |
|---|---:|---:|---:|---|
| Apple Silicon macOS | Yes | Yes | Yes | Interactive in Codex |
| Windows x64 | Yes | Yes | Yes | Interactive in Codex |
| Linux, Intel macOS, Windows on ARM | No | No | No | No |

The Windows profile is live-qualified on Windows Server 2025 x64. See
[Compatibility](COMPATIBILITY.md) for the exact tested versions and evidence.

## Before you begin

You need:

- Git
- Claude Code for Claude lifecycle operations
- Access to the toolkit repository you want to use
- A terminal running as your normal user

The `ai4j` executable has no Python, Node.js, Java, Homebrew, or WSL runtime
dependency. AI4J does not require `sudo`.

## Install AI4J

### Homebrew on Apple Silicon macOS

```sh
brew install alx4j/tap/ai4j
ai4j version
```

The formula installs the public release and verifies its SHA-256 checksum.

### Manual installation on macOS

Download `ai4j`, `ai4j.sha256`, and `ai4j.version.json` from the same
[GitHub release](https://github.com/alx4j/ai4j/releases). Then verify and install
the executable:

```sh
shasum -a 256 -c ai4j.sha256
chmod 0755 ai4j
mkdir -p "$HOME/.local/bin"
mv ai4j "$HOME/.local/bin/ai4j"
export PATH="$HOME/.local/bin:$PATH"
ai4j version
```

### Manual installation on Windows

Download `ai4j.exe`, `ai4j.exe.sha256`, and `ai4j.exe.version.json` from the
same release. Verify the executable in PowerShell before placing it on your
`PATH`:

```powershell
$Expected = (Get-Content .\ai4j.exe.sha256).Split(' ', [System.StringSplitOptions]::RemoveEmptyEntries)[0]
$Actual = (Get-FileHash .\ai4j.exe -Algorithm SHA256).Hash.ToLowerInvariant()
if ($Actual -ne $Expected) { throw 'AI4J checksum mismatch' }
.\ai4j.exe version
```

Release executables are currently unsigned. Always verify the checksum from
the same release before running a manually downloaded binary.

## Quick start: install the default toolkit for Claude Code

The built-in source is the first-party toolkit in this repository. Start with
read-only validation:

```sh
ai4j validate
```

Preview a user-scope installation of the default bundle:

```sh
ai4j plan install --target claude --scope user --bundle default
```

The plan identifies the exact source commit, selected content, conflicts, and
ordered changes. Copy the full commit hash from the plan and bind the install
to what you reviewed:

```sh
ai4j install --target claude --scope user --bundle default \
  --expected-commit <full-commit-hash> --yes
```

Then inspect the recorded installation:

```sh
ai4j list
ai4j status --installation <installation-id>
```

`--yes` approves the displayed plan. It is not a force flag and does not bypass
ownership, drift, or recovery checks.

## Create a toolkit

Create a minimal toolkit by naming each target explicitly:

```sh
ai4j init --target claude --output my-claude-toolkit
ai4j init --target claude --target codex --output my-cross-target-toolkit
```

Add `--examples` to include a small selectable skill and example bundle:

```sh
ai4j init --target codex --output my-codex-toolkit --examples
```

The output directory must not already exist. AI4J creates a versioned
`toolkit.json`, target-native package roots, a starter README, and a
`.gitignore`. It does not install or execute the generated content.

## Build target-native packages

Build the first-party default bundle for Apple Silicon macOS:

```sh
ai4j build --target claude --host darwin-arm64 \
  --output dist/claude --bundle default

ai4j build --target codex --host darwin-arm64 \
  --output dist/codex --bundle default
```

Use the Windows profile with `ai4j.exe`:

```powershell
ai4j.exe build --target claude --host windows-amd64 --output dist\claude --bundle default
ai4j.exe build --target codex --host windows-amd64 --output dist\codex --bundle default
```

Choose content with one of these selection forms:

- `--all` includes all compatible content.
- Repeated `--bundle <id>` selects named bundles.
- Repeated `--asset <id>` selects individual assets and their dependencies.

`--all` cannot be combined with `--bundle` or `--asset`. Build output includes
the native package, checksums, a build manifest, and the reason each asset was
included. Building never installs or starts toolkit content.

### Install a built Codex package

AI4J builds and validates Codex packages but does not modify Codex's private
caches or registries. After building `dist/codex`:

1. Use Codex's `@plugin-creator` or `$plugin-creator` workflow to add
   `dist/codex/plugin` to a personal local marketplace.
2. Refresh Codex.
3. Open `/plugins` in Codex CLI or the Plugins tab in the desktop app.
4. Install the plugin and start a new session.

Running `ai4j install --target codex ...` stops with
`unsupported_capability` and prints this native handoff instead of claiming an
installation occurred.

## Choose a source

AI4J always resolves a source explicitly and records its identity.

```sh
# First-party default repository
ai4j validate

# Another GitHub repository
ai4j validate --repo owner/repository

# A branch, tag, or full commit
ai4j validate --repo owner/repository --ref v1.0.0

# An explicit local development checkout
ai4j validate --source /path/to/toolkit --target claude
```

Private GitHub repositories use your existing Git credential helper, SSH
agent, or default SSH keys. AI4J does not accept or store credentials.

Local mode is never inferred from the current directory. A dirty local source
is rejected unless you add `--allow-dirty`; approved dirty output is marked
non-reproducible.

## Claude Code scopes

| Scope | Intended use | Project files |
|---|---|---|
| `user` | Available to your Claude Code user environment | None |
| `project-local` | Available only in one local Git worktree | Uses Git-local excludes; does not edit tracked files |
| `project-shared` | Declaration can be committed for collaborators | Updates AI4J-owned entries but never stages or commits them |

For project-local scope, AI4J can discover the nearest Git root or accept an
explicit path:

```sh
ai4j plan install --target claude --scope project-local \
  --project /path/to/project --bundle default
```

For project-shared scope, review and apply the declaration yourself:

```sh
ai4j plan install --target claude --scope project-shared \
  --project /path/to/project --bundle default

ai4j install --target claude --scope project-shared \
  --project /path/to/project --bundle default \
  --expected-commit <full-commit-hash> --yes
```

## Manage an installation

Use the immutable installation ID returned by `install` or `list`.

| Task | Review | Apply |
|---|---|---|
| Update source | `ai4j plan update --installation <id>` | `ai4j update --installation <id> --expected-commit <commit> --yes` |
| Change selection | `ai4j plan sync --installation <id> --bundle <bundle>` | `ai4j sync --installation <id> --bundle <bundle> --yes` |
| Roll back | `ai4j plan rollback --installation <id>` | `ai4j rollback --installation <id> --yes` |
| Uninstall | `ai4j plan uninstall --installation <id>` | `ai4j uninstall --installation <id> --yes` |

Additional inspection commands are read-only:

```sh
ai4j list
ai4j status --installation <installation-id>
ai4j status --installation <installation-id> --check-updates
ai4j history --installation <installation-id>
ai4j doctor --installation <installation-id>
```

`status --check-updates` is the only status form that accesses the source.
Plain status and list commands use local state and observed target state.

## JSON and automation

Every command supports `--json`:

```sh
ai4j plan install --target claude --scope user --bundle default --json
ai4j status --installation <installation-id> --json
```

JSON mode writes one schema-versioned document to standard output. It contains
no prompts or human prose. Modifying commands require `--yes` in JSON or other
non-interactive use.

The stable exit-code families distinguish cancellation, usage errors,
unsupported environments, source failures, validation failures, conflicts,
compensated failures, recovery requirements, and internal failures. See the
[full user guide](USER_GUIDE.md#json-output-and-automation) for the complete
table.

## Safety model

AI4J is deliberately conservative around installation state:

- Plans are read-only and disclose the exact source and active content.
- `--expected-commit` and `--expected-source-digest` bind apply to review.
- Toolkit scripts, binaries, hooks, and MCP servers are not started implicitly.
- AI4J changes or removes only resources it can attribute to an installation.
- Existing drift and ambiguous ownership stop mutation by default.
- Modifying operations are locked, journaled, verified, and recoverable.
- Private state and rollback data use host-native access restrictions.
- Disk capacity is checked before bounded staging and retained-data writes.

`ai4j doctor` is static by default. The optional `--test-mcp <server-id>` form
does start the selected process with your current user permissions after an
explicit warning and approval. It is a bounded startup check, not a sandbox or
a full health guarantee.

If a command reports `recovery_required`, stop modifying operations and run:

```sh
ai4j status --json
```

Retain that output and inspect the reported state before changing files
manually. Do not delete recovery markers merely to bypass the protection.

## Current limits

AI4J v1 intentionally does not provide:

- Automated Codex installation, enablement, update, or uninstall
- Linux, Intel macOS, Windows on ARM, or Rosetta support
- Stored GitHub credentials or OAuth
- Offline GitHub operation or a persistent source cache
- Release signing, Apple notarization, Windows Authenticode, SBOMs, or
  provenance attestations

Unsupported combinations fail explicitly instead of degrading silently.

## Documentation

- [Full user guide](USER_GUIDE.md)
- [Compatibility and qualification evidence](COMPATIBILITY.md)
- [v1 requirements](V1_REQUIREMENTS.md)
- [v1 implementation plan](V1_IMPLEMENTATION_PLAN.md)
- [MVP requirements](MVP_REQUIREMENTS.md)
- [Backlog](BACKLOG.md)

## Contributing

Development happens through pull requests. Create a branch from `main`, keep
the change focused, and open a PR. The protected branch requires the Linux
quality and reproducibility checks, Darwin ARM64 race tests, and Windows x64
contracts to pass before merge.
