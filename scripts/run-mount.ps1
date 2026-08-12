param(
    [string]$Drive = "Z:",
    [string]$EnvFile = "",
    [switch]$SkipBuild,
    [switch]$DebugLogs
)

$ErrorActionPreference = "Stop"

# Build stream_proxy + infinity-storage-mount, load repo .env, mount Z: for Odin UI testing.
# Usage:
#   powershell -ExecutionPolicy Bypass -File .\scripts\run-mount.ps1
# Optional:
#   .\scripts\run-mount.ps1 -Drive Y: -SkipBuild
# Decrypts .env.enc with sops if plaintext .env is missing.

$repository_root = Split-Path -Parent $PSScriptRoot
Set-Location $repository_root

$mount_name = "infinity-storage-mount.exe"
$proxy_name = "stream_proxy.exe"
$mount_bin = Join-Path $repository_root $mount_name
$proxy_bin = Join-Path $repository_root "stream_proxy\zig-out\bin\$proxy_name"
$spool_dir = Join-Path $env:LOCALAPPDATA "InfinityStorage\spool"

if ([string]::IsNullOrWhiteSpace($EnvFile)) {
    $EnvFile = Join-Path $repository_root ".env"
}

function Import-DotEnv {
    param([Parameter(Mandatory = $true)][string]$Path)
    Get-Content -LiteralPath $Path | ForEach-Object {
        $line = $_.Trim()
        if ($line -eq "" -or $line.StartsWith("#")) { return }
        if ($line.StartsWith("export ")) { $line = $line.Substring(7).Trim() }
        $eq = $line.IndexOf("=")
        if ($eq -lt 1) { return }
        $key = $line.Substring(0, $eq).Trim()
        $val = $line.Substring($eq + 1).Trim()
        if (
            ($val.StartsWith('"') -and $val.EndsWith('"')) -or
            ($val.StartsWith("'") -and $val.EndsWith("'"))
        ) {
            $val = $val.Substring(1, $val.Length - 2)
        }
        if ($key -eq "") { return }
        Set-Item -Path "Env:$key" -Value $val
    }
}

function Ensure-PlainEnv {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (Test-Path -LiteralPath $Path) { return }

    $enc = Join-Path $repository_root ".env.enc"
    if (-not (Test-Path -LiteralPath $enc)) {
        throw "missing $Path (and no .env.enc to decrypt)"
    }

    $sops = Join-Path $env:LOCALAPPDATA "Programs\sops\sops.exe"
    if (-not (Test-Path -LiteralPath $sops)) {
        $cmd = Get-Command sops -ErrorAction SilentlyContinue
        if ($null -eq $cmd) { throw "sops not found; cannot decrypt .env.enc" }
        $sops = $cmd.Source
    }

    $keyFile = Join-Path $env:APPDATA "sops\age\keys.txt"
    if (Test-Path -LiteralPath $keyFile) {
        $env:SOPS_AGE_KEY_FILE = $keyFile
    }

    Write-Output "Decrypting .env.enc -> .env"
    $plain = & $sops -d --input-type dotenv --output-type dotenv $enc
    if ($LASTEXITCODE -ne 0) { throw "sops decrypt failed" }
    [System.IO.File]::WriteAllText($Path, (($plain -join "`n") + "`n"))
}

function Ensure-Build {
    foreach ($command_name in @("go", "zig")) {
        if ($null -eq (Get-Command $command_name -ErrorAction SilentlyContinue)) {
            throw "required command not found: $command_name"
        }
    }

    Write-Output "Building stream_proxy (Windows)..."
    Push-Location (Join-Path $repository_root "stream_proxy")
    try {
        & zig build -Dtarget=x86_64-windows-gnu
        if ($LASTEXITCODE -ne 0) { throw "zig build failed" }
    } finally {
        Pop-Location
    }

    Write-Output "Building $mount_name..."
    & go build -o $mount_bin ./cmd/infinity-storage-mount
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
}

Ensure-PlainEnv -Path $EnvFile
Import-DotEnv -Path $EnvFile

$bucket = $env:AWS_BUCKET_NAME
if ([string]::IsNullOrWhiteSpace($bucket)) {
    $bucket = $env:AWS_BUCKET
}
if ([string]::IsNullOrWhiteSpace($bucket)) {
    throw "AWS_BUCKET_NAME (or AWS_BUCKET) missing in $EnvFile"
}

$region = $env:AWS_REGION
if ([string]::IsNullOrWhiteSpace($region)) {
    throw "AWS_REGION missing in $EnvFile"
}

if (
    [string]::IsNullOrWhiteSpace($env:AWS_ACCESS_KEY_ID) -and
    [string]::IsNullOrWhiteSpace($env:AWS_PROFILE) -and
    [string]::IsNullOrWhiteSpace($env:AWS_SECRET_ACCESS_KEY)
) {
    throw "no AWS creds in env (.env needs AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY or AWS_PROFILE)"
}

if (-not $SkipBuild) {
    Ensure-Build
}

if (-not (Test-Path -LiteralPath $mount_bin)) {
    throw "mount exe missing: $mount_bin (re-run without -SkipBuild)"
}
if (-not (Test-Path -LiteralPath $proxy_bin)) {
    throw "proxy exe missing: $proxy_bin (re-run without -SkipBuild)"
}

if (Test-Path -LiteralPath $Drive) {
    throw "drive $Drive already mounted; Ctrl-C the other mount console first"
}

New-Item -ItemType Directory -Force -Path $spool_dir | Out-Null

$mount_arguments = @(
    "--mount", $Drive,
    "--bucket", $bucket,
    "--region", $region,
    "--env-file", $EnvFile,
    "--proxy-bin", $proxy_bin,
    "--spool-dir", $spool_dir
)
if ($DebugLogs) {
    $mount_arguments += "--debug"
}

Write-Output "Mounting $Drive from s3://$bucket (region=$region)"
Write-Output "Env file: $EnvFile"
Write-Output "Spool: $spool_dir"
Write-Output "Leave this window open. Ctrl-C unmounts."
Write-Output "Then run Odin UI; it defaults to ${Drive}/"

& $mount_bin @mount_arguments
exit $LASTEXITCODE
