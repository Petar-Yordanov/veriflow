$ErrorActionPreference = "Stop"

$repoRoot = $PSScriptRoot
$cliPath = Join-Path $repoRoot "packages\veriflow-cli"
$enginePath = Join-Path $repoRoot "packages\spec-engine"

if (-not (Test-Path $cliPath)) {
    throw "CLI path not found: $cliPath"
}

if (-not (Test-Path $enginePath)) {
    throw "Engine path not found: $enginePath"
}

$exampleProject = $null

$candidates = @(
    (Join-Path $enginePath "examples_project"),
    (Join-Path $enginePath "examples\project"),
    (Join-Path $enginePath "example_project"),
    (Join-Path $enginePath "examples")
)

foreach ($candidate in $candidates) {
    if (Test-Path $candidate) {
        $exampleProject = $candidate
        break
    }
}

if (-not $exampleProject) {
    Write-Host "Contents of spec-engine:" -ForegroundColor Yellow
    Get-ChildItem $enginePath | Select-Object Name, FullName
    throw "Could not find example project folder under: $enginePath"
}

$suitesDir = Join-Path $exampleProject "suites"
if (-not (Test-Path $suitesDir)) {
    Write-Host "Contents of example project:" -ForegroundColor Yellow
    Get-ChildItem $exampleProject | Select-Object Name, FullName
    throw "Suites folder not found under: $exampleProject"
}

$suiteFile = Get-ChildItem $suitesDir -Filter *.yml | Select-Object -First 1
if (-not $suiteFile) {
    Write-Host "Contents of suites folder:" -ForegroundColor Yellow
    Get-ChildItem $suitesDir | Select-Object Name, FullName
    throw "No .yml suite file found under: $suitesDir"
}

$suitePath = $suiteFile.FullName
$environmentPath = Join-Path $exampleProject "environments\dev.yml"

$reportDir = Join-Path $repoRoot ".tmp\reports"
$eventJsonl = Join-Path $repoRoot ".tmp\events\run-events.jsonl"
$machineReport = Join-Path $reportDir "suite-machine.json"
$reportPath = Join-Path $repoRoot "artifacts\smoke-report.json"

Write-Host "CLI path: $cliPath"
Write-Host "Example project: $exampleProject"
Write-Host "Suite path: $suitePath"
Write-Host "Environment path: $environmentPath"

New-Item -ItemType Directory -Force -Path $reportDir | Out-Null
New-Item -ItemType Directory -Force -Path (Split-Path $eventJsonl -Parent) | Out-Null
New-Item -ItemType Directory -Force -Path (Split-Path $reportPath -Parent) | Out-Null

Set-Location $cliPath

py -3.12 -m poetry run veriflow validate file "$suitePath" --environment "$environmentPath"
if ($LASTEXITCODE -ne 0) { throw "validate file failed" }

py -3.12 -m poetry run veriflow validate project "$exampleProject"
if ($LASTEXITCODE -ne 0) { throw "validate project failed" }

py -3.12 -m poetry run veriflow discover suites "$exampleProject"
if ($LASTEXITCODE -ne 0) { throw "discover suites failed" }

py -3.12 -m poetry run veriflow discover environments "$exampleProject"
if ($LASTEXITCODE -ne 0) { throw "discover environments failed" }

Write-Host ""
Write-Host "Running suite with discovered environment name..."
py -3.12 -m poetry run veriflow run suite "$suitePath" --environment dev
if ($LASTEXITCODE -ne 0) { throw "run suite with environment failed" }

Write-Host ""
Write-Host "Running suite with ad-hoc vars..."
py -3.12 -m poetry run veriflow run suite "$suitePath" --var "baseUrl=https://httpbin.org" --var "username=test@example.com"
if ($LASTEXITCODE -ne 0) { throw "run suite with vars failed" }

Write-Host ""
Write-Host "Running suite with json output..."
py -3.12 -m poetry run veriflow run suite "$suitePath" --environment dev --json
if ($LASTEXITCODE -ne 0) { throw "run suite with json output failed" }

Write-Host ""
Write-Host "Running suite with JSON report..."
py -3.12 -m poetry run veriflow run suite "$suitePath" --environment dev --report-json "$reportPath"
if ($LASTEXITCODE -ne 0) { throw "run suite with json report failed" }

Write-Host ""
Write-Host "Running suite with JSON event stream + JSON report..."
py -3.12 -m poetry run veriflow run suite "$suitePath" --environment dev --json --event-jsonl "$eventJsonl" --report-json "$machineReport"
if ($LASTEXITCODE -ne 0) { throw "run suite with machine artifacts failed" }

Write-Host ""
Write-Host "Generated files:"
Write-Host "  Event JSONL:   $eventJsonl"
Write-Host "  Machine report: $machineReport"
Write-Host "  Report JSON:    $reportPath"

cd ../..
