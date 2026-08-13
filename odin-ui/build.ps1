$ErrorActionPreference = "Stop"
Set-Location -LiteralPath $PSScriptRoot
odin build . -out:infinity-ui.exe -subsystem:windows
if ($LASTEXITCODE -ne 0) { throw "build failed" }
Write-Host "built infinity-ui.exe"
