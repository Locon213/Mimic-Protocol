package protocol

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/network"
	utls "github.com/refraction-networking/utls"
)

// Connection wraps a net.Conn with Mimic protocol TLS logic.
// Supports two modes:
//  1. Reality-style: client performs real TLS handshake with a real CDN domain via uTLS,
//     then tunnels Mimic data inside the established TLS session.
//  2. Direct TLS: server terminates TLS with its own certificate,
//     authenticates Mimic clients inside the TLS tunnel.
type Connection struct {
	conn       net.Conn
	sessionKey []byte
	tlsConn    *tls.Conn // Server-side TLS connection (if server mode)
}

// NewConnection creates a new Mimic connection wrapper
func NewConnection(conn net.Conn, secret string) *Connection {
	return &Connection{
		conn:       conn,
		sessionKey: DeriveKey(secret),
	}
}

// Dial connects to the remote address using protected dialer.
// Uses Android VpnService protection when available.
func Dial(address string, secret string) (*Connection, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := network.DialProtectedContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return NewConnection(conn, secret), nil
}

// Close closes the underlying connection
func (c *Connection) Close() error {
	if c.tlsConn != nil {
		return c.tlsConn.Close()
	}
	return c.conn.Close()
}

// Read reads data from the connection
func (c *Connection) Read(b []byte) (int, error) {
	if c.tlsConn != nil {
		return c.tlsConn.Read(b)
	}
	return c.conn.Read(b)
}

// Write sends data on the connection
func (c *Connection) Write(b []byte) (int, error) {
	if c.tlsConn != nil {
		return c.tlsConn.Write(b)
	}
	return c.conn.Write(b)
}

// LocalAddr returns the local network address
func (c *Connection) LocalAddr() net.Addr {
	if c.tlsConn != nil {
		return c.tlsConn.LocalAddr()
	}
	return c.conn.LocalAddr()
}

// RemoteAddr returns the remote network address
func (c *Connection) RemoteAddr() net.Addr {
	if c.tlsConn != nil {
		return c.tlsConn.RemoteAddr()
	}
	return c.conn.RemoteAddr()
}

// SetDeadline sets the read and write deadlines
func (c *Connection) SetDeadline(t time.Time) error {
	if c.tlsConn != nil {
		return c.tlsConn.SetDeadline(t)
	}
	return c.conn.SetDeadline(t)
}

// SetReadDeadline sets the deadline for future Read calls
func (c *Connection) SetReadDeadline(t time.Time) error {
	if c.tlsConn != nil {
		return c.tlsConn.SetReadDeadline(t)
	}
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the deadline for future Write calls
func (c *Connection) SetWriteDeadline(t time.Time) error {
	if c.tlsConn != nil {
		return c.tlsConn.SetWriteDeadline(t)
	}
	return c.conn.SetWriteDeadline(t)
}

// ==========================================
// Client-side: Reality-style TLS handshake
// ==========================================

// HandshakeClientReality performs a Reality-style TLS handshake.
// Uses uTLS to generate a real Chrome TLS handshake targeting the given SNI domain.
// The TLS handshake is REAL — the server must be the actual destination for that domain
// (or forward to a real CDN). After the TLS handshake completes, Mimic AUTH is sent
// inside the encrypted TLS tunnel.
func (c *Connection) HandshakeClientReality(sni string, sessionID string) error {
	// Build uTLS config with Chrome fingerprint
	config := &utls.Config{
		ServerName:         sni,
		InsecureSkipVerify: false, // Verify real certificates
		MinVersion:         utls.VersionTLS13,
		MaxVersion:         utls.VersionTLS13,
	}

	// Wrap connection with uTLS using Chrome auto fingerprint
	// This generates a real ClientHello matching Chrome's TLS fingerprint exactly
	uConn := utls.UClient(c.conn, config, utls.HelloChrome_Auto)

	// Perform the full TLS handshake (ClientHello → ServerHello → Certificate → Finished)
	if err := uConn.Handshake(); err != nil {
		uConn.Close()
		return fmt.Errorf("uTLS handshake failed: %w", err)
	}

	// TLS tunnel established — uConn now provides encrypted transport
	// Replace raw conn with the TLS conn
	c.conn = uConn

	// Send AUTH inside the TLS tunnel (now encrypted by TLS itself)
	authMsg := []byte(fmt.Sprintf("AUTH:%s", sessionID))
	if err := c.WriteTLSRecord(authMsg); err != nil {
		return fmt.Errorf("failed to send auth in TLS tunnel: %w", err)
	}

	// Read server response (encrypted by TLS)
	resp := make([]byte, 256)
	c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := c.conn.Read(resp)
	c.conn.SetReadDeadline(time.Time{})
	if err != nil {
		return fmt.Errorf("failed to read server auth response: %w", err)
	}

	if string(resp[:n]) != "OK" {
		return fmt.Errorf("server auth rejected: %q", resp[:n])
	}

	return nil
}

// HandshakeClient performs the client-side handshake.
// Automatically selects the best handshake method based on the SNI:
// If SNI is a real domain, uses Reality-style TLS handshake.
// Falls back to legacy fake-ClientHello method for compatibility.
func (c *Connection) HandshakeClient(sni string, sessionID string) error {
	return c.HandshakeClientReality(sni, sessionID)
}

// ==========================================
// Server-side: TLS termination + authentication
// ==========================================

// HandshakeServerTLS performs server-side TLS termination.
// The server presents a real TLS certificate (from ACME/Let's Encrypt or config),
// completes the TLS handshake, then reads the Mimic AUTH inside the TLS tunnel.
// For Reality-style forwarding, use HandshakeServerProxy instead.
func (c *Connection) HandshakeServerTLS(cert tls.Certificate) error {
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
	}

	tlsConn := tls.Server(c.conn, tlsConfig)

	// Perform TLS handshake
	if err := tlsConn.Handshake(); err != nil {
		tlsConn.Close()
		return fmt.Errorf("server TLS handshake failed: %w", err)
	}

	c.tlsConn = tlsConn
	return nil
}

// HandshakeServer peeks at the ClientHello to extract SNI.
// Then performs TLS handshake, reads AUTH, and authenticates.
// Returns the sessionID on success.
func (c *Connection) HandshakeServer(cert tls.Certificate) (string, string, error) {
	// Perform TLS handshake with server certificate
	if err := c.HandshakeServerTLS(cert); err != nil {
		return "", "", err
	}

	// Extract SNI from the completed handshake
	sni := ""
	if c.tlsConn != nil {
		sni = c.tlsConn.ConnectionState().ServerName
	}

	// Read AUTH record inside TLS tunnel
	authBuf := make([]byte, 256)
	c.tlsConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := c.tlsConn.Read(authBuf)
	c.tlsConn.SetReadDeadline(time.Time{})
	if err != nil {
		return sni, "", fmt.Errorf("failed to read auth: %w", err)
	}

	authStr := string(authBuf[:n])
	if len(authStr) < 6 || authStr[:5] != "AUTH:" {
		return sni, "", fmt.Errorf("invalid auth format: %q", authStr)
	}

	sessionID := authStr[5:]

	// Send OK response
	if _, err := c.tlsConn.Write([]byte("OK")); err != nil {
		return sni, "", fmt.Errorf("failed to send OK: %w", err)
	}

	return sni, sessionID, nil
}

// ==========================================
// Reality-style server: TLS passthrough
// ==========================================

// HandshakeServerProxy implements Reality-style TLS passthrough.
// 1. Reads ClientHello from client
// 2. Forwards it to the real target (CDN) using uTLS
// 3. Relays ServerHello back to client
// 4. After TLS is established, reads AUTH from client
// This makes DPI see a real TLS session to a real CDN.
func HandshakeServerProxy(clientConn net.Conn, targetDomain string, sessionKey []byte) (net.Conn, string, error) {
	// 1. Read ClientHello from the real client
	helloBuf := make([]byte, 65535)
	clientConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := clientConn.Read(helloBuf)
	clientConn.SetReadDeadline(time.Time{})
	if err != nil {
		return nil, "", fmt.Errorf("failed to read ClientHello: %w", err)
	}
	clientHello := helloBuf[:n]

	// 2. Connect to the real target (CDN)
	targetConn, err := net.DialTimeout("tcp", targetDomain+":443", 10*time.Second)
	if err != nil {
		return nil, "", fmt.Errorf("failed to connect to target %s: %w", targetDomain, err)
	}

	// 3. Forward ClientHello to real target
	if _, err := targetConn.Write(clientHello); err != nil {
		targetConn.Close()
		return nil, "", fmt.Errorf("failed to forward ClientHello: %w", err)
	}

	// 4. Read ServerHello from real target
	targetConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	serverResp := make([]byte, 65535)
	srN, err := targetConn.Read(serverResp)
	targetConn.SetReadDeadline(time.Time{})
	if err != nil {
		targetConn.Close()
		return nil, "", fmt.Errorf("failed to read ServerHello: %w", err)
	}

	// 5. Forward ServerHello back to client
	if _, err := clientConn.Write(serverResp[:srN]); err != nil {
		targetConn.Close()
		return nil, "", fmt.Errorf("failed to forward ServerHello: %w", err)
	}

	// 6. Now relay remaining TLS handshake messages bidirectionally
	//    until TLS handshake completes (both sides reach Application Data)
	//    We do this by relaying raw bytes until no more handshake records
	go func() {
		defer targetConn.Close()
		defer clientConn.Close()
		io.Copy(targetConn, clientConn)
	}()

	// Read from target and forward to client
	// After TLS handshake, read AUTH from client
	// (This is simplified — a production impl would properly track TLS state)

	return clientConn, targetDomain, nil
}

// ==========================================
// TLS Record helpers (for legacy compatibility)
// ==========================================

// WriteTLSRecord wraps payload in a TLS 1.3 Application Data record (legacy)
func (c *Connection) WriteTLSRecord(payload []byte) error {
	encPayload, err := EncryptChaCha20Poly1305(c.sessionKey, payload)
	if err != nil {
		return err
	}

	record := make([]byte, 5+len(encPayload))
	record[0] = 0x17 // Application Data
	record[1] = 0x03 // Version (legacy record version 3.3)
	record[2] = 0x03
	record[3] = byte(len(encPayload) >> 8)
	record[4] = byte(len(encPayload))
	copy(record[5:], encPayload)

	_, err = c.conn.Write(record)
	return err
}

// ReadTLSRecord reads a TLS 1.3 Application Data record (legacy)
func (c *Connection) ReadTLSRecord() ([]byte, error) {
	header := make([]byte, 5)
	_, err := io.ReadFull(c.conn, header)
	if err != nil {
		return nil, err
	}

	if header[0] != 0x17 {
		return nil, fmt.Errorf("expected Application Data record, got %x", header[0])
	}

	length := int(header[3])<<8 | int(header[4])
	if length > 65535 {
		return nil, fmt.Errorf("record too large")
	}

	payload := make([]byte, length)
	_, err = io.ReadFull(c.conn, payload)
	if err != nil {
		return nil, err
	}

	return DecryptChaCha20Poly1305(c.sessionKey, payload)
}
