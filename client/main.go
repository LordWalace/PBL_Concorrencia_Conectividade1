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
	selectedID   string
	// gateway connection (protected by connMu)
	gwConn net.Conn
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
	// smoothing factor for telemetry (EMA). Lower -> slower changes.
	tempAlpha = 0.25
	memAlpha  = 0.25
	// consider device offline if no updates within this duration
	offlineTimeout = 15 * time.Second
)

// isDeviceOffline returns true if device has no recent LastSeen timestamp
func isDeviceOffline(d *DeviceInfo) bool {
	if d == nil {
		return true
	}
	if d.LastSeen.IsZero() {
		return true
	}
	return time.Since(d.LastSeen) > offlineTimeout
}

// formatActuators returns a human-friendly string for a slice of actuator states.
func formatActuators(acts []bool) string {
	if acts == nil || len(acts) == 0 {
		return "[]"
	}
	var sb strings.Builder
	sb.WriteString("[")
	for i, a := range acts {
		if i > 0 {
			sb.WriteString(" ")
		}
		if a {
			sb.WriteString(fmt.Sprintf("A%d:ON", i+1))
		} else {
			sb.WriteString(fmt.Sprintf("A%d:OFF", i+1))
		}
	}
	sb.WriteString("]")
	return sb.String()
}

func main() {
	// gateway host/port
	gatewayHost := getenv("GATEWAY_HOST", "gateway")
	// use GATEWAY_TCP_PORT with default 8080
	gwPort := getenv("GATEWAY_TCP_PORT", "8080")

	// channel to receive stdin lines (reader goroutine pushes into this)
	inputCh := make(chan string, 10)

	// start connection manager
	go func() {
		connectAddr := net.JoinHostPort(gatewayHost, gwPort)
		backoff := 1 * time.Second
		for {
			pushNotif("tentando conectar ao gateway")
			conn, err := net.Dial("tcp", connectAddr)
			if err != nil {
				// try fallback GATEWAY_IP
				gwIP := getenv("GATEWAY_IP", getenv("IP_REAL", "127.0.0.1"))
				fallback := net.JoinHostPort(gwIP, gwPort)
				conn, err = net.Dial("tcp", fallback)
				if err != nil {
					pushNotif("falha ao conectar ao gateway, tentando novamente...")
					time.Sleep(backoff)
					if backoff < 30*time.Second {
						backoff *= 2
					}
					continue
				}
				connectAddr = fallback
			}
			// reset backoff
			backoff = 1 * time.Second
			// set global connection
			connMu.Lock()
			gwConn = conn
			connected = true
			connMu.Unlock()
			pushNotif("conectado ao gateway")
			// block reading until connection ends
			readGateway(conn)
			// readGateway returned -> connection lost
			connMu.Lock()
			connected = false
			if gwConn != nil {
				gwConn.Close()
				gwConn = nil
			}
			connMu.Unlock()
			pushNotif("desconectado do gateway, reconectando...")
			time.Sleep(2 * time.Second)
		}
	}()

	// connection manager handles connecting and reading from gateway

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

	// desenho inicial
	render()

	// main loop: processa teclado e atualiza tela apenas quando houver comando
	for {
		ln := <-inputCh
		if ln == "" {
			// empty Enter: open menu for convenience in TTYs where typing may be hidden
			menuMu.Lock()
			showMenu = true
			menuUntil = time.Time{}
			menuMu.Unlock()
			render()
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
						render()
						continue
					}
					id := latest[resolved].ID
					selectedID = id
					mu.Unlock()
					switch cmd {
					case "led":
						if len(partsCmd) < 3 {
							fmt.Println("use: led <index> on|off")
							continue
						}
						action := strings.ToLower(partsCmd[2])
						if action == "on" {
							if err := sendToGateway("%s|ACT2_ON\n", id); err != nil {
								fmt.Printf("[ERR] envio falhou: %v\n", err)
							} else {
								fmt.Printf("[LOG] LED ON enviado para: %s\n", id)
							}
							oneShotMu.Lock()
							oneShot = fmt.Sprintf("✓ LED ON enviado para %s", id)
							oneShotUntil = time.Now().Add(3 * time.Second)
							oneShotMu.Unlock()
							render()
						} else {
							if err := sendToGateway("%s|ACT2_OFF\n", id); err != nil {
								fmt.Printf("[ERR] envio falhou: %v\n", err)
							} else {
								fmt.Printf("[LOG] LED OFF enviado para: %s\n", id)
							}
							oneShotMu.Lock()
							oneShot = fmt.Sprintf("✓ LED OFF enviado para %s", id)
							oneShotUntil = time.Now().Add(3 * time.Second)
							oneShotMu.Unlock()
							render()
						}
						continue
					case "fan":
						if len(partsCmd) < 3 {
							fmt.Println("use: fan <index> on|off")
							continue
						}
						action := strings.ToLower(partsCmd[2])
						if action == "on" {
							if err := sendToGateway("%s|FAN_ON\n", id); err != nil {
								fmt.Printf("[ERR] envio falhou: %v\n", err)
							} else {
								fmt.Printf("[LOG] FAN ON enviado para: %s\n", id)
							}
							oneShotMu.Lock()
							oneShot = fmt.Sprintf("✓ Ventoinha ligada para %s", id)
							oneShotUntil = time.Now().Add(3 * time.Second)
							oneShotMu.Unlock()
							render()
						} else {
							if err := sendToGateway("%s|FAN_OFF\n", id); err != nil {
								fmt.Printf("[ERR] envio falhou: %v\n", err)
							} else {
								fmt.Printf("[LOG] FAN OFF enviado para: %s\n", id)
							}
							oneShotMu.Lock()
							oneShot = fmt.Sprintf("✓ Ventoinha desligada para %s", id)
							oneShotUntil = time.Now().Add(3 * time.Second)
							oneShotMu.Unlock()
							render()
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
							oneShot = fmt.Sprintf("Últimos atuadores de %s: %s", d.ID, formatActuators(d.Actuators))
							oneShotUntil = time.Now().Add(6 * time.Second)
							oneShotMu.Unlock()
						}
						render()
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
						render()
						continue
					case "clean":
						if err := sendToGateway("%s|CLEAN_MEM\n", id); err != nil {
							fmt.Printf("[ERR] envio falhou: %v\n", err)
						} else {
							fmt.Printf("[LOG] CLEAN_MEM enviado para: %s\n", id)
						}
						oneShotMu.Lock()
						oneShot = fmt.Sprintf("✓ Limpeza de memória solicitada para %s", id)
						oneShotUntil = time.Now().Add(3 * time.Second)
						oneShotMu.Unlock()
						render()
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
								render()
							} else {
								var b strings.Builder
								for i, d := range latest {
									if isDeviceOffline(d) {
										b.WriteString(fmt.Sprintf("%d) %s - OFFLINE\n", i+1, d.ID))
									} else {
										b.WriteString(fmt.Sprintf("%d) %s - Temp: %.2f, Mem: %.2f%%, Atuadores: %s\n", i+1, d.ID, d.Temp, d.MemoryPct, formatActuators(d.Actuators)))
									}
								}
								if len(latest) > 0 {
									selectedID = latest[0].ID
								}
								mu.Unlock()
								oneShotMu.Lock()
								oneShot = b.String()
								oneShotUntil = time.Now().Add(8 * time.Second)
								oneShotMu.Unlock()
								render()
							}
							continue
						case 2, 3, 4, 5, 6:
							// behavior:
							// - 2: toggle FAN for selected device (or first) when no index
							// - 3: toggle LED for selected device (or first) when no index
							// - 4: show actuators for all devices when no index, or single device if index provided
							// - 5: show sensors for all devices when no index, or single device if index provided
							// - 6: clean memory for selected device (or index if provided)
							var resolved int
							var id string
							var deviceInfo *DeviceInfo
							// helpers to resolve by index or selectedID
							resolveSelected := func() (int, string, bool) {
								mu.Lock()
								defer mu.Unlock()
								if selectedID == "" {
									if len(latest) == 0 {
										return 0, "", false
									}
									return 0, latest[0].ID, true
								}
								for i, x := range latest {
									if x.ID == selectedID {
										return i, x.ID, true
									}
								}
								if len(latest) == 0 {
									return 0, "", false
								}
								return 0, latest[0].ID, true
							}

							// Case 4 and 5: show all when no index
							if act == 4 {
								if len(parts) < 2 {
									// show actuators for all devices
									mu.Lock()
									if len(latest) == 0 {
										mu.Unlock()
										oneShotMu.Lock()
										oneShot = "Nenhum dispositivo encontrado"
										oneShotUntil = time.Now().Add(4 * time.Second)
										oneShotMu.Unlock()
										continue
									}
									var sb strings.Builder
									for i, d := range latest {
										if isDeviceOffline(d) {
											sb.WriteString(fmt.Sprintf("%d) %s - Atuadores: OFFLINE\n", i+1, d.ID))
										} else {
											sb.WriteString(fmt.Sprintf("%d) %s - Atuadores: %s\n", i+1, d.ID, formatActuators(d.Actuators)))
										}
									}
									mu.Unlock()
									oneShotMu.Lock()
									oneShot = sb.String()
									oneShotUntil = time.Now().Add(8 * time.Second)
									oneShotMu.Unlock()
									render()
									continue
								}
							}
							if act == 5 {
								if len(parts) < 2 {
									// show sensors for all devices
									mu.Lock()
									if len(latest) == 0 {
										mu.Unlock()
										oneShotMu.Lock()
										oneShot = "Nenhum dispositivo encontrado"
										oneShotUntil = time.Now().Add(4 * time.Second)
										oneShotMu.Unlock()
										continue
									}
									var sb strings.Builder
									for i, d := range latest {
										if isDeviceOffline(d) {
											sb.WriteString(fmt.Sprintf("%d) %s - SENSORES OFFLINE\n", i+1, d.ID))
										} else {
											sb.WriteString(fmt.Sprintf("%d) %s - Temp: %.2f°C, Mem: %.2f%%\n", i+1, d.ID, d.Temp, d.MemoryPct))
										}
									}
									mu.Unlock()
									oneShotMu.Lock()
									oneShot = sb.String()
									oneShotUntil = time.Now().Add(8 * time.Second)
									oneShotMu.Unlock()
									render()
									continue
								}
							}

							// For actions that require a target device (2,3,6) or when index provided for 4,5
							if len(parts) < 2 {
								// use selected device
								idx, sid, ok := resolveSelected()
								if !ok {
									oneShotMu.Lock()
									oneShot = "Nenhum dispositivo disponível"
									oneShotUntil = time.Now().Add(4 * time.Second)
									oneShotMu.Unlock()
									continue
								}
								resolved = idx
								id = sid
							} else {
								idxRaw, err := strconv.Atoi(parts[1])
								if err != nil {
									oneShotMu.Lock()
									oneShot = "indice invalido"
									oneShotUntil = time.Now().Add(4 * time.Second)
									oneShotMu.Unlock()
									continue
								}
								mu.Lock()
								r, ok := resolveIndex(idxRaw, len(latest))
								if !ok {
									mu.Unlock()
									oneShotMu.Lock()
									oneShot = "indice invalido"
									oneShotUntil = time.Now().Add(4 * time.Second)
									oneShotMu.Unlock()
									continue
								}
								resolved = r
								id = latest[resolved].ID
								selectedID = id
								mu.Unlock()
							}

							mu.Lock()
							deviceInfo = latest[resolved]
							mu.Unlock()

							if act == 2 {
								// toggle FAN
								fanOn := false
								if deviceInfo != nil {
									fanOn = deviceInfo.FanOn
								}
								if fanOn {
									if err := sendToGateway("%s|FAN_OFF\n", id); err != nil {
										fmt.Printf("[ERR] envio falhou: %v\n", err)
									} else {
										fmt.Printf("[LOG] FAN OFF enviado para: %s (via menu)\n", id)
									}
									oneShotMu.Lock()
									oneShot = fmt.Sprintf("✓ Ventoinha desligada para %s", id)
									oneShotUntil = time.Now().Add(3 * time.Second)
									oneShotMu.Unlock()
									render()
								} else {
									if err := sendToGateway("%s|FAN_ON\n", id); err != nil {
										fmt.Printf("[ERR] envio falhou: %v\n", err)
									} else {
										fmt.Printf("[LOG] FAN ON enviado para: %s (via menu)\n", id)
									}
									oneShotMu.Lock()
									oneShot = fmt.Sprintf("✓ Ventoinha ligada para %s", id)
									oneShotUntil = time.Now().Add(3 * time.Second)
									oneShotMu.Unlock()
									render()
								}
							} else if act == 3 {
								// toggle LED (actuator 2)
								ledOn := false
								if deviceInfo != nil && len(deviceInfo.Actuators) >= 2 {
									ledOn = deviceInfo.Actuators[1]
								}
								if ledOn {
									if err := sendToGateway("%s|ACT2_OFF\n", id); err != nil {
										fmt.Printf("[ERR] envio falhou: %v\n", err)
									} else {
										fmt.Printf("[LOG] LED OFF enviado para: %s (via menu)\n", id)
									}
									oneShotMu.Lock()
									oneShot = fmt.Sprintf("✓ LED desligado para %s", id)
									oneShotUntil = time.Now().Add(3 * time.Second)
									oneShotMu.Unlock()
									render()
								} else {
									if err := sendToGateway("%s|ACT2_ON\n", id); err != nil {
										fmt.Printf("[ERR] envio falhou: %v\n", err)
									} else {
										fmt.Printf("[LOG] LED ON enviado para: %s (via menu)\n", id)
									}
									oneShotMu.Lock()
									oneShot = fmt.Sprintf("✓ LED ligado para %s", id)
									oneShotUntil = time.Now().Add(3 * time.Second)
									oneShotMu.Unlock()
									render()
								}
							} else if act == 4 {
								// show actuators for selected device (index provided) — already handled 'all' above
								if deviceInfo != nil {
									if isDeviceOffline(deviceInfo) {
										oneShotMu.Lock()
										oneShot = fmt.Sprintf("%s está offline — sem dados de atuadores", deviceInfo.ID)
										oneShotUntil = time.Now().Add(6 * time.Second)
										oneShotMu.Unlock()
									} else {
										oneShotMu.Lock()
										oneShot = fmt.Sprintf("Atuadores de %s: %s", deviceInfo.ID, formatActuators(deviceInfo.Actuators))
										oneShotUntil = time.Now().Add(6 * time.Second)
										oneShotMu.Unlock()
									}
								}
								render()
							} else if act == 5 {
								// show sensors for selected device (index provided)
								if deviceInfo != nil {
									if isDeviceOffline(deviceInfo) {
										oneShotMu.Lock()
										oneShot = fmt.Sprintf("%s está offline — sem dados de sensores", deviceInfo.ID)
										oneShotUntil = time.Now().Add(6 * time.Second)
										oneShotMu.Unlock()
									} else {
										oneShotMu.Lock()
										oneShot = fmt.Sprintf("Sensores de %s - Temp: %.2f°C, Memória: %.2f%%", deviceInfo.ID, deviceInfo.Temp, deviceInfo.MemoryPct)
										oneShotUntil = time.Now().Add(6 * time.Second)
										oneShotMu.Unlock()
									}
								}
								render()
							} else if act == 6 {
								// clean memory for selected device
								if err := sendToGateway("%s|CLEAN_MEM\n", id); err != nil {
									fmt.Printf("[ERR] envio falhou: %v\n", err)
								} else {
									fmt.Printf("[LOG] CLEAN_MEM enviado para: %s (via menu)\n", id)
								}
								oneShotMu.Lock()
								oneShot = fmt.Sprintf("✓ Limpeza de memória solicitada para %s", id)
								oneShotUntil = time.Now().Add(3 * time.Second)
								oneShotMu.Unlock()
								render()
							}
							continue
							continue
						case 7:
							// close menu
							menuMu.Lock()
							showMenu = false
							menuMu.Unlock()
							render()
							continue
						default:
							// unknown action number
						}
					}
				}
			}

			// no implicit numeric actions: only commands trigger behavior
		}
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
						// apply exponential moving average to smooth rapid changes
						if d.LastSeen.IsZero() {
							d.Temp = temp
						} else {
							d.Temp = d.Temp*(1.0-tempAlpha) + temp*tempAlpha
						}
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
						// smooth memory percentage as well
						if d.LastSeen.IsZero() {
							d.MemoryPct = pct
						} else {
							d.MemoryPct = d.MemoryPct*(1.0-memAlpha) + pct*memAlpha
						}
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

// sendToGateway writes a formatted line to the current gateway connection.
// Returns error if not connected or write fails.
func sendToGateway(format string, a ...interface{}) error {
	connMu.Lock()
	defer connMu.Unlock()
	if gwConn == nil {
		return errors.New("not connected to gateway")
	}
	_, err := fmt.Fprintf(gwConn, format, a...)
	return err
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
			if err := sendToGateway("%s|FAN_ON\n", id); err != nil {
				pushNotif("failed to send FAN_ON: " + err.Error())
			}
		} else if cmd == "OFF" {
			if err := sendToGateway("%s|FAN_OFF\n", id); err != nil {
				pushNotif("failed to send FAN_OFF: " + err.Error())
			}
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

	if selectedID == "" && len(list) > 0 {
		selectedID = list[0].ID
	}

	var b strings.Builder
	for i, d := range latest {
		if isDeviceOffline(d) {
			b.WriteString(fmt.Sprintf("%d) %s - OFFLINE\n", i+1, d.ID))
		} else {
			b.WriteString(fmt.Sprintf("%d) %s - Temp: %.2f, Mem: %.2f%%, Atuadores: %s\n", i+1, d.ID, d.Temp, d.MemoryPct, formatActuators(d.Actuators)))
		}
	}

	// Linha-resumo do dispositivo selecionado com os últimos valores registrados.
	if selectedID != "" {
		var sel *DeviceInfo
		for _, d := range list {
			if d.ID == selectedID {
				sel = d
				break
			}
		}
		if sel != nil {
			led := "OFF"
			if len(sel.Actuators) > 1 && sel.Actuators[1] {
				led = "ON"
			}
			fan := "OFF"
			if sel.FanOn {
				fan = "ON"
			}
			b.WriteString(fmt.Sprintf("[DEVICE %s] Temp: %.2f°C  Mem: %.2f%%  Fan:%s  LED (%s)\n", sel.ID, sel.Temp, sel.MemoryPct, fan, led))
		} else {
			b.WriteString(fmt.Sprintf("[DEVICE %s] sem dados recentes\n", selectedID))
		}
	} else {
		b.WriteString("[DEVICE] sem dispositivos registrados\n")
	}

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
		b.WriteString("  0) Sair da aplicação\n")
		b.WriteString("  1) Mostrar todos os dispositivos e seus valores\n")
		b.WriteString("  2) Alternar ventoinha (ex: 2 1)\n")
		b.WriteString("  3) Alternar LED (ex: 3 1)\n")
		b.WriteString("  4) Ver atuadores do dispositivo (ex: 4 1)\n")
		b.WriteString("  5) Ver sensores do dispositivo (ex: 5 1)\n")
		b.WriteString("  6) Limpar memória (ex: 6 1)\n")
		b.WriteString("  7) Fechar este menu\n")

		// list devices with numbers for quick selection
		b.WriteString(" Dispositivos registrados:\n")
		mu.Lock()
		if len(latest) == 0 {
			b.WriteString("  (Nenhum dispositivo encontrado)\n")
		} else {
			for i, d := range latest {
				off := ""
				if d.Offline {
					off = " (Offline)"
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
	lastScreen = screen
	// Limpeza forte para evitar "rastros" em terminais com TTY/attach.
	fmt.Print("\033[2J\033[H\033[3J")
	fmt.Print(screen)
	fmt.Println(strings.Repeat("-", 58))
	if !menuOpen {
		fmt.Println(" Digite 'menu' para abrir opções, 'q' para sair")
	} else {
		fmt.Println(" Digite '7' para fechar, 'q' para sair")
	}
}

func getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}
