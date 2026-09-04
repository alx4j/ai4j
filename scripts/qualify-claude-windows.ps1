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
        [Parameter(Mandatory)][AllowEmptyString()][string]$Text
    )

    $path = Join-Path $script:EvidenceRoot $Name
    [IO.File]::WriteAllText($path, $Text + [Environment]::NewLine)
    Write-Host $Text
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
    if ($document.status -notin @('ok', 'no_change') -or $document.exitCode -ne 0 -or $document.errors.Count -ne 0) {
        throw 'ai4j command returned an unsuccessful document'
    }
    return $document
}

function Invoke-ClaudeJSON {
    param(
        [Parameter(Mandatory)][string]$EvidenceName,
        [Parameter(Mandatory)][string[]]$Arguments,
        [Parameter(Mandatory)][string]$WorkingDirectory
    )

    Push-Location $WorkingDirectory
    try {
        $lines = & claude @Arguments 2>&1
        $exitCode = $LASTEXITCODE
    }
    finally {
        Pop-Location
    }
    $text = ($lines -join [Environment]::NewLine)
    Write-Evidence -Name $EvidenceName -Text $text
    if ($exitCode -ne 0) {
        throw "Claude command failed with exit code $exitCode"
    }
    $document = $text | ConvertFrom-Json -Depth 100 -NoEnumerate
    return ,$document
}

function Invoke-ClaudeText {
    param(
        [Parameter(Mandatory)][string]$EvidenceName,
        [Parameter(Mandatory)][string[]]$Arguments,
        [Parameter(Mandatory)][string]$WorkingDirectory
    )

    Push-Location $WorkingDirectory
    try {
        $lines = & claude @Arguments 2>&1
        $exitCode = $LASTEXITCODE
    }
    finally {
        Pop-Location
    }
    $text = ($lines -join [Environment]::NewLine)
    Write-Evidence -Name $EvidenceName -Text $text
    if ($exitCode -ne 0) {
        throw "Claude command failed with exit code $exitCode"
    }
}

function Assert-JSONContainsString {
    param(
        [Parameter(Mandatory)]$Document,
        [Parameter(Mandatory)][string]$Expected
    )

    $json = ConvertTo-Json -InputObject $Document -Depth 100 -Compress
    if (-not $json.Contains($Expected, [StringComparison]::Ordinal)) {
        throw "Claude output did not contain $Expected"
    }
}

function Assert-DefaultBundleStatus {
    param([Parameter(Mandatory)]$Status)

    if ([string]::Join(',', @($Status.data.installation.nativePluginIds)) -ne 'ai4j-review,ai4j-reviewer,ai4j-tools' -or
        $Status.data.summary.requestedBundle -ne 'default' -or
        [string]::Join(',', @($Status.data.summary.resolvedBundles)) -ne 'default,review,tools' -or
        [string]::Join(',', @($Status.data.summary.packages)) -ne 'ai4j-review,ai4j-reviewer,ai4j-tools') {
        throw 'status did not report the flattened default bundle'
    }
}

function Invoke-ProjectJourney {
    param([Parameter(Mandatory)][ValidateSet('project-local', 'project-shared')][string]$Scope)

    $projectRoot = Join-Path $script:WorkRoot "project-$Scope"
    $prefix = "project-$Scope"
    & git clone --quiet --no-hardlinks $script:RepoRoot $projectRoot
    Assert-NativeSuccess "clone $Scope project"

    $plan = Invoke-AI4J -EvidenceName "$prefix-plan.json" -Arguments @(
        'install', '--dry-run', '--repo', 'alx4j/ai4j', '--ref', $script:QualificationSourceRef,
        '--target', 'claude', '--scope', $Scope, '--project', $projectRoot, '--bundle', 'default'
    )
    $install = Invoke-AI4J -EvidenceName "$prefix-install.json" -Arguments @(
        'install', '--repo', 'alx4j/ai4j', '--ref', $script:QualificationSourceRef,
        '--target', 'claude', '--scope', $Scope, '--project', $projectRoot, '--bundle', 'default',
        '--expected-commit', $script:QualificationRef, '--yes'
    )

    $script:ActiveInstallation = $install.data.installationId
    $status = Invoke-AI4J -EvidenceName "$prefix-status.json" -Arguments @('status', $script:ActiveInstallation)
    if ($status.data.nativeState.registration -ne 'registered' -or $status.data.nativeState.installation -ne 'installed' -or $status.data.nativeState.enablement -ne 'enabled') {
        throw "$Scope native state is incomplete"
    }
    Assert-DefaultBundleStatus -Status $status
    $marketplaceId = ($plan.data.actions | Where-Object kind -eq 'register_marketplace' | Select-Object -First 1).resource
    $pluginList = Invoke-ClaudeJSON -EvidenceName "$prefix-plugin-list.json" -Arguments @('plugin', 'list', '--json') -WorkingDirectory $projectRoot
    foreach ($nativePluginId in @($status.data.installation.nativePluginIds)) {
        Assert-JSONContainsString -Document $pluginList -Expected "$nativePluginId@$marketplaceId"
    }
    $marketplaceList = Invoke-ClaudeJSON -EvidenceName "$prefix-marketplace-list.json" -Arguments @('plugin', 'marketplace', 'list', '--json') -WorkingDirectory $projectRoot
    Assert-JSONContainsString -Document $marketplaceList -Expected $marketplaceId

    if ($Scope -eq 'project-local') {
        $rulesFile = Get-ChildItem -LiteralPath (Join-Path $projectRoot '.claude\rules') -Filter '*.md' -File -Recurse | Select-Object -First 1
        if ($null -eq $rulesFile) {
            throw 'project-local rules file is missing'
        }
        $ignoreLines = & git -C $projectRoot check-ignore -v -- $rulesFile.FullName 2>&1
        Assert-NativeSuccess 'project-local Git exclusion check'
        Write-Evidence -Name "$prefix-git-exclusion.txt" -Text ($ignoreLines -join [Environment]::NewLine)
    }
    else {
        $settingsPath = Join-Path $projectRoot '.claude\settings.json'
        $settingsText = Get-Content -Raw -LiteralPath $settingsPath
        Write-Evidence -Name "$prefix-settings.json" -Text $settingsText
        $settings = $settingsText | ConvertFrom-Json -Depth 100
        $marketplace = $settings.extraKnownMarketplaces.PSObject.Properties[$marketplaceId].Value
        $plugins = @($marketplace.source.plugins)
        $expectedPluginPaths = @{
            'ai4j-review' = 'plugins/ai4j-review'
            'ai4j-reviewer' = 'plugins/ai4j-reviewer-claude'
            'ai4j-tools' = 'plugins/ai4j-tools'
        }
        if ($marketplace.source.source -ne 'settings' -or
            [string]::Join(',', @($plugins.name)) -ne 'ai4j-review,ai4j-reviewer,ai4j-tools' -or
            @($plugins | Where-Object {
                    $_.source.source -ne 'git-subdir' -or
                    $_.source.sha -ne $script:QualificationRef -or
                    $_.source.path -ne $expectedPluginPaths[$_.name]
                }).Count -ne 0) {
            throw 'project-shared settings do not retain the exact Git source declaration'
        }
    }

    Invoke-AI4J -EvidenceName "$prefix-doctor.json" -Arguments @('doctor', $script:ActiveInstallation) | Out-Null
    Invoke-AI4J -EvidenceName "$prefix-uninstall.json" -Arguments @('uninstall', $script:ActiveInstallation, '--yes') | Out-Null
    $script:ActiveInstallation = $null
    $gitStatus = (& git -C $projectRoot status --short --untracked-files=all 2>&1) -join [Environment]::NewLine
    Assert-NativeSuccess "$Scope post-uninstall Git status"
    Write-Evidence -Name "$prefix-post-uninstall-git-status.txt" -Text $gitStatus
    if (-not [string]::IsNullOrWhiteSpace($gitStatus)) {
        throw "$Scope uninstall left project changes"
    }
}

$script:QualificationRef = Get-RequiredEnvironmentValue 'AI4J_QUALIFICATION_REF'
$script:QualificationSourceRef = Get-RequiredEnvironmentValue 'AI4J_QUALIFICATION_SOURCE_REF'
$script:EvidenceRoot = [IO.Path]::GetFullPath((Get-RequiredEnvironmentValue 'AI4J_QUALIFICATION_EVIDENCE'))
$claudeVersion = Get-RequiredEnvironmentValue 'AI4J_CLAUDE_VERSION'
$githubToken = Get-RequiredEnvironmentValue 'AI4J_QUALIFICATION_GITHUB_TOKEN'
$runnerTemp = [IO.Path]::GetFullPath((Get-RequiredEnvironmentValue 'RUNNER_TEMP'))
$script:RepoRoot = (& git -C (Join-Path $PSScriptRoot '..') rev-parse --show-toplevel).Trim()
Assert-NativeSuccess 'resolve repository root'
Set-Location $script:RepoRoot

$script:WorkRoot = [IO.Path]::GetFullPath((Join-Path $runnerTemp "ai4j-claude-qualification-$([guid]::NewGuid().ToString('N'))"))
if (-not $script:WorkRoot.StartsWith($runnerTemp.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'qualification workspace escaped the runner temporary directory'
}
$releaseRoot = Join-Path $script:WorkRoot 'release'
$script:AI4J = Join-Path $releaseRoot 'ai4j.exe'
$script:ActiveInstallation = $null
New-Item -ItemType Directory -Force -Path $script:EvidenceRoot, $releaseRoot | Out-Null
Enable-GitHubCredential -Token $githubToken -WorkRoot $script:WorkRoot
$githubToken = $null
Remove-Item Env:AI4J_QUALIFICATION_GITHUB_TOKEN

try {
    if ($env:RUNNER_OS -ne 'Windows' -or $env:RUNNER_ARCH -ne 'X64') {
        throw "unexpected runner: $($env:RUNNER_OS)/$($env:RUNNER_ARCH)"
    }
    if ((go env GOVERSION) -ne 'go1.26.6' -or (go env GOOS) -ne 'windows' -or (go env GOARCH) -ne 'amd64' -or (go env CGO_ENABLED) -ne '0') {
        throw 'unexpected Go build environment'
    }

    $claudeOutput = (& claude --version 2>&1) -join [Environment]::NewLine
    Assert-NativeSuccess 'Claude version probe'
    Write-Evidence -Name 'claude-version.txt' -Text $claudeOutput
    if ($claudeOutput -notmatch "^$([regex]::Escape($claudeVersion))([\s]|$)") {
        throw "unexpected Claude Code version: $claudeOutput"
    }
    $os = Get-CimInstance Win32_OperatingSystem
    $environment = @(
        "runner=$($env:RUNNER_OS)/$($env:RUNNER_ARCH)"
        "windows_caption=$($os.Caption)"
        "windows_version=$($os.Version)"
        "windows_build=$($os.BuildNumber)"
        "git=$(& git --version)"
        "go=$(go env GOVERSION)"
        "claude=$claudeOutput"
        "source_ref=$($script:QualificationSourceRef)"
        "commit=$($script:QualificationRef)"
    ) -join [Environment]::NewLine
    Write-Evidence -Name 'environment.txt' -Text $environment

    $lockLines = & go test -mod=readonly ./internal/host/darwin/installlock -run 'TestWindowsLockBlocksConcurrentMutationAndReleases' -count=1 2>&1
    Assert-NativeSuccess 'Windows lock tests'
    Write-Evidence -Name 'windows-lock-tests.txt' -Text ($lockLines -join [Environment]::NewLine)

    $activationPlugin = Join-Path $script:WorkRoot 'agent-activation-plugin'
    $activationConfig = Join-Path $script:WorkRoot 'agent-activation-config'
    $activationMetadata = Join-Path $activationPlugin '.claude-plugin'
    $activationAgents = Join-Path $activationPlugin 'agents'
    $activationHooks = Join-Path $activationPlugin 'hooks'
    New-Item -ItemType Directory -Force -Path $activationMetadata, $activationAgents, $activationHooks, $activationConfig | Out-Null
    [IO.File]::WriteAllText(
        (Join-Path $activationMetadata 'plugin.json'),
        "{`n  `"name`": `"agent-activation-fixture`",`n  `"version`": `"1.0.0`",`n  `"description`": `"Validates Claude main-agent activation`",`n  `"author`": {`n    `"name`": `"AI4J`"`n  }`n}`n"
    )
    [IO.File]::WriteAllText(
        (Join-Path $activationAgents 'root-orchestrator.md'),
        "---`nname: root-orchestrator`ndescription: Coordinates the requested work.`ntools: Read, Grep, Glob`n---`n`nCoordinate the requested work.`n"
    )
    [IO.File]::WriteAllText(
        (Join-Path $activationPlugin 'settings.json'),
        "{`n  `"agent`": `"root-orchestrator`"`n}`n"
    )
    [IO.File]::WriteAllText(
        (Join-Path $activationPlugin 'capture-session-start.ps1'),
        "`$payload = [Console]::In.ReadToEnd()`n[IO.File]::WriteAllText((Join-Path `$PSScriptRoot 'session-start.json'), `$payload)`n"
    )
    $activationHooksDocument = [ordered]@{
        hooks = [ordered]@{
            SessionStart = @(
                [ordered]@{
                    matcher = 'startup'
                    hooks = @(
                        [ordered]@{
                            type = 'command'
                            command = 'powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "${CLAUDE_PLUGIN_ROOT}/capture-session-start.ps1"'
                        }
                    )
                }
            )
        }
    }
    [IO.File]::WriteAllText(
        (Join-Path $activationHooks 'hooks.json'),
        (ConvertTo-Json -InputObject $activationHooksDocument -Depth 8) + [Environment]::NewLine
    )
    Invoke-ClaudeText -EvidenceName 'native-agent-activation-validate.txt' -Arguments @('plugin', 'validate', '.', '--strict') -WorkingDirectory $activationPlugin

    $previousClaudeConfig = [Environment]::GetEnvironmentVariable('CLAUDE_CONFIG_DIR', 'Process')
    try {
        $env:CLAUDE_CONFIG_DIR = $activationConfig
        Invoke-ClaudeText -EvidenceName 'native-agent-activation-load.txt' -Arguments @('--plugin-dir', $activationPlugin, '--init-only') -WorkingDirectory $activationPlugin
    }
    finally {
        if ($null -eq $previousClaudeConfig) {
            Remove-Item Env:CLAUDE_CONFIG_DIR -ErrorAction SilentlyContinue
        }
        else {
            $env:CLAUDE_CONFIG_DIR = $previousClaudeConfig
        }
    }
    $activationReceiptPath = Join-Path $activationPlugin 'session-start.json'
    if (-not (Test-Path -LiteralPath $activationReceiptPath -PathType Leaf)) {
        throw 'Claude main-agent activation did not produce a SessionStart receipt'
    }
    $activationReceiptText = Get-Content -Raw -LiteralPath $activationReceiptPath
    Write-Evidence -Name 'native-agent-activation-receipt.json' -Text ($activationReceiptText.TrimEnd())
    $activationReceipt = $activationReceiptText | ConvertFrom-Json -Depth 100
    $activationAgentType = $activationReceipt.PSObject.Properties['agent_type']
    if ($activationReceipt.hook_event_name -ne 'SessionStart' -or
        $activationReceipt.source -ne 'startup' -or
        $null -eq $activationAgentType -or
        $activationAgentType.Value -ne 'agent-activation-fixture:root-orchestrator') {
        throw 'Claude did not activate the configured plugin agent as the main agent'
    }

    foreach ($package in @('ai4j-review', 'ai4j-reviewer-claude', 'ai4j-tools')) {
        Invoke-ClaudeText -EvidenceName "native-plugin-validate-$package.txt" -Arguments @('plugin', 'validate', '.', '--strict') -WorkingDirectory (Join-Path $script:RepoRoot "plugins\$package")
    }

    & go build -mod=readonly -trimpath -buildvcs=true -o $script:AI4J ./cmd/ai4j
    Assert-NativeSuccess 'Windows executable build'
    $checksum = (Get-FileHash -Algorithm SHA256 -LiteralPath $script:AI4J).Hash.ToLowerInvariant()
    Write-Evidence -Name 'ai4j.exe.sha256' -Text "$checksum  ai4j.exe"
    $version = Invoke-AI4J -EvidenceName 'version.json' -Arguments @('version')
    if ($version.data.target.os -ne 'windows' -or $version.data.target.arch -ne 'amd64') {
        throw 'ai4j.exe reported an unexpected build identity'
    }

    $validation = Invoke-AI4J -EvidenceName 'validate.json' -Arguments @(
        'validate', '--repo', 'alx4j/ai4j', '--ref', $script:QualificationSourceRef, '--target', 'claude'
    )
    if (-not $validation.data.validation.valid) {
        throw 'Claude toolkit validation failed'
    }

    $plan = Invoke-AI4J -EvidenceName 'user-plan.json' -Arguments @(
        'install', '--dry-run', '--repo', 'alx4j/ai4j', '--ref', $script:QualificationSourceRef,
        '--target', 'claude', '--scope', 'user', '--bundle', 'default'
    )
    $install = Invoke-AI4J -EvidenceName 'user-install.json' -Arguments @(
        'install', '--repo', 'alx4j/ai4j', '--ref', $script:QualificationSourceRef,
        '--target', 'claude', '--scope', 'user', '--bundle', 'default',
        '--expected-commit', $script:QualificationRef, '--yes'
    )
    $script:ActiveInstallation = $install.data.installationId
    $status = Invoke-AI4J -EvidenceName 'user-status.json' -Arguments @('status', $script:ActiveInstallation)
    if ($status.data.nativeState.registration -ne 'registered' -or $status.data.nativeState.installation -ne 'installed' -or $status.data.nativeState.enablement -ne 'enabled') {
        throw 'user native state is incomplete'
    }
    Assert-DefaultBundleStatus -Status $status
    $marketplaceId = "ai4j-$($script:ActiveInstallation)"
    $nativePluginIds = @($status.data.installation.nativePluginIds | ForEach-Object { "$_@$marketplaceId" })
    $marketplaceList = Invoke-ClaudeJSON -EvidenceName 'user-marketplace-list.json' -Arguments @('plugin', 'marketplace', 'list', '--json') -WorkingDirectory $script:RepoRoot
    Assert-JSONContainsString -Document $marketplaceList -Expected $marketplaceId
    $pluginList = Invoke-ClaudeJSON -EvidenceName 'user-plugin-list.json' -Arguments @('plugin', 'list', '--json') -WorkingDirectory $script:RepoRoot
    foreach ($nativePluginId in $nativePluginIds) {
        Assert-JSONContainsString -Document $pluginList -Expected $nativePluginId
    }

    Invoke-ClaudeText -EvidenceName 'user-marketplace-update.txt' -Arguments @('plugin', 'marketplace', 'update', $marketplaceId) -WorkingDirectory $script:RepoRoot
    foreach ($nativePluginId in $nativePluginIds) {
        Invoke-ClaudeText -EvidenceName "user-plugin-update-$($nativePluginId.Split('@')[0]).txt" -Arguments @('plugin', 'update', $nativePluginId, '--scope', 'user') -WorkingDirectory $script:RepoRoot
    }
    Invoke-AI4J -EvidenceName 'user-status-after-refresh.json' -Arguments @('status', $script:ActiveInstallation) | Out-Null

    Invoke-AI4J -EvidenceName 'user-doctor.json' -Arguments @('doctor', $script:ActiveInstallation) | Out-Null
    $previewLines = & $script:AI4J doctor $script:ActiveInstallation --test-mcp claude-tools --json 2>&1
    $previewExit = $LASTEXITCODE
    $previewText = ($previewLines -join [Environment]::NewLine)
    Write-Evidence -Name 'user-mcp-preview.json' -Text $previewText
    $preview = $previewText | ConvertFrom-Json -Depth 100
    if ($previewExit -ne 2 -or $preview.status -ne 'error' -or $null -eq $preview.data.startupCheck) {
        throw 'MCP startup preview did not require explicit approval'
    }
    $startup = Invoke-AI4J -EvidenceName 'user-mcp-startup.json' -Arguments @(
        'doctor', $script:ActiveInstallation, '--test-mcp', 'claude-tools', '--yes'
    )
    if ($startup.data.startupCheck.result -ne 'timed_out' -and $startup.data.startupCheck.result -ne 'exited') {
        throw 'MCP startup check returned an unexpected result'
    }

    Invoke-AI4J -EvidenceName 'user-uninstall.json' -Arguments @('uninstall', $script:ActiveInstallation, '--yes') | Out-Null
    $script:ActiveInstallation = $null
    $postMarketplaceList = Invoke-ClaudeJSON -EvidenceName 'user-post-uninstall-marketplace-list.json' -Arguments @('plugin', 'marketplace', 'list', '--json') -WorkingDirectory $script:RepoRoot
    $postMarketplaceJSON = ConvertTo-Json -InputObject $postMarketplaceList -Depth 100 -Compress
    if ($postMarketplaceJSON.Contains($marketplaceId, [StringComparison]::Ordinal)) {
        throw 'user marketplace remained after uninstall'
    }
    $postPluginList = Invoke-ClaudeJSON -EvidenceName 'user-post-uninstall-plugin-list.json' -Arguments @('plugin', 'list', '--json') -WorkingDirectory $script:RepoRoot
    $postPluginJSON = ConvertTo-Json -InputObject $postPluginList -Depth 100 -Compress
    foreach ($nativePluginId in $nativePluginIds) {
        if ($postPluginJSON.Contains($nativePluginId, [StringComparison]::Ordinal)) {
            throw "user plugin remained after uninstall: $nativePluginId"
        }
    }

    Invoke-ProjectJourney 'project-local'
    Invoke-ProjectJourney 'project-shared'

    Write-Evidence -Name 'qualification-summary.txt' -Text "PASS: Claude $claudeVersion on Windows AMD64 at $($script:QualificationRef)"
}
finally {
    if (-not [string]::IsNullOrWhiteSpace($script:ActiveInstallation) -and (Test-Path -LiteralPath $script:AI4J)) {
        & $script:AI4J uninstall $script:ActiveInstallation --yes --json *> $null
    }
    if (Test-Path -LiteralPath $script:WorkRoot) {
        Remove-Item -LiteralPath $script:WorkRoot -Recurse -Force
    }
}
