#!/bin/bash

echo "Iniciando 500 clientes..."
for i in $(seq 1 500); do
  docker run -d \
    --name iot_client_$i \
    --network iot_net \
    -e GATEWAY_HOST=iot_gateway \
    -e GATEWAY_TCP_PORT=8081 \
    -e CLIENT_UI_PORT=$((9000 + i)) \
    iot_system ./client
done

echo "500 clientes iniciados em background."