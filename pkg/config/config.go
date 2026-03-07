package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ClientConfig defines the configuration for the Mimic Client
type ClientConfig struct {
	Server    string         `yaml:"server"`
	UUID      string         `yaml:"uuid"`
	Domains   []string       `yaml:"domains"`
	Settings  ClientSettings `yaml:"settings"`
	Transport string         `yaml:"transport"`  // "mtp" (default) or "tcp"
	LocalPort int            `yaml:"local_port"` // SOCKS5 proxy port (default 1080)
	DNS       string         `yaml:"dns"`        // Custom DNS resolver (e.g. 1.1.1.1:53)
}

type ClientSettings struct {
	SwitchTimeRangeStr string `yaml:"switch_time"` // e.g. "60s-300s"
	Randomize          bool   `yaml:"randomize"`

	// Parsed values (internal use)
	SwitchMin time.Duration `yaml:"-"`
	SwitchMax time.Duration `yaml:"-"`
}

// ServerConfig defines the configuration for the Mimic Server
type ServerConfig struct {
	Port        int      `yaml:"port"`
	UUID        string   `yaml:"uuid"`
	DomainList  []string `yaml:"domain_list"`
	DomainsFile string   `yaml:"domains_file"`
	PresetsDir  string   `yaml:"presets_dir"`
	MaxClients  int      `yaml:"max_clients"`
	RateLimit   int      `yaml:"rate_limit"`
	Transport   string   `yaml:"transport"` // "mtp" (default) or "tcp"
	DNS         string   `yaml:"dns"`       // Custom DNS resolver (e.g. 1.1.1.1:53)
}

// LoadClientConfig reads and parses the client configuration file
func LoadClientConfig(path string) (*ClientConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg ClientConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Parse switch time range: "60s-300s" or "60-300" (seconds)
	cfg.Settings.SwitchMin, cfg.Settings.SwitchMax = parseSwitchTime(cfg.Settings.SwitchTimeRangeStr)

	// Defaults
	if cfg.Transport == "" {
		cfg.Transport = "mtp"
	}
	if cfg.LocalPort == 0 {
		cfg.LocalPort = 1080
	}

	// Validation
	if cfg.Server == "" {
		return nil, fmt.Errorf("config: 'server' is required")
	}
	if cfg.UUID == "" {
		return nil, fmt.Errorf("config: 'uuid' is required")
	}
	if len(cfg.Domains) == 0 {
		return nil, fmt.Errorf("config: 'domains' must contain at least one domain")
	}

	return &cfg, nil
}

// LoadServerConfig reads and parses the server configuration file
func LoadServerConfig(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg ServerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Defaults
	if cfg.Port == 0 {
		cfg.Port = 443
	}
	if cfg.MaxClients == 0 {
		cfg.MaxClients = 100
	}
	if cfg.Transport == "" {
		cfg.Transport = "mtp"
	}

	// Validation
	if cfg.UUID == "" {
		return nil, fmt.Errorf("config: 'uuid' is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("config: 'port' must be between 1 and 65535")
	}

	return &cfg, nil
}

// parseSwitchTime parses a switch time range string like "60s-300s" or "60-300"
func parseSwitchTime(s string) (min, max time.Duration) {
	// Defaults
	min = 60 * time.Second
	max = 300 * time.Second

	if s == "" {
		return
	}

	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return
	}

	// Try parsing as durations first (e.g., "60s", "5m")
	minD, errMin := time.ParseDuration(strings.TrimSpace(parts[0]))
	maxD, errMax := time.ParseDuration(strings.TrimSpace(parts[1]))

	if errMin == nil && errMax == nil {
		min = minD
		max = maxD
		return
	}

	// Try parsing as plain numbers (seconds)
	var minSec, maxSec int
	_, err := fmt.Sscanf(strings.TrimSpace(parts[0]), "%d", &minSec)
	if err == nil {
		min = time.Duration(minSec) * time.Second
	}
	_, err = fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &maxSec)
	if err == nil {
		max = time.Duration(maxSec) * time.Second
	}

	return
}
