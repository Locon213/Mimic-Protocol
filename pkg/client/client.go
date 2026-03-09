package client

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/config"
	"github.com/Locon213/Mimic-Protocol/pkg/mimic"
	"github.com/Locon213/Mimic-Protocol/pkg/presets"
	"github.com/Locon213/Mimic-Protocol/pkg/proxy"
	"github.com/Locon213/Mimic-Protocol/pkg/routing"
	"github.com/Locon213/Mimic-Protocol/pkg/transport"
	"github.com/hashicorp/yamux"
)

const StreamTypeMimic = 0x02

// Client defines the embeddable SDK client for Mimic Protocol
type Client struct {
	cfg           *config.ClientConfig
	currentPreset *presets.Preset
	currentDomain string
	gen           *mimic.Generator
	tm            *transport.Manager
	router        *routing.Router
	session       *yamux.Session

	proxies     []interface{ Close() error }
	connectedAt time.Time

	mutex    sync.RWMutex
	stopChan chan struct{}
}

// NewClient initializes a new core client instance based on the configuration
func NewClient(cfg *config.ClientConfig) (*Client, error) {
	// Initialize Router
	var rules []*routing.Rule
	for _, r := range cfg.Routing.Rules {
		rules = append(rules, &routing.Rule{
			Type:   r.Type,
			Value:  r.Value,
			Policy: routing.Policy(r.Policy),
		})
	}
	router := routing.NewRouter(rules, routing.Policy(cfg.Routing.DefaultPolicy))

	c := &Client{
		cfg:      cfg,
		tm:       transport.NewManager(cfg.Server, cfg.UUID, cfg.DNS),
		router:   router,
		stopChan: make(chan struct{}),
	}

	// Set initial domain/preset
	if len(cfg.Domains) > 0 {
		c.switchDomain(cfg.Domains[0])
	}

	return c, nil
}

// Start initiates the MTP connection to the remote server and starts backend tasks
func (c *Client) Start(ctx context.Context) error {
	log.Printf("🔌 Connecting to %s via MTP (UDP)...", c.cfg.Server)
	session, err := c.tm.StartSession(c.currentDomain)
	if err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}

	c.session = session
	c.connectedAt = time.Now()
	log.Println("✅ Session established!")

	// Domain switching task
	go c.runDomainSwitcher()

	// Mimic traffic generator task
	stream, err := session.Open()
	if err != nil {
		log.Printf("⚠️ Could not open mimic stream: %v", err)
	} else {
		stream.Write([]byte{StreamTypeMimic})
		go c.runTrafficLoop(stream)
	}

	return nil
}

// StartProxies binds the local SOCKS5 and HTTP listeners defined in config
func (c *Client) StartProxies() error {
	for _, pConfig := range c.cfg.Proxies {
		bindAddr := fmt.Sprintf("127.0.0.1:%d", pConfig.Port)

		if pConfig.Type == "socks5" {
			socks5, err := proxy.NewSOCKS5Server(bindAddr, c.session, c.router)
			if err != nil {
				return fmt.Errorf("failed to start SOCKS5 proxy on %s: %w", bindAddr, err)
			}
			go socks5.Serve()
			c.proxies = append(c.proxies, socks5)

			// Optional background stats display for the first SOCKS5 started natively
			if len(c.proxies) == 1 {
				go c.runStatsDisplay(socks5)
			}
			log.Printf("🌐 SOCKS5 Proxy started: %s", socks5.Addr().String())
		} else if pConfig.Type == "http" {
			httpProxy, err := proxy.NewHTTPProxyServer(bindAddr, c.session, c.router)
			if err != nil {
				return fmt.Errorf("failed to start HTTP proxy on %s: %w", bindAddr, err)
			}
			go httpProxy.Serve()
			c.proxies = append(c.proxies, httpProxy)
			log.Printf("🌐 HTTP/HTTPS Proxy started: %s", httpProxy.Addr().String())
		} else {
			log.Printf("⚠️ Unknown proxy type '%s' on port %d", pConfig.Type, pConfig.Port)
		}
	}
	return nil
}

// Stop closes all proxies and underlying protocols gracefully
func (c *Client) Stop() {
	close(c.stopChan)
	for _, p := range c.proxies {
		p.Close()
	}
	if c.session != nil {
		c.session.Close()
	}
}

func (c *Client) switchDomain(domain string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.currentDomain = domain
	preset, err := presets.DetectPreset(domain)
	if err != nil {
		preset = presets.DefaultPresets()["web_generic"]
	}
	c.currentPreset = &preset

	if c.gen == nil {
		c.gen = mimic.NewGenerator(&preset)
	} else {
		c.gen.SetPreset(&preset)
	}

	log.Printf("🎭 [Mimic] Switching profile >>> %s (Preset: %s)", domain, preset.Name)
}

func (c *Client) runDomainSwitcher() {
	domainIndex := 0

	getNextSwitchTime := func() time.Duration {
		minD := c.cfg.Settings.SwitchMin
		maxD := c.cfg.Settings.SwitchMax
		if minD >= maxD {
			return minD
		}
		diff := maxD - minD
		return minD + time.Duration(rand.Int63n(int64(diff)))
	}

	timer := time.NewTimer(getNextSwitchTime())
	defer timer.Stop()

	for {
		select {
		case <-c.stopChan:
			return
		case <-timer.C:
			domains := c.cfg.Domains
			domainIndex = (domainIndex + 1) % len(domains)
			newDomain := domains[domainIndex]

			c.switchDomain(newDomain)
			timer.Reset(getNextSwitchTime())
		}
	}
}

func (c *Client) runTrafficLoop(stream net.Conn) {
	buf := make([]byte, 65535)

	for {
		select {
		case <-c.stopChan:
			stream.Close()
			return
		default:
			c.mutex.RLock()
			gen := c.gen
			c.mutex.RUnlock()

			delay := gen.NextPacketDelay()
			size := gen.NextPacketSize()

			time.Sleep(delay)

			payload := make([]byte, size)
			rand.Read(payload)

			if _, err := stream.Write(payload); err != nil {
				return
			}

			stream.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			stream.Read(buf)
		}
	}
}

// runStatsDisplay prints stats directly to stdout (useful for CLI fallback)
func (c *Client) runStatsDisplay(socks5 *proxy.SOCKS5Server) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	stats := socks5.GetStats()
	var prevUp, prevDown int64

	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			var mtpUpTotal, mtpDownTotal int64
			mtpConn := c.tm.GetMTPConn()
			if mtpConn != nil {
				mtpUpTotal = mtpConn.BytesSent.Load()
				mtpDownTotal = mtpConn.BytesRecv.Load()
			}

			upSpeed := mtpUpTotal - prevUp
			downSpeed := mtpDownTotal - prevDown
			prevUp = mtpUpTotal
			prevDown = mtpDownTotal
			totalTraffic := mtpUpTotal + mtpDownTotal
			uptime := time.Since(c.connectedAt)

			statusLine := fmt.Sprintf("\r  ↑ %s/s  ↓ %s/s  │  Traffic: %s  │  Connected: %s  │  Active: %d  ",
				formatBytes(upSpeed), formatBytes(downSpeed), formatBytes(totalTraffic),
				formatDuration(uptime), stats.ActiveConns.Load(),
			)

			fmt.Print(statusLine)
		}
	}
}

func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
