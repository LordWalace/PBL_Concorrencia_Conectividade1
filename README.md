# 🚀 Sistema de Monitoramento de Hardware IoT

Um sistema de monitoramento e controle de dispositivos IoT construído em **Go** com **Docker**, utilizado para simular sensores de hardware (GPU, temperatura, memória) e gerenciar atuadores remotamente via TCP/UDP.

## 📋 Sumário

- [Visão Geral](#visão-geral)
- [Arquitetura](#arquitetura)
- [Pré-requisitos](#pré-requisitos)
- [Configuração](#configuração)
- [Quick Start](#quick-start)
- [Comandos Docker](#comandos-docker)
- [Como Usar](#como-usar)
- [Estrutura do Projeto](#estrutura-do-projeto)
- [Troubleshooting](#troubleshooting)

---

## 🎯 Visão Geral

Este projeto implementa um sistema cliente-servidor IoT com:

- **Gateway** — Centraliza comunicação entre múltiplos dispositivos e clientes
- **Device** — Simula sensores e atuadores, reporta telemetria via UDP
- **Client** — Dashboard interativo para monitoramento e controle

**Características principais:**
- ✅ Comunicação híbrida (UDP para telemetria, TCP para registro/controle)
- ✅ Autodescoberta (dispositivos se registram automaticamente)
- ✅ Controle remoto de atuadores
- ✅ Simulação realista de sensores (temperatura, memória, etc.)
- ✅ Containerização completa com Docker Compose

---

## 🏗️ Arquitetura

```
┌─────────────────────────────────────────────────────┐
│                    GATEWAY (Port 8080/8081/8082)    │
│  • TCP 8080  → Registro de dispositivos             │
│  • TCP 8081  → Comunicação com clientes             │
│  • UDP 8082  → Recepção de telemetria               │
└─────────────────────────────────────────────────────┘
         ▲                              ▲
         │ (TCP/UDP)                    │ (TCP)
         │                              │
    ┌────┴─────────┐            ┌──────┴─────────┐
    │   DEVICE 1   │            │    CLIENT      │
    │ (GPU_01...)  │            │  (Dashboard)   │
    │              │            │                │
    │ • TCP Ctrl   │            │ • Monitor      │
    │ • UDP Stats  │            │ • Controlar    │
    └──────────────┘            └────────────────┘
```

### Componentes

| Componente | Porta | Protocolo | Função |
|-----------|-------|-----------|--------|
| **Gateway** | 8080 (TCP) | TCP | Registro de dispositivos |
| | 8081 (TCP) | TCP | Comunicação com clientes |
| | 8082 (UDP) | UDP | Recepção de telemetria |
| **Device** | 6001–6999 | TCP | Escuta comandos de controle |
| **Client** | — | TCP | Conecta ao gateway |

---

## 📦 Pré-requisitos

- **Docker** (versão 20.10+)
- **Docker Compose** (versão 2.0+)
- **Go** 1.22+ (apenas se compilar localmente, não necessário com Docker)

### Verificar instalação

```bash
docker --version
docker compose version
```

---

## ⚙️ Configuração

### Variáveis de Ambiente (`.env`)

O arquivo [.env](.env) define as variáveis padrão do sistema:

```env
# Gateway - hostname interno (dentro do Docker, use "gateway")
GATEWAY_HOST=gateway
GATEWAY_IP=gateway
IP=172.16.201.8

# Portas do Gateway
GATEWAY_TCP_PORT=8081
GATEWAY_UDP_PORT=8082
GATEWAY_TCP_REG_PORT=8080
GATEWAY_TCP_CLIENT_PORT=8081

# Configuração do Device
DEVICE_ID=GPU_NVIDIA_3080
DEVICE_CONTROL_PORT=8083
DEVICE_HOST=device
```

**⚠️ IMPORTANTE:** Use `GATEWAY_HOST=gateway` (nome do serviço Docker) dentro dos containers!

### Personalizar Configuração

Para usar IPs locais (fora do Docker):

```bash
cp .env .env.local
# Edite .env.local com seus valores
docker compose --env-file .env.local up
```

---

## 🚀 Quick Start

### 1️⃣ Preparar o Projeto

```bash
cd ~/Downloads/PBL_Redes-Sensores
```

### 2️⃣ Build das Imagens

```bash
docker compose build
```

**Output esperado:**
```
[+] Building 15.2s (15/15) FINISHED
 => => naming to docker.io/library/pbl_device:latest
 => => naming to docker.io/library/pbl_gateway:latest
 => => naming to docker.io/library/pbl_client:latest
```

### 3️⃣ Rodar Tudo Junto

```bash
docker compose up
```

**Isto vai:**
- ✅ Iniciar Gateway
- ✅ Iniciar Device
- ✅ Iniciar Client (dashboard interativo)

**Resultado:** Você vê os logs de todos os 3 containers no terminal.

### 4️⃣ Parar os Serviços

Pressione `Ctrl+C` no terminal ou em outro terminal:

```bash
docker compose down
```

---

## 🐳 Comandos Docker

### BUILD (Compilar Imagens)

```bash
# Build padrão
docker compose build

# Build sem cache (força recompilação)
docker compose build --no-cache

# Build de um serviço específico
docker compose build gateway
docker compose build device
docker compose build client
```

### UP (Iniciar Containers)

```bash
# Iniciar tudo em foreground (vê logs)
docker compose up

# Iniciar tudo em background (-d = detach)
docker compose up -d

# Iniciar serviços específicos
docker compose up gateway
docker compose up device
docker compose up client

# Exemplo: Iniciar em terminais separados (recomendado para debug)
# Terminal 1:
docker compose up gateway

# Terminal 2:
docker compose up device

# Terminal 3:
docker compose up client
```

### EXEC (Executar Comandos)

```bash
# Entrar em um shell interativo do container
docker compose exec gateway sh
docker compose exec device sh
docker compose exec client sh

# Executar comando específico
docker compose exec device ls -la
docker compose exec gateway ps aux

# Exemplo: Rodar comando interativo
docker compose exec -it client bash
```

### LOGS (Visualizar Output)

```bash
# Todos os logs
docker compose logs

# Logs em tempo real (follow = -f)
docker compose logs -f

# Logs de um serviço específico
docker compose logs gateway
docker compose logs device -f
docker compose logs client

# Últimas 50 linhas
docker compose logs --tail=50

# Últimas 50 linhas em tempo real
docker compose logs -f --tail=50 device
```

### DOWN (Parar e Remover)

```bash
# Parar todos os containers
docker compose down

# Parar e remover volumes
docker compose down -v

# Parar e remover imagens também
docker compose down --rmi all
```

### Comandos Úteis

```bash
# Ver status de todos os containers
docker compose ps

# Reiniciar um serviço
docker compose restart gateway

# Parar um serviço (sem remover)
docker compose stop gateway

# Iniciar um serviço que estava parado
docker compose start gateway
```

---

## 💻 Como Usar

### Cenário 1: Simples - Rodar Tudo Junto

```bash
cd ~/Downloads/PBL_Redes-Sensores

# Build
docker compose build

# Rodar (ver todos os logs juntos)
docker compose up
```

**Prós:** Simples, vê tudo de uma vez  
**Contras:** Difícil debugar com muitos logs

### Cenário 2: Recomendado - Terminais Separados

**Terminal 1 - Gateway:**
```bash
docker compose up gateway
```

**Terminal 2 - Device:**
```bash
docker compose up device
```

**Terminal 3 - Client:**
```bash
docker compose up client
```

**Prós:** Vê logs separados, fácil debugar  
**Contras:** Precisa de múltiplos terminais

### Cenário 3: Background + Interagir

```bash
# Rodar tudo em background (-d = detach)
docker compose up -d

# Verificar que estão rodando
docker compose ps

# Ver logs em tempo real
docker compose logs -f

# Entrar no shell do device
docker compose exec device sh

# Dentro do shell, você pode executar qualquer comando
# Para sair:
exit

# Ver apenas logs do device
docker compose logs device --tail=30

# Parar tudo
docker compose down
```

### Cenário 4: Debug/Troubleshooting

```bash
# Ve tudo que está rodando
docker compose ps

# Ver detalhes da rede Docker
docker network ls
docker network inspect pbl_redes-sensores_iot_net

# Verificar logs do device para registros
docker compose logs gateway | grep REGISTRO

# Seguir logs em tempo real com grep (busca por erros)
docker compose logs -f | grep -i error

# Redirecionar todos os logs para arquivo
docker compose logs > output_logs.txt
```

---

## 📂 Estrutura do Projeto

```
PBL_Redes-Sensores/
├── README.md                      # Este arquivo
├── docker-compose.yml             # Orquestração dos containers
├── .env                          # Variáveis de ambiente (padrão)
├── .env.docker                   # Alternativa para Docker
│
├── gateway/
│   ├── Dockerfile
│   ├── go.mod
│   │── go.sum
│   └── main.go                   # Gateway (gerencia comunicação)
│
├── device/
│   ├── Dockerfile
│   ├── go.mod
│   │── go.sum
│   └── main.go                   # Device (simula sensores/atuadores)
│
├── client/
│   ├── Dockerfile
│   ├── go.mod
│   │── go.sum
│   └── main.go                   # Client (dashboard CLI)
│
└── scripts/
    ├── build_and_up.sh           # Build + Up automatizado
    ├── run_500_clients.sh        # Rodar 500 clients para teste
    ├── run_500_devices.sh        # Rodar 500 devices para teste
    ├── run_manual_test.sh        # Teste manual
    ├── run_view_logs.sh          # Visualizar logs
    └── stop_all.sh               # Parar tudo
```

---

## 🔍 Troubleshooting

### ❌ "Gateway desconectado"

**Problema:** Client mostra "Gateway desconectado"

**Solução:**
```bash
# Verificar se gateway está rodando
docker compose ps

# Checar logs do gateway
docker compose logs gateway

# Reiniciar gateway
docker compose restart gateway
```

### ❌ Device não aparece no Client

**Problema:** Device não aparece na lista de dispositivos

**Solução:**
```bash
# Verificar se device está registrando
docker compose logs device | grep REGISTRO

# Verificar variáveis de ambiente do device
docker compose exec device env | grep GATEWAY

# Deve mostrar GATEWAY_HOST=gateway
```

### ❌ Porta já em uso

**Problema:** `Error: bind: address already in use`

**Solução (Linux/Mac):**
```bash
# Encontrar processo usando a porta
lsof -i :8080

# Matar o processo
sudo kill -9 <PID>
```

**Solução (Windows):**
```bash
netstat -ano | findstr :8080
taskkill /PID <PID> /F
```

### ❌ Conexão recusada entre containers

**Problema:** Device não consegue conectar ao gateway

**Solução:**
```bash
# Verificar se GATEWAY_HOST está correto
cat .env | grep GATEWAY_HOST

# Deve ser "gateway", NÃO um IP
# Se estiver IP, edite o .env

# Testar conectividade entre containers
docker compose exec device ping gateway

# Se der erro, reinicie tudo
docker compose down
docker compose up
```

### 🔧 Ver Logs Detalhados

```bash
# Todos os logs com timestamp
docker compose logs --timestamps

# Buscar apenas erros
docker compose logs | grep -i error

# Salvar logs em arquivo
docker compose logs > all_logs_$(date +%Y%m%d_%H%M%S).txt
```

---

## 📝 Exemplos Completos

### Exemplo 1: Execução Básica Completa

```bash
#Entrar na pasta
cd ~/Downloads/PBL_Redes-Sensores

# Build (primeira vez)
docker compose build

# Rodar em foreground
docker compose up

# Ctrl+C para parar

# Limpar
docker compose down
```

### Exemplo 2: Debug com 3 Terminais

```bash
# Terminal 1:
docker compose up gateway

# Terminal 2:
docker compose up device

# Terminal 3:
docker compose up client

# Para parar em qualquer terminal:
docker compose down
```

### Exemplo 3: Rodar em Background + Logs

```bash
# Rodar tudo em background
docker compose up -d

# Aguardar 2 segundos para inicializar
sleep 2

# Ver status
docker compose ps

# Ver logs em tempo real
docker compose logs -f

# Em outro terminal, parar
docker compose down
```

---

## 🛠️ Manutenção

### Limpar Completamente

```bash
# Parar todos os containers
docker compose down

# Remover volumes também
docker compose down -v

# Remover imagens também
docker compose down --rmi all
```

### Atualizar Código

```bash
# Parar tudo
docker compose down

# Fazer alterações nos arquivos .go

# Rebuild sem cache
docker compose build --no-cache

# Rodar novamente
docker compose up
```

### Backup de Logs

```bash
# Salvar logs com timestamp
docker compose logs > logs_backup_$(date +%Y%m%d_%H%M%S).txt

# Conferir tamanho
ls -lh logs_backup_*.txt
```

---

## ✅ Checklist de Primeira Execução

Garanta que:

- [ ] Docker está instalado: `docker --version`
- [ ] Docker Compose está instalado: `docker compose version`
- [ ] Você está na pasta correta: `pwd` (deve ser `...PBL_Redes-Sensores`)
- [ ] Arquivo `.env` existe e tem `GATEWAY_HOST=gateway`
- [ ] Executou `docker compose build`
- [ ] Executou `docker compose up`
- [ ] Gateway iniciou sem erro (procure por "Gateway starting")
- [ ] Device registrou no gateway (procure por "REGISTRO" nos logs)
- [ ] Client conectou ao gateway (procure por "conectado ao gateway")

Se tudo OK, você pode usar o dashboard do client via CLI.

---

## 💡 Dicas Rápidas

| Tarefa | Comando |
|--------|---------|
| Build | `docker compose build` |
| Iniciar tudo | `docker compose up` |
| Background | `docker compose up -d` |
| Logs | `docker compose logs -f` |
| Logs de um serviço | `docker compose logs -f device` |
| Parar | `docker compose down` |
| Shell do container | `docker compose exec device sh` |
| Restart | `docker compose restart` |
| Ver containers | `docker compose ps` |

---

## 📞 Suporte

Se algo não funcionar:

1. **Verifique os logs:** `docker compose logs -f`
2. **Verifique o `.env`:** Confirme que `GATEWAY_HOST=gateway`
3. **Restart:** `docker compose restart`
4. **Rebuild:** `docker compose build --no-cache && docker compose up`
5. **Limpar:** `docker compose down -v && docker compose up`

---

## 📊 Versões

- **Go:** 1.22
- **Docker:** 20.10+
- **Docker Compose:** 2.0+
- **Última atualização:** Abril 2026

---

**Pronto para começar?**

```bash
docker compose build && docker compose up
```