package transport

import (
	"net"
	"sync"
	"time"
)

// VirtualConn is a persistent connection wrapper that can switch its underlying physical connection
// This allows upper layers (like Yamux) to think they have a stable connection
type VirtualConn struct {
	conn  net.Conn
	mutex sync.RWMutex
}

func NewVirtualConn(conn net.Conn) *VirtualConn {
	return &VirtualConn{conn: conn}
}

// SwapConnection replaces the underlying connection with a new one
// The old connection is NOT closed here, caller handles it
func (v *VirtualConn) SwapConnection(newConn net.Conn) {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	v.conn = newConn
}

func (v *VirtualConn) Read(b []byte) (n int, err error) {
	v.mutex.RLock()
	conn := v.conn
	v.mutex.RUnlock()

	if conn == nil {
		return 0, net.ErrClosed
	}
	return conn.Read(b)
}

func (v *VirtualConn) Write(b []byte) (n int, err error) {
	v.mutex.RLock()
	conn := v.conn
	v.mutex.RUnlock()

	if conn == nil {
		return 0, net.ErrClosed
	}
	return conn.Write(b)
}

func (v *VirtualConn) Close() error {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	if v.conn != nil {
		return v.conn.Close()
	}
	return nil
}

func (v *VirtualConn) LocalAddr() net.Addr {
	v.mutex.RLock()
	conn := v.conn
	v.mutex.RUnlock()

	if conn != nil {
		return conn.LocalAddr()
	}
	return &net.TCPAddr{}
}

func (v *VirtualConn) RemoteAddr() net.Addr {
	v.mutex.RLock()
	conn := v.conn
	v.mutex.RUnlock()

	if conn != nil {
		return conn.RemoteAddr()
	}
	return &net.TCPAddr{}
}

func (v *VirtualConn) SetDeadline(t time.Time) error {
	v.mutex.RLock()
	conn := v.conn
	v.mutex.RUnlock()

	if conn != nil {
		return conn.SetDeadline(t)
	}
	return nil
}

func (v *VirtualConn) SetReadDeadline(t time.Time) error {
	v.mutex.RLock()
	conn := v.conn
	v.mutex.RUnlock()

	if conn != nil {
		return conn.SetReadDeadline(t)
	}
	return nil
}

func (v *VirtualConn) SetWriteDeadline(t time.Time) error {
	v.mutex.RLock()
	conn := v.conn
	v.mutex.RUnlock()

	if conn != nil {
		return conn.SetWriteDeadline(t)
	}
	return nil
}
