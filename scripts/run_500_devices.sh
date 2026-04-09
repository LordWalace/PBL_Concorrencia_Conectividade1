#!/bin/bash

echo "Iniciando 500 dispositivos..."
for i in $(seq 1 500); do
  docker run -d \
    --name iot_device_$i \
    --network iot_net \
    -e GATEWAY_HOST=iot_gateway \
    -e GATEWAY_TCP_REG_PORT=8080 \
    -e GATEWAY_UDP_PORT=8082 \
    -e DEVICE_ID="GPU_$i" \
    -e DEVICE_CONTROL_PORT=8083 \
    -e DEVICE_ACT_COUNT=3 \
    iot_system ./device
done

echo "500 dispositivos iniciados em background."