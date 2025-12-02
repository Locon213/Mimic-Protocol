package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ClientConfig defines the configuration for the Mimic Client
type ClientConfig struct {
	Server   string         `yaml:"server"`
	UUID     string         `yaml:"uuid"`
	Domains  []string       `yaml:"domains"`
	Settings ClientSettings `yaml:"settings"`
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
	Port        int    `yaml:"port"`
	DomainsFile string `yaml:"domains_file"`
	PresetsDir  string `yaml:"presets_dir"`
	MaxClients  int    `yaml:"max_clients"`
	RateLimit   int    `yaml:"rate_limit"`
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

	// Parse switch time range
	// For MVP we will just default if parsing is complex, or implement simple parsing
	// Here we assume a simple implementation for now or default values
	cfg.Settings.SwitchMin = 60 * time.Second
	cfg.Settings.SwitchMax = 300 * time.Second

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

	return &cfg, nil
}
