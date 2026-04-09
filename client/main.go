package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
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
	lastScreen   string
	selectedID   string
	gwConn       net.Conn
	sseMu        sync.Mutex
	sseConns     = make(map[chan string]struct{})
)

const (
	colorReset     = "\033[0m"
	colorBlue      = "\033[34m"
	colorYellow    = "\033[33m"
	colorRed       = "\033[31m"
	colorGreen     = "\033[32m"
	colorCyan      = "\033[36m"
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

func broadcastEvent(msg string) {
	sseMu.Lock()
	defer sseMu.Unlock()
	for ch := range sseConns {
		select {
		case ch <- msg:
		default:
		}
	}
}

func isDeviceOffline(d *DeviceInfo) bool {
	if d == nil { return true }
	return d.Offline || d.LastSeen.IsZero() || time.Since(d.LastSeen) > offlineTimeout
}

func formatActuators(acts []bool) string {
	if len(acts) == 0 { return "[]" }
	var sb strings.Builder
	sb.WriteString("[")
	for i, a := range acts {
		if i > 0 { sb.WriteString(" ") }
		if a { sb.WriteString(fmt.Sprintf("A%d:ON", i+1)) } else { sb.WriteString(fmt.Sprintf("A%d:OFF", i+1)) }
	}
	sb.WriteString("]")
	return sb.String()
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
	gwPort := getenv("GATEWAY_TCP_PORT", "8080")
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

	uiPort := getenv("CLIENT_UI_PORT", "9000")
	go startHTTPServer(":" + uiPort)

	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for range t.C {
			changed := false
			mu.Lock()
			for _, d := range latest {
				was, now := d.Offline, isDeviceOffline(d)
				if was != now { d.Offline = now; changed = true }
			}
			mu.Unlock()
			if changed { render() }
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
				menuMu.Lock(); showMenu = true; menuUntil = time.Time{}; menuMu.Unlock(); continue
			}
			if cmd == "close" || cmd == "hide" {
				menuMu.Lock(); showMenu = false; menuMu.Unlock(); continue
			}
			
			menuMu.Lock()
			menuOpen := showMenu
			menuMu.Unlock()

			if cmd == "led" || cmd == "fan" || cmd == "actuators" || cmd == "sensors" || cmd == "clean" {
				if len(partsCmd) < 2 {
					fmt.Println("use: " + cmd + " <index> [on|off]")
					continue
				}
				idxRaw, err := strconv.Atoi(partsCmd[1])
				if err != nil { fmt.Println("indice invalido"); continue }
				
				mu.Lock()
				resolved, ok := resolveIndex(idxRaw, len(latest))
				if !ok { mu.Unlock(); fmt.Println("indice invalido"); continue }
				id := latest[resolved].ID
				selectedID = id
				mu.Unlock()

				action := ""
				if len(partsCmd) >= 3 { action = strings.ToUpper(partsCmd[2]) }

				switch cmd {
				case "led":
					if action == "ON" { executeCommand(id, "ACT2_ON", "LED ligado", "LED ja estava ON") } else { executeCommand(id, "ACT2_OFF", "LED desligado", "LED ja estava OFF") }
				case "fan":
					if action == "ON" { executeCommand(id, "FAN_ON", "Ventoinha ligada", "Ventoinha ja estava ON") } else { executeCommand(id, "FAN_OFF", "Ventoinha desligada", "Ventoinha ja estava OFF") }
				case "clean":
					executeCommand(id, "CLEAN_MEM", "Limpeza de memoria iniciada", "Limpeza ja em andamento")
				case "actuators", "sensors":
					showDeviceInfo(id, cmd)
				}
				continue
			}

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
					selectedID = id
					mu.Unlock()

					switch act {
					case 1:
						powerOn := len(deviceInfo.Actuators) >= 3 && deviceInfo.Actuators[2]
						if powerOn { executeCommand(id, "ACT3_OFF", "POWER desligado", "POWER ja estava OFF") } else { executeCommand(id, "ACT3_ON", "POWER ligado", "POWER ja estava ON") }
					case 2: showAllDevices()
					case 3:
						if deviceInfo.FanOn { executeCommand(id, "FAN_OFF", "Ventoinha desligada", "Ventoinha ja estava OFF") } else { executeCommand(id, "FAN_ON", "Ventoinha ligada", "Ventoinha ja estava ON") }
					case 4:
						ledOn := len(deviceInfo.Actuators) >= 2 && deviceInfo.Actuators[1]
						if ledOn { executeCommand(id, "ACT2_OFF", "LED desligado", "LED ja estava OFF") } else { executeCommand(id, "ACT2_ON", "LED ligado", "LED ja estava ON") }
					case 5: showDeviceInfo(id, "actuators")
					case 6: showDeviceInfo(id, "sensors")
					case 7: executeCommand(id, "CLEAN_MEM", "Limpeza de memoria iniciada", "Limpeza ja em andamento")
					}
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
		fmt.Printf("[ERR] Falha ao enviar %s: %v\n", cmdStr, err)
		setOneShot("ERRO: Falha na comunicacao com o gateway.", 4)
		return
	} 
	
	if resp == "OK" || strings.HasPrefix(resp, "ACK") {
		fmt.Printf("[LOG] %s confirmado para: %s\n", cmdStr, id)
		setOneShot(fmt.Sprintf("Sucesso: %s para %s", successMsg, id), 3)
	} else if resp == "NOOP" {
		fmt.Printf("[LOG] %s: %s\n", alreadyMsg, id)
		setOneShot(fmt.Sprintf("Aviso: %s para %s", alreadyMsg, id), 3)
	} else {
		fmt.Printf("[LOG] Resposta: %s\n", resp)
		setOneShot(fmt.Sprintf("Resposta do servidor: %s", resp), 3)
	}
}

func showDeviceInfo(id, infoType string) {
	mu.Lock()
	var d *DeviceInfo
	for _, x := range latest {
		if x.ID == id { d = x; break }
	}
	mu.Unlock()

	if d != nil {
		if isDeviceOffline(d) {
			setOneShot(fmt.Sprintf("ERRO: %s esta offline — dados indisponiveis", d.ID), 5)
		} else {
			if infoType == "actuators" {
				setOneShot(fmt.Sprintf("Atuadores de %s: %s", d.ID, formatActuators(d.Actuators)), 6)
			} else if infoType == "sensors" {
				setOneShot(fmt.Sprintf("Sensores de %s - Temp: %.2f°C, Mem: %.2f%%", d.ID, d.Temp, d.MemoryPct), 6)
			}
		}
	}
}

func showAllDevices() {
	mu.Lock()
	if len(latest) == 0 {
		mu.Unlock()
		setOneShot("Nenhum dispositivo registrado", 4)
		return
	}
	var b strings.Builder
	for i, d := range latest {
		off := ""
		if d.Offline { off = " - OFFLINE" }
		b.WriteString(fmt.Sprintf("%d) %s%s\n", i+1, d.ID, off))
	}
	mu.Unlock()
	setOneShot(b.String(), 8)
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
				render()
			}
		case "RESP":
			if len(parts) >= 3 {
				id, payload := parts[1], strings.Join(parts[2:], "|")
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
				created := false
				if d == nil {
					d = &DeviceInfo{ID: id}
					latest = append(latest, d)
					created = true
				}
				prevOffline := d.Offline
				
				if sensorType == "T" {
					if d.LastSeen.IsZero() { d.Temp = val } else { d.Temp = d.Temp*(1.0-tempAlpha) + val*tempAlpha }
					broadcastEvent(string(mustMarshal(map[string]interface{}{"type": "telemetry", "id": id, "temp": val, "time": time.Now().Unix()})))
				} else if sensorType == "M" {
					if d.LastSeen.IsZero() { d.MemoryPct = val } else { d.MemoryPct = d.MemoryPct*(1.0-memAlpha) + val*memAlpha }
					broadcastEvent(string(mustMarshal(map[string]interface{}{"type": "memory", "id": id, "pct": val, "time": time.Now().Unix()})))
				}
				d.Offline = false
				d.LastSeen = time.Now()
				mu.Unlock()
				if created || prevOffline { render() }
			}
		case "STAT":
			if len(parts) >= 3 {
				id, states := parts[1], strings.Split(parts[2], ",")
				mu.Lock()
				var d *DeviceInfo
				for _, x := range latest { if x.ID == id { d = x; break } }
				created := false
				if d == nil { d = &DeviceInfo{ID: id}; latest = append(latest, d); created = true }
				prevOffline := d.Offline
				
				acts := make([]bool, len(states))
				for i, s := range states { acts[i] = (s == "1") }
				d.Actuators = acts
				if len(acts) > 0 { d.FanOn = acts[0] }
				d.Offline = false
				d.LastSeen = time.Now()
				mu.Unlock()
				if created || prevOffline { render() }
			}
		case "CLEAN":
			if len(parts) >= 3 {
				id, val := parts[1], parts[2]
				mu.Lock()
				var d *DeviceInfo
				for _, x := range latest { if x.ID == id { d = x; break } }
				created := false
				if d == nil { d = &DeviceInfo{ID: id}; latest = append(latest, d); created = true }
				
				prev := d.Cleaning
				d.Cleaning = (val == "1" || val == "true")
				d.Offline = false
				d.LastSeen = time.Now()
				mu.Unlock()
				if created || prev != d.Cleaning { render() }
			}
		}
	}
}

func mustMarshal(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
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
	case <-time.After(3 * time.Second):
		pendingMu.Lock(); delete(pending, id); pendingMu.Unlock()
		return "", errors.New("timeout aguardando resposta")
	}
}

func startHTTPServer(addr string) {
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	http.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok { http.Error(w, "Streaming unsupported", 500); return }
		
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch := make(chan string, 10)
		sseMu.Lock(); sseConns[ch] = struct{}{}; sseMu.Unlock()
		notify := w.(http.CloseNotifier).CloseNotify()

		for {
			select {
			case msg := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			case <-notify:
				sseMu.Lock(); delete(sseConns, ch); sseMu.Unlock()
				return
			}
		}
	})

	fmt.Println("Starting UI on " + addr)
	http.ListenAndServe(addr, nil)
}

func render() {
	mu.Lock()
	list := make([]*DeviceInfo, len(latest))
	copy(list, latest)
	mu.Unlock()

	if selectedID == "" && len(list) > 0 { selectedID = list[0].ID }

	var b strings.Builder
	if len(list) == 0 {
		b.WriteString("  (Nenhum dispositivo registrado)\n")
	} else {
		for i, d := range list {
			off, powerMarker := "", ""
			if isDeviceOffline(d) { off = " - OFFLINE" }
			if len(d.Actuators) >= 3 && !d.Actuators[2] { powerMarker = " - POWER OFF" }
			b.WriteString(fmt.Sprintf("%d) %s%s%s\n", i+1, d.ID, off, powerMarker))
		}
	}

	if selectedID != "" {
		var sel *DeviceInfo
		for _, d := range list { if d.ID == selectedID { sel = d; break } }
		if sel != nil {
			if isDeviceOffline(sel) {
				b.WriteString(fmt.Sprintf("[DEVICE %s] OFFLINE — Sem dados (ultimo: %s)\n", sel.ID, sel.LastSeen.Format("15:04:05")))
			} else {
				led, vent, ledColor := "OFF", "OFF", ""
				if len(sel.Actuators) > 1 && sel.Actuators[1] { led = "ON" }
				if sel.FanOn { vent = "ON" }
				
				// A cor so muda se o LED estiver fisicamente ligado (ON)
				if led == "ON" {
					if sel.Cleaning {
						ledColor = colorBlue
					} else if sel.MemoryPct >= 90.0 { 
						ledColor = colorRed 
					} else if sel.MemoryPct <= 50.0 { 
						ledColor = colorGreen 
					} else { 
						ledColor = colorYellow 
					}
				}
				
				// Se a ledColor estiver vazia (porque o LED esta OFF), ele usa o texto normal sem formatacao de cor
				if ledColor == "" {
					b.WriteString(fmt.Sprintf("[DEVICE %s] Temp: %.2f°C  Mem: %.2f%%  Ventoinha:%s  LED (%s)\n", sel.ID, sel.Temp, sel.MemoryPct, vent, led))
				} else {
					b.WriteString(fmt.Sprintf("[DEVICE %s] Temp: %.2f°C  Mem: %.2f%%  Ventoinha:%s  %sLED%s (%s)\n", sel.ID, sel.Temp, sel.MemoryPct, vent, ledColor, colorReset, led))
				}
			}
		}
	}

	menuMu.Lock()
	menuOpen := showMenu
	menuMu.Unlock()
	if menuOpen {
		b.WriteString(strings.Repeat("-", 58) + "\n")
		b.WriteString("\n Acoes rapidas:\n")
		b.WriteString("  0) Sair da aplicacao\n")
		b.WriteString("  1) Ligar/Desligar GPU (POWER)\n")
		b.WriteString("  2) Mostrar todos os dispositivos\n")
		b.WriteString("  3) Ligar/Desligar ventoinha (ex: 3 1)\n")
		b.WriteString("  4) Ligar/Desligar LED (ex: 4 1)\n")
		b.WriteString("  5) Listar atuadores (ex: 5 1)\n")
		b.WriteString("  6) Listar sensores (ex: 6 1)\n")
		b.WriteString("  7) Limpar memoria (ex: 7 1)\n")
	}

	connMu.Lock()
	isConn := connected
	connMu.Unlock()
	
	b.WriteString(strings.Repeat("-", 58) + "\n")
	if isConn { b.WriteString("STATUS: Conectado ao gateway\n") } else { b.WriteString("STATUS: Desconectado do gateway\n") }

	oneShotMu.Lock()
	if time.Now().Before(oneShotUntil) && oneShot != "" {
		b.WriteString(strings.Repeat("-", 58) + "\n" + oneShot + "\n")
	}
	oneShotMu.Unlock()

	fmt.Print("\033[2J\033[H\033[3J")
	fmt.Print(b.String())
	fmt.Println(strings.Repeat("-", 58))
}