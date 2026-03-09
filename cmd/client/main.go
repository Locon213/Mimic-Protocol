package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/config"
	"github.com/Locon213/Mimic-Protocol/pkg/mimic"
	"github.com/Locon213/Mimic-Protocol/pkg/presets"
	"github.com/Locon213/Mimic-Protocol/pkg/proxy"
	"github.com/Locon213/Mimic-Protocol/pkg/routing"
	"github.com/Locon213/Mimic-Protocol/pkg/transport"
)

// Stream type marker (must match server)
const StreamTypeMimic = 0x02

type AppState struct {
	cfg           *config.ClientConfig
	currentPreset *presets.Preset
	currentDomain string
	gen           *mimic.Generator
	tm            *transport.Manager
	mutex         sync.RWMutex
	connectedAt   time.Time
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════════════╗")
	fmt.Println("║           Mimic Client v0.2.0 (MTP)         ║")
	fmt.Println("╚══════════════════════════════════════════════╝")

	// 1. Load configuration
	cfg, err := config.LoadClientConfig(*configPath)
	if err != nil {
		log.Printf("Warning: could not load config file: %v", err)
		log.Println("Using default configuration for demo...")
		cfg = &config.ClientConfig{
			Server:  "localhost:8080",
			UUID:    "demo-uuid-12345",
			Domains: []string{"vk.com", "rutube.ru", "telegram.org"},
			Settings: config.ClientSettings{
				SwitchMin: 10 * time.Second,
				SwitchMax: 30 * time.Second,
			},
			Transport: "mtp",
			LocalPort: 1080,
		}
	}

	state := &AppState{
		cfg: cfg,
		tm:  transport.NewManager(cfg.Server, cfg.UUID, cfg.DNS),
	}

	// Initial domain setup
	state.switchDomain(cfg.Domains[0])

	// 2. Start Session (MTP + Yamux)
	fmt.Printf("🔌 Connecting to %s via MTP (UDP)...\n", cfg.Server)
	session, err := state.tm.StartSession(state.currentDomain)
	if err != nil {
		log.Fatalf("❌ Failed to start session: %v", err)
	}

	state.connectedAt = time.Now()
	fmt.Println("✅ Session established!")
	fmt.Println("────────────────────────────────────────────────")

	// 3. Initialize Routing Engine
	var rules []*routing.Rule
	for _, r := range cfg.Routing.Rules {
		rules = append(rules, &routing.Rule{
			Type:   r.Type,
			Value:  r.Value,
			Policy: routing.Policy(r.Policy),
		})
	}
	router := routing.NewRouter(rules, routing.Policy(cfg.Routing.DefaultPolicy))

	// 4. Start SOCKS5 proxy
	bindAddr := fmt.Sprintf("127.0.0.1:%d", cfg.LocalPort)
	socks5, err := proxy.NewSOCKS5Server(bindAddr, session, router)
	if err != nil {
		log.Fatalf("❌ Failed to start SOCKS5 proxy: %v", err)
	}

	go socks5.Serve()

	fmt.Printf("🌐 SOCKS5 Proxy: %s\n", socks5.Addr().String())
	fmt.Printf("🔑 UUID: %s\n", cfg.UUID)
	fmt.Printf("🛡️  Transport: MTP (Mimic Transport Protocol)\n")
	fmt.Println("────────────────────────────────────────────────")
	fmt.Println("Configure your browser/application to use:")
	fmt.Printf("  Protocol: SOCKS5\n")
	fmt.Printf("  Address:  127.0.0.1\n")
	fmt.Printf("  Port:     %d\n", cfg.LocalPort)
	fmt.Println("────────────────────────────────────────────────")

	// 4. Start control loop (domain/preset switching only — no transport migration)
	stopChan := make(chan struct{})
	go runDomainSwitcher(state, stopChan)

	// 5. Start stats display
	go runStatsDisplay(state, socks5, stopChan)

	// 6. Start mimic traffic loop (background noise)
	stream, err := session.Open()
	if err != nil {
		log.Printf("⚠️ Could not open mimic stream: %v", err)
	} else {
		// Send stream type marker first so server knows this is mimic traffic
		stream.Write([]byte{StreamTypeMimic})
		go runTrafficLoop(state, stream, stopChan)
	}

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\n🛑 Shutting down Mimic Client...")
	close(stopChan)
	socks5.Close()
	time.Sleep(500 * time.Millisecond)
}

func (s *AppState) switchDomain(domain string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.currentDomain = domain
	preset, err := presets.DetectPreset(domain)
	if err != nil {
		preset = presets.DefaultPresets()["web_generic"]
	}
	s.currentPreset = &preset

	if s.gen == nil {
		s.gen = mimic.NewGenerator(&preset)
	} else {
		s.gen.SetPreset(&preset)
	}

	fmt.Printf("\n🎭 [Mimic] Switching profile >>> %s (Preset: %s)\n", domain, preset.Name)
}

func (s *AppState) getParams() (string, *mimic.Generator) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.currentDomain, s.gen
}

func runDomainSwitcher(state *AppState, stop <-chan struct{}) {
	domainIndex := 0

	getNextSwitchTime := func() time.Duration {
		cfg := state.cfg
		minD := cfg.Settings.SwitchMin
		maxD := cfg.Settings.SwitchMax
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
		case <-stop:
			return
		case <-timer.C:
			domains := state.cfg.Domains
			domainIndex = (domainIndex + 1) % len(domains)
			newDomain := domains[domainIndex]

			// Switch the mimic behavior profile (preset)
			// NOTE: No transport migration — MTP's polymorphic packets
			// already change appearance on every send, making physical
			// connection rotation unnecessary for DPI evasion.
			state.switchDomain(newDomain)

			timer.Reset(getNextSwitchTime())
		}
	}
}

func runTrafficLoop(state *AppState, stream net.Conn, stop <-chan struct{}) {
	buf := make([]byte, 65535)

	for {
		select {
		case <-stop:
			return
		default:
			_, gen := state.getParams()

			// Mimicry logic
			delay := gen.NextPacketDelay()
			size := gen.NextPacketSize()

			time.Sleep(delay)

			payload := make([]byte, size)
			rand.Read(payload)

			_, err := stream.Write(payload)
			if err != nil {
				log.Printf("[Mimic] Write error: %v", err)
				return
			}

			// Read response
			stream.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			stream.Read(buf)
		}
	}
}

// runStatsDisplay prints live connection statistics
func runStatsDisplay(state *AppState, socks5 *proxy.SOCKS5Server, stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	stats := socks5.GetStats()

	var prevUp, prevDown int64

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// Get MTP-level stats
			var mtpUpTotal, mtpDownTotal int64
			mtpConn := state.tm.GetMTPConn()
			if mtpConn != nil {
				mtpUpTotal = mtpConn.BytesSent.Load()
				mtpDownTotal = mtpConn.BytesRecv.Load()
			}

			// Calculate speed based on actual MTP traffic (includes mimicry pacing)
			upSpeed := mtpUpTotal - prevUp
			downSpeed := mtpDownTotal - prevDown
			prevUp = mtpUpTotal
			prevDown = mtpDownTotal

			totalTraffic := mtpUpTotal + mtpDownTotal

			// Format uptime
			uptime := time.Since(state.connectedAt)

			// Build status line
			statusLine := fmt.Sprintf("\r  ↑ %s/s  ↓ %s/s  │  Traffic: %s  │  Connected: %s  │  Active: %d  ",
				formatBytes(upSpeed),
				formatBytes(downSpeed),
				formatBytes(totalTraffic),
				formatDuration(uptime),
				stats.ActiveConns.Load(),
			)

			fmt.Print(statusLine)
		}
	}
}

// formatBytes formats bytes into human-readable string
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

// formatDuration formats a duration as HH:MM:SS
func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
