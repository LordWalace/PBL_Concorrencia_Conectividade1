# Inicia o serviço gateway em background e mostra os logs no mesmo terminal
docker-compose up -d gateway
docker-compose logs -f gateway
