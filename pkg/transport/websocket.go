package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocketConfig holds WebSocket-specific configuration
type WebSocketConfig struct {
	// Path is the URL path for WebSocket endpoint (e.g., "/ws")
	Path string

	// Host is the Host header value for masquerading
	Host string

	// TLS enables WSS (WebSocket over TLS)
	TLS bool

	// TLSConfig allows custom TLS configuration
	TLSConfig *tls.Config

	// Headers are additional HTTP headers to send during handshake
	Headers http.Header

	// HandshakeTimeout is the timeout for WebSocket handshake
	HandshakeTimeout time.Duration

	// ReadBufferSize is the size of the read buffer
	ReadBufferSize int

	// WriteBufferSize is the size of the write buffer
	WriteBufferSize int
}

// DefaultWebSocketConfig returns default WebSocket configuration
func DefaultWebSocketConfig() *WebSocketConfig {
	return &WebSocketConfig{
		Path:             "/ws",
		Host:             "",
		TLS:              false,
		HandshakeTimeout: 30 * time.Second,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
	}
}

// WebSocketTransport implements Transport interface for WebSocket connections
type WebSocketTransport struct {
	config *WebSocketConfig
	dialer *websocket.Dialer
	server *http.Server
	mu     sync.RWMutex
	closed bool
}

// NewWebSocketTransport creates a new WebSocket transport
func NewWebSocketTransport(config *WebSocketConfig) *WebSocketTransport {
	if config == nil {
		config = DefaultWebSocketConfig()
	}

	dialer := &websocket.Dialer{
		HandshakeTimeout: config.HandshakeTimeout,
		ReadBufferSize:   config.ReadBufferSize,
		WriteBufferSize:  config.WriteBufferSize,
	}

	if config.TLS {
		dialer.TLSClientConfig = config.TLSConfig
	}

	return &WebSocketTransport{
		config: config,
		dialer: dialer,
	}
}

// Dial connects to the remote WebSocket server
func (t *WebSocketTransport) Dial(ctx context.Context, address string) (net.Conn, error) {
	t.mu.RLock()
	if t.closed {
		t.mu.RUnlock()
		return nil, fmt.Errorf("ws: transport closed")
	}
	t.mu.RUnlock()

	// Parse address and build WebSocket URL
	wsURL, err := t.buildWebSocketURL(address)
	if err != nil {
		return nil, fmt.Errorf("ws: invalid address: %w", err)
	}

	// Prepare headers
	headers := make(http.Header)
	if t.config.Host != "" {
		headers.Set("Host", t.config.Host)
	}
	// Copy custom headers
	for k, v := range t.config.Headers {
		headers[k] = v
	}

	// Dial WebSocket
	wsConn, resp, err := t.dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		if resp != nil {
			log.Printf("[WS] Dial failed: %v (status: %d)", err, resp.StatusCode)
		}
		return nil, fmt.Errorf("ws: dial failed: %w", err)
	}

	log.Printf("[WS] Connected to %s", wsURL)

	// Wrap WebSocket connection as net.Conn
	return &wsConnWrapper{
		wsConn: wsConn,
		local:  &wsAddr{network: "ws", address: "local"},
		remote: &wsAddr{network: "ws", address: address},
	}, nil
}

// Listen starts a WebSocket server
func (t *WebSocketTransport) Listen(address string) (net.Listener, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil, fmt.Errorf("ws: transport closed")
	}

	listener := &wsListener{
		address:  address,
		config:   t.config,
		acceptCh: make(chan net.Conn, 256),
		closeCh:  make(chan struct{}),
	}

	// Setup HTTP server with WebSocket handler
	mux := http.NewServeMux()
	mux.HandleFunc(t.config.Path, listener.handleWebSocket)

	t.server = &http.Server{
		Addr:    address,
		Handler: mux,
	}

	// Start server in background
	go func() {
		var err error
		if t.config.TLS {
			if t.config.TLSConfig == nil {
				log.Printf("[WS] TLS enabled but no TLSConfig provided")
				return
			}
			err = t.server.ListenAndServeTLS("", "")
		} else {
			err = t.server.ListenAndServe()
		}

		if err != nil && err != http.ErrServerClosed {
			log.Printf("[WS] Server error: %v", err)
		}
	}()

	log.Printf("[WS] Listening on %s (path: %s)", address, t.config.Path)
	return listener, nil
}

// Name returns the transport name
func (t *WebSocketTransport) Name() string {
	if t.config.TLS {
		return "wss"
	}
	return "ws"
}

// Close closes the WebSocket transport
func (t *WebSocketTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true

	if t.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return t.server.Shutdown(ctx)
	}

	return nil
}

// buildWebSocketURL constructs WebSocket URL from address
func (t *WebSocketTransport) buildWebSocketURL(address string) (string, error) {
	// Parse address
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		// Try to parse as host without port
		host = address
		port = "443"
		if !t.config.TLS {
			port = "80"
		}
	}

	// Build URL
	scheme := "ws"
	if t.config.TLS {
		scheme = "wss"
	}

	path := t.config.Path
	if path == "" {
		path = "/ws"
	}

	wsURL := fmt.Sprintf("%s://%s:%s%s", scheme, host, port, path)
	return wsURL, nil
}

// wsAddr implements net.Addr for WebSocket
type wsAddr struct {
	network string
	address string
}

func (a *wsAddr) Network() string { return a.network }
func (a *wsAddr) String() string  { return a.address }

// wsConnWrapper wraps websocket.Conn to implement net.Conn
type wsConnWrapper struct {
	wsConn *websocket.Conn
	local  net.Addr
	remote net.Addr
	mu     sync.Mutex
}

// Read reads data from WebSocket connection
func (c *wsConnWrapper) Read(b []byte) (int, error) {
	_, message, err := c.wsConn.ReadMessage()
	if err != nil {
		return 0, err
	}
	n := copy(b, message)
	return n, nil
}

// Write writes data to WebSocket connection
func (c *wsConnWrapper) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.wsConn.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

// Close closes the WebSocket connection
func (c *wsConnWrapper) Close() error {
	return c.wsConn.Close()
}

// LocalAddr returns the local network address
func (c *wsConnWrapper) LocalAddr() net.Addr {
	return c.local
}

// RemoteAddr returns the remote network address
func (c *wsConnWrapper) RemoteAddr() net.Addr {
	return c.remote
}

// SetDeadline sets the read and write deadlines
func (c *wsConnWrapper) SetDeadline(t time.Time) error {
	if err := c.wsConn.SetReadDeadline(t); err != nil {
		return err
	}
	return c.wsConn.SetWriteDeadline(t)
}

// SetReadDeadline sets the deadline for future Read calls
func (c *wsConnWrapper) SetReadDeadline(t time.Time) error {
	return c.wsConn.SetReadDeadline(t)
}

// SetWriteDeadline sets the deadline for future Write calls
func (c *wsConnWrapper) SetWriteDeadline(t time.Time) error {
	return c.wsConn.SetWriteDeadline(t)
}

// wsListener implements net.Listener for WebSocket server
type wsListener struct {
	address  string
	config   *WebSocketConfig
	acceptCh chan net.Conn
	closeCh  chan struct{}
	mu       sync.Mutex
	closed   bool
}

// Accept waits for and returns the next connection
func (l *wsListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.acceptCh:
		return conn, nil
	case <-l.closeCh:
		return nil, fmt.Errorf("ws: listener closed")
	}
}

// Close closes the listener
func (l *wsListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true
	close(l.closeCh)
	return nil
}

// Addr returns the listener's network address
func (l *wsListener) Addr() net.Addr {
	return &wsAddr{network: "ws", address: l.address}
}

// handleWebSocket handles incoming WebSocket connections
func (l *wsListener) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  l.config.ReadBufferSize,
		WriteBufferSize: l.config.WriteBufferSize,
		CheckOrigin: func(r *http.Request) bool {
			// Allow all origins for now
			// In production, you might want to validate Origin header
			return true
		},
	}

	wsConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade failed: %v", err)
		return
	}

	conn := &wsConnWrapper{
		wsConn: wsConn,
		local:  &wsAddr{network: "ws", address: l.address},
		remote: &wsAddr{network: "ws", address: r.RemoteAddr},
	}

	select {
	case l.acceptCh <- conn:
		log.Printf("[WS] Accepted connection from %s", r.RemoteAddr)
	case <-l.closeCh:
		conn.Close()
	default:
		// Buffer full, reject connection
		conn.Close()
		log.Printf("[WS] Connection buffer full, rejected %s", r.RemoteAddr)
	}
}

// ParseWebSocketURL parses a WebSocket URL and returns components
func ParseWebSocketURL(rawURL string) (scheme, host, port, path string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", "", "", err
	}

	scheme = u.Scheme
	if scheme != "ws" && scheme != "wss" {
		return "", "", "", "", fmt.Errorf("invalid WebSocket scheme: %s", scheme)
	}

	host = u.Hostname()
	port = u.Port()
	if port == "" {
		if scheme == "wss" {
			port = "443"
		} else {
			port = "80"
		}
	}

	path = u.Path
	if path == "" {
		path = "/ws"
	}

	return scheme, host, port, path, nil
}
