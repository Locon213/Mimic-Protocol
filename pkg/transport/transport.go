package transport

import (
	"context"
	"net"
)

// Transport is the interface that all transport protocols must implement.
// It provides a unified API for different transport mechanisms (MTP, WebSocket, QUIC, etc.)
type Transport interface {
	// Dial connects to the remote address and returns a net.Conn
	Dial(ctx context.Context, address string) (net.Conn, error)

	// Listen starts listening for incoming connections on the given address
	Listen(address string) (net.Listener, error)

	// Name returns the transport protocol name (e.g., "mtp", "ws", "quic")
	Name() string

	// Close closes the transport and releases resources
	Close() error
}

// Config holds common configuration for all transports
type Config struct {
	// Secret is the authentication secret/key
	Secret string

	// DNS is the DNS server address for resolution
	DNS string

	// Compression holds optional compression settings
	Compression *CompressionConfig
}

// CompressionConfig holds compression settings
type CompressionConfig struct {
	Enable  bool
	Level   int
	MinSize int
}
