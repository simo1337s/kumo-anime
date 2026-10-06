<#
.SYNOPSIS
    Builds the Kumo installer and portable zip for Windows.

.DESCRIPTION
    Does on your own PC what the "Windows release" GitHub workflow does:
    builds the web UI, embeds it into the server, runs go vet and go test,
    builds kumo.exe, then packages the desktop app with electron-builder.

    Needs Go 1.21 or newer (it fetches the version server\go.mod asks for)
    and Node.js LTS with npm, both on the PATH:
        winget install GoLang.Go
        winget install OpenJS.NodeJS.LTS

    The results land in dist\windows\release:
        Kumo-Setup-<version>-windows-x64.exe      the installer
        Kumo-<version>-windows-x64-portable.zip   the portable build

.PARAMETER SkipTests
    Don't run go vet and go test.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File packaging\windows\build.ps1
#>
[CmdletBinding()]
param(
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version 3.0

$Root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path

# Stops the build when the program that just ran failed: $ErrorActionPreference
# only covers PowerShell's own commands, not programs like npm and go. (npm and
# npx are called directly, not through a helper, because their PowerShell
# shims re-read the command line they were called with.)
function Assert-Success([string]$What, [string]$Hint = "") {
    if ($LASTEXITCODE -ne 0) {
        throw "$What failed (exit code $LASTEXITCODE).$Hint"
    }
}

function Write-Step([string]$Text) {
    Write-Host ""
    Write-Host "==> $Text" -ForegroundColor Cyan
}

$tools = @(
    @{ Name = "go"; Install = "winget install GoLang.Go" },
    @{ Name = "npm"; Install = "winget install OpenJS.NodeJS.LTS" }
)
foreach ($tool in $tools) {
    if (-not (Get-Command $tool.Name -ErrorAction SilentlyContinue)) {
        throw "$($tool.Name) wasn't found. Install it ($($tool.Install)), then open a new terminal and try again."
    }
}

$version = (Get-Content (Join-Path $Root "desktop\package.json") -Raw | ConvertFrom-Json).version
Write-Host "Building Kumo $version for Windows (x64) in $Root"

Write-Step "Web UI"
Push-Location (Join-Path $Root "web")
try {
    npm ci --no-audit --no-fund
    Assert-Success "npm ci (web)"
    npm run build
    Assert-Success "npm run build (web)"
} finally {
    Pop-Location
}

# Same as the Makefile's "embed" target.
Write-Step "Embedding the web UI into the server"
$embed = Join-Path $Root "server\internal\webui\dist"
if (Test-Path $embed) {
    Remove-Item $embed -Recurse -Force
}
New-Item -ItemType Directory -Path $embed | Out-Null
Copy-Item -Path (Join-Path $Root "web\dist\*") -Destination $embed -Recurse -Force
New-Item -ItemType File -Path (Join-Path $embed ".keep") -Force | Out-Null

# The Go settings are only for this build: put the old values back after.
$savedEnv = @{}
foreach ($name in @("CGO_ENABLED", "GOOS", "GOARCH")) {
    $savedEnv[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}
Push-Location (Join-Path $Root "server")
try {
    $env:CGO_ENABLED = "0"
    if ($SkipTests) {
        Write-Host ""
        Write-Host "Skipping go vet and go test (-SkipTests)." -ForegroundColor Yellow
    } else {
        Write-Step "go vet and go test"
        go vet ./...
        Assert-Success "go vet"
        go test ./...
        Assert-Success "go test" " Add -SkipTests to build without running the tests."
    }

    Write-Step "Server: dist\windows\kumo.exe"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    New-Item -ItemType Directory -Path (Join-Path $Root "dist\windows") -Force | Out-Null
    go build -trimpath -ldflags "-s -w" -o ..\dist\windows\kumo.exe ./cmd/kumo
    Assert-Success "go build"
} finally {
    Pop-Location
    foreach ($name in $savedEnv.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnv[$name], "Process")
    }
}

Write-Step "Desktop app: installer and portable zip (desktop\electron-builder.yml)"
$release = Join-Path $Root "dist\windows\release"
if (Test-Path $release) {
    Remove-Item $release -Recurse -Force
}
Push-Location (Join-Path $Root "desktop")
try {
    npm ci --no-audit --no-fund
    Assert-Success "npm ci (desktop)"
    npx electron-builder --win --x64 --publish never
    Assert-Success "electron-builder"
} finally {
    Pop-Location
}

Write-Host ""
Write-Host "Done:" -ForegroundColor Green
foreach ($name in @("Kumo-Setup-$version-windows-x64.exe", "Kumo-$version-windows-x64-portable.zip")) {
    $file = Join-Path $release $name
    if (-not (Test-Path $file)) {
        throw "$file is missing: check electron-builder's output above."
    }
    Write-Host "  $file"
}
