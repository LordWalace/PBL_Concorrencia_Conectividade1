<#
Stop and remove all services
Usage: .\stop.ps1
#>
Write-Host "Stopping all services..."
docker compose down
