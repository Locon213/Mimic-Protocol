package transport

import (
	"net"
	"sync"
	"time"
)

// VirtualConn is a persistent connection wrapper that can switch its underlying physical connection.
// This allows upper layers (like Yamux) to think they have a stable connection.
// It supports buffered seamless swap to prevent data loss during transport rotation.
type VirtualConn struct {
	conn  net.Conn
	mutex sync.RWMutex

	// Swap buffering: during a swap, writes are buffered and replayed on the new connection
	swapping   bool
	swapBuf    [][]byte
	swapMu     sync.Mutex
	swapSignal chan struct{}
}

func NewVirtualConn(conn net.Conn) *VirtualConn {
	return &VirtualConn{
		conn:       conn,
		swapSignal: make(chan struct{}, 1),
	}
}

// SwapConnection replaces the underlying connection with a new one.
// The old connection is NOT closed here, caller handles it.
func (v *VirtualConn) SwapConnection(newConn net.Conn) {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	v.conn = newConn
}

// SwapConnectionSeamless replaces the underlying connection with buffered transition.
// Writes during the swap are captured and replayed on the new connection.
func (v *VirtualConn) SwapConnectionSeamless(newConn net.Conn) error {
	// 1. Enable swap buffering
	v.swapMu.Lock()
	v.swapping = true
	v.swapBuf = nil
	v.swapMu.Unlock()

	// 2. Swap the connection
	v.mutex.Lock()
	v.conn = newConn
	v.mutex.Unlock()

	// 3. Replay buffered writes on new connection
	v.swapMu.Lock()
	v.swapping = false
	buffered := v.swapBuf
	v.swapBuf = nil
	v.swapMu.Unlock()

	for _, data := range buffered {
		if _, err := newConn.Write(data); err != nil {
			return err
		}
	}

	return nil
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
	// Check if we're in swap mode — buffer writes
	v.swapMu.Lock()
	if v.swapping {
		cp := make([]byte, len(b))
		copy(cp, b)
		v.swapBuf = append(v.swapBuf, cp)
		v.swapMu.Unlock()
		return len(b), nil
	}
	v.swapMu.Unlock()

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
	return &net.UDPAddr{}
}

func (v *VirtualConn) RemoteAddr() net.Addr {
	v.mutex.RLock()
	conn := v.conn
	v.mutex.RUnlock()

	if conn != nil {
		return conn.RemoteAddr()
	}
	return &net.UDPAddr{}
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
