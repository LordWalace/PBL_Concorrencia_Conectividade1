<#
Start all services (build + detach)
Usage: .\start.ps1
#>
Write-Host "Starting all services (build)..."
docker compose up -d --build
