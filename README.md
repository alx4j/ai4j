# AI4J

[![CI](https://github.com/alx4j/ai4j/actions/workflows/ci.yml/badge.svg)](https://github.com/alx4j/ai4j/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/alx4j/ai4j)](https://github.com/alx4j/ai4j/releases/latest)

AI4J packages reusable skills, agents, instructions, scripts, and MCP settings
for Claude Code and Codex.

Use it to manage toolkits in Claude Code or to build native packages for
Codex. The repository includes a small default toolkit, so you can try both
workflows without creating your own toolkit first.

Examples use `ai4j` on macOS. Use `ai4j.exe` on Windows.

## Supported workflows

| Workflow | Apple Silicon macOS | Windows x64 |
|---|---|---|
| Build Claude packages | Supported | Supported |
| Install and manage Claude toolkits | Supported | Supported |
| Build Codex packages | Supported | Supported |
| Install Codex packages | Interactive Codex handoff | Interactive Codex handoff |

AI4J manages Claude installations through Claude's supported plugin commands.
Codex installation remains an interactive Codex step after AI4J builds the
package. Other host profiles are not currently supported.

You need Git and the AI clients named by the workflow. Run AI4J as your normal
user; it does not require `sudo` or an administrator terminal.

## Install AI4J

### macOS

Install AI4J with Homebrew:

```sh
brew install alx4j/tap/ai4j
ai4j version
```

### Windows

Download `ai4j.exe` and `ai4j.exe.sha256` from the same
[AI4J release](https://github.com/alx4j/ai4j/releases/latest). Open PowerShell
in the download directory and verify the checksum:

```powershell
$Expected = ((Get-Content .\ai4j.exe.sha256 -Raw) -split '\s+')[0].ToLowerInvariant()
$Actual = (Get-FileHash .\ai4j.exe -Algorithm SHA256).Hash.ToLowerInvariant()
if ($Actual -ne $Expected) { throw 'AI4J checksum mismatch' }
```

Install the executable for your Windows user:

```powershell
$InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\AI4J'
New-Item -ItemType Directory -Force $InstallDir | Out-Null
Copy-Item .\ai4j.exe $InstallDir -Force

$UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($InstallDir -notin ($UserPath -split ';')) {
    $UpdatedPath = @($UserPath, $InstallDir) | Where-Object { $_ }
    [Environment]::SetEnvironmentVariable('Path', ($UpdatedPath -join ';'), 'User')
}

$env:Path += ";$InstallDir"
ai4j.exe version
```

New PowerShell windows will also find `ai4j.exe` on `PATH`.

## Install the default toolkit in Claude on macOS

Start Claude Code once so that its configuration directory exists. Then
validate the default toolkit without changing Claude:

```sh
ai4j validate --target claude
```

Preview the installation:

```sh
ai4j install --dry-run --target claude --scope user --bundle default
```

The preview shows the exact source commit and everything that will become
active. Copy the full commit hash, then install the reviewed content:

```sh
ai4j install --target claude --scope user --bundle default \
  --expected-commit <full-commit-hash>
```

AI4J shows the plan again and asks for confirmation. After installation, use
the returned installation ID to check the result:

```sh
ai4j list
ai4j status --installation <installation-id>
```

Start a new Claude Code session before using the installed content.

## Build the default plugin for Codex on Windows

The default bundle includes a Claude-backed MCP server, so both Claude Code and
Codex are part of this workflow. Make sure `claude` is on `PATH`, then build the
first-party Codex package:

```powershell
ai4j.exe build --target codex --host windows-amd64 `
    --output codex-build --bundle default
```

The build validates the package and writes it to `codex-build\plugin`. The
output directory must not already exist.

AI4J does not install Codex plugins automatically. Complete the handoff in
Codex:

1. Ask Codex:
   `Use $plugin-creator to add C:\full\path\to\codex-build\plugin to my personal marketplace.`
2. Refresh Codex.
3. Open the Plugins tab in the desktop app, or run `/plugins` in Codex CLI.
4. Install `ai4j-default` and start a new session.

You are done when Codex shows `ai4j-default` as installed and the
`repository-review` skill is available.

## Manage a Claude installation

Use the installation ID returned by `install` or shown by `list`.

| Task | Preview | Apply |
|---|---|---|
| Update | `ai4j update <id> --dry-run` | `ai4j update <id>` |
| Change selection | `ai4j sync <id> --dry-run --bundle <bundle>` | `ai4j sync <id> --bundle <bundle>` |
| Roll back | `ai4j rollback <id> --dry-run` | `ai4j rollback <id>` |
| Remove | `ai4j uninstall <id> --dry-run` | `ai4j uninstall <id>` |

Apply commands show their plan and ask for confirmation. Add `--yes` only for
an already-reviewed non-interactive operation. When applying a Git-backed
install or update, pass the commit from the preview as
`--expected-commit <commit>` to ensure the source has not changed.

Read-only inspection commands include:

```sh
ai4j list
ai4j status --installation <id>
ai4j status --installation <id> --check-updates
ai4j history <id>
ai4j doctor <id>
```

Add `--json` when you need machine-readable output.

## Create your own toolkit

Create a new toolkit by naming its target explicitly:

```sh
ai4j init --target claude --output my-claude-toolkit
ai4j init --target codex --output my-codex-toolkit --examples
ai4j init --target claude --target codex --output my-cross-target-toolkit
```

`--examples` adds a small working skill and bundle. AI4J creates
`toolkit.json` and the target-native package structure; it does not install or
execute the generated content.

Local source commands require the toolkit directory to be the root of a Git
worktree. Commit the generated files before validating them:

```sh
git -C my-claude-toolkit init
git -C my-claude-toolkit add .
git -C my-claude-toolkit commit -m "Create toolkit"
```

Then validate the local Claude toolkit:

```sh
ai4j validate --source ./my-claude-toolkit --target claude
```

Build a local Codex toolkit on Windows:

```powershell
ai4j.exe build --source .\my-codex-toolkit --target codex `
    --host windows-amd64 --output my-codex-build --all
```

Choose content for `build`, `install`, or `sync` with either:

- `--all` by itself for the complete toolkit
- One or more `--bundle <id>` and/or `--asset <id>` options

New `validate`, `build`, and `install` commands use the first-party repository
by default. For another source, use `--repo owner/repository` with an optional
`--ref <branch|tag|commit>`, or `--source <path>` for an explicit local
checkout. Update and sync start from the installation's recorded source.
