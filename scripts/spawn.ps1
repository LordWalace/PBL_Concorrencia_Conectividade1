<#
Spawn/scale devices and clients using docker compose --scale

Usage:
  .\spawn.ps1 -Devices 10 -Clients 1

Parameters:
  -Devices: number of `device` replicas (default 1)
  -Clients: number of `client` replicas (default 0)
#>
param(
    [int]$Devices = 1,
    [int]$Clients = 0
)

Write-Host "Scaling: devices=$Devices, clients=$Clients"

$scaleArgs = @()
if ($Devices -gt 0) { $scaleArgs += "--scale"; $scaleArgs += "device=$Devices" }
if ($Clients -gt 0) { $scaleArgs += "--scale"; $scaleArgs += "client=$Clients" }

if ($scaleArgs.Count -eq 0) {
    Write-Host "Nothing to scale. Use -Devices or -Clients."; exit 1
}

# Build then scale
docker compose up -d --build $scaleArgs
