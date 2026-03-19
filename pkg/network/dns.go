package network

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// cacheEntry holds the resolved IP addresses and expiration time
type cacheEntry struct {
	ips       []net.IPAddr
	expiresAt time.Time
}

// CachedResolver provides DNS resolution with an in-memory cache and custom upstream DNS.
type CachedResolver struct {
	resolver *net.Resolver
	cache    map[string]cacheEntry
	mu       sync.RWMutex
	ttl      time.Duration
}

// NewCachedResolver creates a new resolver.
// If nameserver is empty, it uses the system default.
// Example nameserver: "1.1.1.1:53"
func NewCachedResolver(nameserver string, ttl time.Duration) *CachedResolver {
	r := &CachedResolver{
		cache: make(map[string]cacheEntry),
		ttl:   ttl,
	}

	if nameserver != "" {
		r.resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				// Use protected dialer for DNS connections under Android VpnService
				return DialProtectedContext(ctx, "udp", nameserver)
			},
		}
	} else {
		r.resolver = net.DefaultResolver
	}

	// Start a background cleaner for expired cache entries
	go r.cleanupLoop()

	return r
}

func (r *CachedResolver) cleanupLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	for range ticker.C {
		r.mu.Lock()
		now := time.Now()
		for host, entry := range r.cache {
			if now.After(entry.expiresAt) {
				delete(r.cache, host)
			}
		}
		r.mu.Unlock()
	}
}

// ResolveIPAddr caches and resolves a host to its IP addresses.
func (r *CachedResolver) ResolveIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	// Handle localhost locally
	if host == "localhost" {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}

	// 1. Check Cache
	r.mu.RLock()
	entry, exists := r.cache[host]
	r.mu.RUnlock()

	if exists && time.Now().Before(entry.expiresAt) {
		return entry.ips, nil
	}

	// 2. Resolve
	ips, err := r.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no IPs found for %s", host)
	}

	// 3. Update Cache
	r.mu.Lock()
	r.cache[host] = cacheEntry{
		ips:       ips,
		expiresAt: time.Now().Add(r.ttl),
	}
	r.mu.Unlock()

	return ips, nil
}

// ResolveUDPAddr acts like net.ResolveUDPAddr but uses the cache.
func (r *CachedResolver) ResolveUDPAddr(network, address string) (*net.UDPAddr, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	// If host is already an IP, parse it directly
	if ip := net.ParseIP(host); ip != nil {
		portNum, _ := net.LookupPort(network, port)
		return &net.UDPAddr{IP: ip, Port: portNum}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := r.ResolveIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}

	portNum, err := net.LookupPort(network, port)
	if err != nil {
		return nil, err
	}

	return &net.UDPAddr{IP: ips[0].IP, Port: portNum}, nil
}

// DialContext acts like net.DialContext but uses the cache for DNS resolution.
// It resolves the hostname and dials the first available IP.
// Uses protected dialer when running under Android VpnService.
func (r *CachedResolver) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	if net.ParseIP(host) != nil {
		// It's already an IP, dial directly using protected dialer
		return DialProtectedContext(ctx, network, address)
	}

	ips, err := r.ResolveIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("dns resolve error: %w", err)
	}

	var lastErr error

	// Try IPs until one succeeds (using protected dialer)
	for _, ip := range ips {
		targetAddr := net.JoinHostPort(ip.IP.String(), portStr)
		conn, err := DialProtectedContext(ctx, network, targetAddr)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("failed to dial all matched IPs, last error: %w", lastErr)
}
