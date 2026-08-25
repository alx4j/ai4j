# AI4J

[![CI](https://github.com/alx4j/ai4j/actions/workflows/ci.yml/badge.svg)](https://github.com/alx4j/ai4j/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/alx4j/ai4j)](https://github.com/alx4j/ai4j/releases/latest)

AI4J packages reusable skills, agents, instructions, scripts, and MCP settings
for Claude Code and Codex.

Use it to install and manage a toolkit in Claude Code or to build a native
plugin for Codex. AI4J includes a small default toolkit for repository reviews,
so you can try the complete workflow without creating a toolkit first.

## Supported workflows

The primary workflows are Claude Code on Apple Silicon macOS and Codex on
Windows x64.

| Host | Claude Code | Codex |
|---|---|---|
| Apple Silicon macOS | Build, install, update, and remove | Build a native plugin |
| Windows x64 | Build, install, update, and remove | Build a native plugin |

Codex plugins are installed through Codex after AI4J builds them. Other
operating systems and architectures are not currently supported. See
[Compatibility](COMPATIBILITY.md) for tested versions.

Before you start, install Git and the AI client you intend to use. Run AI4J as
your normal user; it does not require `sudo` or an administrator terminal.

## Install AI4J

### macOS

Install AI4J with Homebrew:

```sh
brew install alx4j/tap/ai4j
ai4j version
```

### Windows

Download `ai4j.exe` and `ai4j.exe.sha256` from the same
[AI4J release](https://github.com/alx4j/ai4j/releases/latest). Open PowerShell in
the download directory and verify the checksum:

```powershell
$Expected = ((Get-Content .\ai4j.exe.sha256 -Raw) -split '\s+')[0].ToLowerInvariant()
$Actual = (Get-FileHash .\ai4j.exe -Algorithm SHA256).Hash.ToLowerInvariant()
if ($Actual -ne $Expected) { throw 'AI4J checksum mismatch' }
```

Install it for your Windows user and add it to `PATH`:

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

## Claude on macOS: install the default toolkit

This workflow installs the default toolkit for your Claude Code user account.

First, validate the toolkit without changing your Claude installation:

```sh
ai4j validate --target claude
```

Next, preview the installation:

```sh
ai4j plan install --target claude --scope user --bundle default
```

The plan shows the source commit and the content that will become active. Copy
the full commit hash from the plan, then run:

```sh
ai4j install --target claude --scope user --bundle default \
  --expected-commit <full-commit-hash> --yes
```

Check the installed toolkit:

```sh
ai4j list
ai4j status
```

You are done when `status` reports the installation as healthy. Start a new
Claude Code session before using the installed repository-review content.

## Codex on Windows: build and install the default plugin

Validate the default toolkit, then build its Codex plugin:

```powershell
ai4j.exe validate --target codex
ai4j.exe build --target codex --host windows-amd64 `
    --output dist\codex --bundle default
```

The build creates the plugin in `dist\codex\plugin`. The output directory must
not already exist.

AI4J does not install the plugin directly. Complete the installation in Codex:

1. Open Codex and ask it: `Use $plugin-creator to add C:\full\path\to\dist\codex\plugin to my personal marketplace.`
2. Refresh Codex.
3. Open the Plugins tab in the desktop app, or run `/plugins` in Codex CLI.
4. Install `ai4j-default` and start a new session.

You are done when Codex shows `ai4j-default` as installed and the
`repository-review` skill is available.

## Create your own toolkit

Create a toolkit in a new directory:

```sh
ai4j init --target claude --output my-claude-toolkit
ai4j init --target codex --output my-codex-toolkit --examples
ai4j init --target claude --target codex --output my-cross-target-toolkit
```

The target is always explicit. Add `--examples` when you want a working sample
skill and bundle. AI4J creates `toolkit.json` and the required target-native
package structure; it does not install the generated content.

To use a toolkit from another GitHub repository, provide its repository and an
optional branch, tag, or full commit:

```sh
ai4j validate --repo owner/repository
ai4j validate --repo owner/repository --ref v1.0.0
```

## Manage a Claude installation

Use the installation ID returned by `install` or shown by `list`.

| Task | Preview | Apply |
|---|---|---|
| Update | `ai4j plan update --installation <id>` | `ai4j update --installation <id> --expected-commit <commit> --yes` |
| Change bundle | `ai4j plan sync --installation <id> --bundle <bundle>` | `ai4j sync --installation <id> --bundle <bundle> --yes` |
| Roll back | `ai4j plan rollback --installation <id>` | `ai4j rollback --installation <id> --yes` |
| Remove | `ai4j plan uninstall --installation <id>` | `ai4j uninstall --installation <id> --yes` |

Read-only inspection commands include:

```sh
ai4j list
ai4j status --installation <id>
ai4j status --installation <id> --check-updates
ai4j history --installation <id>
ai4j doctor --installation <id>
```

Add `--json` to any command for machine-readable output.

For project-scoped installations, individual asset selection, local development
sources, recovery, and automation, see the [full user guide](USER_GUIDE.md).
