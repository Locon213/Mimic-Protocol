package transport

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/mtp"
	"github.com/Locon213/Mimic-Protocol/pkg/network"
	"github.com/hashicorp/yamux"
)

// RotationConfig holds domain rotation settings
type RotationConfig struct {
	SwitchMin time.Duration // Minimum interval between rotations
	SwitchMax time.Duration // Maximum interval between rotations
	Randomize bool          // If true, pick random domain; otherwise sequential
}

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

	// Domain rotation
	currentDomain string   // Current domain for TLS SNI masking
	domainIdx     int      // Current index in the domain list
	domains       []string // List of domains to rotate through
	rotCfg        RotationConfig
}

// NewManager creates a new transport manager
func NewManager(serverAddr string, uuid string, dns string) *Manager {
	return &Manager{
		serverAddr: serverAddr,
		uuid:       uuid,
		resolver:   network.NewCachedResolver(dns, 5*time.Minute),
		rotCfg: RotationConfig{
			SwitchMin: 60 * time.Second,
			SwitchMax: 300 * time.Second,
		},
	}
}

// SetDomains configures the list of domains for rotation
func (m *Manager) SetDomains(domains []string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.domains = domains
	if len(domains) > 0 {
		m.currentDomain = domains[0]
	}
}

// SetRotationConfig sets the rotation timing and randomization settings
func (m *Manager) SetRotationConfig(cfg RotationConfig) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if cfg.SwitchMin > 0 {
		m.rotCfg.SwitchMin = cfg.SwitchMin
	}
	if cfg.SwitchMax > cfg.SwitchMin {
		m.rotCfg.SwitchMax = cfg.SwitchMax
	} else if cfg.SwitchMax == 0 {
		m.rotCfg.SwitchMax = m.rotCfg.SwitchMin * 5
	}
	m.rotCfg.Randomize = cfg.Randomize
}

// NextRotationDelay returns a random delay until the next rotation based on config
func (m *Manager) NextRotationDelay() time.Duration {
	m.mutex.Lock()
	min, max := m.rotCfg.SwitchMin, m.rotCfg.SwitchMax
	m.mutex.Unlock()

	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int63n(int64(max-min)))
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

	// 3. Set initial domain
	if initialDomain != "" {
		m.currentDomain = initialDomain
	} else if len(m.domains) > 0 {
		m.currentDomain = m.domains[0]
	}

	// 4. Init Yamux Client
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

// RotateTransport performs a seamless MTP migration to a new transport.
// This swaps the underlying UDP socket while keeping the Yamux session alive.
// The VirtualConn buffers writes during the swap to prevent data loss.
func (m *Manager) RotateTransport(newDomain string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.virtualConn == nil || m.mtpConn == nil {
		return fmt.Errorf("session not initialized")
	}

	// Cycle to next domain if no specific domain provided
	if newDomain == "" && len(m.domains) > 1 {
		if m.rotCfg.Randomize {
			// Pick a random domain different from current
			newIdx := rand.Intn(len(m.domains) - 1)
			if newIdx >= m.domainIdx {
				newIdx++
			}
			m.domainIdx = newIdx
		} else {
			// Sequential rotation
			m.domainIdx = (m.domainIdx + 1) % len(m.domains)
		}
		newDomain = m.domains[m.domainIdx]
	}

	if newDomain != "" {
		log.Printf("[Transport] Seamless rotation -> domain: %s", newDomain)
	} else {
		log.Printf("[Transport] Seamless rotation (UDP socket refresh)")
	}

	// 1. Enable write buffering on VirtualConn to prevent data loss during swap
	m.virtualConn.swapMu.Lock()
	m.virtualConn.swapping = true
	m.virtualConn.swapBuf = nil
	m.virtualConn.swapMu.Unlock()

	// 2. Migrate MTPConn: opens new UDP socket, performs MIGRATE handshake,
	//    swaps the socket inside MTPConn (preserves ARQ state & sequence numbers)
	if err := m.mtpConn.Migrate(m.resolver, m.serverAddr); err != nil {
		// Restore normal write path on failure
		m.virtualConn.swapMu.Lock()
		m.virtualConn.swapping = false
		m.virtualConn.swapBuf = nil
		m.virtualConn.swapMu.Unlock()
		return fmt.Errorf("failed to migrate MTP session: %w", err)
	}

	// 3. MTPConn's internal socket is now new — VirtualConn wraps MTPConn (not the raw UDP),
	//    so VirtualConn automatically reads/writes through the new socket via MTPConn.
	//    No VirtualConn.SwapConnection needed — the MTPConn IS the connection VirtualConn wraps.

	// 4. Flush any buffered writes and restore normal write path
	m.virtualConn.swapMu.Lock()
	m.virtualConn.swapping = false
	buffered := m.virtualConn.swapBuf
	m.virtualConn.swapBuf = nil
	m.virtualConn.swapMu.Unlock()

	for _, data := range buffered {
		if _, err := m.virtualConn.conn.Write(data); err != nil {
			log.Printf("[Transport] Warning: failed to replay buffered write: %v", err)
			// Non-fatal: the data will be retransmitted by upper layers
			break
		}
	}

	// 5. Update current domain
	if newDomain != "" {
		m.currentDomain = newDomain
	}

	log.Printf("[Transport] Seamless rotation complete")
	return nil
}

// GetCurrentDomain returns the currently active domain for TLS SNI masking
func (m *Manager) GetCurrentDomain() string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.currentDomain
}

// GetMTPConn returns the current MTP connection (for stats)
func (m *Manager) GetMTPConn() *mtp.MTPConn {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.mtpConn
}

// GetSession returns the current yamux session
func (m *Manager) GetSession() *yamux.Session {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.session
}

// IsSessionAlive returns true if the yamux session is still usable
func (m *Manager) IsSessionAlive() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.session == nil {
		return false
	}
	return !m.session.IsClosed()
}

// Shutdown closes the transport manager and all underlying resources
func (m *Manager) Shutdown() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.session != nil {
		m.session.Close()
		m.session = nil
	}
	if m.mtpConn != nil {
		m.mtpConn.Close()
		m.mtpConn = nil
	}
	m.virtualConn = nil
}
