Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Get-RequiredEnvironmentValue {
    param([Parameter(Mandatory)][string]$Name)

    $value = [Environment]::GetEnvironmentVariable($Name)
    if ([string]::IsNullOrWhiteSpace($value)) {
        throw "$Name is required"
    }
    return $value
}

function Write-Evidence {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$Text
    )

    $path = Join-Path $script:EvidenceRoot $Name
    [IO.File]::WriteAllText($path, $Text + [Environment]::NewLine)
    Write-Host $Text
}

function Invoke-AI4J {
    param(
        [Parameter(Mandatory)][string]$EvidenceName,
        [Parameter(Mandatory)][string[]]$Arguments
    )

    $lines = & $script:AI4J @Arguments --json 2>&1
    $exitCode = $LASTEXITCODE
    $text = ($lines -join [Environment]::NewLine)
    Write-Evidence -Name $EvidenceName -Text $text
    if ($exitCode -ne 0) {
        throw "ai4j command failed with exit code $exitCode"
    }
    $document = $text | ConvertFrom-Json -Depth 100
    if ($document.status -ne 'ok' -or $document.exitCode -ne 0 -or $document.errors.Count -ne 0) {
        throw "ai4j command returned an unsuccessful document"
    }
    return $document
}

function Assert-NativeSuccess {
    param([Parameter(Mandatory)][string]$Description)

    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE"
    }
}

function Enable-GitHubCredential {
    param(
        [Parameter(Mandatory)][string]$Token,
        [Parameter(Mandatory)][string]$WorkRoot
    )

    $tokenPath = Join-Path $WorkRoot 'github-token'
    $helperPath = Join-Path $WorkRoot 'git-credential-ai4j.ps1'
    $configPath = Join-Path $WorkRoot 'gitconfig'
    [IO.File]::WriteAllText($tokenPath, $Token)
    $escapedTokenPath = $tokenPath.Replace("'", "''")
    $helper = @"
param([string]`$Operation)
if (`$Operation -eq 'get') {
    `$token = [IO.File]::ReadAllText('$escapedTokenPath').Trim()
    [Console]::Out.WriteLine('username=x-access-token')
    [Console]::Out.WriteLine("password=`$token")
}
"@
    [IO.File]::WriteAllText($helperPath, $helper)
    $helperCommand = "!pwsh -NoProfile -NonInteractive -File `"$($helperPath.Replace('\', '/'))`""
    & git config --file $configPath credential.helper ''
    Assert-NativeSuccess 'reset inherited Git credential helpers'
    & git config --file $configPath --add credential.helper $helperCommand
    Assert-NativeSuccess 'configure GitHub credential helper'
    $env:GIT_CONFIG_GLOBAL = $configPath
    $probe = "protocol=https`nhost=github.com`n`n" | git credential fill
    Assert-NativeSuccess 'probe GitHub credential helper'
    if (-not ($probe -contains 'username=x-access-token') -or -not ($probe | Where-Object { $_ -like 'password=?*' })) {
        throw 'GitHub credential helper did not return a credential'
    }
}

$qualificationRef = Get-RequiredEnvironmentValue 'AI4J_QUALIFICATION_REF'
$qualificationSourceRef = Get-RequiredEnvironmentValue 'AI4J_QUALIFICATION_SOURCE_REF'
$script:EvidenceRoot = [IO.Path]::GetFullPath((Get-RequiredEnvironmentValue 'AI4J_QUALIFICATION_EVIDENCE'))
$codexVersion = Get-RequiredEnvironmentValue 'AI4J_CODEX_VERSION'
$githubToken = Get-RequiredEnvironmentValue 'AI4J_QUALIFICATION_GITHUB_TOKEN'
$runnerTemp = [IO.Path]::GetFullPath((Get-RequiredEnvironmentValue 'RUNNER_TEMP'))
$repoRoot = (& git -C (Join-Path $PSScriptRoot '..') rev-parse --show-toplevel).Trim()
Assert-NativeSuccess 'resolve repository root'
Set-Location $repoRoot

$workRoot = [IO.Path]::GetFullPath((Join-Path $runnerTemp "ai4j-codex-qualification-$([guid]::NewGuid().ToString('N'))"))
if (-not $workRoot.StartsWith($runnerTemp.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'qualification workspace escaped the runner temporary directory'
}
$releaseRoot = Join-Path $workRoot 'release'
$script:AI4J = Join-Path $releaseRoot 'ai4j.exe'
$buildRoot = Join-Path $workRoot 'codex-package'
New-Item -ItemType Directory -Force -Path $script:EvidenceRoot, $releaseRoot | Out-Null
Enable-GitHubCredential -Token $githubToken -WorkRoot $workRoot
$githubToken = $null
Remove-Item Env:AI4J_QUALIFICATION_GITHUB_TOKEN

try {
    if ($env:RUNNER_OS -ne 'Windows' -or $env:RUNNER_ARCH -ne 'X64') {
        throw "unexpected runner: $($env:RUNNER_OS)/$($env:RUNNER_ARCH)"
    }
    if ((go env GOVERSION) -ne 'go1.26.6' -or (go env GOOS) -ne 'windows' -or (go env GOARCH) -ne 'amd64' -or (go env CGO_ENABLED) -ne '0') {
        throw 'unexpected Go build environment'
    }

    $os = Get-CimInstance Win32_OperatingSystem
    $codexOutput = (& codex --version 2>&1) -join [Environment]::NewLine
    Assert-NativeSuccess 'Codex version probe'
    Write-Evidence -Name 'codex-version.txt' -Text $codexOutput
    if ($codexOutput -notmatch [regex]::Escape($codexVersion)) {
        throw "unexpected Codex CLI version: $codexOutput"
    }
    $codexHelp = (& codex --help 2>&1) -join [Environment]::NewLine
    Assert-NativeSuccess 'Codex help probe'
    Write-Evidence -Name 'codex-help.txt' -Text $codexHelp

    $environment = @(
        "runner=$($env:RUNNER_OS)/$($env:RUNNER_ARCH)"
        "windows_caption=$($os.Caption)"
        "windows_version=$($os.Version)"
        "windows_build=$($os.BuildNumber)"
        "git=$(& git --version)"
        "go=$(go env GOVERSION)"
        "node=$(& node --version)"
        "npm=$(& npm --version)"
        "codex=$codexOutput"
        "source_ref=$qualificationSourceRef"
        "commit=$qualificationRef"
    ) -join [Environment]::NewLine
    Write-Evidence -Name 'environment.txt' -Text $environment

    & go build -mod=readonly -trimpath -buildvcs=true -o $script:AI4J ./cmd/ai4j
    Assert-NativeSuccess 'Windows executable build'
    $checksum = (Get-FileHash -Algorithm SHA256 -LiteralPath $script:AI4J).Hash.ToLowerInvariant()
    Write-Evidence -Name 'ai4j.exe.sha256' -Text "$checksum  ai4j.exe"

    $version = Invoke-AI4J -EvidenceName 'version.json' -Arguments @('version')
    if ($version.data.executable -ne 'ai4j.exe' -or $version.data.target.os -ne 'windows' -or $version.data.target.arch -ne 'amd64') {
        throw 'ai4j.exe reported an unexpected build identity'
    }

    $build = Invoke-AI4J -EvidenceName 'build.json' -Arguments @(
        'build', '--repo', 'alx4j/ai4j', '--ref', $qualificationSourceRef,
        '--target', 'codex', '--host', 'windows-amd64', '--output', $buildRoot, '--all'
    )
    if ($build.data.target -ne 'codex' -or $build.data.host -ne 'windows-amd64' -or -not $build.data.reproducible -or -not $build.data.validation.valid) {
        throw 'Codex build report did not confirm a reproducible Windows package'
    }
    if ($build.data.source.commit.oid -ne $qualificationRef) {
        throw 'Codex package was not built from the workflow commit'
    }

    foreach ($relativePath in @(
        'ai4j-build.json',
        'configuration\AGENTS.md',
        'configuration\.codex\agents\repository-reviewer.toml',
        'plugin\.codex-plugin\plugin.json',
        'plugin\.mcp.json',
        'plugin\skills\repository-review\SKILL.md',
        'plugin\skills\repository-review\scripts\check-diff.ps1'
    )) {
        if (-not (Test-Path -LiteralPath (Join-Path $buildRoot $relativePath) -PathType Leaf)) {
            throw "Codex package is missing $relativePath"
        }
    }

    $manifest = Get-Content -Raw -LiteralPath (Join-Path $buildRoot 'ai4j-build.json') | ConvertFrom-Json -Depth 100
    if ($manifest.target -ne 'codex' -or $manifest.host -ne 'windows-amd64' -or -not $manifest.reproducible) {
        throw 'Codex package manifest has an unexpected target profile'
    }
    foreach ($artifact in $manifest.artifacts) {
        $artifactPath = Join-Path $buildRoot ($artifact.path -replace '/', [IO.Path]::DirectorySeparatorChar)
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $artifactPath).Hash.ToLowerInvariant()
        if ($actual -ne $artifact.sha256) {
            throw "Codex package checksum mismatch for $($artifact.path)"
        }
    }

    $stateRoot = Join-Path $env:LOCALAPPDATA 'AI4J'
    $stateExisted = Test-Path -LiteralPath $stateRoot
    $handoffLines = & $script:AI4J install --repo alx4j/ai4j --ref $qualificationSourceRef --target codex --scope user --all --yes --json 2>&1
    $handoffExit = $LASTEXITCODE
    $handoffText = ($handoffLines -join [Environment]::NewLine)
    Write-Evidence -Name 'native-handoff.json' -Text $handoffText
    $handoff = $handoffText | ConvertFrom-Json -Depth 100
    if ($handoffExit -eq 0 -or $handoff.status -ne 'error' -or $handoff.data -ne $null -or
        $handoff.errors.Count -ne 1 -or $handoff.errors[0].code -ne 'unsupported_capability' -or
        $handoff.errors[0].message -notmatch '/plugins') {
        throw 'Codex lifecycle did not stop at the documented native handoff'
    }
    if ((Test-Path -LiteralPath $stateRoot) -ne $stateExisted) {
        throw 'Codex native handoff changed AI4J state'
    }

    Write-Evidence -Name 'qualification-summary.txt' -Text "PASS: Codex CLI $codexVersion package handoff on Windows AMD64 at $qualificationRef"
    $global:LASTEXITCODE = 0
}
finally {
    if (Test-Path -LiteralPath $workRoot) {
        Remove-Item -LiteralPath $workRoot -Recurse -Force
    }
}
