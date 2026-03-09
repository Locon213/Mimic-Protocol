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
	LocalPort int            `yaml:"local_port"` // Deprecated: SOCKS5 proxy port (default 1080). Use Proxies instead.
	Proxies   []ProxyConfig  `yaml:"proxies"`    // List of local proxies to start (e.g. socks5, http)
	DNS       string         `yaml:"dns"`        // Custom DNS resolver (e.g. 1.1.1.1:53)
	Routing   RoutingConfig  `yaml:"routing"`    // Routing rules
}

// ProxyConfig defines a local proxy endpoint
type ProxyConfig struct {
	Type string `yaml:"type"` // "socks5", "http"
	Port int    `yaml:"port"` // Listen port
}

// RoutingConfig defines routing engine rules for the client
type RoutingConfig struct {
	DefaultPolicy string        `yaml:"default_policy"` // "proxy" (default) or "direct" or "block"
	Rules         []RoutingRule `yaml:"rules"`
}

// RoutingRule defines a single rule
type RoutingRule struct {
	Type   string `yaml:"type"`   // "domain_suffix", "domain_keyword", "ip_cidr"
	Value  string `yaml:"value"`  // e.g. "google.com" or "192.168.0.0/16"
	Policy string `yaml:"policy"` // "proxy", "direct", "block"
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

	// Legacy support for single local_port
	if len(cfg.Proxies) == 0 {
		port := cfg.LocalPort
		if port == 0 {
			port = 1080
		}
		cfg.Proxies = append(cfg.Proxies, ProxyConfig{
			Type: "socks5",
			Port: port,
		})
	}

	if cfg.Routing.DefaultPolicy == "" {
		cfg.Routing.DefaultPolicy = "proxy"
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
