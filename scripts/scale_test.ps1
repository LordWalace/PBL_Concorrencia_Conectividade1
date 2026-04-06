# Script de teste de escala: incrementa número de instâncias `device` em passos e aguarda observação
param(
    [int]$Max = 50,
    [int]$Step = 5,
    [int]$WaitSeconds = 10
)

for ($n = $Step; $n -le $Max; $n += $Step) {
    Write-Host "Scaling devices to $n..."
    docker-compose up -d --scale device=$n
    Write-Host "Aguardando $WaitSeconds segundos para estabilizar..."
    Start-Sleep -Seconds $WaitSeconds
    Write-Host "Exibindo containers ativos (contar devices):"
    docker ps --filter "name=device" --format "{{.Names}}"
    Write-Host "Gateway logs (últimas linhas):"
    docker-compose logs --tail=20 gateway
}

Write-Host "Teste concluído. Para encerrar os containers execute: scripts\stop_all.ps1"
