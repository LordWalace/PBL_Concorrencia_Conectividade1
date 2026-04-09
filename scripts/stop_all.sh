#!/bin/bash
cd "$(dirname "$0")/.." || exit

echo "Encerrando conteineres e limpando a rede..."
docker compose down -v
echo "Ambiente limpo com sucesso!"