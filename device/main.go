package main

import (
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

type Telemetry struct {
	ID    string  `json:"id"`
	Temp  float64 `json:"temp"`
	FanOn bool    `json:"fan_on"`
}

func main() {
	deviceID := getenv("DEVICE_ID", "GPU_01")
	controlPortStr := getenv("DEVICE_CONTROL_PORT", "6001")
	controlPort, _ := strconv.Atoi(controlPortStr)
	gatewayHost := getenv("GATEWAY_HOST", "gateway")
	gatewayRegPort := getenv("GATEWAY_TCP_REG_PORT", "5002")
	gatewayUDPPort := getenv("GATEWAY_UDP_PORT", "5001")

	// prepare gateway UDP address for sending status updates
	gatewayUDPAddr = net.JoinHostPort(gatewayHost, gatewayUDPPort)

	// setup actuators (count configurable)
	actCountStr := getenv("DEVICE_ACT_COUNT", "2")
	actCount, _ := strconv.Atoi(actCountStr)
	if actCount < 1 {
		actCount = 1
	}
	actuatorsMu.Lock()
	actuators = make([]bool, actCount)
	actuatorsMu.Unlock()

	log.Printf("Device %s starting (control:%d) -> gateway %s (reg:%s udp:%s)", deviceID, controlPort, gatewayHost, gatewayRegPort, gatewayUDPPort)

	// register to gateway using textual protocol expected by PBL integrador
	regAddr := net.JoinHostPort(gatewayHost, gatewayRegPort)
	conn, err := net.Dial("tcp", regAddr)
	if err != nil {
		log.Fatalf("failed to register to gateway: %v", err)
	}
	// send registration in format: REG|AC|<ID>|<PORT> (AC = tipo de atuador)
	regLine := fmt.Sprintf("REG|AC|%s|%d\n", deviceID, controlPort)
	if _, err := conn.Write([]byte(regLine)); err != nil {
		log.Fatalf("failed to send reg: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	conn.Close()

	// start TCP control listener
	go startControlListener(controlPort, &deviceID)

	// telemetry loop
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(gatewayHost, gatewayUDPPort))
	if err != nil {
		log.Fatalf("resolve udp addr: %v", err)
	}
	dialConn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Fatalf("dial udp: %v", err)
	}
	defer dialConn.Close()

	temp := 65.0
	// memory percent simulation
	memoryMu.Lock()
	memoryPct = 30.0
	cleaning = false
	memoryMu.Unlock()
	// telemetry at 200ms and smoother thermal deltas
	ticker := time.NewTicker(200 * time.Millisecond)
	for range ticker.C {
		// read latest fan state at start of cycle
		fanOn := getFanState()
		if !fanOn {
			temp += 0.2 // slower rise when fan off
			if temp > 100.0 {
				temp = 100.0
			}
		} else {
			temp -= 0.4 // cooling when fan on
			if temp < 30.0 {
				temp = 30.0
			}
		}
		// send telemetry as textual message: T|<ID>|<temp>
		teleLine := fmt.Sprintf("T|%s|%.2f\n", deviceID, temp)
		if _, err := dialConn.Write([]byte(teleLine)); err != nil {
			log.Printf("udp write err: %v", err)
		}
		// update memory usage (unless cleaning)
		memoryMu.Lock()
		if !cleaning {
			memoryPct += 0.3
			if memoryPct > 100.0 {
				memoryPct = 100.0
			}
		}
		memPct := memoryPct
		memoryMu.Unlock()
		memLine := fmt.Sprintf("MEM|%s|%.2f\n", deviceID, memPct)
		if _, err := dialConn.Write([]byte(memLine)); err != nil {
			log.Printf("udp write err: %v", err)
		}

		// print local status to stdout (actuators + sensors)
		actuatorsMu.Lock()
		snap := make([]bool, len(actuators))
		copy(snap, actuators)
		actuatorsMu.Unlock()
		// LED color rules: if cleaning -> blue, if pct>=90 red, pct<=50 green, else yellow
		ledColor := ""
		ledText := "LED"
		if cleaning {
			ledColor = "\033[34m" // blue
		} else if memPct >= 90.0 {
			ledColor = "\033[31m" // red
		} else if memPct <= 50.0 {
			ledColor = "\033[32m" // green
		} else {
			ledColor = "\033[33m" // yellow
		}
		// show LED only if actuator 2 exists
		ledStatus := "OFF"
		if len(snap) > 1 && snap[1] {
			ledStatus = "ON"
		}
		fanStatus := "OFF"
		if len(snap) > 0 && snap[0] {
			fanStatus = "ON"
		}
		fmt.Printf("[DEVICE %s] Temp: %.2f°C  Mem: %.2f%%  Fan:%s  %s%s%s (%s)\n", deviceID, temp, memPct, fanStatus, ledColor, ledText, "\033[0m", ledStatus)
		// fanOn may be updated by control listener; read file-scoped var
		// short sleep handled by ticker
		// ensure we read the latest fan state from small shared storage via file scope variable
		// (control listener will update the global variable via package-level state)
		// We'll use a small file-level mechanism: read from control state file
		// but for simplicity here, we set fanOn via a shared global updated in listener
		fanOn = getFanState()
	}
}

var (
	actuators      []bool
	actuatorsMu    sync.Mutex
	gatewayUDPAddr string
	// memory simulation globals
	memoryPct float64
	memoryMu  sync.Mutex
	cleaning  bool
)

func getFanState() bool {
	return getActuatorState(0)
}

func setFanState(v bool) {
	setActuatorState(0, v, "")
}

func getActuatorState(idx int) bool {
	actuatorsMu.Lock()
	defer actuatorsMu.Unlock()
	if idx < 0 || idx >= len(actuators) {
		return false
	}
	return actuators[idx]
}

func setActuatorState(idx int, v bool, deviceID string) {
	actuatorsMu.Lock()
	if idx >= 0 && idx < len(actuators) {
		actuators[idx] = v
	}
	snap := make([]bool, len(actuators))
	copy(snap, actuators)
	actuatorsMu.Unlock()

	// if deviceID missing, nothing to report
	if deviceID == "" {
		return
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
	addr, err := net.ResolveUDPAddr("udp", gatewayUDPAddr)
	if err != nil {
		return
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.Write([]byte(statLine))
}

func startControlListener(port int, deviceID *string) {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		log.Fatalf("control listener err: %v", err)
	}
	defer ln.Close()
	log.Printf("control listener on %d", port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("control accept: %v", err)
			continue
		}
		go handleControlConn(conn, deviceID)
	}
}

func handleControlConn(conn net.Conn, deviceID *string) {
	defer conn.Close()
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		log.Printf("control read err: %v", err)
		return
	}
	cmd := string(buf[:n])
	cmd = strings.TrimSpace(cmd)
	log.Printf("control cmd received for %s: %s", *deviceID, cmd)
	// support multiple command syntaxes for compatibility
	switch {
	case cmd == "FAN_ON":
		setActuatorState(0, true, *deviceID)
		conn.Write([]byte("OK\n"))
	case cmd == "FAN_OFF":
		setActuatorState(0, false, *deviceID)
		conn.Write([]byte("OK\n"))
	case strings.HasPrefix(cmd, "ACT") && strings.Contains(cmd, "_ON"):
		// ACT1_ON, ACT2_ON ... parse number
		s := strings.TrimPrefix(cmd, "ACT")
		if idxS := strings.Split(s, "_")[0]; idxS != "" {
			if idx, err := strconv.Atoi(idxS); err == nil {
				setActuatorState(idx-1, true, *deviceID)
				conn.Write([]byte("OK\n"))
				return
			}
		}
		conn.Write([]byte("ERR\n"))
	case strings.HasPrefix(cmd, "ACT") && strings.Contains(cmd, "_OFF"):
		s := strings.TrimPrefix(cmd, "ACT")
		if idxS := strings.Split(s, "_")[0]; idxS != "" {
			if idx, err := strconv.Atoi(idxS); err == nil {
				setActuatorState(idx-1, false, *deviceID)
				conn.Write([]byte("OK\n"))
				return
			}
		}
		conn.Write([]byte("ERR\n"))
	case strings.HasPrefix(cmd, "AC_") && (strings.Contains(cmd, "LIGAR") || strings.Contains(cmd, "DESLIGAR")):
		// commands like AC_SALA_1|LIGAR
		if strings.Contains(cmd, "LIGAR") {
			setActuatorState(0, true, *deviceID)
			conn.Write([]byte("ACK|AC|" + *deviceID + "|LIGADO\n"))
		} else {
			setActuatorState(0, false, *deviceID)
			conn.Write([]byte("ACK|AC|" + *deviceID + "|DESLIGADO\n"))
		}
	default:
		conn.Write([]byte("ERR\n"))
	}

	// custom cleaning command
	if cmd == "CLEAN_MEM" {
		// start cleaning in background
		go func() {
			memoryMu.Lock()
			if cleaning {
				memoryMu.Unlock()
				return
			}
			cleaning = true
			memoryMu.Unlock()
			// gradually reduce memory percent
			for {
				memoryMu.Lock()
				if memoryPct <= 0 {
					memoryPct = 0
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
		}()
		conn.Write([]byte("OK\n"))
		return
	}
}

// extend to handle CLEAN_MEM command
func init() {
	// nothing here; keep for organizing helper if needed
}

func getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}
