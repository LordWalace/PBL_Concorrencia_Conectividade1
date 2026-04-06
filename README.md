**Projeto PBL: Sistema de Monitoramento de Hardware IoT**

**Sobre**
- **Descrição:** Sistema em Go que simula sensores/atuadores (ex.: GPU) reportando telemetria e recebendo comandos via sockets nativos (UDP/TCP). O Gateway centraliza lógica de automação e roteamento.
- **Diferenciais:** comunicação híbrida (UDP para telemetria, TCP para registro/controle), descoberta ativa (sensores se registram) e controle centralizado.

**Arquitetura**
- **Gateway:** Serviço de integração que gerencia estado e regras (veja [gateway/main.go](gateway/main.go#L1)).
- **Device(s):** Sensores simulados em `device/` (cópias podem ser criadas por sensor). Ex.: [device/main.go](device/main.go#L1).
- **Client:** Dashboard CLI em [client/main.go](client/main.go#L1) que consome o estado do Gateway.

**Configuração (variáveis)**
- `GATEWAY_HOST` — host do gateway (usado em containers: `gateway`).
- `GATEWAY_UDP_PORT` — porta UDP de telemetria (padrão: `8082`).
- `GATEWAY_TCP_REG_PORT` — porta TCP para registro/controle (padrão: `8080`).
- `GATEWAY_TCP_CLIENT_PORT` — porta TCP para clientes (padrão: `8081`).
- `DEVICE_ID` — identificador único do sensor (ex.: `GPU_NVIDIA_3080`).
- `DEVICE_CONTROL_PORT` — porta TCP onde o device escuta comandos (ex.: `8083`).
Veja o arquivo na raiz: [.env](.env).

**Como executar (local/com Docker)**
- Pré-requisitos: `docker` e `docker compose` instalados.
- Build + run (na raiz do projeto):

```powershell
docker compose build
docker compose up
```

- Rodar serviços isolados (em terminais separados):

```powershell
cd 'C:\Users\Walace\Documents\PBL_Redes-SensoresAAAAAAAAAAAAAAA'
# Gateway
docker compose up gateway
# Device (GPU)
docker compose up device
# Client
docker compose up client
```

**Execução em terminais separados (exemplo solicitado)**

- Objetivo: executar os 4 primeiros containers em um único terminal (para acompanhar logs conjuntos)
  e executar o 5º container em outro terminal separado.

- Observação: adapte os nomes dos serviços abaixo para os que constam em seu `docker-compose.yml`.

Exemplo genérico (substitua `service1..service5` pelos nomes reais):

```powershell
# PBL — Sistema IoT (resumo)

Projeto em Go: dispositivos simulados enviam telemetria (UDP) e recebem comandos (TCP); o `gateway` centraliza regras de automação e expõe estado para clientes.

## Rápido (pré-requisitos)
- Docker e Docker Compose instalados.
- Configure `.env` na raiz (ports, DEVICE_ID, etc.).

## Comandos essenciais

Build e subir todos (detach):

```powershell
docker compose build
docker compose up -d
```

Parar e limpar:

```powershell
docker compose down
```

## Executar 4 serviços em um terminal e 1 em outro

Terminal A (ver logs de 4 serviços):

```powershell
docker compose up gateway device device_cpu client
```

Terminal B (5º serviço isolado):

```powershell
docker compose up device_ssd
```

Para rodar em background, adicione `-d` após `up`.

## Notas rápidas
- `docker-compose.yml` define os serviços e portas mapeadas (ex.: `8080`..`8083`).
- Mensagens: telemetria = JSON UDP; registro/control = JSON/TCP.
- Para rodar o client no host (fora do Docker) use `GATEWAY_IP=127.0.0.1` em `.env` e garanta que as portas estão mapeadas.

Se quiser, eu adiciono exemplos prontos de `device_cpu` e `device_ssd` no `docker-compose.yml` para testar múltiplos sensores localmente.

---

COMANDOS PRONTOS (copie e cole no PowerShell)

1) Entrar na pasta do projeto
```powershell
cd C:\Users\Walace\Documents\PBL_Redes-SensoresAAAAAAAAAAAAAAA
```

2) Permitir execução temporária de scripts
```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
```

3) Gerenciador interativo (menu)
```powershell
.\scripts\manage.ps1
# No menu: 1=start all | 2=gateway | 3=start devices | 4=client | 5=logs gateway | 6=logs devices | 7=attach client | 8=scale devices | 9=stop all
```

4) Comandos diretos (exemplos)
- Iniciar todos com 3 devices:
```powershell
.\scripts\start_all.ps1 -DeviceCount 3
```
- Escalar devices para 10:
```powershell
.\scripts\start_devices.ps1 -Count 10
```
- Iniciar gateway:
```powershell
.\scripts\start_gateway.ps1
```
- Iniciar client e anexar:
```powershell
.\scripts\start_client.ps1
```
- Parar tudo:
```powershell
.\scripts\stop_all.ps1
```

5) Testes e métricas
- Teste de escala (ex.: até 50 em passos de 10):
```powershell
.\scripts\scale_test.ps1 -Max 50 -Step 10 -WaitSeconds 10
```
- Coleta automática de métricas (interativo):
```powershell
.\scripts\auto_scale_metrics.ps1
# responda: quantos sensores, quantas amostras (ex:12), intervalo (ex:5)
```

6) Docker (se preferir usar docker compose direto)
- Ver containers ativos:
```powershell
docker ps
```
- Ver logs do gateway:
```powershell
docker-compose logs -f gateway
# ou, se usar Docker CLI v2:
docker compose logs -f gateway
```

Observações:
- Para desanexar do `docker attach client` sem parar o container: `Ctrl+P` `Ctrl+Q`.
- Se houver erro de conexão com o daemon, abra o Docker Desktop e aguarde o daemon iniciar; se usar WSL2, `wsl --shutdown` e reabra o Docker.

Cole estes comandos no PowerShell da outra máquina; se surgir erro cole a saída aqui que eu ajudo.