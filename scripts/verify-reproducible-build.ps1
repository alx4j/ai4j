[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$ScriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepositoryRoot = (& git -C (Join-Path $ScriptRoot '..') rev-parse --show-toplevel).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Unable to resolve repository root.' }
Set-Location -LiteralPath $RepositoryRoot
if ((& git status --porcelain=v1 --untracked-files=normal -- . ':(exclude).idea/**')) {
    throw 'Reproducibility check requires a clean tree.'
}

$Ai4jGo = if ($env:AI4J_GO) { $env:AI4J_GO } else { 'go' }
$TempBase = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
$TempRoot = Join-Path $TempBase ("ai4j-repro-" + [System.Guid]::NewGuid().ToString('N'))
$ResolvedTempRoot = [System.IO.Path]::GetFullPath($TempRoot)
if (-not $ResolvedTempRoot.StartsWith($TempBase, [System.StringComparison]::OrdinalIgnoreCase) -or
    -not ([System.IO.Path]::GetFileName($ResolvedTempRoot)).StartsWith('ai4j-repro-', [System.StringComparison]::Ordinal)) {
    throw 'Temporary build root escaped the intended temp directory.'
}
[System.IO.Directory]::CreateDirectory($TempRoot) | Out-Null
try {
    & git clone --quiet --no-hardlinks $RepositoryRoot (Join-Path $TempRoot 'one')
    if ($LASTEXITCODE -ne 0) { throw 'First isolated clone failed.' }
    & git clone --quiet --no-hardlinks $RepositoryRoot (Join-Path $TempRoot 'two')
    if ($LASTEXITCODE -ne 0) { throw 'Second isolated clone failed.' }

    $env:AI4J_GO = $Ai4jGo
	& (Join-Path $TempRoot 'one/scripts/build-release.ps1') -Output (Join-Path $TempRoot 'one/dist/ai4j') |
		Set-Content -LiteralPath (Join-Path $TempRoot 'one-build.json') -Encoding Ascii
	& (Join-Path $TempRoot 'two/scripts/build-release.ps1') -Output (Join-Path $TempRoot 'two/dist/ai4j') |
        Set-Content -LiteralPath (Join-Path $TempRoot 'two-build.json') -Encoding Ascii

	$FirstMetadata = Get-Content -Raw -LiteralPath (Join-Path $TempRoot 'one-build.json')
	$SecondMetadata = Get-Content -Raw -LiteralPath (Join-Path $TempRoot 'two-build.json')
	if ($FirstMetadata -ne $SecondMetadata) { throw 'Normalized release metadata differs.' }
	foreach ($Name in @('ai4j', 'ai4j.version.json', 'ai4j.sha256', 'ai4j.exe', 'ai4j.exe.version.json', 'ai4j.exe.sha256')) {
		$FirstHash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $TempRoot "one/dist/$Name")).Hash
		$SecondHash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $TempRoot "two/dist/$Name")).Hash
		if ($FirstHash -ne $SecondHash) { throw "Isolated release output differs: $Name" }
	}
} finally {
    Set-Location -LiteralPath $RepositoryRoot
    if (Test-Path -LiteralPath $TempRoot) {
        $CleanupRoot = [System.IO.Path]::GetFullPath($TempRoot)
        if (-not $CleanupRoot.StartsWith($TempBase, [System.StringComparison]::OrdinalIgnoreCase) -or
            -not ([System.IO.Path]::GetFileName($CleanupRoot)).StartsWith('ai4j-repro-', [System.StringComparison]::Ordinal)) {
            throw 'Refusing to clean an unexpected temporary build root.'
        }
        Remove-Item -LiteralPath $CleanupRoot -Recurse -Force
    }
}
