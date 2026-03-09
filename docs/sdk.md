# Mimic Client SDK

The **Mimic Client SDK** allows you to embed the Mimic Protocol client directly into your own Go applications, such as a desktop GUI, an Android wrapper, or a specialized CLI tool.

It effectively turns your Go binary into a fully functional MTP (Mimic Transport Protocol) Client with programmatic control over the connection and local proxies.

## Installation

```bash
go get github.com/Locon213/Mimic-Protocol/pkg/client
go get github.com/Locon213/Mimic-Protocol/pkg/config
```

## Basic Example

Here is a minimal example of how to start the Mimic Client inside another application.

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/Locon213/Mimic-Protocol/pkg/client"
	"github.com/Locon213/Mimic-Protocol/pkg/config"
)

func main() {
	// 1. Define the Client Configuration
	cfg := &config.ClientConfig{
		Server: "your-server-ip:443", // The remote MTP server
		UUID:   "your-uuid-here",
		Domains: []string{
			"vk.com", 
			"rutube.ru",
		},
		Settings: config.ClientSettings{
			// Rotate the disguise domain every 60-300 seconds
			SwitchMin: 60 * time.Second,
			SwitchMax: 300 * time.Second,
		},
		Transport: "mtp",
		
		// Define which local proxies to start
		Proxies: []config.ProxyConfig{
			{Type: "socks5", Port: 1080},
			{Type: "http", Port: 1081},
		},
		
		// Optional: Define routing engine rules
		Routing: config.RoutingConfig{
			DefaultPolicy: "proxy",
			Rules: []config.RoutingRule{
				{Type: "domain_suffix", Value: "ru", Policy: "direct"},
			},
		},
	}

	// 2. Initialize the SDK
	mimicClient, err := client.NewClient(cfg)
	if err != nil {
		log.Fatalf("Failed to init SDK: %v", err)
	}

	// 3. Start the MTP Connection loop background tasks
	ctx := context.Background()
	if err := mimicClient.Start(ctx); err != nil {
		log.Fatalf("Failed to start MTP connection: %v", err)
	}
	
	// 4. Start the local listeners (SOCKS5/HTTP Proxy)
	if err := mimicClient.StartProxies(); err != nil {
		log.Fatalf("Failed to bind proxy ports: %v", err)
	}
	
	log.Println("Mimic Protocol SDK successfully started!")

	// 5. Block the main thread or do your own app rendering here...
	select {}
	
	// 6. Graceful shutdown
	// mimicClient.Stop()
}
```

## Core Functions

### `NewClient(cfg *config.ClientConfig) (*Client, error)`
Initializes the SDK. Compiles routing rules and prepares the transport manager. Does not block or connect to the network.

### `Start(ctx context.Context) error`
Establishes the initial UDP connection to the server, completes the authentication handshake, negotiates `yamux`, and starts background tasks (domain rolling and dummy traffic noise).

### `StartProxies() error`
Binds to local ports as specified in the `Proxies` configuration array. Starts listening for SOCKS5 or HTTP/HTTPS proxy connections and handles all routing internally.

### `Stop()`
Gracefully shuts down all local proxies, the `yamux` multiplexer, the UDP connection, and terminates all background goroutines. This is safe to call during your application's teardown phase.
