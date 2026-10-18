# Builds bin\kopi.exe as a tray app (no console window).
#   powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1 [-Version v0.1.0]
param([string]$Version = "dev")

$ErrorActionPreference = "Stop"
Set-Location (Split-Path -Parent $PSScriptRoot)
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go) { $go = "C:\Program Files\Go\bin\go.exe" }

New-Item -ItemType Directory -Force bin | Out-Null
& $go build -trimpath -ldflags "-H windowsgui -s -w -X main.version=$Version" -o bin\kopi.exe ./cmd/kopi
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Built bin\kopi.exe ($Version)"
