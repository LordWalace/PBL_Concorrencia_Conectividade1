# Parâmetro opcional: número de instâncias de `device` (default 3)
param(
    [int]$Count = 3
)

# Escala o serviço device e mostra logs no mesmo terminal
docker-compose up -d --scale device=$Count
docker-compose logs -f device
