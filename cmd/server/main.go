package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/config"
	"github.com/Locon213/Mimic-Protocol/pkg/mtp"
	"github.com/Locon213/Mimic-Protocol/pkg/network"
	"github.com/Locon213/Mimic-Protocol/pkg/transport"
	"github.com/google/uuid"
	"github.com/hashicorp/yamux"
)

// Stream type markers
const (
	StreamTypeProxy = 0x01 // SOCKS5 proxy relay
	StreamTypeMimic = 0x02 // Mimic traffic (echo)
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
	if len(os.Args) > 1 && os.Args[1] == "generate-uuid" {
		id := uuid.New()
		fmt.Println(id.String())
		return
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

	fmt.Println("╔══════════════════════════════════════════════╗")
	fmt.Println("║           Mimic Server v0.2.0 (MTP)         ║")
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
			DomainList: []string{"vk.com", "wikipedia.org"},
			Transport:  "mtp",
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

	// 2. Start MTP Listener (UDP)
	mtpListener, err := mtp.Listen(addr, cfg.UUID)
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
	yamuxCfg.EnableKeepAlive = true
	yamuxCfg.KeepAliveInterval = 10 * time.Second
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
		for {
			stream, err := yamuxSession.Accept()
			if err != nil {
				log.Printf("Session %s closed: %v", sessionID, err)
				s.removeSession(sessionID)
				return
			}
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
