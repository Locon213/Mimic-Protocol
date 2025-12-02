package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	port := flag.Int("port", 443, "Server port")
	configPath := flag.String("config", "server.yaml", "Path to configuration file")
	flag.Parse()

	fmt.Println("Mimic Server v0.0.1")
	fmt.Printf("Starting on port %d\n", *port)
	fmt.Printf("Loading configuration from: %s\n", *configPath)

	// TODO: Load configuration
	// TODO: Initialize listeners
	// TODO: Handle connections

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\nShutting down Mimic Server...")
}
