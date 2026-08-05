#!/usr/bin/env pwsh
# descendence job param shim (task 6.4) - PowerShell.
#
# argv is [this shim, the real script's path] - manifest.Manifest.Argv puts
# it there when the job declares at least one param, names no explicit
# command, and the script's own extension is .ps1. Reads params.json - an
# array of {"name":..., "value":...} in contract order (task 6.2/6.3) - and
# invokes the real script with a splatted hashtable, so a normal param()
# block consumes them exactly as if invoked directly.
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$ScriptPath
)

$paramsPath = $env:DESCENDENCE_PARAMS_FILE
if (-not $paramsPath) { $paramsPath = "/run/job/params.json" }

$params = @(Get-Content -Raw -Path $paramsPath | ConvertFrom-Json)

$splat = @{}
foreach ($p in $params) {
    $splat[$p.name] = $p.value
}

& $ScriptPath @splat
exit $LASTEXITCODE
