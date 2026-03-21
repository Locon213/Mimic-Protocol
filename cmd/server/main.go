package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/config"
	"github.com/Locon213/Mimic-Protocol/pkg/mtp"
	"github.com/Locon213/Mimic-Protocol/pkg/network"
	"github.com/Locon213/Mimic-Protocol/pkg/transport"
	"github.com/Locon213/Mimic-Protocol/pkg/version"
	"github.com/google/uuid"
	"github.com/hashicorp/yamux"
)

// detectPublicIP attempts to detect the server's public IP address
func detectPublicIP() string {
	// Method 1: Check external services (with timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	services := []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
	}

	for _, service := range services {
		resp, err := client.Get(service)
		if err == nil {
			defer resp.Body.Close()
			ip, err := io.ReadAll(resp.Body)
			if err == nil && len(ip) > 0 {
				return strings.TrimSpace(string(ip))
			}
		}
	}

	// Method 2: Get local IP (might be private)
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				if !ipNet.IP.IsPrivate() {
					return ipNet.IP.String()
				}
			}
		}
		// Fallback to first non-loopback IP
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				return ipNet.IP.String()
			}
		}
	}

	return ""
}

// Stream type markers
const (
	StreamTypeProxy = 0x01 // SOCKS5 proxy relay
	StreamTypeMimic = 0x02 // Mimic traffic (echo)
	StreamTypeUDP   = 0x03 // UDP Associate tunneling
)

type Session struct {
	ID          string
	VirtualConn *transport.VirtualConn
	Yamux       *yamux.Session
}

type Server struct {
	sessions map[string]*Session
	mutex    sync.RWMutex
	resolver *network.CachedResolver
}

func main() {
	// Check for subcommands
	if len(os.Args) > 1 {
		if os.Args[1] == "generate-uuid" {
			id := uuid.New()
			fmt.Println(id.String())
			return
		}
		if os.Args[1] == "generate-link" {
			configPath := "server.yaml"
			host := ""

			// Parse arguments: generate-link [config.yaml] [--host IP]
			for i := 2; i < len(os.Args); i++ {
				if os.Args[i] == "--host" && i+1 < len(os.Args) {
					host = os.Args[i+1]
					i++
				} else if !strings.HasPrefix(os.Args[i], "--") {
					configPath = os.Args[i]
				}
			}

			cfg, err := config.LoadServerConfig(configPath)
			if err != nil {
				log.Fatalf("Failed to load config %s: %v", configPath, err)
			}

			// Auto-detect host if not provided
			if host == "" {
				host = detectPublicIP()
				if host == "" {
					host = "YOUR_SERVER_IP"
				}
			}

			// Create ClientConfig for URL generation
			clientCfg := &config.ClientConfig{
				Server:      fmt.Sprintf("%s:%d", host, cfg.Port),
				UUID:        cfg.UUID,
				Domains:     cfg.DomainList,
				Transport:   cfg.Transport,
				DNS:         cfg.DNS,
				ServerName:  cfg.Name,
				Compression: cfg.Compression,
			}
			link := config.GenerateMimicURL(clientCfg)
			fmt.Println("\n================================================================")
			fmt.Println("🚀 Share this link with clients to connect:")
			fmt.Println()
			fmt.Println(link)
			fmt.Println("================================================================")

			if host == "YOUR_SERVER_IP" {
				fmt.Println("\n⚠️  Warning: Could not auto-detect public IP.")
				fmt.Println("   Please specify your server's public IP:")
				fmt.Println("   ./server generate-link server.yaml --host 192.168.1.100")
			}
			return
		}
	}

	// Parse flags manually
	port := 443
	configPath := "server.yaml"

	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "-port":
			if i+1 < len(os.Args) {
				fmt.Sscanf(os.Args[i+1], "%d", &port)
				i++
			}
		case "-config":
			if i+1 < len(os.Args) {
				configPath = os.Args[i+1]
				i++
			}
		}
	}

	ver := version.GetVersion()
	fmt.Println("╔══════════════════════════════════════════════╗")
	fmt.Printf("║           Mimic Server %s (MTP)         ║\n", ver)
	fmt.Println("╚══════════════════════════════════════════════╝")

	// 1. Load configuration
	cfg, err := config.LoadServerConfig(configPath)
	if err != nil {
		log.Printf("Warning: could not load config file: %v", err)
		log.Println("Using default configuration...")
		cfg = &config.ServerConfig{
			Port:       port,
			MaxClients: 100,
			UUID:       "550e8400-e29b-41d4-a716-446655440000",
			DomainList: []config.DomainEntry{
				{Domain: "vk.com"},
				{Domain: "wikipedia.org"},
			},
			Transport: "mtp",
		}
	} else {
		if port != 443 {
			cfg.Port = port
		}
	}

	server := &Server{
		sessions: make(map[string]*Session),
		resolver: network.NewCachedResolver(cfg.DNS, 5*time.Minute),
	}

	addr := fmt.Sprintf(":%d", cfg.Port)

	// 2. Start MTP Listener (UDP) with compression config
	var compression *mtp.CompressionConfig
	if cfg.Compression.Enable {
		compression = &mtp.CompressionConfig{
			Enable:  cfg.Compression.Enable,
			Level:   cfg.Compression.Level,
			MinSize: cfg.Compression.MinSize,
		}
		log.Printf("🗜️  Compression enabled (level=%d, min_size=%d)", cfg.Compression.Level, cfg.Compression.MinSize)
	}

	mtpListener, err := mtp.ListenWithConfig(addr, cfg.UUID, compression)
	if err != nil {
		log.Fatalf("Failed to start MTP listener on port %d: %v", cfg.Port, err)
	}

	fmt.Printf("🚀 MTP (UDP) listening on port %d\n", cfg.Port)
	fmt.Printf("🔑 UUID: %s\n", cfg.UUID)
	fmt.Printf("🛡️  Transport: MTP (Mimic Transport Protocol)\n")
	fmt.Println("────────────────────────────────────────────────")

	// 3. Accept Loop
	go func() {
		for {
			conn, err := mtpListener.Accept()
			if err != nil {
				log.Printf("Accept error: %v", err)
				return
			}
			go server.handleMTPConnection(conn, cfg)
		}
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\n🛑 Shutting down Mimic Server...")
	mtpListener.Close()
}

func (s *Server) handleMTPConnection(conn net.Conn, cfg *config.ServerConfig) {
	remoteAddr := conn.RemoteAddr().String()
	log.Printf("[%s] New MTP connection", remoteAddr)

	mtpConn, ok := conn.(*mtp.MTPConn)
	if !ok {
		log.Printf("[%s] Invalid connection type", remoteAddr)
		conn.Close()
		return
	}

	sessionID := mtpConn.SessionID()

	s.mutex.Lock()
	session, exists := s.sessions[sessionID]

	if exists {
		// ROAMING: Existing session, swap the connection
		s.mutex.Unlock()
		log.Printf("[%s] Roaming to existing session %s", remoteAddr, sessionID)
		session.VirtualConn.SwapConnectionSeamless(conn)
		return
	}

	// NEW SESSION
	log.Printf("[%s] Creating new session %s", remoteAddr, sessionID)
	vConn := transport.NewVirtualConn(conn)

	// Init Yamux Server on this VirtualConn
	yamuxCfg := yamux.DefaultConfig()
	yamuxCfg.MaxStreamWindowSize = 16 * 1024 * 1024
	yamuxCfg.EnableKeepAlive = true
	yamuxCfg.KeepAliveInterval = 30 * time.Second
	yamuxCfg.ConnectionWriteTimeout = 15 * time.Second

	yamuxSession, err := yamux.Server(vConn, yamuxCfg)
	if err != nil {
		s.mutex.Unlock()
		log.Printf("Failed to start Yamux server: %v", err)
		conn.Close()
		return
	}

	newSession := &Session{
		ID:          sessionID,
		VirtualConn: vConn,
		Yamux:       yamuxSession,
	}
	s.sessions[sessionID] = newSession
	s.mutex.Unlock()

	// Handle streams from this Yamux session
	go func() {
		log.Printf("[Yamux] Starting Accept loop for session %s", sessionID)
		for {
			stream, err := yamuxSession.Accept()
			if err != nil {
				log.Printf("[Yamux] Session %s Accept error: %v", sessionID, err)
				s.removeSession(sessionID)
				return
			}
			log.Printf("[Yamux] Accepted new stream for session %s", sessionID)
			go s.handleStream(stream)
		}
	}()
}

func (s *Server) removeSession(id string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.sessions, id)
}

// handleStream processes a stream from the yamux session.
// First byte determines stream type: 0x01=proxy, 0x02=mimic
func (s *Server) handleStream(stream net.Conn) {
	defer stream.Close()

	// Read stream type marker (1 byte)
	typeBuf := make([]byte, 1)
	_, err := io.ReadFull(stream, typeBuf)
	if err != nil {
		return
	}

	switch typeBuf[0] {
	case StreamTypeProxy:
		s.handleProxyStream(stream)
	case StreamTypeMimic:
		handleMimicStream(stream)
	case StreamTypeUDP:
		s.handleUDPStream(stream)
	default:
		log.Printf("[Stream] Unknown stream type: 0x%02x", typeBuf[0])
	}
}

// handleProxyStream handles a SOCKS5 proxy relay stream
func (s *Server) handleProxyStream(stream net.Conn) {
	// Read connect header: [1 byte addr_len] [addr_len bytes addr]
	header := make([]byte, 1)
	_, err := io.ReadFull(stream, header)
	if err != nil {
		log.Printf("[Proxy] Failed to read addr length: %v", err)
		return
	}

	addrLen := int(header[0])
	if addrLen == 0 || addrLen > 253 {
		log.Printf("[Proxy] Invalid addr length: %d", addrLen)
		return
	}

	addrBuf := make([]byte, addrLen)
	_, err = io.ReadFull(stream, addrBuf)
	if err != nil {
		log.Printf("[Proxy] Failed to read addr: %v", err)
		return
	}

	targetAddr := string(addrBuf)
	log.Printf("[Proxy] Connecting to %s", targetAddr)

	// Dial target using cached resolver
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	targetConn, err := s.resolver.DialContext(ctx, "tcp", targetAddr)
	if err != nil {
		log.Printf("[Proxy] Failed to connect to %s: %v", targetAddr, err)
		stream.Write([]byte{0x00}) // Failure
		return
	}
	defer targetConn.Close()

	// Send success
	stream.Write([]byte{0x01})
	log.Printf("[Proxy] Connected to %s", targetAddr)

	// Relay data
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(targetConn, stream)
	}()

	go func() {
		defer wg.Done()
		io.Copy(stream, targetConn)
	}()

	wg.Wait()
	log.Printf("[Proxy] Closed connection to %s", targetAddr)
}

// handleMimicStream echoes mimic traffic back
func handleMimicStream(stream net.Conn) {
	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		if err != nil {
			return
		}
		_, err = stream.Write(buf[:n])
		if err != nil {
			return
		}
	}
}

// handleUDPStream handles UDP encapsulating over Yamux stream
func (s *Server) handleUDPStream(stream net.Conn) {
	// Read connect header: [1 byte addrLen] [addrLen bytes targetAddr]
	header := make([]byte, 1)
	if _, err := io.ReadFull(stream, header); err != nil {
		log.Printf("[UDP] Failed to read addr length: %v", err)
		return
	}

	addrLen := int(header[0])
	if addrLen == 0 || addrLen > 253 {
		log.Printf("[UDP] Invalid addr length: %d", addrLen)
		return
	}

	addrBuf := make([]byte, addrLen)
	if _, err := io.ReadFull(stream, addrBuf); err != nil {
		log.Printf("[UDP] Failed to read addr: %v", err)
		return
	}
	targetAddr := string(addrBuf)

	// Read data length: [2 bytes dataLen]
	dataLenBuf := make([]byte, 2)
	if _, err := io.ReadFull(stream, dataLenBuf); err != nil {
		log.Printf("[UDP] Failed to read data length: %v", err)
		return
	}
	dataLen := int(dataLenBuf[0])<<8 | int(dataLenBuf[1])

	// Read data
	data := make([]byte, dataLen)
	if _, err := io.ReadFull(stream, data); err != nil {
		log.Printf("[UDP] Failed to read datagram: %v", err)
		return
	}

	// For simple asymmetric NAT scenarios: Dial UDP directly and read the response.
	// For full symmetric NAT routing we'd keep a map of `net.Conn` attached to the Yamux Session.
	log.Printf("[UDP] Relaying datagram (%d bytes) to %s", dataLen, targetAddr)

	// Our resolver currently only supports standard network lookups
	// For simplicity in this iteration we'll rely on the default net.ResolveUDPAddr
	resolvedAddr, err := net.ResolveUDPAddr("udp", targetAddr)
	if err != nil {
		log.Printf("[UDP] Failed to resolve %s: %v", targetAddr, err)
		return
	}

	targetConn, err := net.DialUDP("udp", nil, resolvedAddr)
	if err != nil {
		log.Printf("[UDP] Failed to dial %s: %v", targetAddr, err)
		return
	}
	defer targetConn.Close()

	targetConn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := targetConn.Write(data); err != nil {
		return
	}

	// Read response
	respBuf := make([]byte, 65535)
	rn, err := targetConn.Read(respBuf)
	if err != nil {
		return // timeout or closed
	}

	// Send back to client: [2 bytes RespLen] [RespData]
	respHeader := make([]byte, 2)
	respHeader[0] = byte(rn >> 8)
	respHeader[1] = byte(rn & 0xFF)

	stream.Write(respHeader)
	stream.Write(respBuf[:rn])
}
