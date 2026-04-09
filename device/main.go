package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RegMessage struct {
	ID          string `json:"id"`
	ControlPort int    `json:"control_port"`
}

var (
	actuators      []bool
	actuatorsMu    sync.Mutex
	gatewayUDPAddr string
	memoryPct      float64
	memoryMu       sync.Mutex
	cleaning       bool
	powered        bool = false
	poweredMu      sync.Mutex
)

func main() {
	baseID := getenv("DEVICE_ID", "GPU_01")

	// ==============================================================
	// CORRECAO: Garante que cada um dos 500 devices tenha um ID unico
	// Pegando os 4 ultimos caracteres do hostname do Docker
	// ==============================================================
	hostname, err := os.Hostname()
	deviceID := baseID
	if err == nil && len(hostname) >= 4 {
		deviceID = fmt.Sprintf("%s_%s", baseID, hostname[len(hostname)-4:])
	} else {
		deviceID = fmt.Sprintf("%s_%d", baseID, time.Now().UnixNano()%1000)
	}

	controlPortStr := getenv("DEVICE_CONTROL_PORT", "6001")
	controlPort, _ := strconv.Atoi(controlPortStr)
	gatewayHost := getenv("GATEWAY_HOST", "gateway")
	gatewayRegPort := getenv("GATEWAY_TCP_REG_PORT", "5002")
	gatewayUDPPort := getenv("GATEWAY_UDP_PORT", "5001")

	gatewayUDPAddr = net.JoinHostPort(gatewayHost, gatewayUDPPort)

	actCountStr := getenv("DEVICE_ACT_COUNT", "3")
	actCount, _ := strconv.Atoi(actCountStr)
	if actCount < 3 {
		actCount = 3
	}

	actuatorsMu.Lock()
	actuators = make([]bool, actCount)
	actuatorsMu.Unlock()

	log.Printf("Device %s starting -> gateway %s", deviceID, gatewayHost)

	safeGo(func() {
		for {
			err := registerToGateway(deviceID, gatewayHost, gatewayRegPort, controlPortStr)
			if err != nil {
				if gwIP := getenv("GATEWAY_IP", ""); gwIP != "" {
					errFallback := registerToGateway(deviceID, gwIP, gatewayRegPort, controlPortStr)
					if errFallback == nil {
						log.Printf("Reconectado/Registrado no gateway (fallback) com sucesso!")
					}
				}
			}
			time.Sleep(3 * time.Second)
		}
	})

	safeGo(func() { startControlListener(controlPort, &deviceID) })

	safeGo(func() {
		for {
			telemetryLoop(deviceID, gatewayHost, gatewayUDPPort)
			time.Sleep(1 * time.Second)
		}
	})

	select {}
}

func safeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic recovered: %v", r)
			}
		}()
		fn()
	}()
}

func getFanState() bool { return getActuatorState(0) }
func getActuatorState(idx int) bool {
	actuatorsMu.Lock()
	defer actuatorsMu.Unlock()
	if idx < 0 || idx >= len(actuators) {
		return false
	}
	return actuators[idx]
}

func setActuatorState(idx int, v bool, deviceID string) bool {
	actuatorsMu.Lock()
	changed := false
	if idx >= 0 && idx < len(actuators) {
		if actuators[idx] != v {
			actuators[idx] = v
			changed = true
		}
	}
	snap := make([]bool, len(actuators))
	copy(snap, actuators)
	actuatorsMu.Unlock()

	if deviceID == "" || !changed {
		return changed
	}

	parts := make([]string, len(snap))
	for i, b := range snap {
		if b {
			parts[i] = "1"
		} else {
			parts[i] = "0"
		}
	}
	statLine := fmt.Sprintf("STAT|%s|%s\n", deviceID, strings.Join(parts, ","))

	if addr, err := net.ResolveUDPAddr("udp", gatewayUDPAddr); err == nil {
		if conn, err := net.DialUDP("udp", nil, addr); err == nil {
			conn.Write([]byte(statLine))
			conn.Close()
		}
	}
	return changed
}

func startControlListener(port int, deviceID *string) {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		log.Fatalf("control listener err: %v", err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		safeGo(func() { handleControlConn(conn, deviceID) })
	}
}

func handleControlConn(conn net.Conn, deviceID *string) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		cmd := strings.TrimSpace(line)
		if cmd == "" {
			continue
		}

		var changed bool
		var resp string = "ERR\n"

		switch {
		case cmd == "FAN_ON", cmd == "AC_LIGAR":
			changed = setActuatorState(0, true, *deviceID)
			if changed {
				resp = "OK\n"
			} else {
				resp = "NOOP\n"
			}
		case cmd == "FAN_OFF", cmd == "AC_DESLIGAR":
			changed = setActuatorState(0, false, *deviceID)
			if changed {
				resp = "OK\n"
			} else {
				resp = "NOOP\n"
			}
		case strings.HasPrefix(cmd, "ACT") && (strings.HasSuffix(cmd, "_ON") || strings.HasSuffix(cmd, "_OFF")):
			parts := strings.Split(strings.TrimPrefix(cmd, "ACT"), "_")
			if idx, err := strconv.Atoi(parts[0]); err == nil {
				isOn := parts[1] == "ON"
				changed = setActuatorState(idx-1, isOn, *deviceID)
				if changed && idx == 3 {
					poweredMu.Lock()
					powered = isOn
					poweredMu.Unlock()
				}
				if changed {
					resp = "OK\n"
				} else {
					resp = "NOOP\n"
				}
			}
		case cmd == "CLEAN_MEM":
			safeGo(func() { runMemoryClean(*deviceID) })
			resp = "OK\n"
		}

		conn.Write([]byte(resp))
	}
}

func runMemoryClean(deviceID string) {
	memoryMu.Lock()
	if cleaning {
		memoryMu.Unlock()
		return
	}
	cleaning = true
	memoryMu.Unlock()

	notifyGateway(fmt.Sprintf("CLEAN|%s|1\n", deviceID))

	for {
		memoryMu.Lock()
		if memoryPct <= 0 {
			memoryPct = 0
			memoryMu.Unlock()
			notifyGateway(fmt.Sprintf("CLEAN|%s|0\n", deviceID))
			memoryMu.Lock()
			cleaning = false
			memoryMu.Unlock()
			break
		}
		memoryPct -= 5.0
		if memoryPct < 0 {
			memoryPct = 0
		}
		memoryMu.Unlock()
		time.Sleep(500 * time.Millisecond)
	}
}

func notifyGateway(msg string) {
	addr, err := net.ResolveUDPAddr("udp", gatewayUDPAddr)
	if err == nil {
		if c, err := net.DialUDP("udp", nil, addr); err == nil {
			c.Write([]byte(msg))
			c.Close()
		}
	}
}

func registerToGateway(deviceID, gatewayHost, gatewayRegPort, controlPort string) error {
	regAddr := net.JoinHostPort(gatewayHost, gatewayRegPort)
	conn, err := net.DialTimeout("tcp", regAddr, 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = conn.Write([]byte(fmt.Sprintf("REG|AC|%s|%s\n", deviceID, controlPort)))
	if err != nil {
		return err
	}

	resp, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(resp, "ACK") {
		return fmt.Errorf("unexpected response: %s", resp)
	}

	return nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func telemetryLoop(deviceID, gatewayHost, gatewayUDPPort string) {
	var localConn *net.UDPConn
	backoff := 1 * time.Second
	var remoteAddr *net.UDPAddr

	for {
		var err error
		remoteAddr, err = net.ResolveUDPAddr("udp", net.JoinHostPort(gatewayHost, gatewayUDPPort))
		if err != nil {
			time.Sleep(backoff)
			continue
		}
		localConn, err = net.ListenUDP("udp", nil)
		if err != nil {
			time.Sleep(backoff)
			continue
		}
		break
	}
	defer func() {
		if localConn != nil {
			localConn.Close()
		}
	}()

	actuatorsMu.Lock()
	snap := make([]bool, len(actuators))
	copy(snap, actuators)
	actuatorsMu.Unlock()
	parts := make([]string, len(snap))
	for i, b := range snap {
		if b {
			parts[i] = "1"
		} else {
			parts[i] = "0"
		}
	}
	localConn.WriteToUDP([]byte(fmt.Sprintf("STAT|%s|%s\n", deviceID, strings.Join(parts, ","))), remoteAddr)

	temp := 65.0
	memoryMu.Lock()
	memoryPct = 30.0
	cleaning = false
	memoryMu.Unlock()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	lastStat := time.Now().Add(-10 * time.Second)

	for range ticker.C {
		poweredMu.Lock()
		p := powered
		poweredMu.Unlock()
		if !p {
			if time.Since(lastStat) >= 2*time.Second {
				if localConn == nil {
					localConn, _ = net.ListenUDP("udp", nil)
				}
				if localConn != nil {
					actuatorsMu.Lock()
					copy(snap, actuators)
					actuatorsMu.Unlock()
					for i, b := range snap {
						if b {
							parts[i] = "1"
						} else {
							parts[i] = "0"
						}
					}
					if _, err := localConn.WriteToUDP([]byte(fmt.Sprintf("STAT|%s|%s\n", deviceID, strings.Join(parts, ","))), remoteAddr); err != nil {
						localConn.Close()
						localConn = nil
					} else {
						lastStat = time.Now()
					}
				}
			}
			continue
		}

		fanOn := getFanState()
		if !fanOn {
			temp += 0.2
			if temp > 100.0 {
				temp = 100.0
			}
		} else {
			temp -= 0.4
			if temp < 30.0 {
				temp = 30.0
			}
		}

		if localConn == nil {
			localConn, _ = net.ListenUDP("udp", nil)
		}
		if localConn != nil {
			if _, err := localConn.WriteToUDP([]byte(fmt.Sprintf("T|%s|%.2f\n", deviceID, temp)), remoteAddr); err != nil {
				localConn.Close()
				localConn = nil
			}
		}

		memoryMu.Lock()
		if !cleaning {
			memoryPct += 0.3
			if memoryPct > 100.0 {
				memoryPct = 100.0
			}
		}
		memPct := memoryPct
		cleanState := cleaning
		memoryMu.Unlock()

		if localConn != nil {
			if _, err := localConn.WriteToUDP([]byte(fmt.Sprintf("MEM|%s|%.2f\n", deviceID, memPct)), remoteAddr); err != nil {
				localConn.Close()
				localConn = nil
			}
		}

		actuatorsMu.Lock()
		copy(snap, actuators)
		actuatorsMu.Unlock()

		ledColor, ledText, ledStatus, fanStatus := "\033[33m", "LED", "OFF", "OFF"
		if cleanState {
			ledColor = "\033[34m"
		} else if memPct >= 90.0 {
			ledColor = "\033[31m"
		} else if memPct <= 50.0 {
			ledColor = "\033[32m"
		}
		if len(snap) > 1 && snap[1] {
			ledStatus = "ON"
		}
		if len(snap) > 0 && snap[0] {
			fanStatus = "ON"
		}

		// Mantive a telemetria limpa do device aqui, apenas para debug se voce precisar
		fmt.Printf("[DEVICE %s] Temp: %.2f°C  Mem: %.2f%%  Fan:%s  %s%s\033[0m (%s)\n", deviceID, temp, memPct, fanStatus, ledColor, ledText, ledStatus)
	}
}