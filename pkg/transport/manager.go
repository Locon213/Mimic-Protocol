package transport

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/mtp"
	"github.com/Locon213/Mimic-Protocol/pkg/network"
	"github.com/hashicorp/yamux"
)

// Manager handles the lifecycle of underlying network connections
// and seamless switching between them using Yamux session resumption.
// Uses MTP (Mimic Transport Protocol) over UDP for anti-DPI transport.
type Manager struct {
	serverAddr string
	uuid       string // Client UUID for Session ID

	session     *yamux.Session
	virtualConn *VirtualConn

	currentConn net.Conn
	mtpConn     *mtp.MTPConn
	mutex       sync.Mutex

	resolver *network.CachedResolver
}

// NewManager creates a new transport manager
func NewManager(serverAddr string, uuid string, dns string) *Manager {
	return &Manager{
		serverAddr: serverAddr,
		uuid:       uuid,
		resolver:   network.NewCachedResolver(dns, 5*time.Minute),
	}
}

// StartSession establishes the initial connection and Yamux session over MTP
func (m *Manager) StartSession(initialDomain string) (*yamux.Session, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.session != nil && !m.session.IsClosed() {
		return m.session, nil
	}

	// Log if protected sockets are enabled
	if network.IsProtectorSet() {
		log.Printf("[Transport] Using protected sockets (Android VpnService mode)")
	}

	// 1. Dial MTP (UDP) - uses protected dialer internally
	conn, err := mtp.Dial(m.resolver, m.serverAddr, m.uuid)
	if err != nil {
		return nil, fmt.Errorf("mtp dial failed: %w", err)
	}

	// 2. Wrap in VirtualConn
	m.currentConn = conn
	m.mtpConn = conn
	m.virtualConn = NewVirtualConn(conn)

	// 3. Init Yamux Client
	yamuxCfg := yamux.DefaultConfig()
	yamuxCfg.MaxStreamWindowSize = 16 * 1024 * 1024
	yamuxCfg.EnableKeepAlive = false                   // MTP handles keepalives natively
	yamuxCfg.ConnectionWriteTimeout = 10 * time.Minute // Prevent sudden session shutdowns
	yamuxCfg.StreamCloseTimeout = 30 * time.Second

	session, err := yamux.Client(m.virtualConn, yamuxCfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("yamux client init failed: %w", err)
	}

	m.session = session
	log.Printf("[Transport] Session established via MTP")
	return session, nil
}

// RotateTransport switches the underlying transport to a new domain
// while keeping the Yamux session alive via MTP session migration.
// Uses protected dialer for Android VpnService compatibility.
func (m *Manager) RotateTransport(newDomain string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.virtualConn == nil {
		return fmt.Errorf("session not initialized")
	}

	log.Printf("[Transport] Rotating transport (MTP migration)...")

	// 1. Migrate existing MTPConn (seamlessly swaps underlying UDP socket while keeping ARQ state)
	if err := m.mtpConn.Migrate(m.resolver, m.serverAddr); err != nil {
		return fmt.Errorf("failed to migrate MTP session: %w", err)
	}

	// The VirtualConn and Yamux session on top remain fully valid since mtpConn internally swapped sockets!

	log.Printf("[Transport] Transport rotated successfully via MTP migration")
	return nil
}

// GetMTPConn returns the current MTP connection (for stats)
func (m *Manager) GetMTPConn() *mtp.MTPConn {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.mtpConn
}
