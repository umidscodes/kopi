# The one check command: format, vet, build, test, race. Same steps as CI.
# Usage: powershell -ExecutionPolicy Bypass -File scripts/check.ps1 [-Quick]
# Never installs anything, needs no network, changes no files.
# Written for Windows PowerShell 5.1 (no && || ?? or ternary).
param([switch]$Quick)

$ErrorActionPreference = 'Continue'
Set-Location (Join-Path $PSScriptRoot '..')

function Step($name, [scriptblock]$cmd) {
    Write-Host "==> $name"
    & $cmd
    if ($LASTEXITCODE -ne 0) {
        Write-Host "FAIL $name"
        exit 1
    }
    Write-Host "PASS $name"
}

Write-Host "==> gofmt"
$unformatted = gofmt -l .
if ($LASTEXITCODE -ne 0) {
    Write-Host "FAIL gofmt"
    exit 1
}
if ($unformatted) {
    Write-Host "Not gofmt'd:"
    $unformatted | ForEach-Object { Write-Host $_ }
    Write-Host "FAIL gofmt"
    exit 1
}
Write-Host "PASS gofmt"

Step "go vet ./..." { go vet ./... }
Step "go build ./..." { go build ./... }
Step "go test ./..." { go test ./... }

if ($Quick) {
    Write-Host "SKIP race: -Quick"
    exit 0
}

# Race needs cgo: CGO_ENABLED=1 and a C compiler that exists on PATH.
$haveCC = $false
$ccName = ''
$cc = (go env CC)
if ($LASTEXITCODE -ne 0) {
    Write-Host "FAIL go env CC"
    exit 1
}
$cgo = (go env CGO_ENABLED)
if ($LASTEXITCODE -ne 0) {
    Write-Host "FAIL go env CGO_ENABLED"
    exit 1
}
if ($cc) {
    $ccName = ($cc.Trim() -split '\s+')[0]
    if (($cgo -eq '1') -and (Get-Command $ccName -ErrorAction SilentlyContinue)) {
        $haveCC = $true
    }
}
if (-not $haveCC) {
    Write-Host "SKIP race: no C toolchain (see AGENTS.md)"
    exit 0
}
Step "go test -race ./... (cc: $ccName)" { go test -race ./... }
