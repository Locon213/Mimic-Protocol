package network

import (
	"context"
	"testing"
	"time"
)

func TestCachedResolver_Resolve(t *testing.T) {
	// Use Cloudflare for testing
	resolver := NewCachedResolver("1.1.1.1:53", 2*time.Second)

	host := "example.com"

	// 1. First resolution should take some time (network bound)
	start := time.Now()
	ips1, err := resolver.ResolveIPAddr(context.Background(), host)
	if err != nil {
		t.Fatalf("First resolve failed: %v", err)
	}
	firstDuration := time.Since(start)
	t.Logf("First resolve took: %v", firstDuration)

	if len(ips1) == 0 {
		t.Fatalf("Expected IPs, got none")
	}

	// 2. Second resolution should be instant (cache bound)
	start = time.Now()
	ips2, err := resolver.ResolveIPAddr(context.Background(), host)
	if err != nil {
		t.Fatalf("Second resolve failed: %v", err)
	}
	secondDuration := time.Since(start)
	t.Logf("Second resolve took: %v", secondDuration)

	if len(ips2) == 0 {
		t.Fatalf("Expected IPs on cached resolve, got none")
	}

	// Cache lookup is very fast, usually < 1ms
	if secondDuration > firstDuration {
		t.Errorf("Cache was slower than network! Cached: %v, Network: %v", secondDuration, firstDuration)
	}

	// 3. Test expiration
	time.Sleep(2500 * time.Millisecond) // wait for TTL to pass

	resolver.mu.RLock()
	_, exists := resolver.cache[host]
	resolver.mu.RUnlock()

	if exists {
		t.Errorf("Cache entry should have been evicted by cleanup loop")
	}
}

func TestCachedResolver_Dial(t *testing.T) {
	resolver := NewCachedResolver("8.8.8.8:53", 10*time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Should resolve example.com and successfully TCP connect to port 80
	conn, err := resolver.DialContext(ctx, "tcp", "example.com:80")
	if err != nil {
		t.Fatalf("Failed to dial using cached resolver: %v", err)
	}
	defer conn.Close()

	if conn.RemoteAddr() == nil {
		t.Fatalf("Expected remote address, got nil")
	}
}

func TestCachedResolver_ResolveUDPAddr(t *testing.T) {
	resolver := NewCachedResolver("1.1.1.1:53", 10*time.Minute)

	addr, err := resolver.ResolveUDPAddr("udp", "example.com:1234")
	if err != nil {
		t.Fatalf("ResolveUDPAddr failed: %v", err)
	}

	if addr.Port != 1234 {
		t.Errorf("Expected port 1234, got %d", addr.Port)
	}

	if addr.IP == nil {
		t.Errorf("Expected non-nil IP")
	}
}
