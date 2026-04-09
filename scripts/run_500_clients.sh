#!/bin/bash
cd "$(dirname "$0")/.." || exit

echo "Conectando ao Painel de Controle..."
# Usa -it para terminal interativo e --rm para apagar o conteiner ao sair
docker compose run -it --rm client