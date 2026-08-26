# AI4J

[![CI](https://github.com/alx4j/ai4j/actions/workflows/ci.yml/badge.svg)](https://github.com/alx4j/ai4j/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/alx4j/ai4j)](https://github.com/alx4j/ai4j/releases/latest)

AI4J installs reusable AI toolkits in Claude Code and builds them as plugins
for Codex. Toolkits live in Git, so their contents can be reviewed, versioned,
and shared like source code.

This repository includes a toolkit with a `repository-review` skill. The
examples below use that toolkit, so you can try AI4J without creating anything
first.

## Start here

AI4J's two primary workflows are:

- **Claude Code on Apple silicon macOS:** AI4J installs the toolkit and manages
  updates, health checks, rollbacks, and removal.
- **Codex on 64-bit Windows:** AI4J builds a Codex plugin, then Codex installs
  it through its normal plugin interface.

The Claude workflow is tested end to end with Claude Code 2.1.211 on macOS 15.
The Codex build and handoff are tested with Codex CLI 0.149.1 on Windows Server
2025; installing the plugin in Codex remains a manual step.

## Install AI4J

### macOS

Before you begin, install Homebrew.

1. Install AI4J:

   ```sh
   brew install alx4j/tap/ai4j
   ```

2. Verify the installation:

   ```sh
   ai4j version
   ```

A successful check prints the AI4J version and its build target, for example
`AI4J 1.0.1 (darwin/arm64)`.

### Windows

You do not need an administrator PowerShell window.

1. Download `ai4j.exe` and `ai4j.exe.sha256` from the same
   [AI4J release](https://github.com/alx4j/ai4j/releases/latest).

2. Open PowerShell in the download directory and verify the checksum:

   ```powershell
   $Expected = ((Get-Content .\ai4j.exe.sha256 -Raw) -split '\s+')[0]
   $Actual = (Get-FileHash .\ai4j.exe -Algorithm SHA256).Hash
   if ($Actual.ToLowerInvariant() -ne $Expected.ToLowerInvariant()) {
       throw 'AI4J checksum mismatch'
   }
   ```

3. Install AI4J for your Windows user and add it to `PATH`:

   ```powershell
   $Ai4jInstallDir = Join-Path $env:LOCALAPPDATA 'Programs\AI4J'
   New-Item -ItemType Directory -Force $Ai4jInstallDir | Out-Null
   Copy-Item .\ai4j.exe $Ai4jInstallDir -Force

   $UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
   if ($Ai4jInstallDir -notin ($UserPath -split ';')) {
       $NewPath = @($UserPath, $Ai4jInstallDir) | Where-Object { $_ }
       [Environment]::SetEnvironmentVariable('Path', ($NewPath -join ';'), 'User')
   }
   ```

4. Open a new PowerShell window and verify the installation:

   ```powershell
   ai4j.exe version
   ```

A successful check prints the AI4J version and its build target, for example
`AI4J 1.0.1 (windows/amd64)`.

## Install the toolkit in Claude Code

These steps show the primary macOS workflow.

Before you begin, make sure Git and Claude Code are on `PATH`:

```sh
git --version
claude --version
```

1. Start Claude Code once. This creates the configuration directory that AI4J
   needs.

2. Start the installation:

   ```sh
   ai4j install --target claude --scope user --bundle default
   ```

3. Read the plan shown by AI4J. It names the exact Git commit and every item
   that will become active. Confirm the prompt to continue.

4. Copy the installation ID shown after the installation. In the commands
   below, `<INSTALLATION_ID>` means that value.

5. Check the installation:

   ```sh
   ai4j status <INSTALLATION_ID>
   ```

You are ready when the command succeeds, shows the Claude plugin as
registered, installed, and enabled, reports no drift or pending recovery, and
checks whether the toolkit source has an update.

Start a new Claude Code session, then ask:

```text
Use the repository-review skill to review my current changes.
```

To inspect a plan without installing anything, add `--dry-run` to the install
command.

## Install the toolkit in Codex

These steps show the primary Windows workflow. Install Codex Desktop first.
The included plugin also contains a Claude-backed MCP server, so Git, Claude
Code, and AI4J must be on `PATH`.

1. Start Claude Code once so it creates its configuration directory. AI4J
   needs that directory even when it is building for Codex.

2. Open PowerShell and check the prerequisites:

   ```powershell
   git --version
   claude --version
   ai4j.exe version
   ```

3. Build the plugin into a new directory:

   ```powershell
   ai4j.exe build --target codex --host windows-amd64 `
       --output codex-build --bundle default
   ```

   The `codex-build` directory must not already exist.

4. Verify the package:

   ```powershell
   Test-Path .\codex-build\plugin\.codex-plugin\plugin.json
   ```

   PowerShell should print `True`.

5. Open Codex and ask:

   ```text
   Use $plugin-creator to add C:\full\path\to\codex-build\plugin to my personal marketplace.
   ```

   Replace the example path with the absolute path to your generated
   `codex-build\plugin` directory.

6. Refresh the plugin list in Codex Desktop.

7. Open the Plugins tab and install `ai4j-default`.

8. Start a new Codex session.

The plugin is ready when Codex shows `ai4j-default` as installed and the
`repository-review` skill is available.

AI4J builds the plugin but does not install or manage it inside Codex. Use
Codex to update or remove it. If you use Codex CLI instead of Desktop, manage
the plugin through `/plugins`.

## Manage a Claude installation

An **installation** is one AI4J-managed placement of a toolkit. Its record ties
together the target and scope, exact source revision, selected toolkit content,
and files owned by AI4J. The installation ID lets AI4J distinguish multiple
user or project installations and safely direct status checks, updates,
rollbacks, and removal to the right one.

`<INSTALLATION_ID>` is the value returned by `install` or shown by `ai4j list`.
The `status` command always checks both the current installation health and its
source for available updates.

| Task | Command |
|---|---|
| List installations | `ai4j list` |
| Check health and updates | `ai4j status <INSTALLATION_ID>` |
| Update the toolkit | `ai4j update <INSTALLATION_ID>` |
| Select a different bundle | `ai4j sync <INSTALLATION_ID> --bundle default` |
| Inspect history | `ai4j history <INSTALLATION_ID>` |
| Diagnose a problem | `ai4j doctor <INSTALLATION_ID>` |
| Roll back the last change | `ai4j rollback <INSTALLATION_ID>` |
| Remove the toolkit | `ai4j uninstall <INSTALLATION_ID>` |

Commands that change an installation show their plan and ask for confirmation.
Add `--dry-run` to stop after the plan. Use `--yes` only for an
already-reviewed, non-interactive operation.

After an update, selection change, rollback, or removal, run `status` again to
confirm the result. Add `--json` when you need structured output.

On Windows, replace `ai4j` with `ai4j.exe`.

## Create a toolkit

The output directory must not already exist.

1. Create a toolkit for both supported clients:

   ```sh
   ai4j init --target claude --target codex --output my-toolkit --examples
   ```

2. Put the generated toolkit under Git:

   ```sh
   cd my-toolkit
   git init
   git add .
   git commit -m "Create toolkit"
   ```

3. Validate the Claude package:

   ```sh
   ai4j validate --source . --target claude
   ```

4. On Windows, build the Codex package:

   ```powershell
   ai4j.exe build --source . --target codex --host windows-amd64 `
       --output ..\my-toolkit-codex --all
   ```

The generated toolkit is now ready for local development or publication in a
GitHub repository.

## Use another toolkit source

To work from GitHub, replace `OWNER/REPOSITORY` with the toolkit repository:

```sh
ai4j validate --repo OWNER/REPOSITORY --ref main --target claude
ai4j install --repo OWNER/REPOSITORY --ref main \
  --target claude --scope user --all
```

`--repo` accepts GitHub repositories. Use it with Claude installation or with
either target's build command; install a built Codex plugin from Codex.

Use `--source PATH` when `PATH` is the root of a local Git checkout. Local
sources cannot use the `project-shared` scope. For a project installation, use
`--scope project-local` or `--scope project-shared` together with
`--project PATH`. The `project-local` scope keeps generated rules out of Git;
the `project-shared` scope writes the toolkit declaration to the project's
Claude settings.

## Update or remove AI4J

Removing the AI4J executable does not remove a Claude toolkit. If you want to
remove both, run `ai4j uninstall <INSTALLATION_ID>` before removing AI4J.

### macOS

Update AI4J, then verify the new executable:

```sh
brew upgrade ai4j
ai4j version
```

Remove AI4J:

```sh
brew uninstall ai4j
```

### Windows

To update AI4J, download and verify a new release, replace the existing
`ai4j.exe`, open a new PowerShell window, and run `ai4j.exe version`.

To remove AI4J, delete `%LOCALAPPDATA%\Programs\AI4J\ai4j.exe` and remove that
directory from your user `PATH`.

## Troubleshooting

| Error | What to do |
|---|---|
| `claude_config_unavailable` | Start Claude Code once. If you set `CLAUDE_CONFIG_DIR`, make sure it points to an existing directory inside your user profile. |
| `git_not_found` or `git_unusable` | Install Git and make sure `git` is on `PATH`. |
| `claude_not_found`, `claude_unusable`, or `missing_required_runtime` | Install the supported Claude Code version and make sure `claude` is on `PATH`. The default Codex plugin also requires it. |
| `output_occupied` | Choose a new build directory. AI4J does not overwrite an existing build. |
| `unsupported_capability` from `install --target codex` | Build the Codex plugin with AI4J, then install it through Codex Plugins. |

If an installed Claude toolkit is unhealthy, run:

```sh
ai4j doctor <INSTALLATION_ID>
```

The command lists each check with an `ok`, `warning`, or `error` status and a
short summary.
