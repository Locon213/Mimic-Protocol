package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/routing"
)

// Stream type markers for Yamux multiplexing
const (
	StreamTypeUDP = 0x03
)

// handleUDPAssociate implements the UDP ASSOCIATE command for SOCKS5
func (s *SOCKS5Server) handleUDPAssociate(clientConn net.Conn) {
	// Start a local UDP listener for this client
	udpAddr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: 0, // let OS choose
	}

	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Printf("[SOCKS5-UDP] Failed to start UDP listener: %v", err)
		clientConn.Write([]byte{socks5Version, 0x01, 0x00, socks5AddrIPv4, 0, 0, 0, 0, 0, 0})
		return
	}
	defer udpConn.Close()

	boundAddr := udpConn.LocalAddr().(*net.UDPAddr)

	// Reply to client with the bound address and port where they should send UDP datagrams
	ipBytes := boundAddr.IP.To4()
	reply := []byte{socks5Version, 0x00, 0x00, socks5AddrIPv4}
	reply = append(reply, ipBytes...)

	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(boundAddr.Port))
	reply = append(reply, portBytes...)

	_, err = clientConn.Write(reply)
	if err != nil {
		return
	}

	log.Printf("[SOCKS5-UDP] UDP Associate bound to %s", boundAddr.String())

	// Wait for the TCP connection to close. According to RFC 1928,
	// the UDP association terminates when the TCP connection terminates.
	go func() {
		io.Copy(io.Discard, clientConn)
		udpConn.Close()
	}()

	s.relayUDP(udpConn)
}

func (s *SOCKS5Server) relayUDP(localClientConn *net.UDPConn) {
	buf := make([]byte, 65535)

	// Caching target connections mapping
	// Real-world proxy needs map to manage multiple targets and timeout cleaning.
	// For simplicity, we forward via Yamux stream for each packet if no existing stream.

	for {
		n, clientAddr, err := localClientConn.ReadFromUDP(buf)
		if err != nil {
			return
		}

		if n < 10 {
			continue // Too short for SOCKS5 UDP header
		}

		// SOCKS5 UDP Request Header
		// +----+------+------+----------+----------+----------+
		// |RSV | FRAG | ATYP | DST.ADDR | DST.PORT |   DATA   |
		// +----+------+------+----------+----------+----------+
		// | 2  |  1   |  1   | Variable |    2     | Variable |
		// +----+------+------+----------+----------+----------+

		if buf[2] != 0x00 {
			continue // Fragments not supported
		}

		atyp := buf[3]
		var targetAddr string
		var headerLen int

		switch atyp {
		case socks5AddrIPv4:
			if n < 10 {
				continue
			}
			ip := net.IPv4(buf[4], buf[5], buf[6], buf[7])
			port := int(binary.BigEndian.Uint16(buf[8:10]))
			targetAddr = fmt.Sprintf("%s:%d", ip.String(), port)
			headerLen = 10
		case socks5AddrDomain:
			domainLen := int(buf[4])
			if n < 5+domainLen+2 {
				continue
			}
			domain := string(buf[5 : 5+domainLen])
			port := int(binary.BigEndian.Uint16(buf[5+domainLen : 5+domainLen+2]))
			targetAddr = fmt.Sprintf("%s:%d", domain, port)
			headerLen = 5 + domainLen + 2
		case socks5AddrIPv6:
			if n < 22 {
				continue
			}
			ip := net.IP(buf[4:20])
			port := int(binary.BigEndian.Uint16(buf[20:22]))
			targetAddr = fmt.Sprintf("[%s]:%d", ip.String(), port)
			headerLen = 22
		default:
			continue
		}

		payload := buf[headerLen:n]

		// Routing Engine
		policy := routing.Proxy
		if s.router != nil {
			policy = s.router.Route(targetAddr)
		}

		if policy == routing.Block {
			continue
		}

		if policy == routing.Direct {
			go s.relayUDPDirect(payload, targetAddr, localClientConn, clientAddr, buf[:headerLen])
			continue
		}

		// Proxy via Yamux (MTP)
		go s.relayUDPProxy(payload, targetAddr, localClientConn, clientAddr, buf[:headerLen])
	}
}

func (s *SOCKS5Server) relayUDPDirect(payload []byte, targetAddr string, localClientConn *net.UDPConn, clientAddr *net.UDPAddr, socksHeader []byte) {
	targetUDPAddr, err := net.ResolveUDPAddr("udp", targetAddr)
	if err != nil {
		return
	}

	targetConn, err := net.DialUDP("udp", nil, targetUDPAddr)
	if err != nil {
		return
	}
	defer targetConn.Close()
	targetConn.SetDeadline(time.Now().Add(10 * time.Second))

	_, err = targetConn.Write(payload)
	if err != nil {
		return
	}

	respBuf := make([]byte, 65535)
	rn, err := targetConn.Read(respBuf)
	if err != nil {
		return
	}

	// Reconstruct SOCKS UDP reply
	reply := append(socksHeader, respBuf[:rn]...)
	localClientConn.WriteToUDP(reply, clientAddr)
}

func (s *SOCKS5Server) relayUDPProxy(payload []byte, targetAddr string, localClientConn *net.UDPConn, clientAddr *net.UDPAddr, socksHeader []byte) {
	// Note: In a production heavily used UDP environment (like gaming), opening a yamux stream per datagram is slow.
	// Optimally, we want one Yamux UDP stream exchanging datagrams. Since Yamux Streams are inexpensive we'll do 1 stream per request,
	// or we multiplex inside the stream. For now, we open a Yamux stream, send [Addr][Data], and read response.

	stream, err := s.session.Open()
	if err != nil {
		return
	}
	defer stream.Close()

	addrBytes := []byte(targetAddr)

	// Protocol: [StreamTypeUDP] [AddrLen 1 byte] [Addr] [DataLen 2 bytes] [Data]
	header := make([]byte, 2+len(addrBytes)+2)
	header[0] = StreamTypeUDP
	header[1] = byte(len(addrBytes))
	copy(header[2:], addrBytes)
	binary.BigEndian.PutUint16(header[2+len(addrBytes):], uint16(len(payload)))

	// Send to server
	_, err = stream.Write(append(header, payload...))
	if err != nil {
		return
	}

	// Read response
	stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	respHeader := make([]byte, 2)
	_, err = io.ReadFull(stream, respHeader)
	if err != nil {
		return
	}

	dataLen := binary.BigEndian.Uint16(respHeader)
	respData := make([]byte, dataLen)
	_, err = io.ReadFull(stream, respData)
	if err != nil {
		return
	}

	// Reconstruct SOCKS UDP reply
	reply := append(socksHeader, respData...)
	localClientConn.WriteToUDP(reply, clientAddr)
}
