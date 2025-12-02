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
			go server.handleConnection(conn)
		}
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\nShutting down Mimic Server...")
	listener.Close()
}

func (s *Server) handleConnection(rawConn net.Conn) {
	// Wrap connection with Mimic protocol
	mimicConn := protocol.NewConnection(rawConn)

	// 1. Handshake
	sni, sessionID, err := mimicConn.HandshakeServer()
	if err != nil {
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
	// Echo logic
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
