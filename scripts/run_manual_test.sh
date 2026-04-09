#!/bin/bash

echo "Iniciando 4 Dispositivos em background..."
for i in $(seq 1 4); do
  docker run -d --name iot_device_$i --network iot_net \
    -e GATEWAY_HOST=iot_gateway -e GATEWAY_TCP_REG_PORT=8080 -e GATEWAY_UDP_PORT=8082 \
    -e DEVICE_ID="GPU_$i" -e DEVICE_CONTROL_PORT=8083 -e DEVICE_ACT_COUNT=3 \
    iot_system ./device
done

echo "Aguardando dispositivos subirem..."
sleep 2

echo "Tentando abrir terminais interativos separados para 4 Clientes..."

for i in $(seq 1 4); do
  PORT=$((9000 + i))
  # Este e o comando exato que roda o cliente interativamente
  CMD="docker run -it --rm --name iot_client_$i --network iot_net -e GATEWAY_HOST=iot_gateway -e GATEWAY_TCP_PORT=8081 -e CLIENT_UI_PORT=$PORT iot_system ./client"

  # Detecta o sistema operacional/terminal para abrir a nova janela
  if command -v gnome-terminal &> /dev/null; then
      # Ubuntu / Linux com GNOME
      gnome-terminal --title="Cliente $i" -- bash -c "$CMD; exec bash"
  elif command -v xterm &> /dev/null; then
      # Fallback Linux genérico
      xterm -title "Cliente $i" -e "$CMD; bash" &
  elif [[ "$OSTYPE" == "darwin"* ]]; then
      # macOS
      osascript -e "tell app \"Terminal\" to do script \"$CMD\""
  else
      # Se não conseguir abrir janelas automaticamente, imprime os comandos para o usuário
      if [ $i -eq 1 ]; then
          echo "Aviso: Nao foi possivel abrir novas janelas automaticamente neste sistema."
          echo "Por favor, abra 4 abas no seu terminal e copie e cole um comando em cada aba:"
          echo "---------------------------------------------------"
      fi
      echo "Aba $i: $CMD"
  fi
done

echo "Ambiente de teste manual configurado!"