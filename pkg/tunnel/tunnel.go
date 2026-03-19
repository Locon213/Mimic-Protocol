// Package tunnel provides TUN tunnel integration for Android VpnService.
// It supports attaching to an existing TUN file descriptor provided by Android.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Tunnel represents a TUN tunnel instance
type Tunnel struct {
	mu            sync.RWMutex
	tunFD         int
	mtu           int
	cmd           *exec.Cmd
	cancel        context.CancelFunc
	ctx           context.Context
	running       atomic.Bool
	startedAt     time.Time
	tun2socksPath string
}

// TunnelConfig holds tunnel configuration
type TunnelConfig struct {
	// TUN file descriptor from Android VpnService
	FD int
	// MTU for the TUN interface (default: 1500)
	MTU int
	// Path to tun2socks binary (optional, uses embedded if empty)
	Tun2socksPath string
	// SOCKS5 server address (default: 127.0.0.1:1080)
	Socks5Addr string
	// DNS server address (default: 8.8.8.8:53)
	DNS string
	// Enable verbose logging
	Verbose bool
}

// TunnelError represents tunnel-related errors
type TunnelError struct {
	Op  string
	Err error
}

func (e *TunnelError) Error() string {
	return fmt.Sprintf("tunnel %s: %v", e.Op, e.Err)
}

func (e *TunnelError) Unwrap() error {
	return e.Err
}

// Predefined errors
var (
	ErrTunnelAlreadyRunning = &TunnelError{Op: "start", Err: errors.New("tunnel already running")}
	ErrTunnelNotRunning     = &TunnelError{Op: "stop", Err: errors.New("tunnel not running")}
	ErrInvalidFD            = &TunnelError{Op: "attach", Err: errors.New("invalid TUN file descriptor")}
	ErrTun2socksNotFound    = &TunnelError{Op: "startup", Err: errors.New("tun2socks binary not found")}
	ErrPlatformNotSupported = &TunnelError{Op: "init", Err: errors.New("TUN tunnel not supported on this platform")}
)

// New creates a new Tunnel instance (not started)
func New() *Tunnel {
	return &Tunnel{}
}

// StartTunnelFromFD starts a TUN tunnel using an existing TUN file descriptor.
// This is the main entry point for Android VpnService integration.
//
// Parameters:
//   - fd: TUN file descriptor from Android VpnService.protect()
//   - mtu: MTU for the TUN interface (use 0 for default 1500)
//
// Returns error if tunnel fails to start. Does NOT kill the process on failure.
func (t *Tunnel) StartTunnelFromFD(fd int, mtu int) error {
	return t.StartTunnelFromFDWithConfig(fd, mtu, TunnelConfig{})
}

// StartTunnelFromFDWithConfig starts tunnel with full configuration
func (t *Tunnel) StartTunnelFromFDWithConfig(fd int, mtu int, cfg TunnelConfig) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running.Load() {
		return ErrTunnelAlreadyRunning
	}

	if fd < 0 {
		return &TunnelError{Op: "attach", Err: fmt.Errorf("invalid fd: %d", fd)}
	}

	// Set defaults
	if mtu <= 0 {
		mtu = 1500
	}
	if cfg.Socks5Addr == "" {
		cfg.Socks5Addr = "127.0.0.1:1080"
	}
	if cfg.DNS == "" {
		cfg.DNS = "8.8.8.8:53"
	}

	ctx, cancel := context.WithCancel(context.Background())

	log.Printf("[Tunnel] Starting TUN tunnel with fd=%d, mtu=%d", fd, mtu)

	// Create tun2socks command
	cmd, err := t.createTun2socksCommand(ctx, fd, mtu, cfg)
	if err != nil {
		cancel()
		return &TunnelError{Op: "startup", Err: fmt.Errorf("failed to create tun2socks: %w", err)}
	}

	t.cmd = cmd
	t.cancel = cancel
	t.tunFD = fd
	t.mtu = mtu

	// Start tun2socks process
	if err := cmd.Start(); err != nil {
		cancel()
		return &TunnelError{Op: "startup", Err: fmt.Errorf("tun2socks start failed: %w", err)}
	}

	t.running.Store(true)
	t.startedAt = time.Now()

	log.Printf("[Tunnel] TUN tunnel started successfully (pid=%d)", cmd.Process.Pid)

	// Monitor process in background
	go t.monitorProcess()

	return nil
}

// createTun2socksCommand creates the tun2socks exec.Cmd
func (t *Tunnel) createTun2socksCommand(ctx context.Context, fd int, mtu int, cfg TunnelConfig) (*exec.Cmd, error) {
	// Find tun2socks binary
	tun2socksPath := cfg.Tun2socksPath
	if tun2socksPath == "" {
		var err error
		tun2socksPath, err = findTun2socksBinary()
		if err != nil {
			return nil, err
		}
	}

	// Build command line for tun2socks
	// Format: tun2socks --tun-fd <fd> --socks5-addr <addr> --mtu <mtu> --enable-udp-relay
	args := []string{
		"--tun-fd", fmt.Sprintf("%d", fd),
		"--socks5-addr", cfg.Socks5Addr,
		"--mtu", fmt.Sprintf("%d", mtu),
		"--enable-udp-relay",
	}

	if cfg.Verbose {
		args = append(args, "--verbose")
	}

	log.Printf("[Tunnel] Executing: %s %v", tun2socksPath, args)

	cmd := exec.CommandContext(ctx, tun2socksPath, args...)

	// On Android, we need to inherit the file descriptor
	// The fd is already inherited by default on Unix systems
	if runtime.GOOS == "android" || runtime.GOOS == "linux" {
		// Ensure fd is inherited
		cmd.ExtraFiles = []*os.File{os.NewFile(uintptr(fd), "tun")}
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd, nil
}

// findTun2socksBinary searches for the tun2socks binary
func findTun2socksBinary() (string, error) {
	// Check common locations
	paths := []string{
		"tun2socks", // PATH
		"./tun2socks",
		"/data/local/tmp/tun2socks", // Android
		"/system/bin/tun2socks",
		filepath.Join(os.TempDir(), "tun2socks"),
	}

	for _, path := range paths {
		if _, err := exec.LookPath(path); err == nil {
			return path, nil
		}
	}

	return "", ErrTun2socksNotFound
}

// StopTunnel stops the TUN tunnel gracefully
func (t *Tunnel) StopTunnel() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running.Load() {
		return ErrTunnelNotRunning
	}

	log.Printf("[Tunnel] Stopping TUN tunnel...")

	// Cancel context to stop tun2socks
	if t.cancel != nil {
		t.cancel()
	}

	// Kill process if running
	if t.cmd != nil && t.cmd.Process != nil {
		if err := t.cmd.Process.Kill(); err != nil {
			log.Printf("[Tunnel] Warning: failed to kill tun2socks process: %v", err)
		}
	}

	t.running.Store(false)
	t.cmd = nil
	t.cancel = nil
	t.tunFD = -1
	t.mtu = 0

	log.Printf("[Tunnel] TUN tunnel stopped")
	return nil
}

// IsTunnelRunning returns true if tunnel is currently running
func (t *Tunnel) IsTunnelRunning() bool {
	return t.running.Load()
}

// GetTunnelInfo returns information about the running tunnel
func (t *Tunnel) GetTunnelInfo() map[string]interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()

	info := map[string]interface{}{
		"running": t.running.Load(),
	}

	if t.running.Load() {
		info["fd"] = t.tunFD
		info["mtu"] = t.mtu
		info["started_at"] = t.startedAt
		info["uptime"] = time.Since(t.startedAt).String()
		if t.cmd != nil && t.cmd.Process != nil {
			info["pid"] = t.cmd.Process.Pid
		}
	}

	return info
}

// monitorProcess monitors the tun2socks process and logs when it exits
func (t *Tunnel) monitorProcess() {
	t.mu.RLock()
	cmd := t.cmd
	t.mu.RUnlock()

	if cmd == nil {
		return
	}

	err := cmd.Wait()
	t.running.Store(false)

	if err != nil {
		if errors.Is(err, context.Canceled) {
			log.Printf("[Tunnel] TUN tunnel stopped by user")
		} else {
			log.Printf("[Tunnel] TUN tunnel exited with error: %v", err)
		}
	} else {
		log.Printf("[Tunnel] TUN tunnel stopped gracefully")
	}
}

// GetTUNFD returns the TUN file descriptor (for advanced use)
func (t *Tunnel) GetTUNFD() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.tunFD
}

// GetMTU returns the MTU of the TUN interface
func (t *Tunnel) GetMTU() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.mtu
}

// GetUptime returns how long the tunnel has been running
func (t *Tunnel) GetUptime() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.running.Load() {
		return 0
	}
	return time.Since(t.startedAt)
}

// Restart restarts the tunnel with the same configuration
func (t *Tunnel) Restart() error {
	t.mu.RLock()
	fd := t.tunFD
	mtu := t.mtu
	t.mu.RUnlock()

	if fd < 0 {
		return &TunnelError{Op: "restart", Err: errors.New("no previous configuration")}
	}

	if err := t.StopTunnel(); err != nil && !errors.Is(err, ErrTunnelNotRunning) {
		return err
	}

	return t.StartTunnelFromFD(fd, mtu)
}

// Close stops the tunnel and cleans up resources
func (t *Tunnel) Close() error {
	return t.StopTunnel()
}

// IsAndroidTUNSupported returns true if Android TUN backend is supported
func IsAndroidTUNSupported() bool {
	return runtime.GOOS == "android" || runtime.GOOS == "linux"
}

// ValidateFD checks if a TUN file descriptor is valid
func ValidateFD(fd int) error {
	if fd < 0 {
		return &TunnelError{Op: "validate", Err: fmt.Errorf("invalid fd: %d", fd)}
	}

	// Try to get file info (basic validation)
	f := os.NewFile(uintptr(fd), "tun")
	if f == nil {
		return &TunnelError{Op: "validate", Err: fmt.Errorf("fd %d is nil", fd)}
	}

	// Note: On Android, we can't actually test the fd without protecting it first
	// This is a basic check only
	return nil
}
