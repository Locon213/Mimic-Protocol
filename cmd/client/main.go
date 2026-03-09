package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/client"
	"github.com/Locon213/Mimic-Protocol/pkg/config"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════════════╗")
	fmt.Println("║           Mimic Client v0.3.0 (MTP)         ║")
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
			Proxies: []config.ProxyConfig{
				{Type: "socks5", Port: 1080},
				{Type: "http", Port: 1081},
			},
		}
	}

	// 2. Initialize SDK Client
	c, err := client.NewClient(cfg)
	if err != nil {
		log.Fatalf("❌ Failed to initialize client: %v", err)
	}

	// 3. Connect to Server
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		log.Fatalf("❌ Failed to start client connection: %v", err)
	}

	// 4. Start Local Proxies (SOCKS5, HTTP)
	if err := c.StartProxies(); err != nil {
		log.Fatalf("❌ Failed to start local proxies: %v", err)
	}

	fmt.Printf("🔑 UUID: %s\n", cfg.UUID)
	fmt.Printf("🛡️  Transport: MTP (Mimic Transport Protocol)\n")
	fmt.Println("────────────────────────────────────────────────")
	fmt.Println("Configure your browser/application to use the endpoints above.")
	fmt.Println("────────────────────────────────────────────────")

	// 5. Graceful shutdown handler
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\n🛑 Shutting down Mimic Client...")
	c.Stop()
	time.Sleep(500 * time.Millisecond)
}
