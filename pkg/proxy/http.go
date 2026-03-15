package proxy

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/routing"
	"github.com/hashicorp/yamux"
)

// HTTPProxyServer implements a standard HTTP/HTTPS forward proxy
type HTTPProxyServer struct {
	listener  net.Listener
	session   *yamux.Session
	router    *routing.Router
	stats     *Stats
	closeCh   chan struct{}
	closeOnce sync.Once
}

// NewHTTPProxyServer creates a local HTTP proxy server
func NewHTTPProxyServer(bindAddr string, session *yamux.Session, router *routing.Router) (*HTTPProxyServer, error) {
	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("http_proxy: bind failed: %w", err)
	}

	s := &HTTPProxyServer{
		listener: listener,
		session:  session,
		router:   router,
		stats: &Stats{
			ConnectedAt: time.Now(),
		},
		closeCh: make(chan struct{}),
	}

	return s, nil
}

// Serve starts accepting HTTP connections
func (s *HTTPProxyServer) Serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closeCh:
				return
			default:
				log.Printf("[HTTP] Accept error: %v", err)
				continue
			}
		}
		s.stats.TotalConns.Add(1)
		s.stats.ActiveConns.Add(1)
		go s.handleConn(conn)
	}
}

// Close stops the HTTP server
func (s *HTTPProxyServer) Close() error {
	s.closeOnce.Do(func() {
		close(s.closeCh)
	})
	return s.listener.Close()
}

// GetStats returns the current stats reference
func (s *HTTPProxyServer) GetStats() *Stats {
	return s.stats
}

// Addr returns the proxy listen address
func (s *HTTPProxyServer) Addr() net.Addr {
	return s.listener.Addr()
}

func (s *HTTPProxyServer) handleConn(conn net.Conn) {
	defer func() {
		conn.Close()
		s.stats.ActiveConns.Add(-1)
	}()

	// Log connection start for debugging
	log.Printf("[HTTP] New connection from %s", conn.RemoteAddr())

	reader := bufio.NewReader(conn)

	// Connection loop for HTTP keep-alive support
	for {
		select {
		case <-s.closeCh:
			return
		default:
		}

		// Set read deadline to prevent hanging on incomplete requests
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		req, err := http.ReadRequest(reader)
		if err != nil {
			if err != io.EOF {
				log.Printf("[HTTP] Failed to read request from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}
		conn.SetReadDeadline(time.Time{})

		targetAddr := req.URL.Host
		if targetAddr == "" {
			log.Printf("[HTTP] Empty target address in request")
			return
		}
		if !strings.Contains(targetAddr, ":") {
			if req.Method == http.MethodConnect {
				targetAddr += ":443"
			} else {
				targetAddr += ":80"
			}
		}

		log.Printf("[HTTP] %s %s", req.Method, targetAddr)

		// Routing Decision
		policy := routing.Proxy
		if s.router != nil {
			policy = s.router.Route(targetAddr)
		}

		if policy == routing.Block {
			log.Printf("[HTTP] BLOCKED %s", targetAddr)
			if req.Method == http.MethodConnect {
				conn.Write([]byte("HTTP/1.1 403 Forbidden\r\n\r\n"))
			} else {
				resp := &http.Response{
					StatusCode: http.StatusForbidden,
					ProtoMajor: 1,
					ProtoMinor: 1,
					Body:       http.NoBody,
				}
				resp.Write(conn)
			}
			return
		}

		var targetConn io.ReadWriteCloser

		// Handle DIRECT routing
		if policy == routing.Direct {
			log.Printf("[HTTP] DIRECT %s", targetAddr)
			tConn, err := net.DialTimeout("tcp", targetAddr, 10*time.Second)
			if err != nil {
				log.Printf("[HTTP] DIRECT dial failed: %v", err)
				if req.Method == http.MethodConnect {
					conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
				} else {
					resp := &http.Response{
						StatusCode: http.StatusBadGateway,
						ProtoMajor: 1,
						ProtoMinor: 1,
						Body:       http.NoBody,
					}
					resp.Write(conn)
				}
				return
			}
			targetConn = tConn
		} else {
			// PROXY via Yamux
			stream, err := s.session.Open()
			if err != nil {
				log.Printf("[HTTP] Failed to open yamux stream: %v", err)
				if req.Method == http.MethodConnect {
					conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
				} else {
					resp := &http.Response{
						StatusCode: http.StatusBadGateway,
						ProtoMajor: 1,
						ProtoMinor: 1,
						Body:       http.NoBody,
					}
					resp.Write(conn)
				}
				return
			}

			// Send connect header
			addrBytes := []byte(targetAddr)
			header := make([]byte, 2+len(addrBytes))
			header[0] = StreamTypeProxy // Share the same server-side logic as SOCKS5 TCP connect
			header[1] = byte(len(addrBytes))
			copy(header[2:], addrBytes)

			if _, err := stream.Write(header); err != nil {
				log.Printf("[HTTP] Failed to send connect header: %v", err)
				stream.Close()
				if req.Method == http.MethodConnect {
					conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
				}
				return
			}

			// Wait for server response
			respBytes := make([]byte, 1)
			stream.SetReadDeadline(time.Now().Add(15 * time.Second))
			_, err = io.ReadFull(stream, respBytes)
			stream.SetReadDeadline(time.Time{})

			if err != nil || respBytes[0] != 0x01 {
				log.Printf("[HTTP] Server rejected connection to %s (err=%v, resp=%v)", targetAddr, err, respBytes)
				stream.Close()
				if req.Method == http.MethodConnect {
					conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
				} else {
					resp := &http.Response{
						StatusCode: http.StatusBadGateway,
						ProtoMajor: 1,
						ProtoMinor: 1,
						Body:       http.NoBody,
					}
					resp.Write(conn)
				}
				return
			}

			targetConn = stream
		}

		// Handle the initial request
		if req.Method == http.MethodConnect {
			// HTTPS uses CONNECT, respond with 200 OK and relay blindly
			_, err := conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
			if err != nil {
				log.Printf("[HTTP] Failed to send CONNECT response: %v", err)
				if targetConn != nil {
					targetConn.Close()
				}
				return
			}
			log.Printf("[HTTP] CONNECT tunnel established to %s", targetAddr)

			// Bidirectional Relay for CONNECT (HTTPS)
			s.relay(conn, targetConn)
			return // CONNECT connections are not reused
		} else {
			// Plain HTTP: we need to send the parsed request to the target
			req.Header.Del("Proxy-Connection")
			if err := req.Write(targetConn); err != nil {
				log.Printf("[HTTP] Failed to write request to target: %v", err)
				targetConn.Close()
				return
			}
			log.Printf("[HTTP] HTTP request forwarded to %s", targetAddr)

			// Read and forward response
			s.forwardHTTPResponse(conn, targetConn)

			// Check if connection should be kept alive
			if req.Close {
				targetConn.Close()
				return
			}

			// Close target connection after forwarding if not using keep-alive
			targetConn.Close()
		}
	}
}

// relay performs bidirectional data relay between two connections
func (s *HTTPProxyServer) relay(client, target io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(&trackingWriter{w: target, counter: &s.stats.BytesUp}, client)
		if c, ok := target.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		} else {
			target.Close()
		}
	}()

	go func() {
		defer wg.Done()
		io.Copy(&trackingWriter{w: client, counter: &s.stats.BytesDown}, target)
		if c, ok := client.(*net.TCPConn); ok {
			c.CloseWrite()
		} else {
			client.Close()
		}
	}()

	wg.Wait()
}

// forwardHTTPResponse reads response from target and writes to client
func (s *HTTPProxyServer) forwardHTTPResponse(client io.Writer, target io.Reader) {
	reader := bufio.NewReader(target)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		log.Printf("[HTTP] Failed to read response: %v", err)
		return
	}

	resp.Write(&trackingWriter{w: client, counter: &s.stats.BytesDown})
}
