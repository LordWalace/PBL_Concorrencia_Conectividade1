#!/bin/bash

# Volta uma pasta para garantir que o docker compose encontre o arquivo .yml na raiz
cd "$(dirname "$0")/.." || exit

echo "Iniciando 500 dispositivos em background..."

# O Compose cuida de instanciar os 500 sem precisar de loops no bash
docker compose up -d --scale device=500 --no-recreate device

echo "Dispositivos escalados com sucesso!"