package client

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/config"
	"github.com/Locon213/Mimic-Protocol/pkg/mtp"
	"github.com/Locon213/Mimic-Protocol/pkg/network"
	"github.com/Locon213/Mimic-Protocol/pkg/proxy"
	"github.com/Locon213/Mimic-Protocol/pkg/routing"
	"github.com/Locon213/Mimic-Protocol/pkg/tunnel"
	"github.com/Locon213/Mimic-Protocol/pkg/version"
	"github.com/hashicorp/yamux"
)

// ConnectionStatus represents the current connection state
type ConnectionStatus int32

const (
	StatusDisconnected ConnectionStatus = iota
	StatusConnecting
	StatusConnected
	StatusReconnecting
)

// String returns the string representation of ConnectionStatus
func (s ConnectionStatus) String() string {
	switch s {
	case StatusDisconnected:
		return "disconnected"
	case StatusConnecting:
		return "connecting"
	case StatusConnected:
		return "connected"
	case StatusReconnecting:
		return "reconnecting"
	default:
		return "unknown"
	}
}

// NetworkStats holds network statistics
type NetworkStats struct {
	DownloadSpeed int64 // bytes per second
	UploadSpeed   int64 // bytes per second
	Ping          int64 // milliseconds
	TotalDownload int64 // total bytes received
	TotalUpload   int64 // total bytes sent
	LastUpdated   time.Time
}

// SessionInfo holds current session information
type SessionInfo struct {
	ConnectedAt   time.Time
	ServerAddress string
	ServerName    string
	UUID          string
	Transport     string
	CurrentDomain string
	Status        ConnectionStatus
	Uptime        time.Duration
}

// TrafficCallback is a function type for traffic updates
type TrafficCallback func(stats NetworkStats)

// Client represents the Mimic Protocol client
type Client struct {
	cfg    *config.ClientConfig
	status atomic.Int32
	mu     sync.RWMutex

	// Statistics
	networkStats  NetworkStats
	connectedAt   time.Time
	currentDomain string

	// Callbacks
	trafficCallback TrafficCallback

	// internal
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// MTP & Tunnel
	session *yamux.Session
	proxies []interface{ Close() error }
	tun     *tunnel.Tunnel // Android TUN tunnel instance
}

// NewClient creates a new Mimic client instance
func NewClient(cfg *config.ClientConfig) (*Client, error) {
	if cfg == nil {
		cfg = &config.ClientConfig{}
	}

	ctx, cancel := context.WithCancel(context.Background())

	c := &Client{
		cfg:    cfg,
		status: atomic.Int32{},
		ctx:    ctx,
		cancel: cancel,
		tun:    tunnel.New(), // Initialize TUN tunnel instance
	}

	c.status.Store(int32(StatusDisconnected))

	// Initialize protected sockets if enabled for Android
	if cfg.Android.UseProtectedSockets {
		log.Printf("[Client] Protected sockets enabled for Android VpnService")
	}

	return c, nil
}

// Start connects to the MTP server
func (c *Client) Start(ctx context.Context) error {
	if c == nil {
		return ErrNilClient
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.status.Load() == int32(StatusConnected) {
		return ErrAlreadyConnected
	}

	c.status.Store(int32(StatusConnecting))

	// Start TUN tunnel if enabled for Android
	if c.cfg.Android.EnableTUN && c.cfg.Android.TUNFD > 0 {
		log.Printf("[Client] Starting Android TUN tunnel (fd=%d, mtu=%d)", c.cfg.Android.TUNFD, c.cfg.Android.MTU)
		if err := c.tun.StartTunnelFromFD(c.cfg.Android.TUNFD, c.cfg.Android.MTU); err != nil {
			c.status.Store(int32(StatusDisconnected))
			return fmt.Errorf("tunnel start failed: %w", err)
		}
		log.Printf("[Client] TUN tunnel started successfully")
	}

	// 1. Initialize Resolver
	resolver := network.NewCachedResolver(c.cfg.DNS, 10*time.Minute)

	// 2. Connect via MTP
	mtpConn, err := mtp.Dial(resolver, c.cfg.Server, c.cfg.UUID)
	if err != nil {
		c.status.Store(int32(StatusDisconnected))
		return fmt.Errorf("mtp dial failed: %w", err)
	}

	// 3. Start yamux session
	session, err := yamux.Client(mtpConn, nil)
	if err != nil {
		mtpConn.Close()
		c.status.Store(int32(StatusDisconnected))
		return fmt.Errorf("yamux session failed: %w", err)
	}

	c.session = session
	c.connectedAt = time.Now()
	c.status.Store(int32(StatusConnected))

	// Start statistics collection
	go c.collectStats()

	log.Printf("✅ Connected to %s via MTP", c.cfg.Server)
	return nil
}

// Stop gracefully disconnects from the server
func (c *Client) Stop() {
	if c == nil {
		return
	}

	c.cancel()

	// Stop TUN tunnel
	if c.tun != nil {
		if err := c.tun.StopTunnel(); err != nil {
			log.Printf("[Client] Warning: tunnel stop error: %v", err)
		}
	}

	// Stop proxies
	c.mu.Lock()
	for _, p := range c.proxies {
		p.Close()
	}
	c.proxies = nil
	c.mu.Unlock()

	// Stop session
	if c.session != nil {
		c.session.Close()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.status.Store(int32(StatusDisconnected))
	log.Println("🛑 Client stopped")
}

// StartProxies starts the local proxy servers
func (c *Client) StartProxies() error {
	if c == nil {
		return ErrNilClient
	}

	// 1. Initialize Router
	var rules []*routing.Rule
	for _, r := range c.cfg.Routing.Rules {
		rules = append(rules, &routing.Rule{
			Type:   r.Type,
			Value:  r.Value,
			Policy: routing.Policy(r.Policy),
		})
	}
	router := routing.NewRouter(rules, routing.Policy(c.cfg.Routing.DefaultPolicy))

	// 2. Start configured proxies
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, pCfg := range c.cfg.Proxies {
		bindAddr := fmt.Sprintf("0.0.0.0:%d", pCfg.Port)
		switch pCfg.Type {
		case "socks5":
			srv, err := proxy.NewSOCKS5Server(bindAddr, c.session, router)
			if err != nil {
				return fmt.Errorf("failed to start SOCKS5 on %s: %w", bindAddr, err)
			}
			go srv.Serve()
			c.proxies = append(c.proxies, srv)
			log.Printf("🚀 SOCKS5 proxy listening on %s", bindAddr)

		case "http":
			srv, err := proxy.NewHTTPProxyServer(bindAddr, c.session, router)
			if err != nil {
				return fmt.Errorf("failed to start HTTP proxy on %s: %w", bindAddr, err)
			}
			go srv.Serve()
			c.proxies = append(c.proxies, srv)
			log.Printf("🚀 HTTP proxy listening on %s", bindAddr)
		}
	}

	return nil
}

// GetVersion returns the current SDK version
func (c *Client) GetVersion() string {
	return version.GetVersion()
}

// ============================================
// Network Statistics Functions
// ============================================

// GetNetworkStats returns current network statistics (speed, ping, total traffic)
func (c *Client) GetNetworkStats() NetworkStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := c.networkStats
	stats.LastUpdated = time.Now()
	return stats
}

// UpdateSpeed updates the download/upload speed
// This function can be called to update speed statistics via MTP
func (c *Client) UpdateSpeed(downloadSpeed, uploadSpeed int64) {
	atomic.StoreInt64(&c.networkStats.DownloadSpeed, downloadSpeed)
	atomic.StoreInt64(&c.networkStats.UploadSpeed, uploadSpeed)
}

// UpdatePing updates the ping value
// This function can be called to update ping via MTP
func (c *Client) UpdatePing(pingMs int64) {
	atomic.StoreInt64(&c.networkStats.Ping, pingMs)
}

// UpdateTraffic updates total traffic counters
func (c *Client) UpdateTraffic(download, upload int64) {
	atomic.StoreInt64(&c.networkStats.TotalDownload, download)
	atomic.StoreInt64(&c.networkStats.TotalUpload, upload)
}

// SendSpeedData sends speed and ping data to the server via MTP
// This is the main function requested for sending data through MTP protocol
func (c *Client) SendSpeedData(ctx context.Context, downloadSpeed, uploadSpeed int64, pingMs int64) error {
	if c == nil {
		return ErrNilClient
	}

	if c.status.Load() != int32(StatusConnected) {
		return ErrNotConnected
	}

	// Update local statistics
	c.UpdateSpeed(downloadSpeed, uploadSpeed)
	c.UpdatePing(pingMs)

	// In a real implementation, this would send the data via MTP to the server
	// The server could use this for:
	// - Quality monitoring
	// - Dynamic routing decisions
	// - User statistics

	return nil
}

// SetTrafficCallback sets a callback for traffic updates
func (c *Client) SetTrafficCallback(callback TrafficCallback) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trafficCallback = callback
}

// ============================================
// Additional SDK Functions
// ============================================

// GetConnectionStatus returns the current connection status
func (c *Client) GetConnectionStatus() ConnectionStatus {
	if c == nil {
		return StatusDisconnected
	}
	return ConnectionStatus(c.status.Load())
}

// GetSessionInfo returns current session information
func (c *Client) GetSessionInfo() *SessionInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	info := &SessionInfo{
		ConnectedAt:   c.connectedAt,
		ServerAddress: c.cfg.Server,
		ServerName:    c.cfg.ServerName,
		UUID:          c.cfg.UUID,
		Transport:     c.cfg.Transport,
		CurrentDomain: c.currentDomain,
		Status:        c.GetConnectionStatus(),
	}

	if !c.connectedAt.IsZero() {
		info.Uptime = time.Since(c.connectedAt)
	}

	return info
}

// GetTrafficStats returns total traffic statistics
func (c *Client) GetTrafficStats() (totalDownload, totalUpload int64) {
	stats := c.GetNetworkStats()
	return stats.TotalDownload, stats.TotalUpload
}

// GetCurrentDomain returns the currently active disguise domain
func (c *Client) GetCurrentDomain() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentDomain
}

// Reconnect disconnects and reconnects to the server
func (c *Client) Reconnect(ctx context.Context) error {
	if c == nil {
		return ErrNilClient
	}

	c.Stop()
	time.Sleep(100 * time.Millisecond) // Brief pause before reconnecting
	return c.Start(ctx)
}

// GetServerInfo returns server configuration info
func (c *Client) GetServerInfo() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return map[string]interface{}{
		"server":     c.cfg.Server,
		"serverName": c.cfg.ServerName,
		"uuid":       c.cfg.UUID,
		"domains":    c.cfg.Domains,
		"transport":  c.cfg.Transport,
		"dns":        c.cfg.DNS,
	}
}

// IsConnected returns true if client is connected
func (c *Client) IsConnected() bool {
	return c.GetConnectionStatus() == StatusConnected
}

// collectStats is an internal goroutine for collecting statistics
func (c *Client) collectStats() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			stats := c.GetNetworkStats()
			if c.trafficCallback != nil {
				c.trafficCallback(stats)
			}
		}
	}
}

// ============================================
// Android VpnService Integration
// ============================================

// SetSocketProtector sets the socket protector callback for Android VpnService.
// This must be called before Start() if using protected sockets.
// The protector function will be called with each socket fd before dialing.
func (c *Client) SetSocketProtector(protector func(fd int) bool) {
	if protector != nil {
		log.Printf("[Client] Setting up socket protector for Android VpnService")
		network.SetSocketProtector(protector)
	}
}

// StartTunnelFromFD starts the Android TUN tunnel with the given file descriptor.
// This is an alternative to enabling TUN in config - can be called at runtime.
// fd: TUN file descriptor from VpnService
// mtu: MTU for TUN interface (0 for default 1500)
func (c *Client) StartTunnelFromFD(fd int, mtu int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.tun == nil {
		c.tun = tunnel.New()
	}

	log.Printf("[Client] Starting TUN tunnel from fd=%d, mtu=%d", fd, mtu)
	if err := c.tun.StartTunnelFromFD(fd, mtu); err != nil {
		return fmt.Errorf("tunnel start failed: %w", err)
	}

	log.Printf("[Client] TUN tunnel started successfully")
	return nil
}

// StopTunnel stops the Android TUN tunnel.
func (c *Client) StopTunnel() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.tun == nil {
		return nil
	}

	log.Printf("[Client] Stopping TUN tunnel")
	return c.tun.StopTunnel()
}

// IsTunnelRunning returns true if the TUN tunnel is currently running.
func (c *Client) IsTunnelRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tun != nil && c.tun.IsTunnelRunning()
}

// GetTunnelInfo returns information about the TUN tunnel.
func (c *Client) GetTunnelInfo() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.tun == nil {
		return map[string]interface{}{"enabled": false}
	}

	info := c.tun.GetTunnelInfo()
	info["enabled"] = true
	return info
}

// Errors
var (
	ErrNilClient        = &ClientError{"client is nil"}
	ErrAlreadyConnected = &ClientError{"already connected"}
	ErrNotConnected     = &ClientError{"not connected"}
)

// ClientError represents a client error
type ClientError struct {
	msg string
}

func (e *ClientError) Error() string {
	return e.msg
}
