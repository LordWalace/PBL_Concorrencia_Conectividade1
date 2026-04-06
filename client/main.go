package main

import (
	"bufio"
	"encoding/json"
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
	Offline   bool      `json:"offline"`
}

var (
	latest = make([]*DeviceInfo, 0)
	mu     sync.Mutex
	notMu  sync.Mutex
	notifs = make([]string, 0)
	// pendingDevice removed: client only shows data on explicit request
	// menu display control (show for a short time)
	menuMu    sync.Mutex
	menuUntil time.Time
	showMenu  bool = true
	// connection state
	connMu    sync.Mutex
	connected bool
	// one-shot display for user-requested values
	oneShotMu    sync.Mutex
	oneShot      string
	oneShotUntil time.Time
	lastScreen   string
)

// resolveIndex aceita índices 0-based ou 1-based e retorna o índice resolvido (0-based).
// Retorna (idx, true) se válido, ou (0, false) se inválido.
func resolveIndex(raw int, listLen int) (int, bool) {
	if listLen <= 0 {
		return 0, false
	}
	if raw >= 1 && raw <= listLen {
		return raw - 1, true
	}
	if raw >= 0 && raw < listLen {
		return raw, true
	}
	return 0, false
}

// SSE clients
var (
	sseMu    sync.Mutex
	sseConns = make(map[chan string]struct{})
)

func broadcastEvent(msg string) {
	sseMu.Lock()
	defer sseMu.Unlock()
	for ch := range sseConns {
		select {
		case ch <- msg:
		default:
			// drop if client slow
		}
	}
}

const (
	colorReset  = "\033[0m"
	colorBlue   = "\033[34m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorCyan   = "\033[36m"
)

func main() {
	// Try connecting to Docker hostname first (works when client runs in container)
	gatewayHost := getenv("GATEWAY_HOST", "gateway")
	// use GATEWAY_TCP_PORT with default 8080
	gwPort := getenv("GATEWAY_TCP_PORT", "8080")
	// notify trying to connect
	pushNotif("tentando conectar ao gateway")
	addr := net.JoinHostPort(gatewayHost, gwPort)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		// fallback to GATEWAY_IP (useful when running client locally)
		gwIP := getenv("GATEWAY_IP", getenv("IP_REAL", "127.0.0.1"))
		fallbackAddr := net.JoinHostPort(gwIP, gwPort)
		conn, err = net.Dial("tcp", fallbackAddr)
		if err != nil {
			// cannot connect to gateway, exit
			return
		}
		addr = fallbackAddr
	}
	pushNotif("conectado ao gateway")
	defer conn.Close()

	// channel to receive stdin lines (reader goroutine pushes into this)
	inputCh := make(chan string, 10)

	// mark connected
	connMu.Lock()
	connected = true
	connMu.Unlock()
	// start gateway reader in background
	go func() {
		readGateway(conn)
		connMu.Lock()
		connected = false
		connMu.Unlock()
		pushNotif("desconectado do gateway")
	}()

	// start simple HTTP server to serve UI and SSE events
	uiPort := getenv("CLIENT_UI_PORT", "9000")
	go startHTTPServer(":" + uiPort)

	// stdin reader goroutine: reads lines and pushes to inputCh
	go func(ch chan<- string) {
		r := bufio.NewReader(os.Stdin)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			ch <- strings.TrimSpace(line)
		}
	}(inputCh)

	// main loop: limpa tela, desenha e processa teclado
	for {
		// clear screen
		fmt.Print("\033[H\033[2J")
		render()

		// non-blocking input processing: expect single number to toggle, or Q to quit
		select {
		case ln := <-inputCh:
			if ln == "" {
				// empty Enter: open menu for convenience in TTYs where typing may be hidden
				menuMu.Lock()
				showMenu = true
				menuUntil = time.Time{}
				menuMu.Unlock()
				continue
			} else {
				if strings.EqualFold(ln, "q") {
					return
				}
				// menu commands: help/menu, led <index> on|off, fan <index> on|off, actuators <index>, sensors <index>, clean <index>
				partsCmd := strings.Fields(ln)
				if len(partsCmd) > 0 {
					cmd := strings.ToLower(partsCmd[0])
					if cmd == "help" || cmd == "menu" {
						// show menu overlay until user closes it
						menuMu.Lock()
						showMenu = true
						menuUntil = time.Time{}
						menuMu.Unlock()
						continue
					}
					if cmd == "close" || cmd == "hide" {
						menuMu.Lock()
						showMenu = false
						menuMu.Unlock()
						continue
					}
					if cmd == "led" || cmd == "fan" || cmd == "actuators" || cmd == "sensors" || cmd == "clean" {
						// require index
						if len(partsCmd) < 2 {
							fmt.Println("use: " + cmd + " <index> [on|off]")
							continue
						}
						idxRaw, err := strconv.Atoi(partsCmd[1])
						if err != nil {
							fmt.Println("indice invalido")
							continue
						}
						mu.Lock()
						resolved, ok := resolveIndex(idxRaw, len(latest))
						if !ok {
							mu.Unlock()
							fmt.Println("indice invalido")
							continue
						}
						id := latest[resolved].ID
						mu.Unlock()
						switch cmd {
						case "led":
							if len(partsCmd) < 3 {
								fmt.Println("use: led <index> on|off")
								continue
							}
							action := strings.ToLower(partsCmd[2])
							if action == "on" {
								fmt.Fprintf(conn, "%s|ACT2_ON\n", id)
							} else {
								fmt.Fprintf(conn, "%s|ACT2_OFF\n", id)
							}
							continue
						case "fan":
							if len(partsCmd) < 3 {
								fmt.Println("use: fan <index> on|off")
								continue
							}
							action := strings.ToLower(partsCmd[2])
							if action == "on" {
								fmt.Fprintf(conn, "%s|FAN_ON\n", id)
							} else {
								fmt.Fprintf(conn, "%s|FAN_OFF\n", id)
							}
							continue
						case "actuators":
							// find device by id and show actuators
							mu.Lock()
							var d *DeviceInfo
							for _, x := range latest {
								if x.ID == id {
									d = x
									break
								}
							}
							mu.Unlock()
							if d != nil {
								oneShotMu.Lock()
								oneShot = fmt.Sprintf("Últimos atuadores de %s: %v", d.ID, d.Actuators)
								oneShotUntil = time.Now().Add(6 * time.Second)
								oneShotMu.Unlock()
							}
							continue
						case "sensors":
							// find device by id and show sensors
							mu.Lock()
							var d *DeviceInfo
							for _, x := range latest {
								if x.ID == id {
									d = x
									break
								}
							}
							mu.Unlock()
							if d != nil {
								oneShotMu.Lock()
								oneShot = fmt.Sprintf("Últimos sensores de %s - Temp: %.2f, Mem: %.2f%%", d.ID, d.Temp, d.MemoryPct)
								oneShotUntil = time.Now().Add(6 * time.Second)
								oneShotMu.Unlock()
							}
							continue
						case "clean":
							fmt.Fprintf(conn, "%s|CLEAN_MEM\n", id)
							continue
						}
					}
				}
				// if menu is open, interpret a single numeric press as "show sensors" for that index
				menuMu.Lock()
				menuOpen := showMenu
				menuMu.Unlock()

				if menuOpen {
					parts := strings.Fields(ln)
					if len(parts) > 0 {
						if act, err := strconv.Atoi(parts[0]); err == nil {
							switch act {
							case 0:
								return
							case 1:
								// show all devices values
								mu.Lock()
								if len(latest) == 0 {
									mu.Unlock()
									oneShotMu.Lock()
									oneShot = "Nenhum dispositivo disponível"
									oneShotUntil = time.Now().Add(4 * time.Second)
									oneShotMu.Unlock()
								} else {
									var sb strings.Builder
									for i, d := range latest {
										sb.WriteString(fmt.Sprintf("%d) %s - Temp: %.2f, Mem: %.2f%%, Atuadores: %v\n", i+1, d.ID, d.Temp, d.MemoryPct, d.Actuators))
									}
									mu.Unlock()
									oneShotMu.Lock()
									oneShot = sb.String()
									oneShotUntil = time.Now().Add(8 * time.Second)
									oneShotMu.Unlock()
								}
								continue
							case 2, 3, 4:
								// require device index as second token
								if len(parts) < 2 {
									oneShotMu.Lock()
									oneShot = "use: <acao> <device_index> (ex: 2 1)"
									oneShotUntil = time.Now().Add(4 * time.Second)
									oneShotMu.Unlock()
									continue
								}
								idxRaw, err := strconv.Atoi(parts[1])
								if err != nil {
									oneShotMu.Lock()
									oneShot = "indice invalido"
									oneShotUntil = time.Now().Add(4 * time.Second)
									oneShotMu.Unlock()
									continue
								}
								mu.Lock()
								resolved, ok := resolveIndex(idxRaw, len(latest))
								if !ok {
									mu.Unlock()
									oneShotMu.Lock()
									oneShot = "indice invalido"
									oneShotUntil = time.Now().Add(4 * time.Second)
									oneShotMu.Unlock()
									continue
								}
								id := latest[resolved].ID
								fanOn := latest[resolved].FanOn
								mu.Unlock()
								if act == 2 {
									if fanOn {
										fmt.Fprintf(conn, "%s|FAN_OFF\n", id)
										oneShotMu.Lock()
										oneShot = fmt.Sprintf("Enviado FAN_OFF para %s", id)
										oneShotUntil = time.Now().Add(4 * time.Second)
										oneShotMu.Unlock()
									} else {
										fmt.Fprintf(conn, "%s|FAN_ON\n", id)
										oneShotMu.Lock()
										oneShot = fmt.Sprintf("Enviado FAN_ON para %s", id)
										oneShotUntil = time.Now().Add(4 * time.Second)
										oneShotMu.Unlock()
									}
								} else if act == 3 {
									// toggle LED (actuator 2)
									mu.Lock()
									var d *DeviceInfo
									for _, x := range latest {
										if x.ID == id {
											d = x
											break
										}
									}
									mu.Unlock()
									ledOn := false
									if d != nil && len(d.Actuators) >= 2 {
										ledOn = d.Actuators[1]
									}
									if ledOn {
										fmt.Fprintf(conn, "%s|ACT2_OFF\n", id)
										oneShotMu.Lock()
										oneShot = fmt.Sprintf("Enviado ACT2_OFF para %s", id)
										oneShotUntil = time.Now().Add(4 * time.Second)
										oneShotMu.Unlock()
									} else {
										fmt.Fprintf(conn, "%s|ACT2_ON\n", id)
										oneShotMu.Lock()
										oneShot = fmt.Sprintf("Enviado ACT2_ON para %s", id)
										oneShotUntil = time.Now().Add(4 * time.Second)
										oneShotMu.Unlock()
									}
								} else if act == 4 {
									fmt.Fprintf(conn, "%s|CLEAN_MEM\n", id)
									oneShotMu.Lock()
									oneShot = fmt.Sprintf("Enviado CLEAN_MEM para %s", id)
									oneShotUntil = time.Now().Add(4 * time.Second)
									oneShotMu.Unlock()
								}
								continue
							default:
								// unknown action number
							}
						}
					}
				}

				// no implicit numeric actions: only commands trigger behavior
			}
		default:
			// nothing to read
		}

		// throttle redraws
		time.Sleep(200 * time.Millisecond)
	}
}

func readGateway(conn net.Conn) {
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			// silent on read errors (connection may oscillate) — exit quietly
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// integrator sends textual messages: TLM|T|<id>|<temp>  or STAT|<id>|1,0
		parts := strings.Split(line, "|")
		if len(parts) == 0 {
			continue
		}
		switch parts[0] {
		case "TLM":
			if len(parts) >= 4 {
				sensorType := parts[1]
				id := parts[2]
				valStr := parts[3]
				if sensorType == "T" {
					if temp, err := strconv.ParseFloat(valStr, 64); err == nil {
						mu.Lock()
						var d *DeviceInfo
						for _, x := range latest {
							if x.ID == id {
								d = x
								break
							}
						}
						if d == nil {
							d = &DeviceInfo{ID: id}
							latest = append(latest, d)
						}
						d.Temp = temp
						d.Offline = false
						d.LastSeen = time.Now()
						mu.Unlock()
						evt := map[string]interface{}{"type": "telemetry", "id": id, "temp": temp, "time": time.Now().Unix()}
						if b, err := json.Marshal(evt); err == nil {
							broadcastEvent(string(b))
						}
					}
				} else if sensorType == "M" {
					if pct, err := strconv.ParseFloat(valStr, 64); err == nil {
						mu.Lock()
						var d *DeviceInfo
						for _, x := range latest {
							if x.ID == id {
								d = x
								break
							}
						}
						if d == nil {
							d = &DeviceInfo{ID: id}
							latest = append(latest, d)
						}
						d.MemoryPct = pct
						d.Offline = false
						d.LastSeen = time.Now()
						mu.Unlock()
						evt := map[string]interface{}{"type": "memory", "id": id, "pct": pct, "time": time.Now().Unix()}
						if b, err := json.Marshal(evt); err == nil {
							broadcastEvent(string(b))
						}
					}
				}
			}
		case "STAT":
			if len(parts) >= 3 {
				id := parts[1]
				states := strings.Split(parts[2], ",")
				mu.Lock()
				var d *DeviceInfo
				for _, x := range latest {
					if x.ID == id {
						d = x
						break
					}
				}
				if d == nil {
					d = &DeviceInfo{ID: id}
					latest = append(latest, d)
				}
				acts := make([]bool, len(states))
				for i, s := range states {
					if s == "1" {
						acts[i] = true
					} else {
						acts[i] = false
					}
				}
				d.Actuators = acts
				if len(acts) > 0 {
					d.FanOn = acts[0]
				}
				d.Offline = false
				d.LastSeen = time.Now()
				mu.Unlock()
			}
		default:
			// ignore other messages for now
		}
	}
}

func readInput(conn net.Conn) {
	r := bufio.NewReader(os.Stdin)
	for {
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// expect: <index> on|off or id on|off
		parts := strings.Fields(line)
		if len(parts) < 2 {
			pushNotif("usage: <index> on|off or <id> on|off")
			continue
		}
		target := parts[0]
		cmd := strings.ToUpper(parts[1])
		var id string
		if n, err := strconv.Atoi(target); err == nil {
			mu.Lock()
			if n <= 0 || n > len(latest) {
				mu.Unlock()
				pushNotif("invalid index")
				continue
			}
			id = latest[n-1].ID
			mu.Unlock()
		} else {
			id = target
		}
		if cmd == "ON" {
			fmt.Fprintf(conn, "%s|FAN_ON\n", id)
		} else if cmd == "OFF" {
			fmt.Fprintf(conn, "%s|FAN_OFF\n", id)
		} else {
			pushNotif("unknown action, use on/off")
		}
	}
}

// startHTTPServer starts a tiny server serving the static UI and an /events SSE endpoint
func startHTTPServer(addr string) {
	// serve static files from ./static
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	http.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		// headers for SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch := make(chan string, 10)
		sseMu.Lock()
		sseConns[ch] = struct{}{}
		sseMu.Unlock()

		// remove on finish
		notify := w.(http.CloseNotifier).CloseNotify()

		for {
			select {
			case msg := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			case <-notify:
				sseMu.Lock()
				delete(sseConns, ch)
				sseMu.Unlock()
				return
			}
		}
	})

	// start
	logInfo := func(msg string) { fmt.Println(msg) }
	logInfo("Starting UI on " + addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		logInfo("UI server exited: " + err.Error())
	}
}

// pushNotif adiciona uma notificação silenciosa exibida pela função render
func pushNotif(msg string) {
	notMu.Lock()
	defer notMu.Unlock()
	// prepend
	notifs = append([]string{msg}, notifs...)
	if len(notifs) > 10 {
		notifs = notifs[:10]
	}
}

func render() {
	mu.Lock()
	list := make([]*DeviceInfo, len(latest))
	copy(list, latest)
	mu.Unlock()

	var b strings.Builder
	b.WriteString(strings.Repeat("=", 58) + "\n")
	b.WriteString("   Controle PC GAMER (pressione número para alternar)\n")
	b.WriteString(strings.Repeat("=", 58) + "\n")

	// by default do not display device list; user requests details with commands
	// no device list shown by default; client displays device data only on user request
	// show menu overlay if requested
	menuMu.Lock()
	menuOpen := showMenu
	menuMu.Unlock()
	if menuOpen {
		b.WriteString(strings.Repeat("-", 58) + "\n")
		// quick actions numbered
		b.WriteString("\n Ações rápidas:\n")
		b.WriteString("  1) Atualizar e mostrar últimos valores de todos os dispositivos\n")
		b.WriteString("  2) Alternar ventoinha (use: 2 <device_index>)\n")
		b.WriteString("  3) Alternar LED (use: 3 <device_index>)\n")
		b.WriteString("  4) Ver atuadores (use: 4 <device_index>)\n")
		b.WriteString("  5) Ver sensores (use: 5 <device_index>)\n")
		b.WriteString("  6) Limpar memória (use: 6 <device_index>)\n")
		b.WriteString("  7) Fechar menu\n")
		// list devices with numbers for quick selection
		mu.Lock()
		if len(latest) == 0 {
			b.WriteString("  (nenhum dispositivo encontrado)\n")
		} else {
			for i, d := range latest {
				off := ""
				if d.Offline {
					off = " (offline)"
				}
				b.WriteString(fmt.Sprintf("  %d) %s%s\n", i+1, d.ID, off))
			}
		}
		mu.Unlock()
	}
	// show connection status
	connMu.Lock()
	isConn := connected
	connMu.Unlock()
	if !isConn {
		b.WriteString(strings.Repeat("-", 58) + "\n")
		b.WriteString("STATUS: desconectado do gateway — verifique se o servidor está ativo\n")
	}

	// show one-shot message if any
	oneShotMu.Lock()
	if time.Now().Before(oneShotUntil) && oneShot != "" {
		b.WriteString(strings.Repeat("-", 58) + "\n")
		b.WriteString(oneShot + "\n")
	}
	oneShotMu.Unlock()

	screen := b.String()
	if screen == lastScreen {
		return
	}
	lastScreen = screen
	fmt.Print(screen)
	fmt.Println(strings.Repeat("-", 58))
	fmt.Println(" Pressione 'menu' para abrir opções, ou Q para sair")
}

func getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}
