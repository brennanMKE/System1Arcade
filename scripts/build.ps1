<#
.SYNOPSIS
  Build System 1 Arcade on Windows.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -Agent -Test

.PARAMETER Agent
  Also set up .venv with Laya for the built-in agent.
.PARAMETER Clean
  Remove previous build output first.
.PARAMETER DebugBuild
  Build with devtools and debug logging.
.PARAMETER Test
  Run the Go tests before building.
#>
param(
  [switch]$Agent,
  [switch]$Clean,
  [switch]$DebugBuild,
  [switch]$Test
)
$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

function Say($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Die($msg) { Write-Error $msg; exit 1 }
function Have($cmd) { [bool](Get-Command $cmd -ErrorAction SilentlyContinue) }

# --- Prerequisites -----------------------------------------------------------
if (-not (Have go)) { Die 'Go is required: https://go.dev/dl/' }
if (-not (Have npm)) { Die 'Node.js and npm are required: https://nodejs.org/' }

# Use the Wails CLI version that matches go.mod, installing it if needed.
$WailsVersion = (Select-String -Path go.mod -Pattern 'github.com/wailsapp/wails/v2 (v\S+)' |
  Where-Object { $_.Line -notmatch 'replace' } | Select-Object -First 1).Matches[0].Groups[1].Value
$GoBin = (go env GOBIN); if (-not $GoBin) { $GoBin = Join-Path (go env GOPATH) 'bin' }
$Wails = (Get-Command wails -ErrorAction SilentlyContinue).Source
if (-not $Wails -and (Test-Path (Join-Path $GoBin 'wails.exe'))) { $Wails = Join-Path $GoBin 'wails.exe' }
if (-not $Wails) {
  Say "Installing the Wails CLI $WailsVersion"
  go install "github.com/wailsapp/wails/v2/cmd/wails@$WailsVersion"
  if ($LASTEXITCODE) { Die 'could not install the Wails CLI' }
  $Wails = Join-Path $GoBin 'wails.exe'
}

# --- Optional steps ----------------------------------------------------------
if ($Test) {
  Say 'Running tests'
  go test ./internal/...
  if ($LASTEXITCODE) { Die 'tests failed' }
}

if ($Agent) {
  $Py = (Get-Command python -ErrorAction SilentlyContinue).Source
  if (-not $Py) { $Py = (Get-Command py -ErrorAction SilentlyContinue).Source }
  if (-not $Py) { Die 'Python 3 is required for the built-in agent' }
  $VenvPy = Join-Path $Root '.venv\Scripts\python.exe'
  if (-not (Test-Path $VenvPy)) {
    Say 'Creating .venv'
    & $Py -m venv .venv
    if ($LASTEXITCODE) { Die 'could not create .venv' }
  }
  Say 'Installing Laya into .venv (the model downloads on first use)'
  & $VenvPy -m pip install --quiet --upgrade pip
  & $VenvPy -m pip install --quiet laya
  if ($LASTEXITCODE) { Die 'could not install laya' }
}

# --- Build -------------------------------------------------------------------
$BuildArgs = @('build')
if ($Clean) { $BuildArgs += '-clean' }
if ($DebugBuild) { $BuildArgs += '-debug' }

Say "Building for windows/$(go env GOARCH)"
& $Wails @BuildArgs
if ($LASTEXITCODE) { Die 'wails build failed' }

$Name = (Get-Content wails.json -Raw | ConvertFrom-Json).outputfilename
$Out = Join-Path $Root "build\bin\$Name.exe"
if (-not (Test-Path $Out)) { Die "build finished but $Out is missing" }
Say "Built $Out"
