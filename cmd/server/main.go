package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"io"

	"github.com/Locon213/Mimic-Protocol/pkg/config"
	"github.com/Locon213/Mimic-Protocol/pkg/protocol"
	"github.com/Locon213/Mimic-Protocol/pkg/transport"
	"github.com/hashicorp/yamux"
)

type Session struct {
	ID          string
	VirtualConn *transport.VirtualConn
	Yamux       *yamux.Session
}

type Server struct {
	sessions map[string]*Session
	mutex    sync.RWMutex
}

func main() {
	port := flag.Int("port", 443, "Server port")
	configPath := flag.String("config", "server.yaml", "Path to configuration file")
	flag.Parse()

	fmt.Println("Mimic Server v0.0.1")

	// 1. Load configuration
	cfg, err := config.LoadServerConfig(*configPath)
	if err != nil {
		log.Printf("Warning: could not load config file: %v", err)
		log.Println("Using default configuration...")
		cfg = &config.ServerConfig{
			Port:       *port,
			MaxClients: 100,
			UUID:       "550e8400-e29b-41d4-a716-446655440000",
			DomainList: []string{"vk.com", "wikipedia.org"},
		}
	} else {
		if *port != 443 {
			cfg.Port = *port
		}
	}

	server := &Server{
		sessions: make(map[string]*Session),
	}

	// 2. Start TCP Listener
	addr := fmt.Sprintf(":%d", cfg.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to bind to port %d: %v", cfg.Port, err)
	}

	fmt.Printf("Starting on port %d\n", cfg.Port)

	// 3. Accept Loop
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Printf("Accept error: %v", err)
				continue
			}
			go server.handleConnection(conn, cfg)
		}
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\nShutting down Mimic Server...")
	listener.Close()
}

func (s *Server) handleConnection(rawConn net.Conn, cfg *config.ServerConfig) {
	// Wrap connection with Mimic protocol
	mimicConn := protocol.NewConnection(rawConn, cfg.UUID)

	// 1. Handshake
	sni, sessionID, peekBytes, err := mimicConn.HandshakeServer()
	if err != nil {
		if err.Error() == "fallback" {
			log.Printf("[%s] Active Probe detected or non-Mimic client. Fallback proxying to %s", rawConn.RemoteAddr(), sni)
			s.handleFallback(rawConn, peekBytes, sni)
			return
		}
		log.Printf("[%s] Handshake failed: %v", rawConn.RemoteAddr(), err)
		rawConn.Close()
		return
	}

	log.Printf("[%s] Connection accepted. SNI: %s | SessionID: %s", rawConn.RemoteAddr(), sni, sessionID)

	// 2. Session Management
	s.mutex.Lock()
	session, exists := s.sessions[sessionID]

	if exists {
		// ROAMING: Existing session, swap the connection
		s.mutex.Unlock()
		log.Printf("[%s] Roaming to existing session %s", rawConn.RemoteAddr(), sessionID)
		session.VirtualConn.SwapConnection(mimicConn)
		return
	}

	// NEW SESSION
	log.Printf("[%s] Creating new session %s", rawConn.RemoteAddr(), sessionID)
	vConn := transport.NewVirtualConn(mimicConn)

	// Init Yamux Server on this VirtualConn
	yamuxSession, err := yamux.Server(vConn, nil)
	if err != nil {
		s.mutex.Unlock()
		log.Printf("Failed to start Yamux server: %v", err)
		rawConn.Close()
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
			go handleStream(stream)
		}
	}()
}

func (s *Server) removeSession(id string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.sessions, id)
}

func handleStream(stream net.Conn) {
	defer stream.Close()
	// Echo logic for testing
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

// handleFallback transparently proxies the connection to a legitimate site
func (s *Server) handleFallback(clientConn net.Conn, pendingBytes []byte, sni string) {
	defer clientConn.Close()

	// Default fallback
	targetAddr := "vk.com:443"
	if sni != "" {
		targetAddr = fmt.Sprintf("%s:443", sni)
	}

	targetConn, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
	if err != nil {
		log.Printf("[%s] Fallback dial failed: %v", clientConn.RemoteAddr(), err)
		return
	}
	defer targetConn.Close()

	if len(pendingBytes) > 0 {
		_, err = targetConn.Write(pendingBytes)
		if err != nil {
			log.Printf("[%s] Fallback write pending bytes failed: %v", clientConn.RemoteAddr(), err)
			return
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(targetConn, clientConn)
		targetConn.(*net.TCPConn).CloseWrite()
	}()

	go func() {
		defer wg.Done()
		io.Copy(clientConn, targetConn)
		clientConn.(*net.TCPConn).CloseWrite()
	}()

	wg.Wait()
}
