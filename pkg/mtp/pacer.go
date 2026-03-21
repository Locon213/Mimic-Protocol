package mtp

import (
	"sync"
	"time"
)

// Pacer implements token bucket rate limiting for smooth packet transmission
// This prevents burst loss and improves throughput on congested networks
type Pacer struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  int
	refillRate float64 // packets per second
	lastRefill time.Time
	stopCh     chan struct{}
}

// NewPacer creates a new pacer with the given rate limit
// rate: packets per second
// burst: maximum burst size (tokens)
func NewPacer(rate int, burst int) *Pacer {
	return &Pacer{
		tokens:     float64(burst),
		maxTokens:  burst,
		refillRate: float64(rate),
		lastRefill: time.Now(),
		stopCh:     make(chan struct{}),
	}
}

// Wait blocks until a token is available or pacer is stopped
func (p *Pacer) Wait() {
	for {
		if p.tryConsume() {
			return
		}
		// Check if stopped
		select {
		case <-p.stopCh:
			return
		default:
		}
		time.Sleep(time.Millisecond)
	}
}

// Close stops the pacer, unblocking any Wait() calls
func (p *Pacer) Close() {
	select {
	case <-p.stopCh:
		// Already closed
	default:
		close(p.stopCh)
	}
}

// tryConsume attempts to consume a token
func (p *Pacer) tryConsume() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(p.lastRefill).Seconds()
	p.tokens += elapsed * p.refillRate
	if p.tokens > float64(p.maxTokens) {
		p.tokens = float64(p.maxTokens)
	}
	p.lastRefill = now

	// Consume token if available
	if p.tokens >= 1.0 {
		p.tokens -= 1.0
		return true
	}
	return false
}

// SetRate updates the pacing rate dynamically
func (p *Pacer) SetRate(rate int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refillRate = float64(rate)
}

// SetBurst updates the maximum burst size
func (p *Pacer) SetBurst(burst int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maxTokens = burst
	if p.tokens > float64(burst) {
		p.tokens = float64(burst)
	}
}

// GetTokens returns current token count (for debugging)
func (p *Pacer) GetTokens() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tokens
}
