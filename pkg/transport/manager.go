package transport

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/protocol"
	"github.com/hashicorp/yamux"
)

// Manager handles the lifecycle of underlying network connections
// and seamless switching between them using Yamux session resumption.
type Manager struct {
	serverAddr string
	uuid       string // Client UUID for Session ID

	session     *yamux.Session
	virtualConn *VirtualConn

	currentConn net.Conn
	mutex       sync.Mutex
}

// NewManager creates a new transport manager
func NewManager(serverAddr string, uuid string) *Manager {
	return &Manager{
		serverAddr: serverAddr,
		uuid:       uuid,
	}
}

// StartSession establishes the initial connection and Yamux session
func (m *Manager) StartSession(initialDomain string) (*yamux.Session, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.session != nil && !m.session.IsClosed() {
		return m.session, nil
	}

	// 1. Dial TCP
	conn, err := protocol.Dial(m.serverAddr)
	if err != nil {
		return nil, err
	}

	// 2. Handshake with Session ID (UUID)
	if err := conn.HandshakeClient(initialDomain, m.uuid); err != nil {
		conn.Close()
		return nil, err
	}

	// 3. Wrap in VirtualConn
	m.currentConn = conn
	m.virtualConn = NewVirtualConn(conn)

	// 4. Init Yamux Client
	session, err := yamux.Client(m.virtualConn, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}

	m.session = session
	return session, nil
}

// RotateTransport switches the underlying transport to a new domain
// while keeping the Yamux session alive.
func (m *Manager) RotateTransport(newDomain string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.virtualConn == nil {
		return fmt.Errorf("session not initialized")
	}

	log.Printf("[Transport] Rotating transport to %s...", newDomain)

	// 1. Dial NEW TCP connection
	newConn, err := protocol.Dial(m.serverAddr)
	if err != nil {
		return fmt.Errorf("failed to dial new transport: %w", err)
	}

	// 2. Handshake with SAME Session ID
	// This tells the server to swap the socket under the hood
	if err := newConn.HandshakeClient(newDomain, m.uuid); err != nil {
		newConn.Close()
		return fmt.Errorf("handshake failed for new transport: %w", err)
	}

	// 3. Swap the connection in VirtualConn
	// Now Yamux traffic flows through the new socket
	oldConn := m.currentConn
	m.virtualConn.SwapConnection(newConn)
	m.currentConn = newConn

	// 4. Gracefully close old connection
	// We wait a bit to ensure any in-flight packets on old conn are handled?
	// In a perfect world, yes. For now, just close.
	// Yamux retries might handle lost packets if TCP didn't deliver.
	if oldConn != nil {
		go func() {
			time.Sleep(1 * time.Second)
			oldConn.Close()
		}()
	}

	log.Printf("[Transport] Transport rotated successfully to %s", newDomain)
	return nil
}
