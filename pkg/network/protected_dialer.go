package network

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"syscall"
	"time"
)

// SocketProtector is a callback function that protects a socket file descriptor.
// On Android, this should call VpnService.protect(fd).
// Returns true if protection was successful, false otherwise.
type SocketProtector func(fd int) bool

// protectedDialer is a net.Dialer that calls SocketProtector before establishing connections
type protectedDialer struct {
	net.Dialer
	protector SocketProtector
	mu        sync.RWMutex
}

// globalProtectedDialer holds the global protected dialer instance
var (
	globalProtectedDialer *protectedDialer
	globalDialerMu        sync.RWMutex
)

// SetSocketProtector sets the global socket protector callback.
// This should be called by the Android app before making any network connections.
// The protector function will be called with the socket fd before each dial.
func SetSocketProtector(protector SocketProtector) {
	globalDialerMu.Lock()
	defer globalDialerMu.Unlock()

	if globalProtectedDialer == nil {
		globalProtectedDialer = &protectedDialer{
			Dialer: net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				Control:   getControlFunc(protector),
			},
			protector: protector,
		}
		log.Printf("[Network] Socket protector initialized")
	} else {
		globalProtectedDialer.mu.Lock()
		globalProtectedDialer.protector = protector
		globalProtectedDialer.Control = getControlFunc(protector)
		globalProtectedDialer.mu.Unlock()
		log.Printf("[Network] Socket protector updated")
	}
}

// getControlFunc returns a Control function for net.Dialer that calls the protector
func getControlFunc(protector SocketProtector) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		if protector == nil {
			return nil
		}

		var protectErr error
		err := c.Control(func(fd uintptr) {
			if !protector(int(fd)) {
				log.Printf("[Network] ⚠️  Failed to protect socket fd=%d for %s -> %s", fd, network, address)
				protectErr = fmt.Errorf("failed to protect socket fd=%d", fd)
			} else {
				log.Printf("[Network] ✅ Protected socket fd=%d for %s -> %s", fd, network, address)
			}
		})
		if err != nil {
			log.Printf("[Network] ❌ Control callback failed for %s -> %s: %v", network, address, err)
			return fmt.Errorf("control callback failed: %w", err)
		}
		return protectErr
	}
}

// GetProtectedDialer returns the global protected dialer.
// If no protector is set, returns a standard net.Dialer.
func GetProtectedDialer() net.Dialer {
	globalDialerMu.RLock()
	defer globalDialerMu.RUnlock()

	if globalProtectedDialer == nil {
		return net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
	}

	globalProtectedDialer.mu.RLock()
	defer globalProtectedDialer.mu.RUnlock()

	return globalProtectedDialer.Dialer
}

// DialProtectedContext dials to the address using the protected dialer.
// This is the main entry point for all protected network connections.
func DialProtectedContext(ctx context.Context, network, address string) (net.Conn, error) {
	globalDialerMu.RLock()
	dialer := globalProtectedDialer
	globalDialerMu.RUnlock()

	if dialer == nil {
		// Fallback to standard dialer if no protector is set
		log.Printf("[Network] No protector set, using standard dialer for %s -> %s", network, address)
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}

	dialer.mu.RLock()
	defer dialer.mu.RUnlock()

	log.Printf("[Network] Dialing protected %s -> %s", network, address)
	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		log.Printf("[Network] ❌ Protected dial failed for %s -> %s: %v", network, address, err)
		return nil, err
	}
	log.Printf("[Network] ✅ Protected dial successful for %s -> %s", network, address)
	return conn, nil
}

// DialProtected dials to the address using the protected dialer (no context).
func DialProtected(network, address string) (net.Conn, error) {
	ctx := context.Background()
	return DialProtectedContext(ctx, network, address)
}

// DialUDPProtected creates a protected UDP connection.
// This is specifically for MTP and other UDP-based protocols.
func DialUDPProtected(network string, laddr, raddr *net.UDPAddr) (*net.UDPConn, error) {
	globalDialerMu.RLock()
	dialer := globalProtectedDialer
	globalDialerMu.RUnlock()

	if dialer == nil {
		// Fallback to standard dialer
		log.Printf("[Network] No protector set, using standard UDP dialer")
		return net.DialUDP(network, laddr, raddr)
	}

	dialer.mu.RLock()
	defer dialer.mu.RUnlock()

	log.Printf("[Network] Dialing protected UDP -> %s", raddr)

	// Use DialContext for protected UDP dial
	ctx := context.Background()
	conn, err := dialer.DialContext(ctx, network, raddr.String())
	if err != nil {
		log.Printf("[Network] ❌ Protected UDP dial failed: %v", err)
		return nil, err
	}

	// Convert to UDPConn if possible
	if udpConn, ok := conn.(*net.UDPConn); ok {
		log.Printf("[Network] ✅ Protected UDP dial successful")
		return udpConn, nil
	}

	// If not a UDPConn, close and return error
	conn.Close()
	log.Printf("[Network] ❌ Protected UDP dial returned non-UDP connection")
	return nil, fmt.Errorf("dial returned non-UDP connection")
}

// IsProtectorSet returns true if a socket protector has been configured.
// This can be used to check if running under Android VpnService.
func IsProtectorSet() bool {
	globalDialerMu.RLock()
	defer globalDialerMu.RUnlock()
	return globalProtectedDialer != nil && globalProtectedDialer.protector != nil
}

// ClearSocketProtector clears the global socket protector.
// This should only be used for testing or when shutting down.
func ClearSocketProtector() {
	globalDialerMu.Lock()
	defer globalDialerMu.Unlock()
	globalProtectedDialer = nil
}
