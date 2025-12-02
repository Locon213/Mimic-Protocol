package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	fmt.Println("Mimic Client v0.0.1")
	fmt.Printf("Loading configuration from: %s\n", *configPath)

	// TODO: Load configuration
	// TODO: Initialize protocol
	// TODO: Start traffic loop

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\nShutting down Mimic Client...")
}