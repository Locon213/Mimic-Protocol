package client

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/config"
	"github.com/Locon213/Mimic-Protocol/pkg/version"
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

	// Internal
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// MTP connection (placeholder - would be implemented in actual transport)
	mtpConn interface{}
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
	}

	c.status.Store(int32(StatusDisconnected))

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

	// In a real implementation, this would:
	// 1. Establish UDP connection to server
	// 2. Perform handshake
	// 3. Start yamux multiplexer
	// 4. Start background tasks (domain rolling, dummy traffic)

	c.connectedAt = time.Now()
	c.status.Store(int32(StatusConnected))

	// Start statistics collection
	c.wg.Go(c.collectStats)

	return nil
}

// Stop gracefully disconnects from the server
func (c *Client) Stop() {
	if c == nil {
		return
	}

	c.cancel()
	c.wg.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()

	c.status.Store(int32(StatusDisconnected))
}

// StartProxies starts the local proxy servers
func (c *Client) StartProxies() error {
	if c == nil {
		return ErrNilClient
	}

	// In a real implementation, this would start SOCKS5/HTTP proxies
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
