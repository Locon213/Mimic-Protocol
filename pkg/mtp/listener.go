package mtp

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
)

// Listener is an MTP server-side listener that accepts MTP connections over UDP.
// It implements a net.Listener-compatible interface.
type Listener struct {
	udpConn     *net.UDPConn
	secret      string
	codec       *PacketCodec
	compression *CompressionConfig

	// Connection tracking: remoteAddr -> MTPConn
	connections map[string]*MTPConn
	connMu      sync.RWMutex

	// Session tracking: sessionID -> MTPConn (for migration)
	sessions map[string]*MTPConn
	sessMu   sync.RWMutex

	// New connections are delivered here
	acceptCh chan *MTPConn

	// Lifecycle
	closed  bool
	closeCh chan struct{}
	closeMu sync.Mutex
}

// Listen creates a new MTP listener on the given address
func Listen(address string, secret string) (*Listener, error) {
	return ListenWithConfig(address, secret, nil)
}

// ListenWithConfig creates a new MTP listener with custom configuration
func ListenWithConfig(address string, secret string, compression *CompressionConfig) (*Listener, error) {
	laddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, fmt.Errorf("mtp: resolve address: %w", err)
	}

	udpConn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return nil, fmt.Errorf("mtp: listen udp: %w", err)
	}

	// Тюнинг буферов для высокой пропускной способности (игнорируем ошибки для кроссплатформенности)
	_ = udpConn.SetReadBuffer(8 * 1024 * 1024)
	_ = udpConn.SetWriteBuffer(8 * 1024 * 1024)

	l := &Listener{
		udpConn: udpConn,
		secret:  secret,
		codec: NewPacketCodecWithConfig(CodecConfig{
			Secret:          secret,
			EnableDCIDRot:   true,
			DCIDRotInterval: 300,
			Compression:     compression,
		}),
		compression: compression,
		connections: make(map[string]*MTPConn),
		sessions:    make(map[string]*MTPConn),
		acceptCh:    make(chan *MTPConn, 64),
		closeCh:     make(chan struct{}),
	}

	go l.readLoop()

	return l, nil
}

// Accept waits for and returns the next incoming connection.
// It implements net.Listener.Accept().
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case conn, ok := <-l.acceptCh:
		if !ok {
			return nil, fmt.Errorf("mtp: listener closed")
		}
		return conn, nil
	case <-l.closeCh:
		return nil, fmt.Errorf("mtp: listener closed")
	}
}

// Close stops the listener and closes the UDP socket.
func (l *Listener) Close() error {
	l.closeMu.Lock()
	defer l.closeMu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true
	close(l.closeCh)
	close(l.acceptCh)

	// Close UDP socket FIRST to unblock readLoop goroutine
	err := l.udpConn.Close()

	// Collect connections under lock, then close outside lock to avoid deadlock
	l.connMu.Lock()
	conns := make([]*MTPConn, 0, len(l.connections))
	for _, conn := range l.connections {
		conns = append(conns, conn)
	}
	l.connMu.Unlock()

	for _, conn := range conns {
		conn.Close()
	}

	// Close the listener's own codec goroutines
	l.codec.Close()

	return err
}

// Addr returns the listener's network address.
func (l *Listener) Addr() net.Addr {
	return l.udpConn.LocalAddr()
}

// sendFakeResponse sends a decoy payload to confuse active probing scanners (DPI).
// It mimics a DNS "Refused" response so the port appears to be a closed/restricted DNS service.
func (l *Listener) sendFakeResponse(addr *net.UDPAddr) {
	fakeDns := []byte{
		0x00, 0x00, // Transaction ID (could be random, 0 is fine)
		0x81, 0x05, // Flags: Standard query response, Refused
		0x00, 0x01, // Questions: 1
		0x00, 0x00, // Answer RRs
		0x00, 0x00, // Authority RRs
		0x00, 0x00, // Additional RRs
		0x00, 0x00, 0x01, 0x00, 0x01, // Dummy question
	}
	l.udpConn.WriteToUDP(fakeDns, addr)
}

// readLoop reads all incoming UDP datagrams and dispatches them
func (l *Listener) readLoop() {
	buf := make([]byte, 65535)

	for {
		select {
		case <-l.closeCh:
			return
		default:
		}

		n, remoteAddr, err := l.udpConn.ReadFromUDP(buf)
		if err != nil {
			if l.closed {
				return
			}
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		addrKey := remoteAddr.String()

		// Check if we already have a connection for this address
		l.connMu.RLock()
		existingConn, exists := l.connections[addrKey]
		l.connMu.RUnlock()

		if exists {
			existingConn.DispatchPacket(data)
			continue
		}

		// New address: try to decode as SYN
		pkt, err := l.codec.Decode(data)
		if err != nil {
			// Fallback: Active Probing Defender
			l.sendFakeResponse(remoteAddr)
			continue // Not a valid MTP packet, ignore
		}

		if pkt.Type != PacketSYN {
			l.sendFakeResponse(remoteAddr)
			continue // Not a SYN, ignore
		}

		// Handle SYN
		l.handleSYN(remoteAddr, pkt)
	}
}

// handleSYN processes an incoming SYN packet
func (l *Listener) handleSYN(remoteAddr *net.UDPAddr, pkt *Packet) {
	payload := string(pkt.Payload)

	if pkt.Flags&FlagMigrate != 0 {
		// Session migration
		l.handleMigration(remoteAddr, payload)
		return
	}

	// Normal SYN: AUTH:<uuid>
	if !strings.HasPrefix(payload, "AUTH:") {
		return
	}

	sessionID := payload[5:]

	// Create new MTPConn for this client with compression config
	conn := newMTPConn(l.udpConn, remoteAddr, l.secret, true, l.compression)
	conn.sessionID = sessionID

	addrKey := remoteAddr.String()

	l.connMu.Lock()
	l.connections[addrKey] = conn
	l.connMu.Unlock()

	l.sessMu.Lock()
	l.sessions[sessionID] = conn
	l.sessMu.Unlock()

	// Register cleanup callback
	conn.onClose = func() {
		l.RemoveConnection(sessionID)
	}

	// Start background workers (recv loop, keepalive)
	conn.startWorkers()

	// Send SYN-ACK
	synAck := &Packet{
		Type:    PacketSYNACK,
		SeqNum:  0,
		AckNum:  1,
		Payload: []byte(sessionID),
	}

	encoded, err := l.codec.Encode(synAck)
	if err != nil {
		log.Printf("[MTP] Failed to encode SYN-ACK: %v", err)
		return
	}

	l.udpConn.WriteToUDP(encoded, remoteAddr)

	log.Printf("[MTP] New connection from %s, session: %s", addrKey, sessionID)

	// Deliver to Accept()
	select {
	case l.acceptCh <- conn:
	default:
		log.Printf("[MTP] Accept channel full, dropping connection from %s", addrKey)
		conn.Close()
	}
}

// handleMigration handles session migration (seamless rotation)
func (l *Listener) handleMigration(newAddr *net.UDPAddr, payload string) {
	// MIGRATE:<sessionID>
	if !strings.HasPrefix(payload, "MIGRATE:") {
		return
	}

	sessionID := payload[8:]

	l.sessMu.RLock()
	existingConn, exists := l.sessions[sessionID]
	l.sessMu.RUnlock()

	if !exists {
		log.Printf("[MTP] Migration request for unknown session: %s", sessionID)
		return
	}

	oldAddrKey := existingConn.remoteAddr.String()
	newAddrKey := newAddr.String()

	log.Printf("[MTP] Migrating session %s: %s -> %s", sessionID, oldAddrKey, newAddrKey)

	// Update the connection's remote address
	existingConn.remoteAddr = newAddr

	// Update connection map
	l.connMu.Lock()
	delete(l.connections, oldAddrKey)
	l.connections[newAddrKey] = existingConn
	l.connMu.Unlock()

	// Send SYN-ACK to new address
	synAck := &Packet{
		Type:    PacketSYNACK,
		SeqNum:  0,
		AckNum:  1,
		Payload: []byte("OK"),
	}

	encoded, err := l.codec.Encode(synAck)
	if err != nil {
		return
	}

	l.udpConn.WriteToUDP(encoded, newAddr)

	log.Printf("[MTP] Session %s migrated successfully", sessionID)
}

// RemoveConnection removes a connection from tracking (called on disconnect)
func (l *Listener) RemoveConnection(sessionID string) {
	l.sessMu.Lock()
	conn, exists := l.sessions[sessionID]
	if exists {
		delete(l.sessions, sessionID)
	}
	l.sessMu.Unlock()

	if conn != nil {
		addrKey := conn.remoteAddr.String()
		l.connMu.Lock()
		delete(l.connections, addrKey)
		l.connMu.Unlock()
	}
}

// GetSession returns the MTPConn for a given session ID (for VirtualConn swap)
func (l *Listener) GetSession(sessionID string) *MTPConn {
	l.sessMu.RLock()
	defer l.sessMu.RUnlock()
	return l.sessions[sessionID]
}
