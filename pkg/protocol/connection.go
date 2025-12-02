package protocol

import (
	"fmt"
	"net"
	"time"
)

// Connection wraps a net.Conn with Mimic protocol logic
type Connection struct {
	conn net.Conn
}

// NewConnection creates a new Mimic connection wrapper
func NewConnection(conn net.Conn) *Connection {
	return &Connection{
		conn: conn,
	}
}

// Dial connects to the remote address
func Dial(address string) (*Connection, error) {
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return NewConnection(conn), nil
}

// Close closes the underlying connection
func (c *Connection) Close() error {
	return c.conn.Close()
}

// Read reads data
func (c *Connection) Read(b []byte) (int, error) {
	return c.conn.Read(b)
}

// Write sends data
func (c *Connection) Write(b []byte) (int, error) {
	return c.conn.Write(b)
}

// LocalAddr returns the local network address
func (c *Connection) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// RemoteAddr returns the remote network address
func (c *Connection) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// SetDeadline sets the read and write deadlines
func (c *Connection) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

// SetReadDeadline sets the deadline for future Read calls
func (c *Connection) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the deadline for future Write calls
func (c *Connection) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// GenerateFakeClientHello creates a byte slice looking like a TLS 1.3 ClientHello
// targeted at the specified SNI (Server Name Indication) and Session ID
func (c *Connection) GenerateFakeClientHello(sni string, sessionID string) []byte {
	buf := make([]byte, 0, 512)

	// TLS Record Header
	buf = append(buf, 0x16)       // Content Type: Handshake
	buf = append(buf, 0x03, 0x01) // Version: TLS 1.0
	buf = append(buf, 0x00, 0x00) // Length placeholder

	// Client Hello
	// handshakeStart := len(buf) // Not used in simplified version
	buf = append(buf, 0x01) // Msg Type: Client Hello
	// Length placeholder
	buf = append(buf, 0x00, 0x00, 0x00)
	buf = append(buf, 0x03, 0x03) // Version: TLS 1.2

	// Format: MIMIC_HELLO_SNI:<sni>|SID:<session_id>
	return []byte(fmt.Sprintf("MIMIC_HELLO_SNI:%s|SID:%s", sni, sessionID))
}

// HandshakeClient performs the client-side handshake
func (c *Connection) HandshakeClient(sni string, sessionID string) error {
	hello := c.GenerateFakeClientHello(sni, sessionID)

	_, err := c.conn.Write(hello)
	if err != nil {
		return fmt.Errorf("failed to send ClientHello: %w", err)
	}

	// Wait for ServerHello response
	buf := make([]byte, 1024)
	n, err := c.conn.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read ServerHello: %w", err)
	}

	response := string(buf[:n])
	if response != "MIMIC_HELLO_OK" {
		return fmt.Errorf("handshake failed, invalid server response: %s", response)
	}

	return nil
}

// HandshakeServer performs the server-side handshake
// Returns SNI and SessionID
func (c *Connection) HandshakeServer() (string, string, error) {
	buf := make([]byte, 1024)
	n, err := c.conn.Read(buf)
	if err != nil {
		return "", "", fmt.Errorf("failed to read ClientHello: %w", err)
	}

	data := string(buf[:n])

	// Simple parsing manually to avoid Sscanf issues with separators
	// Expected: MIMIC_HELLO_SNI:vk.com|SID:12345
	var sni, sessionID string

	if len(data) > 16 && data[:16] == "MIMIC_HELLO_SNI:" {
		rest := data[16:]
		// Find separator
		for i, char := range rest {
			if char == '|' {
				sni = rest[:i]
				if len(rest) > i+5 && rest[i+1:i+5] == "SID:" {
					sessionID = rest[i+5:]
				}
				break
			}
		}
		// Fallback if no SID (old clients or simple test)
		if sni == "" {
			sni = rest // Assume whole thing is SNI if no separator
		}
	} else {
		return "", "", fmt.Errorf("invalid handshake format")
	}

	_, err = c.conn.Write([]byte("MIMIC_HELLO_OK"))
	if err != nil {
		return "", "", fmt.Errorf("failed to send ServerHello: %w", err)
	}

	return sni, sessionID, nil
}
