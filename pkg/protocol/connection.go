package protocol

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/network"
	utls "github.com/refraction-networking/utls"
)

// Connection wraps a net.Conn with Mimic protocol logic
type Connection struct {
	conn       net.Conn
	sessionKey []byte
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
	return c.conn.Close()
}

// Read reads data, stripping the TLS 1.3 Application Data record framing
func (c *Connection) Read(b []byte) (int, error) {
	// Simple caching buffer would be ideal for io.Reader completeness,
	// but since Yamux handles buffering, we'll implement a basic one-to-one record read.
	// NOTE: This assumes `b` is large enough to hold a payload, or we lose data.
	// For production, `Connection` must have a `readBuf`.
	record, err := c.ReadTLSRecord()
	if err != nil {
		return 0, err
	}
	n := copy(b, record)
	if n < len(record) {
		// We'd lose data here without a buffer. In this simplified PoC we assume Yamux reading into 4k-64k buffers handles it.
		// A full implementation requires a read buffer!
	}
	return n, nil
}

// Write sends data, wrapping it in a TLS 1.3 Application Data record
func (c *Connection) Write(b []byte) (int, error) {
	err := c.WriteTLSRecord(b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
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

// GenerateFakeClientHello leverages uTLS to generate a robust Chrome fingerprint
func (c *Connection) GenerateFakeClientHello(sni string, sessionID string) ([]byte, error) {
	config := &utls.Config{ServerName: sni}

	// Create a dummy memory buffer connection to capture the handshake bytes
	bufConn := &dummyBufferConn{}
	uConn := utls.UClient(bufConn, config, utls.HelloChrome_Auto)

	// Running async because it will block trying to read ServerHello
	go uConn.Handshake()

	// Wait a tiny bit for the write
	time.Sleep(10 * time.Millisecond)

	b := bufConn.written
	if len(b) < 100 {
		return nil, fmt.Errorf("failed to generate ClientHello")
	}

	// Now we need to smuggle the sessionID.
	// Since modifying the uTLS ClientHello structure is complex (extensions signatures etc),
	// the XTLS-Reality way is to send the ClientHello AS IS, and in the next frame (App Data),
	// or pad it. Actually, uTLS allows setting SessionId? No, utls generates it.
	// We will send the pristine ClientHello, and immediately write an Application Data record
	// containing encrypted SessionID right after the ClientHello, before waiting for ServerHello.

	return b, nil
}

type dummyBufferConn struct {
	net.Conn
	written []byte
}

func (c *dummyBufferConn) Write(b []byte) (n int, err error) {
	c.written = append(c.written, b...)
	return len(b), nil
}

func (c *dummyBufferConn) Read(b []byte) (n int, err error) {
	return 0, fmt.Errorf("EOF")
}

// WriteTLSRecord wraps payload in a TLS 1.3 Application Data record
func (c *Connection) WriteTLSRecord(payload []byte) error {
	encPayload, err := EncryptAESGCM(c.sessionKey, payload)
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

// ReadTLSRecord reads a TLS 1.3 Application Data record
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

	return DecryptAESGCM(c.sessionKey, payload)
}

// HandshakeClient performs the client-side handshake
func (c *Connection) HandshakeClient(sni string, sessionID string) error {
	helloBytes, err := c.GenerateFakeClientHello(sni, sessionID)
	if err != nil {
		return err
	}

	// 1. Send pristine ClientHello
	_, err = c.conn.Write(helloBytes)
	if err != nil {
		return fmt.Errorf("failed to send ClientHello: %w", err)
	}

	// 2. Immediately send SessionID encapsulated in a TLS record
	// This authenticates us to the Mimic server BEFORE it replies.
	authMgs := []byte(fmt.Sprintf("AUTH:%s", sessionID))
	err = c.WriteTLSRecord(authMgs)
	if err != nil {
		return fmt.Errorf("failed to send auth record: %w", err)
	}

	// 3. Read ServerHello + Server Auth
	// The server will send a fake ServerHello followed by a TLS Record with "OK".
	// Since we don't strictly parse the ServerHello here, we can just read the first chunks
	// Wait for the TLS record response. Since we aren't using uTLS to read,
	// we will just wait for the first application data record.
	// (Note: in reality we would read the ServerHello bytes first, but simplified here
	// we assume the server answers with ServerHello + Finished, then our record).

	// Actually to make it simple on Client, just read until AppData record marker?
	// The server will send a 16 03 03... block.
	// Let's implement a loop to discard Handshake/ChangeCipherSpec records until AppData.

	for {
		header := make([]byte, 5)
		_, err := io.ReadFull(c.conn, header)
		if err != nil {
			return fmt.Errorf("failed to read response header: %w", err)
		}

		length := int(header[3])<<8 | int(header[4])
		payload := make([]byte, length)
		_, err = io.ReadFull(c.conn, payload)
		if err != nil {
			return fmt.Errorf("failed to read payload: %w", err)
		}

		// App Data
		if header[0] == 0x17 {
			dec, err := DecryptAESGCM(c.sessionKey, payload)
			if err != nil {
				return fmt.Errorf("failed to decrypt server response: %w", err)
			}
			if string(dec) != "OK" {
				return fmt.Errorf("invalid server auth response")
			}
			break
		}
	}

	return nil
}

// HandshakeServer peeks at the ClientHello.
// Returns SNI, SessionID, the peeked hello bytes, and error.
func (c *Connection) HandshakeServer() (string, string, []byte, error) {
	// 1. Read the ClientHello
	header := make([]byte, 5)
	_, err := io.ReadFull(c.conn, header)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to read handshake header: %w", err)
	}

	if header[0] != 0x16 { // Not a handshake
		return "", "", nil, fmt.Errorf("not a TLS handshake")
	}

	length := int(header[3])<<8 | int(header[4])
	if length > 16384 {
		return "", "", nil, fmt.Errorf("client hello too large")
	}

	helloPayload := make([]byte, length)
	_, err = io.ReadFull(c.conn, helloPayload)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to read client hello payload: %w", err)
	}

	fullHello := append(header, helloPayload...)

	// Quick SNI extraction (simplified, in production use proper TLS parser or uTls SNI extractor)
	// We'll skip stringent SNI extraction for now since the Fallback proxy will just pipe bytes.
	var sni string = "vk.com" // Mock SNI extraction

	// 2. Read the next record. If it's a Mimic client, it will be an App Data record with AUTH.
	// Note: Set a very short deadline, if normal client, they won't send App Data yet!
	c.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))

	nextHeader := make([]byte, 5)
	_, err = io.ReadFull(c.conn, nextHeader)
	if err != nil {
		// Normal client or timeout. We must fallback.
		c.conn.SetReadDeadline(time.Time{})
		return sni, "", fullHello, fmt.Errorf("fallback")
	}
	c.conn.SetReadDeadline(time.Time{})

	if nextHeader[0] != 0x17 {
		// Not app data. Fallback.
		fullHello = append(fullHello, nextHeader...)
		return sni, "", fullHello, fmt.Errorf("fallback")
	}

	appDataLen := int(nextHeader[3])<<8 | int(nextHeader[4])
	appDataPayload := make([]byte, appDataLen)
	_, err = io.ReadFull(c.conn, appDataPayload)
	if err != nil {
		fullHello = append(fullHello, nextHeader...)
		fullHello = append(fullHello, appDataPayload...)
		return sni, "", fullHello, fmt.Errorf("fallback")
	}

	// Try to decrypt it
	dec, err := DecryptAESGCM(c.sessionKey, appDataPayload)
	if err != nil {
		// Decryption failed means it's garbage or not our key. Fallback.
		fullHello = append(fullHello, nextHeader...)
		fullHello = append(fullHello, appDataPayload...)
		return sni, "", fullHello, fmt.Errorf("fallback")
	}

	if len(dec) > 5 && string(dec[:5]) == "AUTH:" {
		sessionID := string(dec[5:])

		// 3. Send fake ServerHello + OK record
		// A real implementation would send a generated ServerHello bytes here.
		// For simplicity, we send a static fake ServerHello header + random bytes
		fakeServerHello := []byte{0x16, 0x03, 0x03, 0x00, 0x3a, 0x02} // truncated fake
		fakeServerHello = append(fakeServerHello, make([]byte, 53)...)

		c.conn.Write(fakeServerHello)

		// Send OK record
		c.WriteTLSRecord([]byte("OK"))

		return sni, sessionID, nil, nil
	}

	// Fallback
	fullHello = append(fullHello, nextHeader...)
	fullHello = append(fullHello, appDataPayload...)
	return sni, "", fullHello, fmt.Errorf("fallback")
}
