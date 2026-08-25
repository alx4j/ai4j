# AI4J user guide

AI4J authors and builds toolkits for Claude Code and Codex. Its current installation lifecycle resolves selected toolkits from GitHub or an explicit local development checkout for Claude Code, shows exactly what would become active, and uses Claude's supported plugin commands to install, update, synchronize, roll back, or remove them.

Authoring and build commands can generate target-native Claude or Codex packages. The Claude Code lifecycle supports user, project-local, and project-shared installations on Apple Silicon macOS and supported Windows x64 hosts. Both profiles are live-qualified on the hosted systems documented in [COMPATIBILITY.md](COMPATIBILITY.md).

## Before you begin

You need:

- Apple Silicon macOS or a supported Windows x64 host
- Git
- Claude Code
- Access to the selected GitHub repository, or a local Git checkout for development mode
- A terminal running as your normal user; AI4J never requires `sudo`

See [COMPATIBILITY.md](COMPATIBILITY.md) for contract-tested host, Git, Claude Code, and Go versions.

## Install the AI4J executable

Download these three macOS files from the same version on the [AI4J GitHub Releases page](https://github.com/alx4j/ai4j/releases):

- `ai4j`
- `ai4j.version.json`
- `ai4j.sha256`

Place them in one directory and verify the binary before running it:

```sh
shasum -a 256 -c ai4j.sha256
```

The command must report `ai4j: OK`. Make the binary executable and place it in a directory on your `PATH`:

```sh
chmod 0755 ai4j
mkdir -p "$HOME/.local/bin"
mv ai4j "$HOME/.local/bin/ai4j"
```

If needed, add that directory to your shell configuration:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Confirm the installed binary:

```sh
ai4j version
```

On Windows, obtain `ai4j.exe`, `ai4j.exe.version.json`, and `ai4j.exe.sha256` from the same GitHub release. Verify the checksum with PowerShell, place the executable on your `PATH`, and run it without renaming:

```powershell
$Expected = (Get-Content .\ai4j.exe.sha256).Split(' ', [System.StringSplitOptions]::RemoveEmptyEntries)[0]
$Actual = (Get-FileHash .\ai4j.exe -Algorithm SHA256).Hash.ToLowerInvariant()
if ($Actual -ne $Expected) { throw 'AI4J checksum mismatch' }
.\ai4j.exe version
```

Use `ai4j version --json` when you need machine-readable build, commit, toolchain, and target information.

## Quick start

The default source is the first-party AI4J toolkit at `github.com/alx4j/ai4j`.

First validate it without installing anything:

```sh
ai4j validate
```

Review a complete v1 installation plan and select the content you want:

```sh
ai4j install --dry-run --target claude --scope user --bundle default
```

The plan shows the exact Git commit, active content, ordered actions, conflicts, and expected final state. Copy the full commit hash from the plan and bind installation to that reviewed commit:

```sh
ai4j install --target claude --scope user --bundle default \
  --expected-commit <full-commit-hash> --yes
```

Check the result:

```sh
ai4j status
```

AI4J installs the selected Claude native package, enables it for the current user, and writes an installation-specific rules file. The returned immutable installation ID selects that installation for later commands.

## Create a toolkit

Create a minimal toolkit in a new directory by naming each target explicitly:

```sh
ai4j init --target claude --output my-toolkit
ai4j init --target claude --target codex --output my-cross-target-toolkit
```

Add `--examples` to include a small selectable skill and an `examples` bundle:

```sh
ai4j init --target codex --output my-toolkit --examples
```

The output directory must not already exist. AI4J creates a schema-versioned `toolkit.json`, target-native package roots, a README, and a `.gitignore` for build output. It does not install or execute the generated content.

## Build target-native output

Build all content from the first-party toolkit for one target and host:

```sh
ai4j build --target claude --host darwin-arm64 --output dist/claude --all
ai4j build --target codex --host darwin-arm64 --output dist/codex --all
```

Use the Windows host profile when running `ai4j.exe` on Windows x64:

```powershell
ai4j.exe build --target claude --host windows-amd64 --output dist\claude --all
ai4j.exe build --target codex --host windows-amd64 --output dist\codex --all
```

Or select assets and named bundles. Repeating either option is supported:

```sh
ai4j build --target claude --host darwin-arm64 --output dist/review \
  --asset review-checklist --asset check-diff

ai4j build --target codex --host darwin-arm64 --output dist/default \
  --bundle default
```

`--all` cannot be combined with `--asset` or `--bundle`. AI4J expands dependencies and complete native package units deterministically, reports why each asset was included, validates the rendered native package, writes checksums and a build manifest, and never installs or starts the content. The output directory must not already exist.

### Install a built Codex package

Codex currently documents plugin management through its interactive plugin browser, not a scriptable lifecycle interface. After building `dist/codex`, use Codex's `@plugin-creator` or `$plugin-creator` workflow to add `dist/codex/plugin` to a personal local marketplace, refresh Codex, open `/plugins` in Codex CLI (or the Plugins tab in the desktop app), and install the plugin there. Start a new session before using its skills or tools.

`ai4j install --target codex ...` fails with `unsupported_capability` before source acquisition or mutation and prints this native handoff. AI4J does not edit Codex private caches, databases, or registries and does not claim that a built package is installed, enabled, or loaded.

Build directly from an explicit local checkout with `--source <path>`:

```sh
ai4j build --source /path/to/toolkit --target claude --host darwin-arm64 \
  --output dist/local-claude --all
```

Dirty, untracked, or ignored input is rejected unless you add `--allow-dirty`; an approved dirty build is marked non-reproducible.

## Validate a toolkit

Validation resolves an exact Git commit and checks the toolkit without installing it or starting its content.

Validate the first-party default:

```sh
ai4j validate
```

Validate a specific branch, tag, or full commit:

```sh
ai4j validate --ref main
ai4j validate --ref v1.0.0
ai4j validate --ref <full-commit-hash>
```

Validate another GitHub toolkit repository:

```sh
ai4j validate --repo owner/repository
ai4j validate --repo owner/repository --ref main
```

`--repo` accepts canonical GitHub HTTPS or SSH forms. Private repositories use your existing system Git credential helper, SSH agent, or default SSH keys; AI4J neither accepts nor stores credentials. The short `owner/repository` form is recommended. An explicit source failure never falls back to the built-in repository.

Validate a local development checkout explicitly:

```sh
ai4j validate --source /path/to/toolkit --target claude
ai4j validate --source /path/to/toolkit --target claude --allow-dirty
```

Local mode is never inferred from the current directory and never modifies the checkout.

Validation reports:

- The canonical repository and exact commit, or canonical local checkout and source digest
- Skills and agents
- Shared instructions
- Scripts and binaries
- MCP command declarations and required host executables
- Validation warnings or conflicts

Validation is static. It does not claim that toolkit instructions are trustworthy or that declared executables will start successfully.

## Preview changes with `--dry-run`

Planning is read-only:

```sh
ai4j install --dry-run
ai4j update --dry-run --installation <installation-id>
ai4j sync --dry-run --installation <installation-id> --bundle default
ai4j rollback --dry-run --installation <installation-id>
ai4j uninstall --dry-run --installation <installation-id>
ai4j history purge --dry-run --installation <installation-id> --expired
```

A dry run returns the command's complete plan without installing, updating, removing, enabling, or executing content. Use it when you want a separate read-only review before running the command.

`install --dry-run` accepts `--repo` and `--ref`, or the mutually exclusive `--source`. Update, sync, rollback, and uninstall use the selected installation's retained source and ownership information. `--dry-run` never mutates target state or retained history, never prompts, and cannot be combined with `--yes`, `--expected-commit`, or `--expected-source-digest`.

## Install

Interactive installation displays the plan and asks for confirmation. A v1 install requires one target, scope, and selection:

```sh
ai4j install --target claude --scope user --bundle default
```

Use `--yes` for explicit non-interactive approval:

```sh
ai4j install --target claude --scope user --all --yes
```

For stronger review-to-execution consistency, use the exact commit shown by `install --dry-run`:

```sh
ai4j install --target claude --scope user --all \
  --expected-commit <full-commit-hash> --yes
```

If the source resolves to another commit, installation stops without mutation. Repeating installation after the desired state is already present returns `no_change`.

For a local development install, bind apply to the digest shown by the plan:

```sh
ai4j install --dry-run --source /path/to/toolkit --target claude --scope user --all
ai4j install --source /path/to/toolkit --target claude --scope user --all \
  --expected-source-digest <sha256> --yes
```

The selected native package is copied into a private, immutable, content-addressed backing bundle. The original checkout remains user-owned input, not an AI4J cache.

Multiple toolkits can coexist. AI4J assigns independent marketplace, plugin, catalog, rules, state, and history identities. A duplicate logical target/scope/root/toolkit installation is rejected rather than merged implicitly.

### Install for one project

From anywhere inside a Git worktree, omit `--project` to use its nearest Git root, or provide the project explicitly:

```sh
ai4j install --dry-run --target claude --scope project-local --bundle default
ai4j install --target claude --scope project-local --project /path/to/project \
  --bundle default --yes
```

Project-local rules are uniquely named and added to that worktree's Git-local exclude file before Claude activation. AI4J does not edit tracked project content for this scope. Claude operations run in the selected project with explicit `local` scope.

Use project-shared scope when the declaration should be committed by you for collaborators:

```sh
ai4j install --dry-run --target claude --scope project-shared --project /path/to/project \
  --bundle default
ai4j install --target claude --scope project-shared --project /path/to/project \
  --bundle default --expected-commit <full-commit-hash> --yes
```

AI4J adds only its stable inline marketplace entry to `.claude/settings.json` and writes `.claude/rules/<declaration-id>.md`. The marketplace uses Claude's `source: "settings"` format and pins the plugin's Git-subdirectory source to the validated full commit SHA. Existing settings, unrelated marketplaces, and `enabledPlugins` are preserved. AI4J never stages or commits these files.

Project-shared uninstall first removes the scoped plugin and marketplace through Claude, then removes only AI4J's declaration and rules. A local development checkout cannot be installed at project-shared scope because it is not portable to collaborators.

If `CLAUDE_CONFIG_DIR` is set, user-scope rules use that effective directory. AI4J accepts an existing absolute override contained within the current user's home and rejects empty, missing, outside-home, or unusable overrides before mutation.

## Check status

List locally recorded installations without accessing GitHub or starting toolkit content:

```sh
ai4j list
ai4j list --target claude --scope user
```

The list is ordered deterministically and shows each immutable installation ID, active or archived lifecycle, target, scope root, source commit, requested and resolved selection, health, retained-history count, and last operation. If an older MVP state record is present, `list` also previews the schema migration and its defaults without changing the file.

Inspect one installation by ID:

```sh
ai4j status --installation <installation-id>
```

For backward compatibility, plain `ai4j status` still works when zero or one record exists. An installation ID is required when several records exist. Local status does not access GitHub:

```sh
ai4j status
```

It reports:

- Whether AI4J is installed
- Toolkit and plugin identity
- Installed repository and exact commit
- Observable Claude plugin installation and enablement state
- AI4J-owned rules-file drift
- Whether an interrupted operation needs attention

Check the selected installation's stored source for updates explicitly:

```sh
ai4j status --installation <installation-id> --check-updates
```

This may access GitHub. The update result can be `available`, `up_to_date`, `pinned`, `ref_rewritten`, or `unknown`.

## Diagnose an installation

Run static checks without starting any toolkit script, hook, binary, or MCP server:

```sh
ai4j doctor --installation <installation-id>
```

The report covers the host profile, Git and target executables, state/history/journal integrity, owned-file drift, observable native state, the retained package artifact, MCP declarations, MCP executable availability, and whether referenced environment-variable names are present. Secret values are never displayed.

To test one MCP process explicitly, first preview the exact executable, argument array, working directory, ownership, referenced variable names, and side-effect warning:

```sh
ai4j doctor --installation <installation-id> --test-mcp <server-id>
```

Then approve the same check:

```sh
ai4j doctor --installation <installation-id> --test-mcp <server-id> --yes
```

The command invokes the executable directly without a shell, passes only the documented baseline environment plus the selected server's referenced variables, bounds captured output, and terminates the process tree after five seconds. A `timed_out` result means the process started and was stopped at that boundary; it does not prove that the server is healthy in Claude or that it had no side effects. The process runs with your current user permissions and is not sandboxed.

## Update

Without source flags, AI4J updates only the branch recorded during installation. Repository or reference migration requires explicit `--repo` and/or `--ref` options.

Review one installation's update first:

```sh
ai4j update --dry-run --installation <installation-id>
```

The plan classifies active content as added, removed, changed, or unchanged. Apply the reviewed exact commit with:

```sh
ai4j update --installation <installation-id> \
  --expected-commit <full-commit-hash> --yes
```

Update behavior is intentionally conservative:

- A branch moves only through a verified fast-forward update.
- A tag or full-commit installation remains pinned.
- Rewritten branch history is rejected.
- Changed AI4J-owned files or unexpected Claude state block mutation.
- The new commit is recorded only after files and Claude state are verified.

Repeating an update when the branch has not moved returns `no_change`.

To migrate an existing GitHub installation explicitly, plan with `--repo` and/or `--ref`, then apply with the exact commit shown in that plan. The toolkit and installation IDs are preserved:

```sh
ai4j update --dry-run --installation <installation-id> --repo owner/new-repository --ref main
ai4j update --installation <installation-id> --repo owner/new-repository --ref main \
  --expected-commit <full-commit-hash> --yes
```

For a local development installation, `update` reads the stored checkout. Use `--allow-dirty` when required and `--expected-source-digest` to bind apply to the reviewed snapshot.

## Change the installed selection

For GitHub installations, `sync` keeps the stored exact source commit and changes only the requested selection. For local development installations, it snapshots the stored checkout and can disclose a simultaneous source-digest change:

```sh
ai4j sync --dry-run --installation <installation-id> --asset review-checklist
ai4j sync --installation <installation-id> --asset review-checklist --yes

ai4j sync --dry-run --installation <local-installation-id> --asset review-checklist --allow-dirty
ai4j sync --installation <local-installation-id> --asset review-checklist --allow-dirty \
  --expected-source-digest <sha256> --yes
```

Use `--all`, repeat `--asset`, or repeat `--bundle` exactly as with `build`. Repeating a converged sync returns `no_change` and creates no redundant history.

## Resolve owned drift

The default conflict policy is `fail`. Update, sync, rollback, and uninstall also accept:

- `keep`: preserve conflicting installation-owned bytes and complete with degraded status.
- `replace-owned`: replace only resources already attributed to that installation; the prior bytes are retained privately for rollback.
- `interactive`: choose between those safe actions in an interactive terminal. It is not accepted with `--dry-run`, JSON, or non-terminal input.

`--yes` approves the displayed plan; it is never a force flag.

## History and rollback

List retained structural rollback points without displaying their opaque content:

```sh
ai4j history --installation <installation-id>
```

Rollback uses the latest point by default, or an explicitly selected operation:

```sh
ai4j rollback --dry-run --installation <installation-id>
ai4j rollback --installation <installation-id> --yes

ai4j rollback --dry-run --installation <installation-id> --operation <operation-id>
ai4j rollback --installation <installation-id> --operation <operation-id> --yes
```

Purge one point, expired points, or all points only after reviewing the purge plan:

```sh
ai4j history purge --dry-run --installation <installation-id> --expired
ai4j history purge --installation <installation-id> --expired --yes
```

History purge never changes current Claude or AI4J-owned target content. Purging the final point of an archived installation also removes its tombstone, as disclosed by the plan.

## Uninstall

Review removal before applying it:

```sh
ai4j uninstall --dry-run --installation <installation-id>
ai4j uninstall --installation <installation-id> --yes
```

Uninstall:

- Removes `ai4j-default@ai4j` from Claude's user scope
- Retains Claude persistent plugin data
- Removes the `ai4j` marketplace registration from user scope
- Removes only checksum-matching AI4J catalog and rules files
- Archives a minimal installation tombstone after target removal
- Retains a rollback point that can restore the removed installation
- Preserves unrelated Claude configuration, rules, plugins, and marketplaces

Claude may retain cache bytes after uninstall, and an already-running Claude session may still have previously loaded content until it is restarted. AI4J does not edit Claude's private cache or claim immediate session revocation.

An archived installation can be inspected, rolled back, reactivated explicitly with `ai4j install --installation <installation-id> --yes`, or have its retained history purged. Update, sync, and another uninstall reject archived records.

## JSON output and automation

Every command supports `--json`:

```sh
ai4j install --dry-run --json
ai4j install --expected-commit <full-commit-hash> --yes --json
ai4j status --json
```

JSON mode writes one schema-versioned JSON document to standard output. It contains no prompts or human prose. Executing a modifying command therefore requires `--yes` in JSON or other non-interactive use; `--dry-run` remains read-only and needs no approval. The JSON `command` field stays the actual command name, while `data` contains either the dry-run plan or the execution result.

Exit codes are stable:

| Code | Meaning |
|---:|---|
| `0` | Success, including `no_change` or a pinned update |
| `1` | Cancelled before mutation |
| `2` | Invalid command usage or missing approval |
| `3` | Unsupported or incomplete environment |
| `4` | Git or source failure |
| `5` | Toolkit or Claude package validation failure |
| `6` | Ownership, drift, concurrency, or expected-commit conflict |
| `7` | Failed operation was safely compensated |
| `8` | Recovery is required or cleanup remains pending |
| `9` | Unexpected internal failure |

## Safety and recovery

AI4J uses a single modifying-command lock. It checks existing files and Claude identities before mutation and verifies checksum ownership before replacing or removing its files.

Before bounded source materialization, build or init staging, target-owned file replacement, journal/history retention, state commit, and rollback-artifact expansion, AI4J verifies available capacity on the destination filesystem. An unavailable capacity reading or insufficient space stops the write with `disk_capacity_unavailable` or `insufficient_disk_space`.

On Windows, AI4J keeps private state and retained artifacts under `%LOCALAPPDATA%\AI4J`, creates them with a protected current-user/System ACL, rejects junction or reparse-point traversal, and contains external Git and Claude processes in a Job Object. On macOS, the equivalent private root is under `~/Library/Application Support/ai4j`.

It also records an operation marker and a private structural history entry before a modifying command changes external or owned state. If completion becomes ambiguous, AI4J keeps the marker and reports `recovery_required` instead of guessing or overwriting uncertain state.

Temporary source, build, init, lifecycle, and rollback workspaces carry an AI4J sidecar marker plus a live operating-system lease. A later command removes a crash orphan only when both identify a recognized AI4J workspace and the lease proves that no process still owns it. Live and unmarked directories are retained. Completed build and init staging directories are published with one atomic directory rename, so a partial output tree is not exposed.

Before another modifying command starts, AI4J now reconciles an interrupted v1 operation automatically in three exact cases: the recorded final state is already fully committed, the target exactly matches the recorded final state but the state/history commit is incomplete, or both state and target still exactly match the recorded pre-operation state. It rechecks state and target immediately before repair. Mixed state, drift, unsupported journals, history purge interruptions, and target state that cannot be observed exactly remain fail-closed as `recovery_required`.

When recovery is required:

1. Stop running modifying AI4J commands.
2. Run `ai4j status --json` and retain its output.
3. Inspect the reported AI4J-owned and Claude state before changing anything manually.
4. Do not delete the operation marker merely to bypass the protection.

Retained history is separate from the crash journal. Rollback is available only when current checksums and native state still match the selected point; it does not overwrite concurrent drift automatically.

## Common problems

### `unsupported_host`

AI4J lifecycle commands require Apple Silicon macOS or a supported Windows x64 host. Current live Windows evidence covers Windows Server 2025 x64. Linux, Intel macOS, Windows on ARM, and Rosetta are not supported hosts.

### Git or Claude Code is unavailable

Confirm both commands start from the same terminal:

```sh
git --version
claude --version
```

See [COMPATIBILITY.md](COMPATIBILITY.md) for the qualified versions.

### `approval_required`

Interactive commands ask before mutation. JSON and non-interactive commands require `--yes`.

### `expected_commit_mismatch`

The source no longer resolves to the commit you reviewed. Run the corresponding command again with `--dry-run`, then review the new exact commit before retrying.

### Drift or ownership conflict

AI4J found a missing or changed owned file, or unexpected Claude state. It will not overwrite or remove that resource. Review `ai4j status` and preserve any user edits before deciding how to reconcile the conflict.

### `recovery_required`

A previous modifying operation may have stopped after mutation began and did not match one of the exact automatic-recovery cases. Follow the recovery steps above; do not force another modification.

### `insufficient_disk_space` or `disk_capacity_unavailable`

Free space on the filesystem named by the command, then retry. If capacity cannot be read, verify that the destination and its nearest existing parent are accessible local filesystem paths.

## Current lifecycle limits

The following are intentionally deferred:

- Automated Codex installation and lifecycle management; Codex build output and native interactive handoff are supported
- Linux, Intel macOS, Windows on ARM, and Rosetta
- OAuth and stored credentials
- Offline GitHub operation and persistent source caches
- Automatic crash compensation and general repair
- Release signing, Apple notarization, Windows Authenticode, SBOMs, and provenance attestations; v1 artifacts are explicitly unsigned and checksum-verifiable

Unsupported target, host, scope, or lifecycle combinations fail explicitly with `unsupported_capability` and exit code `3`; parsing a command does not imply that the selected combination is supported.
