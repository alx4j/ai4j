[CmdletBinding()]
param(
    [string]$Output = 'dist/ai4j'
)

$ErrorActionPreference = 'Stop'
$ScriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepositoryRoot = (& git -C (Join-Path $ScriptRoot '..') rev-parse --show-toplevel).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Unable to resolve repository root.' }
Set-Location -LiteralPath $RepositoryRoot

$Ai4jGo = if ($env:AI4J_GO) { $env:AI4J_GO } else { 'go' }
$env:GOTOOLCHAIN = 'local'

if ($env:GOWORK -and $env:GOWORK -ne 'off') {
    throw "Active GOWORK override is prohibited: $env:GOWORK"
}
if ($env:GOWORK -ne 'off') {
    $DetectedWork = (& $Ai4jGo env GOWORK).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Unable to inspect Go workspace state.' }
    if ($DetectedWork) { throw "Active Go workspace is prohibited: $DetectedWork" }
}
$env:GOWORK = 'off'

$EffectiveFlags = (& $Ai4jGo env GOFLAGS).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Unable to inspect GOFLAGS.' }
$EffectiveExperiment = (& $Ai4jGo env GOEXPERIMENT).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Unable to inspect GOEXPERIMENT.' }
if ($EffectiveFlags) { throw "GOFLAGS must be empty for release builds: $EffectiveFlags" }
if ($EffectiveExperiment) { throw "GOEXPERIMENT must be empty for release builds: $EffectiveExperiment" }

$env:GOFLAGS = ''
$env:GOEXPERIMENT = ''
$env:CGO_ENABLED = '0'

& $Ai4jGo run -mod=readonly ./internal/repocheck/cmd/repocheck release-inputs
if ($LASTEXITCODE -ne 0) { throw 'Release input policy failed.' }
$Revision = (& git rev-parse --verify HEAD).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Unable to resolve VCS revision.' }

if ([System.IO.Path]::IsPathRooted($Output)) {
    $OutputPath = [System.IO.Path]::GetFullPath($Output)
} else {
    $OutputPath = [System.IO.Path]::GetFullPath((Join-Path $RepositoryRoot $Output))
}
$OutputDirectory = Split-Path -Parent $OutputPath
[System.IO.Directory]::CreateDirectory($OutputDirectory) | Out-Null
$WindowsOutputPath = Join-Path $OutputDirectory 'ai4j.exe'

$OriginalOS = $env:GOOS
$OriginalArch = $env:GOARCH
try {
	$Artifacts = @(
		[PSCustomObject]@{ OS = 'darwin'; Arch = 'arm64'; Path = $OutputPath },
		[PSCustomObject]@{ OS = 'windows'; Arch = 'amd64'; Path = $WindowsOutputPath }
	)
	foreach ($Artifact in $Artifacts) {
		$env:GOOS = $Artifact.OS
		$env:GOARCH = $Artifact.Arch
		& $Ai4jGo build -mod=readonly -trimpath -buildvcs=true -o $Artifact.Path ./cmd/ai4j
		if ($LASTEXITCODE -ne 0) { throw "Release build failed for $($Artifact.OS)/$($Artifact.Arch)." }
		$env:GOOS = $OriginalOS
		$env:GOARCH = $OriginalArch

		$VersionPath = $Artifact.Path + '.version.json'
		$ChecksumPath = $Artifact.Path + '.sha256'
		$Evidence = & $Ai4jGo run -mod=readonly ./internal/repocheck/cmd/repocheck binary --file $Artifact.Path --revision $Revision
		if ($LASTEXITCODE -ne 0) { throw "Release binary policy failed for $($Artifact.OS)/$($Artifact.Arch)." }
		$EvidenceText = ($Evidence -join "`n") + "`n"
		[System.IO.File]::WriteAllText($VersionPath, $EvidenceText, [System.Text.UTF8Encoding]::new($false))
		$Digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $Artifact.Path).Hash.ToLowerInvariant()
		$ChecksumText = $Digest + '  ' + [System.IO.Path]::GetFileName($Artifact.Path) + "`n"
		[System.IO.File]::WriteAllText($ChecksumPath, $ChecksumText, [System.Text.UTF8Encoding]::new($false))
		$EvidenceText.TrimEnd()
	}
} finally {
	$env:GOOS = $OriginalOS
	$env:GOARCH = $OriginalArch
}
