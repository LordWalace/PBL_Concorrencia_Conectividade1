#!/bin/bash

echo "Tentando abrir terminais separados para os logs..."

if command -v gnome-terminal &> /dev/null; then
    # Para Ubuntu / distribuições com GNOME
    gnome-terminal --title="Gateway Log" -- bash -c "docker logs -f iot_gateway; exec bash"
    gnome-terminal --title="Device 1 Log" -- bash -c "docker logs -f iot_device_1; exec bash"
    gnome-terminal --title="Client 1 Log" -- bash -c "docker logs -f iot_client_1; exec bash"
elif command -v xterm &> /dev/null; then
    # Fallback genérico para Linux
    xterm -title "Gateway Log" -e "docker logs -f iot_gateway; bash" &
    xterm -title "Device 1 Log" -e "docker logs -f iot_device_1; bash" &
    xterm -title "Client 1 Log" -e "docker logs -f iot_client_1; bash" &
elif [[ "$OSTYPE" == "darwin"* ]]; then
    # Para macOS
    osascript -e 'tell app "Terminal" to do script "docker logs -f iot_gateway"'
    osascript -e 'tell app "Terminal" to do script "docker logs -f iot_device_1"'
    osascript -e 'tell app "Terminal" to do script "docker logs -f iot_client_1"'
else
    echo "Gerenciador de terminal automatico nao suportado."
    echo "Para ver os logs manualmente, abra 3 abas no seu terminal e digite:"
    echo "Aba 1: docker logs -f iot_gateway"
    echo "Aba 2: docker logs -f iot_device_1"
    echo "Aba 3: docker logs -f iot_client_1"
fi