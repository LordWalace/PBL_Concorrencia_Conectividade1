#!/bin/bash

echo "Limpando rede e gateway antigos..."
docker rm -f iot_gateway 2>/dev/null
docker network rm iot_net 2>/dev/null

echo "Criando rede virtual isolada (iot_net)..."
docker network create iot_net

echo "Construindo a imagem Docker iot_system..."
docker build -t iot_system .

echo "Iniciando o Gateway..."
docker run -d \
  --name iot_gateway \
  --network iot_net \
  -e GATEWAY_UDP_PORT=8082 \
  -e GATEWAY_TCP_REG_PORT=8080 \
  -e GATEWAY_TCP_CLIENT_PORT=8081 \
  -p 8080:8080 -p 8081:8081 -p 8082:8082/udp \
  iot_system ./gateway

echo "Gateway em execucao e aguardando conexoes!"