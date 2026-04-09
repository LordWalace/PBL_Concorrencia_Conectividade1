package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DeviceInfo struct {
	ID        string    `json:"id"`
	IP        string    `json:"ip"`
	Port      int       `json:"port"`
	LastSeen  time.Time `json:"last_seen"`
	Temp      float64   `json:"temp"`
	MemoryPct float64   `json:"memory_pct"`
	FanOn     bool      `json:"fan_on"`
	Actuators []bool    `json:"actuators,omitempty"`
	Cleaning  bool      `json:"cleaning"`
	Offline   bool      `json:"offline"`
}

var (
	latest       = make([]*DeviceInfo, 0)
	mu           sync.Mutex
	notMu        sync.Mutex
	notifs       = make([]string, 0)
	menuMu       sync.Mutex
	menuUntil    time.Time
	showMenu     bool = true
	connMu       sync.Mutex
	connected    bool
	pendingMu    sync.Mutex
	pending      = make(map[string]chan string)
	oneShotMu    sync.Mutex
	oneShot      string
	oneShotUntil time.Time
	gwConn       net.Conn
)

const (
	colorReset     = "\033[0m"
	colorBlue      = "\033[34m"
	colorYellow    = "\033[33m"
	colorRed       = "\033[31m"
	colorGreen     = "\033[32m"
	tempAlpha      = 0.25
	memAlpha       = 0.25
	offlineTimeout = 5 * time.Second
)

func resolveIndex(raw int, listLen int) (int, bool) {
	if listLen <= 0 { return 0, false }
	if raw >= 1 && raw <= listLen { return raw - 1, true }
	if raw >= 0 && raw < listLen { return raw, true }
	return 0, false
}

func isDeviceOffline(d *DeviceInfo) bool {
	if d == nil { return true }
	return d.Offline || d.LastSeen.IsZero() || time.Since(d.LastSeen) > offlineTimeout
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" { return v }
	return def
}

func pushNotif(msg string) {
	notMu.Lock()
	defer notMu.Unlock()
	notifs = append([]string{msg}, notifs...)
	if len(notifs) > 10 { notifs = notifs[:10] }
}

func main() {
	gatewayHost := getenv("GATEWAY_HOST", "gateway")
	gwPort := getenv("GATEWAY_TCP_CLIENT_PORT", "8081")
	inputCh := make(chan string, 10)

	go func() {
		connectAddr := net.JoinHostPort(gatewayHost, gwPort)
		retryInterval := 5 * time.Second
		for {
			pushNotif("tentando conectar ao gateway")
			conn, err := net.Dial("tcp", connectAddr)
			if err != nil {
				gwIP := getenv("GATEWAY_IP", getenv("IP_REAL", "127.0.0.1"))
				fallback := net.JoinHostPort(gwIP, gwPort)
				conn, err = net.Dial("tcp", fallback)
				if err != nil {
					pushNotif("falha ao conectar ao gateway, tentando novamente...")
					time.Sleep(retryInterval)
					continue
				}
				connectAddr = fallback
			}
			connMu.Lock()
			gwConn = conn
			connected = true
			connMu.Unlock()
			pushNotif("conectado ao gateway")
			
			render()
			readGateway(conn)
			
			connMu.Lock()
			connected = false
			if gwConn != nil { gwConn.Close(); gwConn = nil }
			connMu.Unlock()
			pushNotif("desconectado do gateway, reconectando...")
			
			render()
			time.Sleep(retryInterval)
		}
	}()

	go func(ch chan<- string) {
		r := bufio.NewReader(os.Stdin)
		for {
			if line, err := r.ReadString('\n'); err == nil {
				ch <- strings.TrimSpace(line)
			} else { return }
		}
	}(inputCh)

	render()

	for {
		ln := <-inputCh
		
		if ln == "" {
			menuMu.Lock(); showMenu = true; menuUntil = time.Time{}; menuMu.Unlock()
			render()
			continue
		}
		
		if strings.EqualFold(ln, "q") { return }

		partsCmd := strings.Fields(ln)
		if len(partsCmd) > 0 {
			cmd := strings.ToLower(partsCmd[0])
			
			if cmd == "help" || cmd == "menu" {
				menuMu.Lock(); showMenu = true; menuUntil = time.Time{}; menuMu.Unlock()
				render()
				continue
			}
			if cmd == "close" || cmd == "hide" {
				menuMu.Lock(); showMenu = false; menuMu.Unlock()
				render()
				continue
			}
			
			menuMu.Lock()
			menuOpen := showMenu
			menuMu.Unlock()

			if menuOpen {
				if act, err := strconv.Atoi(partsCmd[0]); err == nil {
					if act == 0 { return }
					
					idxRaw := 1
					if len(partsCmd) >= 2 {
						if n, e := strconv.Atoi(partsCmd[1]); e == nil { idxRaw = n }
					}
					
					mu.Lock()
					resolved, ok := resolveIndex(idxRaw, len(latest))
					if !ok && len(latest) > 0 { resolved = 0; ok = true }
					if !ok {
						mu.Unlock()
						setOneShot("Nenhum dispositivo disponivel", 4)
						continue
					}
					id := latest[resolved].ID
					deviceInfo := latest[resolved]
					mu.Unlock()

					switch act {
					case 1:
						powerOn := len(deviceInfo.Actuators) >= 3 && deviceInfo.Actuators[2]
						if powerOn { executeCommand(id, "ACT3_OFF", "POWER desligado", "POWER ja estava OFF") } else { executeCommand(id, "ACT3_ON", "POWER ligado", "POWER ja estava ON") }
					case 2:
						setOneShot("Lista dos dispositivos ja visivel acima.", 3)
					case 3:
						if deviceInfo.FanOn { executeCommand(id, "FAN_OFF", "Ventoinha desligada", "Ventoinha ja estava OFF") } else { executeCommand(id, "FAN_ON", "Ventoinha ligada", "Ventoinha ja estava ON") }
					case 4:
						ledOn := len(deviceInfo.Actuators) >= 2 && deviceInfo.Actuators[1]
						if ledOn { executeCommand(id, "ACT2_OFF", "LED desligado", "LED ja estava OFF") } else { executeCommand(id, "ACT2_ON", "LED ligado", "LED ja estava ON") }
					case 5:
						setOneShot("Informacoes de atuadores ja estao visiveis na lista principal.", 4)
					case 6:
						setOneShot("Informacoes de sensores ja estao visiveis na lista principal.", 4)
					case 7:
						executeCommand(id, "CLEAN_MEM", "Limpeza de memoria iniciada", "Limpeza ja em andamento")
					}
				} else {
					setOneShot("Comando invalido", 3)
				}
			}
		}
	}
}

func executeCommand(id, cmdStr, successMsg, alreadyMsg string) {
	connMu.Lock()
	isConn := connected
	connMu.Unlock()

	if !isConn {
		setOneShot("ERRO: Gateway desconectado. Comando abortado.", 4)
		return
	}

	mu.Lock()
	var dptr *DeviceInfo
	for _, x := range latest {
		if x.ID == id { dptr = x; break }
	}
	mu.Unlock()

	if isDeviceOffline(dptr) {
		setOneShot(fmt.Sprintf("ERRO: %s esta offline. Comando abortado.", id), 4)
		return
	}

	resp, err := sendToGateway("%s|%s\n", id, cmdStr)
	if err != nil {
		setOneShot(fmt.Sprintf("ERRO: Falha na comunicacao: %v", err), 4)
		return
	} 
	
	if resp == "OK" || strings.HasPrefix(resp, "ACK") {
		setOneShot(fmt.Sprintf("Sucesso: %s para %s", successMsg, id), 3)
	} else if resp == "NOOP" {
		setOneShot(fmt.Sprintf("Aviso: %s para %s", alreadyMsg, id), 3)
	} else if strings.HasPrefix(resp, "ERRO_GATEWAY:") {
		setOneShot(fmt.Sprintf("Gateway Recusou: %s", resp), 5)
	} else {
		setOneShot(fmt.Sprintf("Resposta do servidor: %s", resp), 3)
	}
}

func setOneShot(msg string, seconds int) {
	oneShotMu.Lock()
	oneShot = msg
	oneShotUntil = time.Now().Add(time.Duration(seconds) * time.Second)
	oneShotMu.Unlock()
	render()
}

func readGateway(conn net.Conn) {
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil { return }
		line = strings.TrimSpace(line)
		if line == "" { continue }
		
		parts := strings.Split(line, "|")
		if len(parts) == 0 { continue }
		
		switch parts[0] {
		case "OFFLINE":
			if len(parts) >= 2 {
				id := parts[1]
				mu.Lock()
				for _, x := range latest {
					if x.ID == id {
						x.Offline = true
						x.LastSeen = time.Time{}
						break
					}
				}
				mu.Unlock()
			}
		case "RESP", "ERR":
			if len(parts) >= 3 {
				id, payload := parts[1], strings.Join(parts[2:], "|")
				if parts[0] == "ERR" { payload = "ERRO_GATEWAY: " + payload }

				pendingMu.Lock()
				if ch, ok := pending[id]; ok {
					select { case ch <- payload: default: }
					delete(pending, id)
				}
				pendingMu.Unlock()
			}
		case "TLM":
			if len(parts) >= 4 {
				sensorType, id, valStr := parts[1], parts[2], parts[3]
				val, err := strconv.ParseFloat(valStr, 64)
				if err != nil { continue }
				
				mu.Lock()
				var d *DeviceInfo
				for _, x := range latest { if x.ID == id { d = x; break } }
				if d == nil {
					d = &DeviceInfo{ID: id}
					latest = append(latest, d)
				}
				
				if sensorType == "T" {
					if d.LastSeen.IsZero() { d.Temp = val } else { d.Temp = d.Temp*(1.0-tempAlpha) + val*tempAlpha }
				} else if sensorType == "M" {
					if d.LastSeen.IsZero() { d.MemoryPct = val } else { d.MemoryPct = d.MemoryPct*(1.0-memAlpha) + val*memAlpha }
				}
				d.Offline = false
				d.LastSeen = time.Now()
				mu.Unlock()
			}
		case "STAT":
			if len(parts) >= 3 {
				id, states := parts[1], strings.Split(parts[2], ",")
				mu.Lock()
				var d *DeviceInfo
				for _, x := range latest { if x.ID == id { d = x; break } }
				if d == nil { d = &DeviceInfo{ID: id}; latest = append(latest, d) }
				
				acts := make([]bool, len(states))
				for i, s := range states { acts[i] = (s == "1") }
				d.Actuators = acts
				if len(acts) > 0 { d.FanOn = acts[0] }
				d.Offline = false
				d.LastSeen = time.Now()
				mu.Unlock()
			}
		case "CLEAN":
			if len(parts) >= 3 {
				id, val := parts[1], parts[2]
				mu.Lock()
				var d *DeviceInfo
				for _, x := range latest { if x.ID == id { d = x; break } }
				if d == nil { d = &DeviceInfo{ID: id}; latest = append(latest, d) }
				
				d.Cleaning = (val == "1" || val == "true")
				d.Offline = false
				d.LastSeen = time.Now()
				mu.Unlock()
			}
		}
	}
}

func sendToGateway(format string, a ...interface{}) (string, error) {
	connMu.Lock()
	if gwConn == nil { connMu.Unlock(); return "", errors.New("nao conectado") }
	line := fmt.Sprintf(format, a...)
	
	id := ""
	if parts := strings.SplitN(line, "|", 2); len(parts) >= 1 { id = strings.TrimSpace(parts[0]) }
	
	var ch chan string
	if id != "" {
		ch = make(chan string, 1)
		pendingMu.Lock(); pending[id] = ch; pendingMu.Unlock()
	}
	
	_, err := fmt.Fprintf(gwConn, "%s", line)
	connMu.Unlock()
	if err != nil {
		if id != "" { pendingMu.Lock(); delete(pending, id); pendingMu.Unlock() }
		return "", err
	}
	if ch == nil { return "", nil }
	
	select {
	case resp := <-ch: return resp, nil
	case <-time.After(10 * time.Second):
		pendingMu.Lock(); delete(pending, id); pendingMu.Unlock()
		return "", errors.New("timeout aguardando resposta da rede")
	}
}

func render() {
	mu.Lock()
	list := make([]*DeviceInfo, len(latest))
	copy(list, latest)
	mu.Unlock()

	var b strings.Builder

	// ISSO AQUI APAGA O TERMINAL INTEIRO PARA NAO REPETIR A LISTA
	b.WriteString("\033[2J\033[H") 

	b.WriteString(fmt.Sprintf(" Total de dispositivos na rede: %d\n", len(list)))
	b.WriteString("----------------------------------------------------------\n")

	if len(list) == 0 {
		b.WriteString("  (Nenhum dispositivo registrado)\n")
	} else {
		b.WriteString("  [ Painel de Controle - 10 primeiros dispositivos ]\n")
		for i, d := range list {
			if i >= 10 {
				b.WriteString(fmt.Sprintf("  ... (+ %d ocultos em background para performance)\n", len(list)-10))
				break
			}
			
			if isDeviceOffline(d) {
				b.WriteString(fmt.Sprintf("  %d)[DEVICE %s] OFFLINE — Sem dados\n", i+1, d.ID))
			} else {
				led, vent, powerMarker := "OFF", "OFF", ""
				if len(d.Actuators) > 1 && d.Actuators[1] { led = "ON" }
				if d.FanOn { vent = "ON" }
				if len(d.Actuators) >= 3 && !d.Actuators[2] { powerMarker = " [POWER OFF]" }
				
				ledDisplay := "LED (OFF)"
				if led == "ON" {
					ledColor := colorYellow
					if d.Cleaning {
						ledColor = colorBlue
					} else if d.MemoryPct >= 90.0 { 
						ledColor = colorRed 
					} else if d.MemoryPct <= 50.0 { 
						ledColor = colorGreen 
					}
					// APLICA A COR APENAS AQUI SE ESTIVER LIGADO
					ledDisplay = fmt.Sprintf("%sLED (ON)%s", ledColor, colorReset)
				}
				
				// FORMATO EXATO SOLICITADO (com cores aplicadas no LED)
				b.WriteString(fmt.Sprintf("  %d)[DEVICE %s] Temp: %.2f°C  Mem: %.2f%%  Ventoinha:%s  %s%s\n", 
					i+1, d.ID, d.Temp, d.MemoryPct, vent, ledDisplay, powerMarker))
			}
		}
	}

	b.WriteString("----------------------------------------------------------\n\n")

	menuMu.Lock()
	menuOpen := showMenu
	menuMu.Unlock()
	
	if menuOpen {
		b.WriteString(" Acoes rapidas:\n")
		b.WriteString("  0) Sair da aplicacao\n")
		b.WriteString("  1) Ligar/Desligar GPU (POWER)\n")
		b.WriteString("  2) Mostrar todos os dispositivos\n")
		b.WriteString("  3) Ligar/Desligar ventoinha (ex: 3 1)\n")
		b.WriteString("  4) Ligar/Desligar LED (ex: 4 1)\n")
		b.WriteString("  5) Listar atuadores (ex: 5 1)\n")
		b.WriteString("  6) Listar sensores (ex: 6 1)\n")
		b.WriteString("  7) Limpar memoria (ex: 7 1)\n")
	}

	b.WriteString("----------------------------------------------------------\n")

	connMu.Lock()
	isConn := connected
	connMu.Unlock()
	
	if isConn { 
		b.WriteString("Status: Conectado ao gateway\n") 
	} else { 
		b.WriteString("Status: Desconectado do gateway\n") 
	}

	oneShotMu.Lock()
	if time.Now().Before(oneShotUntil) && oneShot != "" {
		b.WriteString(oneShot + "\n")
	}
	oneShotMu.Unlock()

	b.WriteString("Digite o comando: \n")

	fmt.Print(b.String())
}