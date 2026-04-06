# Script interativo: escala devices, coleta métricas do gateway e salva CSV
Write-Host "=== Auto Scale & Metrics Collector ==="

[int]$count = 0
while ($count -le 0) {
    $inp = Read-Host 'Quantos sensores deseja ativar? (número inteiro maior que 0)'
    if ([int]::TryParse($inp,[ref]$count) -and $count -gt 0) { break }
    Write-Host "Valor inválido. Digite um número inteiro maior que 0." -ForegroundColor Yellow
}

$samplesInput = Read-Host 'Quantas amostras deseja coletar? (default 12)'
[int]$samples = 12
if ($samplesInput -ne '') { [int]::TryParse($samplesInput,[ref]$samples) | Out-Null }

$intervalInput = Read-Host 'Intervalo entre amostras em segundos? (default 5)'
[int]$interval = 5
if ($intervalInput -ne '') { [int]::TryParse($intervalInput,[ref]$interval) | Out-Null }

Write-Host "Iniciando gateway, escalando $count devices e iniciando client..."
docker-compose up -d gateway
docker-compose up -d --scale device=$count
docker-compose up -d client

$stabilize = 5
Write-Host "Aguardando $stabilize segundos para estabilizar..."
Start-Sleep -Seconds $stabilize

$ts = Get-Date -Format "yyyyMMdd_HHmmss"
$outFile = "scripts/scale_metrics_$ts.csv"
"timestamp,devices,cpu_perc,mem_usage,net_io" | Out-File -FilePath $outFile -Encoding utf8

for ($i=1; $i -le $samples; $i++) {
    $now = Get-Date -Format o
    # contar containers de device (nome contém 'device')
    $devCount = (docker ps --filter "name=device" --format "{{.Names}}" | Measure-Object).Count

    # coletar estatísticas do gateway
    $stat = docker stats gateway --no-stream --format "{{.CPUPerc}},{{.MemUsage}},{{.NetIO}}" 2>$null
    if ([string]::IsNullOrWhiteSpace($stat)) { $stat = ",," }

    $line = "$now,$devCount,$stat"
    Add-Content -Path $outFile -Value $line

    Write-Host "[$i/$samples] $line"
    if ($i -lt $samples) { Start-Sleep -Seconds $interval }
}

Write-Host "Coleta finalizada. Arquivo: $outFile"
Write-Host "Para encerrar os containers execute: .\scripts\stop_all.ps1"
