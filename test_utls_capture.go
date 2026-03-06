package main

import (
	"fmt"
	"net"

	utls "github.com/refraction-networking/utls"
)

type dummyConn struct {
	net.Conn
	written []byte
}

func (c *dummyConn) Write(b []byte) (n int, err error) {
	c.written = append(c.written, b...)
	return len(b), nil
}

func (c *dummyConn) Read(b []byte) (n int, err error) {
	return 0, fmt.Errorf("EOF")
}

func main() {
	dConn := &dummyConn{}
	config := &utls.Config{ServerName: "vk.com"}
	uConn := utls.UClient(dConn, config, utls.HelloChrome_Auto)
	go uConn.Handshake()

	// simple wait
	for len(dConn.written) == 0 {
	}

	b := dConn.written
	fmt.Printf("Total len: %d\n", len(b))
	if len(b) > 43 {
		fmt.Printf("Content Type: %x\n", b[0])
		fmt.Printf("Version: %x %x\n", b[1], b[2])
		fmt.Printf("Handshake Type: %x\n", b[5])
		fmt.Printf("Random: %x\n", b[11:43])
		fmt.Printf("SessionID Len: %d\n", b[43])
	}
}
