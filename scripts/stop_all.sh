#!/bin/bash

echo "Parando e removendo todos os conteineres do projeto..."
docker rm -f $(docker ps -a -q --filter name=iot_) 2>/dev/null

echo "Removendo rede iot_net..."
docker network rm iot_net 2>/dev/null

echo "Ambiente completamente limpo!"