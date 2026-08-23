param(
    [string]$EnvFile = "",
    [string]$RelayURL = "",
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

$repository_root = Split-Path -Parent $PSScriptRoot
$api_directory = Join-Path $repository_root "api"
$desktop_directory = Join-Path $repository_root "electron-desktop"
$logs_directory = Join-Path $repository_root ".logs"
$api_stdout_log = Join-Path $logs_directory "api.stdout.log"
$api_stderr_log = Join-Path $logs_directory "api.stderr.log"
$api_process = $null

if ([string]::IsNullOrWhiteSpace($EnvFile)) {
    $EnvFile = $env:INFINITY_STORAGE_ENV_FILE
}
if ([string]::IsNullOrWhiteSpace($EnvFile)) {
    $EnvFile = Join-Path $repository_root ".env"
}
if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) {
    throw "missing environment file: $EnvFile"
}
$EnvFile = (Resolve-Path -LiteralPath $EnvFile).Path

function Import-DotEnv {
    param([Parameter(Mandatory = $true)][string]$Path)

    Get-Content -LiteralPath $Path | ForEach-Object {
        $line = $_.Trim()
        if ($line -eq "" -or $line.StartsWith("#")) { return }
        if ($line.StartsWith("export ")) { $line = $line.Substring(7).Trim() }
        $equals = $line.IndexOf("=")
        if ($equals -lt 1) { return }

        $key = $line.Substring(0, $equals).Trim()
        $value = $line.Substring($equals + 1).Trim()
        if (
            ($value.StartsWith('"') -and $value.EndsWith('"')) -or
            ($value.StartsWith("'") -and $value.EndsWith("'"))
        ) {
            $value = $value.Substring(1, $value.Length - 2)
        }
        if ($key -ne "") { Set-Item -Path "Env:$key" -Value $value }
    }
}

function Require-Command {
    param([Parameter(Mandatory = $true)][string]$Name)

    if ($null -eq (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "required command not found: $Name"
    }
}

function Invoke-Build {
    Push-Location (Join-Path $repository_root "stream_proxy")
    try {
        & zig build -Dtarget=x86_64-windows-gnu
        if ($LASTEXITCODE -ne 0) { throw "zig build failed" }
    } finally {
        Pop-Location
    }

    Push-Location $repository_root
    try {
        $mount_binary = Join-Path $repository_root "infinity-storage-mount.exe"
        & go build -o $mount_binary ./cmd/infinity-storage-mount
        if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    } finally {
        Pop-Location
    }

    Push-Location $desktop_directory
    try {
        & npm exec vite build
        if ($LASTEXITCODE -ne 0) { throw "Electron renderer build failed" }
    } finally {
        Pop-Location
    }
}

function Stop-Api {
    if ($null -eq $api_process) { return }
    $api_process.Refresh()
    if (-not $api_process.HasExited) {
        & taskkill.exe /PID $api_process.Id /T /F *> $null
    }
    $api_process.Dispose()
}

Import-DotEnv -Path $EnvFile

if ([string]::IsNullOrWhiteSpace($env:INFINITY_STORAGE_API_URL)) {
    if ([string]::IsNullOrWhiteSpace($env:BETTER_AUTH_URL)) {
        throw "BETTER_AUTH_URL missing from $EnvFile"
    }
    $env:INFINITY_STORAGE_API_URL = $env:BETTER_AUTH_URL
}
if (-not [string]::IsNullOrWhiteSpace($RelayURL)) {
    $env:INFINITY_STORAGE_LIVE_RELAY_URL = $RelayURL
}

Require-Command -Name "go"
Require-Command -Name "npm"
Require-Command -Name "zig"

$api_runner = Join-Path $api_directory "node_modules\.bin\tsx.cmd"
if (-not (Test-Path -LiteralPath $api_runner -PathType Leaf)) {
    throw "API dependencies missing; run npm install in api"
}
$electron_runner = Join-Path $desktop_directory "node_modules\.bin\electron.cmd"
if (-not (Test-Path -LiteralPath $electron_runner -PathType Leaf)) {
    throw "Electron dependencies missing; run npm install in electron-desktop"
}

if ($SkipBuild) {
    $mount_binary = Join-Path $repository_root "infinity-storage-mount.exe"
    $proxy_binary = Join-Path $repository_root "stream_proxy\zig-out\bin\stream_proxy.exe"
    foreach ($binary in @($mount_binary, $proxy_binary)) {
        if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
            throw "build artifact missing: $binary (rerun without -SkipBuild)"
        }
    }
}

New-Item -ItemType Directory -Force -Path $logs_directory | Out-Null
Set-Content -LiteralPath $api_stdout_log -Value ""
Set-Content -LiteralPath $api_stderr_log -Value ""

if (-not $SkipBuild) {
    Invoke-Build
}

$api_command = 'call "' + $api_runner + '" --env-file="' + $EnvFile + '" src/server.ts'
$api_process = Start-Process `
    -FilePath $env:ComSpec `
    -ArgumentList @("/d", "/s", "/c", $api_command) `
    -WorkingDirectory $api_directory `
    -RedirectStandardOutput $api_stdout_log `
    -RedirectStandardError $api_stderr_log `
    -WindowStyle Hidden `
    -PassThru

try {
    Start-Sleep -Seconds 2
    $api_process.Refresh()
    if ($api_process.HasExited) {
        $api_error = Get-Content -LiteralPath $api_stderr_log -Raw
        if ([string]::IsNullOrWhiteSpace($api_error)) {
            throw "API exited before Electron started (code $($api_process.ExitCode))"
        }
        throw $api_error.Trim()
    }

    Push-Location $desktop_directory
    try {
        & npm run start
        $exit_code = $LASTEXITCODE
    } finally {
        Pop-Location
    }
} finally {
    Stop-Api
}

exit $exit_code
