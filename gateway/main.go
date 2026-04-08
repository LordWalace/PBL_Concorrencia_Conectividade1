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

type DeviceInfo struct {
	ID        string    `json:"id"`
	IP        string    `json:"ip"`
	Port      int       `json:"port"`
	LastSeen  time.Time `json:"last_seen"`
	Temp      float64   `json:"temp"`
	FanOn     bool      `json:"fan_on"`
	Actuators []bool    `json:"actuators,omitempty"`
	Offline   bool      `json:"offline"`
}

type RegMessage struct {
	ID          string `json:"id"`
	ControlPort int    `json:"control_port"`
}

type Telemetry struct {
	ID    string  `json:"id"`
	Temp  float64 `json:"temp"`
	FanOn bool    `json:"fan_on"`
}

var (
	devices        = make(map[string]*DeviceInfo)
	mu             sync.Mutex
	clientChannels = make(map[int]chan string)
	clientMu       sync.Mutex
	clientCounter  int
)

func main() {
	udpPort := getenv("GATEWAY_UDP_PORT", "5001")
	regPort := getenv("GATEWAY_TCP_REG_PORT", "5002")
	clientPort := getenv("GATEWAY_TCP_CLIENT_PORT", "5003")

	log.Printf("Gateway starting (udp:%s reg:%s client:%s)", udpPort, regPort, clientPort)

	// start UDP listener for telemetry
	go startUDPListener(udpPort)

	// start TCP server for registration
	go startRegTCPServer(regPort)

	// start TCP server for clients
	go startClientTCPServer(clientPort)

	// start periodic status reporter
	go statusReporter()

	// heartbeat checker
	go heartbeatChecker()

	// prevent exit
	select {}
}

func getenv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func startUDPListener(port string) {
	addr, err := net.ResolveUDPAddr("udp", ":"+port)
	if err != nil {
		log.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	buf := make([]byte, 1024)
	log.Printf("UDP listener started on %s", port)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("udp read error: %v", err)
			continue
		}
		// support textual messages: T|<ID>|<value> or MEM|<ID>|<value> or STAT|<ID>|<b1,b2,..>
		msg := strings.TrimSpace(string(buf[:n]))
		parts := strings.Split(msg, "|")
		if len(parts) < 3 {
			log.Printf("invalid udp msg from %v: %s", remote, msg)
			continue
		}
		switch parts[0] {
		case "T":
			// legacy TEMP message: T|<ID>|<value>
			id := parts[1]
			valStr := parts[2]
			var temp float64
			if v, err := strconv.ParseFloat(valStr, 64); err == nil {
				temp = v
			} else {
				log.Printf("invalid telemetry value from %v: %s", remote, valStr)
				continue
			}
			mu.Lock()
			d, ok := devices[id]
			if !ok {
				d = &DeviceInfo{ID: id, IP: remote.IP.String(), Port: 0}
				devices[id] = d
			}
			d.Temp = temp
			d.LastSeen = time.Now()
			d.Offline = false
			mu.Unlock()
			// broadcast telemetry to clients in PBL textual format
			sendToAllClients(fmt.Sprintf("TLM|T|%s|%.2f", id, temp))
			// simple automation
			if temp > 75.0 && !d.FanOn {
				go sendCommandToDevice(id, "FAN_ON")
			} else if temp < 60.0 && d.FanOn {
				go sendCommandToDevice(id, "FAN_OFF")
			}
			continue
		case "MEM":
			// memory telemetry: MEM|<ID>|<percent>
			id := parts[1]
			valStr := parts[2]
			var pct float64
			if v, err := strconv.ParseFloat(valStr, 64); err == nil {
				pct = v
			} else {
				log.Printf("invalid mem value from %v: %s", remote, valStr)
				continue
			}
			mu.Lock()
			d, ok := devices[id]
			if !ok {
				d = &DeviceInfo{ID: id, IP: remote.IP.String(), Port: 0}
				devices[id] = d
			}
			// keep existing temp unchanged; memory is forwarded separately as TLM|M
			// extend DeviceInfo with MemoryPercent? we'll add dynamic map via metadata if needed
			// For now broadcast memory as separate TLM type
			d.LastSeen = time.Now()
			d.Offline = false
			mu.Unlock()
			sendToAllClients(fmt.Sprintf("TLM|M|%s|%.2f", id, pct))
			continue
		case "STAT":
			id := parts[1]
			states := strings.Split(parts[2], ",")
			mu.Lock()
			d, ok := devices[id]
			if !ok {
				d = &DeviceInfo{ID: id, IP: remote.IP.String(), Port: 0}
				devices[id] = d
			}
			// update actuators
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
			d.LastSeen = time.Now()
			d.Offline = false
			mu.Unlock()
			// forward STAT to clients
			sendToAllClients(fmt.Sprintf("STAT|%s|%s", id, parts[2]))
			continue
		default:
			log.Printf("unknown udp msg type from %v: %s", remote, msg)
			continue
		}
	}
}

func startRegTCPServer(port string) {
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	log.Printf("Registration TCP server on %s", port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("reg accept err: %v", err)
			continue
		}
		go handleRegConn(conn)
	}
}

func handleRegConn(conn net.Conn) {
	defer conn.Close()
	remote := conn.RemoteAddr().(*net.TCPAddr)
	// read textual registration: REG|TYPE|ID|PORT
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		log.Printf("invalid reg from %v: %v", remote, err)
		return
	}
	line = strings.TrimSpace(line)
	parts := strings.Split(line, "|")
	if len(parts) < 3 || parts[0] != "REG" {
		log.Printf("invalid reg format from %v: %s", remote, line)
		return
	}
	id := ""
	port := 0
	if len(parts) >= 3 {
		id = parts[2]
	}
	if len(parts) >= 4 {
		if p, err := strconv.Atoi(parts[3]); err == nil {
			port = p
		}
	}
	mu.Lock()
	devices[id] = &DeviceInfo{
		ID:       id,
		IP:       remote.IP.String(),
		Port:     port,
		LastSeen: time.Now(),
		Temp:     0,
		FanOn:    false,
		Offline:  false,
	}
	log.Printf("[DEVICE REGISTRO] %s registrado: IP=%s, PORT=%d", id, remote.IP.String(), port)
	log.Printf("[GATEWAY] Total de dispositivos: %d", len(devices))
	mu.Unlock()
	log.Printf("registered device %s at %s:%d", id, remote.IP.String(), port)
	// ack
	fmt.Fprintf(conn, "ACK|REG|%s|OK\n", id)
}

func startClientTCPServer(port string) {
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	log.Printf("Client TCP server on %s", port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("client accept err: %v", err)
			continue
		}
		log.Printf("client connected: %s", conn.RemoteAddr().String())
		go handleClientConn(conn)
	}
}

func handleClientConn(conn net.Conn) {
	// register client notification channel
	clientMu.Lock()
	clientCounter++
	myID := clientCounter
	ch := make(chan string, 10)
	clientChannels[myID] = ch
	clientMu.Unlock()
	log.Printf("[CLIENT %d] conectado: %s", myID, conn.RemoteAddr().String())
	defer func() {
		clientMu.Lock()
		delete(clientChannels, myID)
		close(ch)
		clientMu.Unlock()
		conn.Close()
		log.Printf("[CLIENT %d] desconectado", myID)
	}()

	reader := bufio.NewReader(conn)
	// start writer goroutine: write messages from channel to client
	go func() {
		for msg := range ch {
			conn.Write([]byte(msg + "\n"))
		}
	}()

	for {
		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		line, err := reader.ReadString('\n')
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			log.Printf("client read error: %v", err)
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// expected: ID_ATUADOR|COMANDO (e.g., AC_SALA_1|LIGAR)
		parts := strings.Split(line, "|")
		if len(parts) != 2 {
			fmt.Fprintf(conn, "ERR invalid command\n")
			continue
		}
		id := parts[0]
		cmd := parts[1]
		log.Printf("[CLIENT %d] comando recebido: %s -> %s", myID, id, cmd)
		// forward command to device (id may match device ID directly)
		go sendCommandToDevice(id, cmd)
	}
}

func sendCommandToDevice(id, cmd string) {
	mu.Lock()
	d, ok := devices[id]
	mu.Unlock()
	if !ok {
		log.Printf("sendCommand: unknown device %s", id)
		return
	}
	if d.Port == 0 {
		log.Printf("sendCommand: device %s has no control port", id)
		return
	}
	addr := fmt.Sprintf("%s:%d", d.IP, d.Port)
	log.Printf("[DEVICE %s] tentando conectar em %s para enviar: %s", id, addr, cmd)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		log.Printf("failed to connect to device %s at %s: %v", id, addr, err)
		return
	}
	defer conn.Close()
	fmt.Fprintf(conn, "%s\n", cmd)
	log.Printf("[DEVICE %s] comando enviado com sucesso: %s", id, cmd)
	// read response (with deadline) and forward ACK to clients
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	respBuf := make([]byte, 512)
	n, err := conn.Read(respBuf)
	if err == nil && n > 0 {
		resp := strings.TrimSpace(string(respBuf[:n]))
		log.Printf("[DEVICE %s] resposta recebida: %s", id, resp)
		// broadcast ACK/response to all clients
		sendToAllClients(resp)
	}
}

func sendToAllClients(msg string) {
	clientMu.Lock()
	defer clientMu.Unlock()
	for _, ch := range clientChannels {
		select {
		case ch <- msg:
		default:
			// drop if client channel full
		}
	}
}

func heartbeatChecker() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		mu.Lock()
		for _, d := range devices {
			if d.LastSeen.IsZero() || now.Sub(d.LastSeen) > 5*time.Second {
				if !d.Offline {
					d.Offline = true
					log.Printf("device %s marked OFFLINE", d.ID)
				}
			}
		}
		mu.Unlock()
	}
}

// statusReporter periodically prints gateway status: devices and clients
func statusReporter() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		// Report minimal gateway status: counts only. Device-specific telemetry
		// (TLM/STAT) is responsibility of the device and forwarded to clients.
		var sb strings.Builder
		sb.WriteString("=== GATEWAY STATUS ===\n")
		mu.Lock()
		sb.WriteString(fmt.Sprintf("Registered devices: %d\n", len(devices)))
		mu.Unlock()

		clientMu.Lock()
		sb.WriteString(fmt.Sprintf("Connected clients: %d\n", len(clientChannels)))
		clientMu.Unlock()

		log.Print(sb.String())
	}
}
