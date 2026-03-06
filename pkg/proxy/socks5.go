package proxy

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/yamux"
)

// Stream type marker (first byte sent on yamux stream)
const StreamTypeProxy = 0x01

// SOCKS5 protocol constants
const (
	socks5Version    = 0x05
	socks5AuthNone   = 0x00
	socks5CmdConnect = 0x01
	socks5AddrIPv4   = 0x01
	socks5AddrDomain = 0x03
	socks5AddrIPv6   = 0x04
)

// Stats tracks proxy traffic statistics
type Stats struct {
	BytesUp     atomic.Int64
	BytesDown   atomic.Int64
	ActiveConns atomic.Int32
	TotalConns  atomic.Int64
	ConnectedAt time.Time
}

// SOCKS5Server is a minimal local SOCKS5 proxy that tunnels through yamux
type SOCKS5Server struct {
	listener  net.Listener
	session   *yamux.Session
	stats     *Stats
	closeCh   chan struct{}
	closeOnce sync.Once
}

// NewSOCKS5Server creates a new SOCKS5 proxy server
func NewSOCKS5Server(bindAddr string, session *yamux.Session) (*SOCKS5Server, error) {
	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("socks5: bind failed: %w", err)
	}

	s := &SOCKS5Server{
		listener: listener,
		session:  session,
		stats: &Stats{
			ConnectedAt: time.Now(),
		},
		closeCh: make(chan struct{}),
	}

	return s, nil
}

// Serve starts accepting SOCKS5 connections (blocking)
func (s *SOCKS5Server) Serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closeCh:
				return
			default:
				log.Printf("[SOCKS5] Accept error: %v", err)
				continue
			}
		}
		s.stats.TotalConns.Add(1)
		s.stats.ActiveConns.Add(1)
		go s.handleConn(conn)
	}
}

// Close stops the SOCKS5 server
func (s *SOCKS5Server) Close() error {
	s.closeOnce.Do(func() {
		close(s.closeCh)
	})
	return s.listener.Close()
}

// GetStats returns the current stats reference
func (s *SOCKS5Server) GetStats() *Stats {
	return s.stats
}

// Addr returns the proxy listen address
func (s *SOCKS5Server) Addr() net.Addr {
	return s.listener.Addr()
}

// trackingWriter wraps an io.Writer and atomically increments a counter
type trackingWriter struct {
	w       io.Writer
	counter *atomic.Int64
}

func (tw *trackingWriter) Write(p []byte) (n int, err error) {
	n, err = tw.w.Write(p)
	if n > 0 {
		tw.counter.Add(int64(n))
	}
	return n, err
}

// handleConn processes a single SOCKS5 connection
func (s *SOCKS5Server) handleConn(conn net.Conn) {
	defer func() {
		conn.Close()
		s.stats.ActiveConns.Add(-1)
	}()

	// 1. Read client greeting
	buf := make([]byte, 258)
	n, err := conn.Read(buf)
	if err != nil || n < 2 {
		log.Printf("[SOCKS5] Failed to read greeting: %v", err)
		return
	}

	if buf[0] != socks5Version {
		log.Printf("[SOCKS5] Invalid version: 0x%02x", buf[0])
		return
	}

	// 2. Send server method selection (no auth)
	_, err = conn.Write([]byte{socks5Version, socks5AuthNone})
	if err != nil {
		log.Printf("[SOCKS5] Failed to send method selection: %v", err)
		return
	}

	// 3. Read connect request
	n, err = conn.Read(buf)
	if err != nil || n < 7 {
		log.Printf("[SOCKS5] Failed to read connect request: %v (n=%d)", err, n)
		return
	}

	if buf[0] != socks5Version || buf[1] != socks5CmdConnect {
		log.Printf("[SOCKS5] Unsupported command: 0x%02x", buf[1])
		conn.Write([]byte{socks5Version, 0x07, 0x00, socks5AddrIPv4, 0, 0, 0, 0, 0, 0})
		return
	}

	// 4. Parse target address
	var targetAddr string
	switch buf[3] {
	case socks5AddrIPv4:
		if n < 10 {
			return
		}
		ip := net.IPv4(buf[4], buf[5], buf[6], buf[7])
		port := int(buf[8])<<8 | int(buf[9])
		targetAddr = fmt.Sprintf("%s:%d", ip.String(), port)

	case socks5AddrDomain:
		domainLen := int(buf[4])
		if n < 5+domainLen+2 {
			return
		}
		domain := string(buf[5 : 5+domainLen])
		port := int(buf[5+domainLen])<<8 | int(buf[5+domainLen+1])
		targetAddr = fmt.Sprintf("%s:%d", domain, port)

	case socks5AddrIPv6:
		if n < 22 {
			return
		}
		ip := net.IP(buf[4:20])
		port := int(buf[20])<<8 | int(buf[21])
		targetAddr = fmt.Sprintf("[%s]:%d", ip.String(), port)

	default:
		conn.Write([]byte{socks5Version, 0x08, 0x00, socks5AddrIPv4, 0, 0, 0, 0, 0, 0})
		return
	}

	log.Printf("[SOCKS5] CONNECT %s", targetAddr)

	// 5. Open yamux stream to server
	stream, err := s.session.Open()
	if err != nil {
		log.Printf("[SOCKS5] Failed to open yamux stream: %v", err)
		conn.Write([]byte{socks5Version, 0x05, 0x00, socks5AddrIPv4, 0, 0, 0, 0, 0, 0})
		return
	}

	// Send stream type marker + target address
	// Protocol: [0x01 stream_type] [1 byte addr_len] [addr string]
	addrBytes := []byte(targetAddr)
	header := make([]byte, 2+len(addrBytes))
	header[0] = StreamTypeProxy // Stream type: proxy
	header[1] = byte(len(addrBytes))
	copy(header[2:], addrBytes)
	_, err = stream.Write(header)
	if err != nil {
		log.Printf("[SOCKS5] Failed to send connect header: %v", err)
		stream.Close()
		conn.Write([]byte{socks5Version, 0x05, 0x00, socks5AddrIPv4, 0, 0, 0, 0, 0, 0})
		return
	}

	// Wait for server response
	resp := make([]byte, 1)
	stream.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, err = io.ReadFull(stream, resp)
	stream.SetReadDeadline(time.Time{})
	if err != nil || resp[0] != 0x01 {
		log.Printf("[SOCKS5] Server rejected connection to %s (err=%v, resp=%v)", targetAddr, err, resp)
		stream.Close()
		conn.Write([]byte{socks5Version, 0x04, 0x00, socks5AddrIPv4, 0, 0, 0, 0, 0, 0})
		return
	}

	// 6. Send success reply to SOCKS5 client
	reply := []byte{socks5Version, 0x00, 0x00, socks5AddrIPv4, 0, 0, 0, 0, 0, 0}
	conn.Write(reply)

	log.Printf("[SOCKS5] Tunnel established to %s", targetAddr)

	// 7. Relay data bidirectionally
	var wg sync.WaitGroup
	wg.Add(2)

	// Client -> Server
	go func() {
		defer wg.Done()
		io.Copy(&trackingWriter{w: stream, counter: &s.stats.BytesUp}, conn)
		stream.Close()
	}()

	// Server -> Client
	go func() {
		defer wg.Done()
		io.Copy(&trackingWriter{w: conn, counter: &s.stats.BytesDown}, stream)
		conn.Close()
	}()

	wg.Wait()
	log.Printf("[SOCKS5] Tunnel closed for %s", targetAddr)
}
