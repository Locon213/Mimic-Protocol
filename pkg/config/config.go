package config

import (
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ClientConfig represents the client configuration
type ClientConfig struct {
	Server     string         // Server address (IP:PORT)
	UUID       string         // Unique authorization identifier
	Domains    []string       // List of domains for masking
	Transport  string         // Transport type: "mtp" or "tcp"
	Proxies    []ProxyConfig  // Local proxy server settings
	DNS        string         // Custom DNS server
	Settings   ClientSettings // Fine-tuning settings
	Routing    RoutingConfig  // Routing rules
	ServerName string         // Server name from URL
	LocalPort  int            // Local proxy port (for backward compatibility)
}

// ServerConfig represents the server configuration
type ServerConfig struct {
	Port       int      // Listening port
	MaxClients int      // Maximum number of clients
	UUID       string   // Server UUID for authentication
	DomainList []string // List of domains for masking
	Transport  string   // Transport type: "mtp" or "tcp"
	DNS        string   // DNS server
	Name       string   // Server name
}

// ProxyConfig represents local proxy configuration
type ProxyConfig struct {
	Type string // Proxy type: "socks5", "http"
	Port int    // Port number
}

// ClientSettings represents fine-tuning settings
type ClientSettings struct {
	SwitchTimeRangeStr string        // Time range for domain switching (e.g., "60s-300s")
	SwitchMin          time.Duration // Minimum switch interval
	SwitchMax          time.Duration // Maximum switch interval
	Randomize          bool          // Randomize domain order
}

// RoutingConfig represents routing configuration
type RoutingConfig struct {
	DefaultPolicy string        // Default policy: "proxy", "direct", "block"
	Rules         []RoutingRule // Routing rules
}

// RoutingRule represents a single routing rule
type RoutingRule struct {
	Type   string // Rule type: "domain_suffix", "domain_keyword", "ip_cidr"
	Value  string // Rule value (domain, keyword, or CIDR)
	Policy string // Policy: "proxy", "direct", "block"
}

// clientYAMLConfig represents the raw YAML client config structure
type clientYAMLConfig struct {
	Server    string   `yaml:"server"`
	UUID      string   `yaml:"uuid"`
	Transport string   `yaml:"transport"`
	LocalPort int      `yaml:"local_port"`
	DNS       string   `yaml:"dns"`
	Domains   []string `yaml:"domains"`
	Settings  struct {
		SwitchTime string `yaml:"switch_time"`
		Randomize  bool   `yaml:"randomize"`
	} `yaml:"settings"`
}

// serverYAMLConfig represents the raw YAML server config structure
type serverYAMLConfig struct {
	Port       int      `yaml:"port"`
	UUID       string   `yaml:"uuid"`
	Transport  string   `yaml:"transport"`
	DNS        string   `yaml:"dns"`
	MaxClients int      `yaml:"max_clients"`
	DomainList []string `yaml:"domain_list"`
	Name       string   `yaml:"name"`
}

// LoadClientConfig loads client configuration from YAML file
func LoadClientConfig(path string) (*ClientConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var yamlCfg clientYAMLConfig
	if err := yaml.Unmarshal(data, &yamlCfg); err != nil {
		return nil, err
	}

	cfg := &ClientConfig{
		Server:    yamlCfg.Server,
		UUID:      yamlCfg.UUID,
		Transport: yamlCfg.Transport,
		DNS:       yamlCfg.DNS,
		Domains:   yamlCfg.Domains,
		LocalPort: yamlCfg.LocalPort,
		Settings: ClientSettings{
			SwitchTimeRangeStr: yamlCfg.Settings.SwitchTime,
			Randomize:          yamlCfg.Settings.Randomize,
		},
		Proxies: []ProxyConfig{
			{Type: "socks5", Port: 1080},
		},
		Routing: RoutingConfig{
			DefaultPolicy: "proxy",
		},
	}

	// Set default values
	if cfg.Transport == "" {
		cfg.Transport = "mtp"
	}
	if cfg.LocalPort == 0 {
		cfg.LocalPort = 1080
	}
	if cfg.Proxies[0].Port == 0 {
		cfg.Proxies[0].Port = 1080
	}

	// Parse switch time
	if cfg.Settings.SwitchTimeRangeStr != "" {
		cfg.Settings.SwitchMin, cfg.Settings.SwitchMax = parseSwitchTime(cfg.Settings.SwitchTimeRangeStr)
	}

	return cfg, nil
}

// LoadServerConfig loads server configuration from YAML file
func LoadServerConfig(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var yamlCfg serverYAMLConfig
	if err := yaml.Unmarshal(data, &yamlCfg); err != nil {
		return nil, err
	}

	cfg := &ServerConfig{
		Port:       yamlCfg.Port,
		UUID:       yamlCfg.UUID,
		Transport:  yamlCfg.Transport,
		DNS:        yamlCfg.DNS,
		MaxClients: yamlCfg.MaxClients,
		DomainList: yamlCfg.DomainList,
		Name:       yamlCfg.Name,
	}

	// Set default values
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.Transport == "" {
		cfg.Transport = "mtp"
	}
	if cfg.MaxClients == 0 {
		cfg.MaxClients = 100
	}

	return cfg, nil
}

// parseSwitchTime parses time range string like "60s-300s" or "1m-5m"
func parseSwitchTime(timeRange string) (min, max time.Duration) {
	parts := strings.Split(timeRange, "-")
	if len(parts) != 2 {
		return 60 * time.Second, 300 * time.Second
	}

	minDur, err := parseDuration(parts[0])
	if err != nil {
		minDur = 60 * time.Second
	}

	maxDur, err := parseDuration(parts[1])
	if err != nil {
		maxDur = 300 * time.Second
	}

	return minDur, maxDur
}

// parseDuration parses duration string with suffix support (s, m)
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "m") {
		s = strings.TrimSuffix(s, "m")
		return time.ParseDuration(s + "m")
	}
	if strings.HasSuffix(s, "s") {
		s = strings.TrimSuffix(s, "s")
		return time.ParseDuration(s + "s")
	}
	return time.ParseDuration(s)
}
