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

type queuedCmd struct {
	cmd      string
	clientID int 
}

type clientEntry struct {
	ch   chan string
	addr string
	conn net.Conn
}

var (
	devices        = make(map[string]*DeviceInfo)
	mu             sync.Mutex
	clientChannels = make(map[int]clientEntry)
	clientMu       sync.Mutex
	clientCounter  int
	deviceQueues   = make(map[string]chan queuedCmd)
	deviceQueuesMu sync.Mutex
)

func main() {
	udpPort := getenv("GATEWAY_UDP_PORT", "5001")
	regPort := getenv("GATEWAY_TCP_REG_PORT", "5002")
	clientPort := getenv("GATEWAY_TCP_CLIENT_PORT", "5003")

	log.Printf("Gateway starting (udp:%s reg:%s client:%s)", udpPort, regPort, clientPort)

	go startUDPListener(udpPort)
	go startRegTCPServer(regPort)
	go startClientTCPServer(clientPort)
	go statusReporter()
	go heartbeatChecker()

	select {}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" { return v }
	return def
}

func startUDPListener(port string) {
	addr, err := net.ResolveUDPAddr("udp", ":"+port)
	if err != nil { log.Fatal(err) }
	conn, err := net.ListenUDP("udp", addr)
	if err != nil { log.Fatal(err) }
	defer conn.Close()
	
	buf := make([]byte, 1024)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil { continue }
		
		msg := strings.TrimSpace(string(buf[:n]))
		parts := strings.Split(msg, "|")
		if len(parts) < 3 { continue }
		
		id := parts[1]
		mu.Lock()
		d, ok := devices[id]
		if !ok {
			d = &DeviceInfo{ID: id, IP: remote.IP.String(), Port: 0}
			devices[id] = d
		}
		
		if d.Offline {
			log.Printf("[RECUPERADO] Dispositivo %s voltou a se comunicar!", id)
		}
		
		d.LastSeen = time.Now()
		d.Offline = false
		
		switch parts[0] {
		case "T":
			if v, err := strconv.ParseFloat(parts[2], 64); err == nil {
				d.Temp = v
				sendToAllClients(fmt.Sprintf("TLM|T|%s|%.2f", id, v))
				if v > 75.0 && !d.FanOn { go enqueueCommandToDevice(id, "FAN_ON", 0) } else if v < 60.0 && d.FanOn { go enqueueCommandToDevice(id, "FAN_OFF", 0) }
			}
		case "MEM":
			if v, err := strconv.ParseFloat(parts[2], 64); err == nil {
				sendToAllClients(fmt.Sprintf("TLM|M|%s|%.2f", id, v))
			}
		case "STAT":
			states := strings.Split(parts[2], ",")
			acts := make([]bool, len(states))
			for i, s := range states { acts[i] = (s == "1") }
			d.Actuators = acts
			if len(acts) > 0 { d.FanOn = acts[0] }
			sendToAllClients(fmt.Sprintf("STAT|%s|%s", id, parts[2]))
		// CORRECAO AQUI: O Gateway agora escuta eventos de CLEAN e repassa para o Cliente
		case "CLEAN":
			sendToAllClients(fmt.Sprintf("CLEAN|%s|%s", id, parts[2]))
		}
		mu.Unlock()
	}
}

func startRegTCPServer(port string) {
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil { log.Fatal(err) }
	defer ln.Close()
	
	for {
		conn, err := ln.Accept()
		if err != nil { continue }
		go handleRegConn(conn)
	}
}

func handleRegConn(conn net.Conn) {
	defer conn.Close()
	remote := conn.RemoteAddr().(*net.TCPAddr)
	
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil { return }
	
	parts := strings.Split(strings.TrimSpace(line), "|")
	if len(parts) < 3 || parts[0] != "REG" { return }
	
	id, port := parts[2], 0
	if len(parts) >= 4 { port, _ = strconv.Atoi(parts[3]) }
	
	mu.Lock()
	d, ok := devices[id]
	if !ok {
		d = &DeviceInfo{ID: id, IP: remote.IP.String(), Port: port, LastSeen: time.Now()}
		devices[id] = d
	} else {
		d.IP = remote.IP.String()
		d.Port = port
		d.LastSeen = time.Now()
		d.Offline = false
	}
	mu.Unlock()
	
	log.Printf("[REGISTRO] Dispositivo %s atualizou registro: IP=%s, PORT=%d", id, remote.IP.String(), port)
	fmt.Fprintf(conn, "ACK|REG|%s|OK\n", id)
}

func startClientTCPServer(port string) {
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil { log.Fatal(err) }
	defer ln.Close()
	
	for {
		conn, err := ln.Accept()
		if err != nil { continue }
		go handleClientConn(conn)
	}
}

func handleClientConn(conn net.Conn) {
	clientMu.Lock()
	clientCounter++
	myID := clientCounter
	ch := make(chan string, 10)
	clientChannels[myID] = clientEntry{ch: ch, addr: conn.RemoteAddr().String(), conn: conn}
	clientMu.Unlock()
	
	log.Printf("[INFO] CLIENTE %d CONECTADO (%s)", myID, conn.RemoteAddr().String())
	
	defer func() {
		clientMu.Lock()
		if e, ok := clientChannels[myID]; ok { close(e.ch); delete(clientChannels, myID) }
		clientMu.Unlock()
		conn.Close()
		log.Printf("[ALERTA] CLIENTE %d DESCONECTADO / OFFLINE", myID)
	}()

	go func() {
		for msg := range ch { conn.Write([]byte(msg + "\n")) }
	}()

	reader := bufio.NewReader(conn)
	for {
		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		line, err := reader.ReadString('\n')
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() { continue }
			return
		}
		
		line = strings.TrimSpace(line)
		if line == "" { continue }
		
		parts := strings.Split(line, "|")
		if len(parts) != 2 {
			fmt.Fprintf(conn, "ERR invalid command\n")
			continue
		}
		go enqueueCommandToDevice(parts[0], parts[1], myID)
	}
}

func enqueueCommandToDevice(id, cmd string, clientID int) {
	deviceQueuesMu.Lock()
	q := deviceQueues[id]
	if q == nil {
		q = make(chan queuedCmd, 100)
		deviceQueues[id] = q
		go deviceCommandWorker(id, q)
	}
	deviceQueuesMu.Unlock()
	q <- queuedCmd{cmd: cmd, clientID: clientID}
}

func deviceCommandWorker(id string, q chan queuedCmd) {
	for qc := range q {
		mu.Lock()
		d, ok := devices[id]
		mu.Unlock()

		failMsg := ""
		if !ok { failMsg = fmt.Sprintf("ERR|%s|unknown_device", id) } else if d.Port == 0 { failMsg = fmt.Sprintf("ERR|%s|no_control_port", id) }

		if failMsg != "" {
			if qc.clientID == 0 { sendToAllClients(failMsg) } else { sendToClient(qc.clientID, failMsg) }
			continue
		}

		addr := fmt.Sprintf("%s:%d", d.IP, d.Port)
		
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			errResp := fmt.Sprintf("ERR|%s|dial_failed", id)
			if qc.clientID == 0 { sendToAllClients(errResp) } else { sendToClient(qc.clientID, errResp) }
			continue
		}
		
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprintf(conn, "%s\n", qc.cmd)
		
		resp, err := bufio.NewReader(conn).ReadString('\n')
		conn.Close()
		
		if err != nil {
			errResp := fmt.Sprintf("ERR|%s|no_response", id)
			if qc.clientID == 0 { sendToAllClients(errResp) } else { sendToClient(qc.clientID, errResp) }
			continue
		}

		out := fmt.Sprintf("RESP|%s|%s", id, strings.TrimSpace(resp))
		if qc.clientID == 0 { sendToAllClients(out) } else { sendToClient(qc.clientID, out) }
	}
}

func sendToClient(clientID int, msg string) {
	clientMu.Lock()
	defer clientMu.Unlock()
	if e, ok := clientChannels[clientID]; ok { select { case e.ch <- msg: default: } }
}

func sendToAllClients(msg string) {
	clientMu.Lock()
	defer clientMu.Unlock()
	for _, entry := range clientChannels { select { case entry.ch <- msg: default: } }
}

func heartbeatChecker() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		mu.Lock()
		for id, d := range devices {
			if !d.LastSeen.IsZero() && now.Sub(d.LastSeen) > 5*time.Second {
				sendToAllClients(fmt.Sprintf("OFFLINE|%s", d.ID))
				log.Printf("[ALERTA] Dispositivo %s caiu. Removendo do registro (Sem sinal ha 5s)!", d.ID)
				delete(devices, id)
			}
		}
		mu.Unlock()
	}
}

func statusReporter() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		var sb strings.Builder
		sb.WriteString("\n========== GATEWAY STATUS ==========\n")
		
		mu.Lock()
		sb.WriteString(fmt.Sprintf("Dispositivos registrados: %d\n", len(devices)))
		for _, d := range devices {
			status := "ONLINE"
			if d.Offline { status = "OFFLINE" }
			sb.WriteString(fmt.Sprintf("  |- %s: %s (Porta: %d)\n", d.ID, status, d.Port))
		}
		mu.Unlock()

		clientMu.Lock()
		sb.WriteString(fmt.Sprintf("Clientes conectados: %d\n", len(clientChannels)))
		for id, e := range clientChannels {
			sb.WriteString(fmt.Sprintf("  |- Cliente %d (%s)\n", id, e.addr))
		}
		clientMu.Unlock()
		
		sb.WriteString("====================================\n")
		log.Print(sb.String())
	}
}