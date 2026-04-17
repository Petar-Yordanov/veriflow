$ErrorActionPreference = "Stop"

$repoRoot = $PSScriptRoot
$enginePath = Join-Path $repoRoot "packages\spec-engine"
$cliPath = Join-Path $repoRoot "packages\veriflow-cli"

if (-not (Test-Path $enginePath)) {
    throw "Engine path not found: $enginePath"
}

if (-not (Test-Path $cliPath)) {
    throw "CLI path not found: $cliPath"
}

Set-Location $enginePath
py -3.12 -m poetry env use 3.12
if ($LASTEXITCODE -ne 0) { throw "Failed to select Python 3.12 for spec-engine" }

py -3.12 -m poetry install
if ($LASTEXITCODE -ne 0) { throw "Failed to install spec-engine" }

Set-Location $cliPath
py -3.12 -m poetry env use 3.12
if ($LASTEXITCODE -ne 0) { throw "Failed to select Python 3.12 for veriflow-cli" }

py -3.12 -m poetry lock
if ($LASTEXITCODE -ne 0) { throw "Failed to lock veriflow-cli" }

py -3.12 -m poetry install
if ($LASTEXITCODE -ne 0) { throw "Failed to install veriflow-cli" }

py -3.12 -m poetry run veriflow --help
if ($LASTEXITCODE -ne 0) { throw "Failed to run veriflow --help" }
