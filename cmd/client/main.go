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
	"github.com/Locon213/Mimic-Protocol/pkg/transport"
)

type AppState struct {
	cfg           *config.ClientConfig
	currentPreset *presets.Preset
	currentDomain string
	gen           *mimic.Generator
	tm            *transport.Manager
	mutex         sync.RWMutex
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	fmt.Println("Mimic Client v0.0.1")

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
		}
	}

	state := &AppState{
		cfg: cfg,
		tm:  transport.NewManager(cfg.Server, cfg.UUID),
	}

	// Initial domain setup
	state.switchDomain(cfg.Domains[0])

	// 2. Start Session (Connect + Handshake + Yamux)
	log.Printf("Connecting to %s...", cfg.Server)
	session, err := state.tm.StartSession(state.currentDomain)
	if err != nil {
		log.Fatalf("Failed to start session: %v", err)
	}
	log.Println("Session established!")

	// 3. Start control loop (domain switching)
	stopChan := make(chan struct{})
	go runDomainSwitcher(state, stopChan)

	// 4. Start traffic loop (using Yamux stream)
	// We open a stream over the persistent session
	stream, err := session.Open()
	if err != nil {
		log.Fatalf("Failed to open stream: %v", err)
	}

	go runTrafficLoop(state, stream, stopChan)

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\nShutting down Mimic Client...")
	close(stopChan)
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

	fmt.Printf("\n[Mimic] Switching profile >>> %s (Preset: %s)\n", domain, preset.Name)
}

func (s *AppState) getParams() (string, *mimic.Generator) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.currentDomain, s.gen
}

func runDomainSwitcher(state *AppState, stop <-chan struct{}) {
	domainIndex := 0

	getNextSwitchTime := func() time.Duration {
		return state.cfg.Settings.SwitchMin // Simplified
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

			// 1. Update Logic
			state.switchDomain(newDomain)

			// 2. Rotate Transport (The magic happens here)
			// This runs in background to not block main logic too much
			go func(d string) {
				err := state.tm.RotateTransport(d)
				if err != nil {
					log.Printf("Failed to rotate transport: %v", err)
				}
			}(newDomain)

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

			// Send data through Yamux stream
			_, err := stream.Write(payload)
			if err != nil {
				log.Printf("Write error: %v", err)
				return
			}

			// Read response
			stream.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			stream.Read(buf)
		}
	}
}
