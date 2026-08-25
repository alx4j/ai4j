[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$ScriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepositoryRoot = (& git -C (Join-Path $ScriptRoot '..') rev-parse --show-toplevel).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Unable to resolve repository root.' }
Set-Location -LiteralPath $RepositoryRoot

$Ai4jGo = if ($env:AI4J_GO) { $env:AI4J_GO } else { 'go' }
$env:GOTOOLCHAIN = 'local'
$env:GOWORK = 'off'
$env:CGO_ENABLED = '0'

$GoVersion = (& $Ai4jGo version).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Unable to run the Go toolchain.' }
if ($GoVersion -notmatch '^go version go1\.26\.6 ') { throw "Unexpected Go toolchain: $GoVersion" }

function Invoke-Checked {
    param([Parameter(Mandatory)][string[]]$Arguments)
    & $Ai4jGo @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Go command failed: $($Arguments -join ' ')" }
}

Invoke-Checked -Arguments @('version')
Invoke-Checked -Arguments @('env', 'GOOS', 'GOARCH', 'CGO_ENABLED', 'GOTOOLCHAIN', 'GOMOD', 'GOWORK')
Invoke-Checked -Arguments @('run', '-mod=readonly', './internal/repocheck/cmd/repocheck', 'format')
Invoke-Checked -Arguments @('run', '-mod=readonly', './internal/repocheck/cmd/repocheck', 'module')
Invoke-Checked -Arguments @('mod', 'tidy', '-diff')
Invoke-Checked -Arguments @('mod', 'verify')
Invoke-Checked -Arguments @('list', '-m', '-mod=readonly', 'all')
Invoke-Checked -Arguments @('test', '-mod=readonly', './...')
Invoke-Checked -Arguments @('vet', '-mod=readonly', './...')
Invoke-Checked -Arguments @('run', '-mod=readonly', './internal/repocheck/cmd/repocheck', 'authorship', '--range', 'HEAD')

& git diff --exit-code -- .
if ($LASTEXITCODE -ne 0) { throw 'Quality checks modified tracked files.' }
