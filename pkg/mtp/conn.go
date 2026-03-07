package mtp

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// MTPConn implements net.Conn over a reliable UDP transport using the MTP protocol.
// It provides ordered, reliable delivery with polymorphic packet encoding.
type MTPConn struct {
	udpConn    *net.UDPConn
	remoteAddr *net.UDPAddr
	codec      *PacketCodec
	arq        *ARQEngine

	// Read buffer for reassembling delivered data
	readBuf []byte
	readMu  sync.Mutex

	// Session
	sessionID string
	isServer  bool

	// Keepalive
	lastRecv atomic.Int64 // Unix nano timestamp
	lastSend atomic.Int64

	// Traffic stats
	BytesSent   atomic.Int64
	BytesRecv   atomic.Int64
	PacketsSent atomic.Int64
	PacketsRecv atomic.Int64

	// Lifecycle
	closed    atomic.Bool
	closeCh   chan struct{}
	closeOnce sync.Once

	// Deadline support
	readDeadline  atomic.Value // time.Time
	writeDeadline atomic.Value // time.Time

	// For server: dispatched from listener
	recvCh chan []byte // raw UDP datagrams dispatched by listener
}

// newMTPConn creates a new MTPConn (used internally by Dial and Listener).
// IMPORTANT: goroutines are NOT started here. Call startWorkers() after handshake.
func newMTPConn(udpConn *net.UDPConn, remoteAddr *net.UDPAddr, secret string, isServer bool) *MTPConn {
	codec := NewPacketCodec(secret)

	c := &MTPConn{
		udpConn:    udpConn,
		remoteAddr: remoteAddr,
		codec:      codec,
		isServer:   isServer,
		closeCh:    make(chan struct{}),
		recvCh:     make(chan []byte, 512),
	}

	c.lastRecv.Store(time.Now().UnixNano())
	c.lastSend.Store(time.Now().UnixNano())

	// Initialize ARQ engine
	c.arq = NewARQEngine(codec, c.sendRaw, 256)

	return c
}

// startWorkers launches the background recv and keepalive goroutines.
// Must be called AFTER the handshake is complete.
func (c *MTPConn) startWorkers() {
	if c.isServer {
		go c.recvLoop()
	} else {
		go c.recvLoopDirect()
	}
	go c.keepaliveLoop()
}

// sendRaw sends raw bytes via UDP to the remote address
func (c *MTPConn) sendRaw(data []byte) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	var err error
	if c.isServer {
		_, err = c.udpConn.WriteToUDP(data, c.remoteAddr)
	} else {
		_, err = c.udpConn.Write(data)
	}
	if err == nil {
		c.BytesSent.Add(int64(len(data)))
		c.PacketsSent.Add(1)
		c.lastSend.Store(time.Now().UnixNano())
	}
	return err
}

// recvLoop reads incoming packets dispatched by the listener (server-side)
func (c *MTPConn) recvLoop() {
	for {
		select {
		case <-c.closeCh:
			return
		case raw, ok := <-c.recvCh:
			if !ok {
				return
			}
			c.processIncoming(raw)
		}
	}
}

// recvLoopDirect is used by client-side connections that own their UDPConn
func (c *MTPConn) recvLoopDirect() {
	buf := make([]byte, 65535)
	for {
		if c.closed.Load() {
			return
		}
		c.udpConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, err := c.udpConn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			if c.closed.Load() {
				return
			}
			continue
		}
		// Make a copy and dispatch
		data := make([]byte, n)
		copy(data, buf[:n])
		c.processIncoming(data)
	}
}

// processIncoming decodes and handles a received packet
func (c *MTPConn) processIncoming(raw []byte) {
	c.BytesRecv.Add(int64(len(raw)))
	c.PacketsRecv.Add(1)
	c.lastRecv.Store(time.Now().UnixNano())

	pkt, err := c.codec.Decode(raw)
	if err != nil {
		return // Corrupted or not ours, silently discard
	}

	switch pkt.Type {
	case PacketDATA:
		c.arq.HandlePacket(pkt)

	case PacketACK:
		c.arq.HandlePacket(pkt)

	case PacketPING:
		// Respond with PONG
		pong := &Packet{
			Type:   PacketPONG,
			SeqNum: pkt.SeqNum,
		}
		c.arq.SendControl(pong)

	case PacketPONG:
		// Keepalive response received, lastRecv already updated

	case PacketFIN:
		// Peer wants to close
		finAck := &Packet{
			Type:   PacketFINACK,
			AckNum: pkt.SeqNum,
		}
		c.arq.SendControl(finAck)
		c.Close()

	case PacketFINACK:
		// Our FIN was acknowledged
		c.Close()
	}
}

// keepaliveLoop sends periodic PINGs and detects dead connections
func (c *MTPConn) keepaliveLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.closeCh:
			return
		case <-ticker.C:
			lastRecv := time.Unix(0, c.lastRecv.Load())
			if time.Since(lastRecv) > 30*time.Second {
				// Connection dead
				c.Close()
				return
			}
			// Send PING
			ping := &Packet{
				Type:   PacketPING,
				SeqNum: uint32(time.Now().UnixMilli()),
			}
			c.arq.SendControl(ping)
		}
	}
}

// Read implements net.Conn. Blocks until data is available, deadline expires, or conn closes.
func (c *MTPConn) Read(b []byte) (int, error) {
	if c.closed.Load() {
		return 0, net.ErrClosed
	}

	// Fast path: check buffered data first
	c.readMu.Lock()
	if len(c.readBuf) > 0 {
		n := copy(b, c.readBuf)
		c.readBuf = c.readBuf[n:]
		c.readMu.Unlock()
		return n, nil
	}
	c.readMu.Unlock()

	// Slow path: wait for data from ARQ delivery channel
	if dl, ok := c.readDeadline.Load().(time.Time); ok && !dl.IsZero() {
		remaining := time.Until(dl)
		if remaining <= 0 {
			return 0, &timeoutError{}
		}
		timer := time.NewTimer(remaining)
		defer timer.Stop()

		select {
		case pkt, ok := <-c.arq.Delivered():
			if !ok {
				return 0, net.ErrClosed
			}
			return c.copyPayload(b, pkt.Payload), nil
		case <-c.closeCh:
			return 0, net.ErrClosed
		case <-timer.C:
			return 0, &timeoutError{}
		}
	}

	// No deadline — block until data or close
	select {
	case pkt, ok := <-c.arq.Delivered():
		if !ok {
			return 0, net.ErrClosed
		}
		return c.copyPayload(b, pkt.Payload), nil
	case <-c.closeCh:
		return 0, net.ErrClosed
	}
}

// copyPayload copies payload into b, buffering overflow
func (c *MTPConn) copyPayload(b []byte, payload []byte) int {
	n := copy(b, payload)
	if n < len(payload) {
		c.readMu.Lock()
		c.readBuf = append(c.readBuf, payload[n:]...)
		c.readMu.Unlock()
	}
	return n
}

// Write implements net.Conn. It segments data and sends via ARQ.
func (c *MTPConn) Write(b []byte) (int, error) {
	if c.closed.Load() {
		return 0, net.ErrClosed
	}

	if dl, ok := c.writeDeadline.Load().(time.Time); ok && !dl.IsZero() {
		if time.Now().After(dl) {
			return 0, &timeoutError{}
		}
	}

	totalSent := 0
	remaining := b

	for len(remaining) > 0 {
		if c.closed.Load() {
			return totalSent, net.ErrClosed
		}

		chunkSize := MaxPayloadSize
		if len(remaining) < chunkSize {
			chunkSize = len(remaining)
		}

		chunk := make([]byte, chunkSize)
		copy(chunk, remaining[:chunkSize])

		if err := c.arq.Send(chunk); err != nil {
			return totalSent, err
		}

		remaining = remaining[chunkSize:]
		totalSent += chunkSize
	}

	return totalSent, nil
}

// Close implements net.Conn
func (c *MTPConn) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)

		// Send FIN
		fin := &Packet{Type: PacketFIN}
		c.arq.SendControl(fin) // Best effort

		// Stop ARQ
		c.arq.Close()

		close(c.closeCh)

		// Don't close the UDP socket if we're server-side (shared socket)
		if !c.isServer {
			c.udpConn.Close()
		}
	})
	return nil
}

// LocalAddr implements net.Conn
func (c *MTPConn) LocalAddr() net.Addr {
	if c.udpConn != nil {
		return c.udpConn.LocalAddr()
	}
	return &net.UDPAddr{}
}

// RemoteAddr implements net.Conn
func (c *MTPConn) RemoteAddr() net.Addr {
	if c.remoteAddr != nil {
		return c.remoteAddr
	}
	return &net.UDPAddr{}
}

// SessionID returns the session identifier
func (c *MTPConn) SessionID() string {
	return c.sessionID
}

// SetDeadline implements net.Conn
func (c *MTPConn) SetDeadline(t time.Time) error {
	c.readDeadline.Store(t)
	c.writeDeadline.Store(t)
	return nil
}

// SetReadDeadline implements net.Conn
func (c *MTPConn) SetReadDeadline(t time.Time) error {
	c.readDeadline.Store(t)
	return nil
}

// SetWriteDeadline implements net.Conn
func (c *MTPConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline.Store(t)
	return nil
}

// DispatchPacket is called by the listener to route a raw datagram to this connection
func (c *MTPConn) DispatchPacket(data []byte) {
	if c.closed.Load() {
		return
	}
	select {
	case c.recvCh <- data:
	default:
		// Drop if buffer full
	}
}

// timeoutError implements net.Error for deadline support
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "mtp: i/o timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

type UDPResolver interface {
	ResolveUDPAddr(network, address string) (*net.UDPAddr, error)
}

// Dial creates a client-side MTPConn to the given server address
func Dial(resolver UDPResolver, address string, secret string) (*MTPConn, error) {
	raddr, err := resolver.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, fmt.Errorf("mtp: resolve address: %w", err)
	}

	udpConn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, fmt.Errorf("mtp: dial udp: %w", err)
	}

	_ = udpConn.SetReadBuffer(4 * 1024 * 1024)
	_ = udpConn.SetWriteBuffer(4 * 1024 * 1024)

	conn := newMTPConn(udpConn, raddr, secret, false)

	// Perform handshake FIRST (before starting recv goroutines)
	if err := conn.handshakeClient(secret); err != nil {
		conn.udpConn.Close()
		return nil, fmt.Errorf("mtp: handshake failed: %w", err)
	}

	// NOW start background workers (recv loop, keepalive)
	conn.startWorkers()

	return conn, nil
}

// DialMigrate creates a new MTPConn for session migration (seamless rotation)
func DialMigrate(resolver UDPResolver, address string, secret string, sessionID string) (*MTPConn, error) {
	raddr, err := resolver.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, fmt.Errorf("mtp: resolve address: %w", err)
	}

	udpConn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, fmt.Errorf("mtp: dial udp: %w", err)
	}

	_ = udpConn.SetReadBuffer(4 * 1024 * 1024)
	_ = udpConn.SetWriteBuffer(4 * 1024 * 1024)

	conn := newMTPConn(udpConn, raddr, secret, false)
	conn.sessionID = sessionID

	// Perform migration handshake FIRST
	if err := conn.handshakeMigrate(sessionID); err != nil {
		conn.udpConn.Close()
		return nil, fmt.Errorf("mtp: migration handshake failed: %w", err)
	}

	// NOW start background workers
	conn.startWorkers()

	return conn, nil
}

// handshakeClient performs the client-side SYN/SYN-ACK handshake
func (c *MTPConn) handshakeClient(uuid string) error {
	syn := &Packet{
		Type:    PacketSYN,
		SeqNum:  0,
		Payload: []byte(fmt.Sprintf("AUTH:%s", uuid)),
	}

	encoded, err := c.codec.Encode(syn)
	if err != nil {
		return err
	}

	// Send SYN and wait for SYN-ACK (with retries)
	buf := make([]byte, 65535)
	for attempt := 0; attempt < 5; attempt++ {
		if err := c.sendRaw(encoded); err != nil {
			return err
		}

		// Wait for response (using Read on connected UDP socket)
		c.udpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := c.udpConn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // Retry
			}
			return err
		}

		pkt, err := c.codec.Decode(buf[:n])
		if err != nil {
			continue
		}

		if pkt.Type == PacketSYNACK {
			c.sessionID = string(pkt.Payload)
			c.udpConn.SetReadDeadline(time.Time{}) // Clear deadline
			return nil
		}
	}

	return fmt.Errorf("mtp: handshake timeout after 5 attempts")
}

// handshakeMigrate performs the migration handshake
func (c *MTPConn) handshakeMigrate(sessionID string) error {
	syn := &Packet{
		Type:    PacketSYN,
		SeqNum:  0,
		Flags:   FlagMigrate,
		Payload: []byte(fmt.Sprintf("MIGRATE:%s", sessionID)),
	}

	encoded, err := c.codec.Encode(syn)
	if err != nil {
		return err
	}

	// Send SYN with MIGRATE flag and wait for SYN-ACK
	buf := make([]byte, 65535)
	for attempt := 0; attempt < 5; attempt++ {
		if err := c.sendRaw(encoded); err != nil {
			return err
		}

		c.udpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := c.udpConn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return err
		}

		pkt, err := c.codec.Decode(buf[:n])
		if err != nil {
			continue
		}

		if pkt.Type == PacketSYNACK {
			c.udpConn.SetReadDeadline(time.Time{})
			return nil
		}
	}

	return fmt.Errorf("mtp: migration handshake timeout")
}
