# Inicia gateway, escala devices (padrão 3) e inicia client; abre terminais de logs
param(
    [int]$DeviceCount = 2
)

docker-compose up -d gateway
docker-compose up -d --scale device=$DeviceCount
docker-compose up -d client

Write-Host "Serviços iniciados. Exibindo logs do gateway (Ctrl+C para voltar):"
docker-compose logs -f gateway
