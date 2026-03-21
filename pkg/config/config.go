package config

import (
	"os"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// DomainEntry represents a domain with optional preset
type DomainEntry struct {
	Domain string `yaml:"domain" json:"domain"` // Domain name
	Preset string `yaml:"preset" json:"preset"` // Preset name (optional, empty = auto-detect)
}

// ClientConfig represents the client configuration
type ClientConfig struct {
	Server        string                        // Server address (IP:PORT)
	UUID          string                        // Unique authorization identifier
	Domains       []DomainEntry                 // List of domains for masking (with optional preset)
	Transport     string                        // Transport type: "mtp", "tcp", "ws", "wss"
	Proxies       []ProxyConfig                 // Local proxy server settings
	DNS           string                        // Custom DNS server
	Settings      ClientSettings                // Fine-tuning settings
	Routing       RoutingConfig                 // Routing rules
	ServerName    string                        // Server name from URL
	LocalPort     int                           // Local proxy port (for backward compatibility)
	Android       AndroidConfig                 // Android-specific configuration
	Compression   CompressionConfig             // Data compression settings
	CustomPresets map[string]CustomPresetConfig // Custom traffic presets
	WebSocket     WebSocketConfig               // WebSocket-specific configuration
	Buffer        BufferConfig                  // Buffer optimization settings
}

// CustomPresetConfig represents a custom traffic preset configuration
type CustomPresetConfig struct {
	Name                string           `yaml:"name"`
	Type                string           `yaml:"type"`
	PacketSize          RangeInt         `yaml:"packet_size"`
	PacketsPerSecond    RangeInt         `yaml:"packets_per_sec"`
	UploadDownloadRatio float64          `yaml:"upload_download_ratio"`
	SessionDuration     string           `yaml:"session_duration"`
	Patterns            []TrafficPattern `yaml:"patterns,omitempty"`
}

// RangeInt represents a min-max integer range (shared with presets package)
type RangeInt struct {
	Min int `yaml:"min"`
	Max int `yaml:"max"`
}

// TrafficPattern represents a traffic pattern (shared with presets package)
type TrafficPattern struct {
	Type     string `yaml:"type"`
	Duration string `yaml:"duration"`
	Interval string `yaml:"interval"`
}

// AndroidConfig represents Android-specific configuration
type AndroidConfig struct {
	// EnableTUN enables Android TUN backend (requires VpnService)
	EnableTUN bool
	// TUNFD is the TUN file descriptor from VpnService (optional, set at runtime)
	TUNFD int
	// MTU for TUN interface (default: 1500)
	MTU int
	// UseProtectedSockets enables socket protection via VpnService.protect()
	UseProtectedSockets bool
}

// CompressionConfig represents data compression settings
type CompressionConfig struct {
	// Enable enables/disables compression (default: false)
	Enable bool `yaml:"enable"`
	// Level is the compression level (1-3): 1=Fastest, 2=Default, 3=Better
	Level int `yaml:"level"`
	// MinSize is the minimum size to attempt compression (default: 64 bytes)
	MinSize int `yaml:"min_size"`
}

// DefaultCompressionConfig returns default compression settings
func DefaultCompressionConfig() CompressionConfig {
	return CompressionConfig{
		Enable:  false, // Disabled by default for performance
		Level:   2,     // Default compression level
		MinSize: 64,    // Don't compress small packets
	}
}

// BufferConfig represents buffer optimization settings
type BufferConfig struct {
	// RelayBufferSize is the buffer size for relay operations in bytes (default: 131072 = 128KB)
	RelayBufferSize int `yaml:"relay_buffer_size"`
	// ReadBufferSize is the buffer size for reading operations in bytes (default: 65536 = 64KB)
	ReadBufferSize int `yaml:"read_buffer_size"`
	// EnableOptimizedBuffers enables optimized buffer sizes for high-speed networks (default: true)
	EnableOptimizedBuffers bool `yaml:"enable_optimized_buffers"`
}

// DefaultBufferConfig returns default buffer settings
func DefaultBufferConfig() BufferConfig {
	return BufferConfig{
		RelayBufferSize:        4 * 1024 * 1024, // 4MB - optimized for high-speed networks
		ReadBufferSize:         1 * 1024 * 1024, // 1MB - optimized for high-speed networks
		EnableOptimizedBuffers: true,            // Enabled by default
	}
}

// WebSocketConfig represents WebSocket-specific configuration
type WebSocketConfig struct {
	// Path is the URL path for WebSocket endpoint (default: "/ws")
	Path string `yaml:"path"`
	// Host is the Host header value for masquerading (optional)
	Host string `yaml:"host"`
	// TLS enables WSS (WebSocket over TLS) (default: false)
	TLS bool `yaml:"tls"`
}

// DefaultWebSocketConfig returns default WebSocket settings
func DefaultWebSocketConfig() WebSocketConfig {
	return WebSocketConfig{
		Path: "/ws",
		Host: "",
		TLS:  false,
	}
}

// ServerConfig represents the server configuration
type ServerConfig struct {
	Port        int               // Listening port
	MaxClients  int               // Maximum number of clients
	UUID        string            // Server UUID for authentication
	DomainList  []DomainEntry     // List of domains for masking (with optional preset)
	Transport   string            // Transport type: "mtp", "tcp", "ws", "wss"
	DNS         string            // DNS server
	Name        string            // Server name
	Compression CompressionConfig // Data compression settings
	WebSocket   WebSocketConfig   // WebSocket-specific configuration
	Buffer      BufferConfig      // Buffer optimization settings
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
	Server    string `yaml:"server"`
	UUID      string `yaml:"uuid"`
	Transport string `yaml:"transport"`
	LocalPort int    `yaml:"local_port"`
	DNS       string `yaml:"dns"`
	Domains   []struct {
		Domain string `yaml:"domain"`
		Preset string `yaml:"preset,omitempty"`
	} `yaml:"domains"`
	Proxies []struct {
		Type string `yaml:"type"`
		Port int    `yaml:"port"`
	} `yaml:"proxies"`
	Settings struct {
		SwitchTime string `yaml:"switch_time"`
		Randomize  bool   `yaml:"randomize"`
	} `yaml:"settings"`
	Routing struct {
		DefaultPolicy string `yaml:"default_policy"`
		Rules         []struct {
			Type   string `yaml:"type"`
			Value  string `yaml:"value"`
			Policy string `yaml:"policy"`
		} `yaml:"rules"`
	} `yaml:"routing"`
	Android struct {
		EnableTUN           bool `yaml:"enable_tun"`
		UseProtectedSockets bool `yaml:"use_protected_sockets"`
		MTU                 int  `yaml:"mtu"`
	} `yaml:"android"`
	Compression struct {
		Enable  bool `yaml:"enable"`
		Level   int  `yaml:"level"`
		MinSize int  `yaml:"min_size"`
	} `yaml:"compression"`
	WebSocket struct {
		Path string `yaml:"path"`
		Host string `yaml:"host"`
		TLS  bool   `yaml:"tls"`
	} `yaml:"websocket"`
	Buffer struct {
		RelayBufferSize        int  `yaml:"relay_buffer_size"`
		ReadBufferSize         int  `yaml:"read_buffer_size"`
		EnableOptimizedBuffers bool `yaml:"enable_optimized_buffers"`
	} `yaml:"buffer"`
	CustomPresets map[string]struct {
		Name                string  `yaml:"name"`
		Type                string  `yaml:"type"`
		PacketSizeMin       int     `yaml:"packet_size_min"`
		PacketSizeMax       int     `yaml:"packet_size_max"`
		PacketsPerSecondMin int     `yaml:"packets_per_sec_min"`
		PacketsPerSecondMax int     `yaml:"packets_per_sec_max"`
		UploadDownloadRatio float64 `yaml:"upload_download_ratio"`
		SessionDuration     string  `yaml:"session_duration"`
		Patterns            []struct {
			Type     string `yaml:"type"`
			Duration string `yaml:"duration"`
			Interval string `yaml:"interval"`
		} `yaml:"patterns,omitempty"`
	} `yaml:"custom_presets,omitempty"`
}

// serverYAMLConfig represents the raw YAML server config structure
type serverYAMLConfig struct {
	Port       int    `yaml:"port"`
	UUID       string `yaml:"uuid"`
	Transport  string `yaml:"transport"`
	DNS        string `yaml:"dns"`
	MaxClients int    `yaml:"max_clients"`
	DomainList []struct {
		Domain string `yaml:"domain"`
		Preset string `yaml:"preset,omitempty"`
	} `yaml:"domain_list"`
	Name        string `yaml:"name"`
	Compression struct {
		Enable  bool `yaml:"enable"`
		Level   int  `yaml:"level"`
		MinSize int  `yaml:"min_size"`
	} `yaml:"compression"`
	WebSocket struct {
		Path string `yaml:"path"`
		Host string `yaml:"host"`
		TLS  bool   `yaml:"tls"`
	} `yaml:"websocket"`
	Buffer struct {
		RelayBufferSize        int  `yaml:"relay_buffer_size"`
		ReadBufferSize         int  `yaml:"read_buffer_size"`
		EnableOptimizedBuffers bool `yaml:"enable_optimized_buffers"`
	} `yaml:"buffer"`
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
		Domains:   make([]DomainEntry, len(yamlCfg.Domains)),
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

	// Convert domains from YAML format
	for i, d := range yamlCfg.Domains {
		cfg.Domains[i] = DomainEntry{
			Domain: d.Domain,
			Preset: d.Preset,
		}
	}

	// Load proxies из конфига (если указаны)
	if len(yamlCfg.Proxies) > 0 {
		cfg.Proxies = make([]ProxyConfig, len(yamlCfg.Proxies))
		for i, p := range yamlCfg.Proxies {
			cfg.Proxies[i] = ProxyConfig{
				Type: p.Type,
				Port: p.Port,
			}
		}
	}

	// Load routing из конфига (если указан)
	if yamlCfg.Routing.DefaultPolicy != "" {
		cfg.Routing.DefaultPolicy = yamlCfg.Routing.DefaultPolicy
	}
	if len(yamlCfg.Routing.Rules) > 0 {
		cfg.Routing.Rules = make([]RoutingRule, len(yamlCfg.Routing.Rules))
		for i, rule := range yamlCfg.Routing.Rules {
			cfg.Routing.Rules[i] = RoutingRule{
				Type:   rule.Type,
				Value:  rule.Value,
				Policy: rule.Policy,
			}
		}
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

	// Load Android config
	cfg.Android = AndroidConfig{
		EnableTUN:           yamlCfg.Android.EnableTUN,
		UseProtectedSockets: yamlCfg.Android.UseProtectedSockets,
		MTU:                 yamlCfg.Android.MTU,
	}

	// Load compression config
	cfg.Compression = DefaultCompressionConfig()
	if yamlCfg.Compression.Level > 0 {
		cfg.Compression.Level = yamlCfg.Compression.Level
	}
	if yamlCfg.Compression.MinSize > 0 {
		cfg.Compression.MinSize = yamlCfg.Compression.MinSize
	}
	cfg.Compression.Enable = yamlCfg.Compression.Enable

	// Load buffer config
	cfg.Buffer = DefaultBufferConfig()
	if yamlCfg.Buffer.RelayBufferSize > 0 {
		cfg.Buffer.RelayBufferSize = yamlCfg.Buffer.RelayBufferSize
	}
	if yamlCfg.Buffer.ReadBufferSize > 0 {
		cfg.Buffer.ReadBufferSize = yamlCfg.Buffer.ReadBufferSize
	}
	cfg.Buffer.EnableOptimizedBuffers = yamlCfg.Buffer.EnableOptimizedBuffers

	// Load WebSocket config
	cfg.WebSocket = DefaultWebSocketConfig()
	if yamlCfg.WebSocket.Path != "" {
		cfg.WebSocket.Path = yamlCfg.WebSocket.Path
	}
	if yamlCfg.WebSocket.Host != "" {
		cfg.WebSocket.Host = yamlCfg.WebSocket.Host
	}
	cfg.WebSocket.TLS = yamlCfg.WebSocket.TLS

	// Load custom presets
	if len(yamlCfg.CustomPresets) > 0 {
		cfg.CustomPresets = make(map[string]CustomPresetConfig)
		for name, preset := range yamlCfg.CustomPresets {
			cfg.CustomPresets[name] = CustomPresetConfig{
				Name:                preset.Name,
				Type:                preset.Type,
				PacketSize:          RangeInt{Min: preset.PacketSizeMin, Max: preset.PacketSizeMax},
				PacketsPerSecond:    RangeInt{Min: preset.PacketsPerSecondMin, Max: preset.PacketsPerSecondMax},
				UploadDownloadRatio: preset.UploadDownloadRatio,
				SessionDuration:     preset.SessionDuration,
				Patterns:            make([]TrafficPattern, len(preset.Patterns)),
			}
			for i, p := range preset.Patterns {
				cfg.CustomPresets[name].Patterns[i] = TrafficPattern{
					Type:     p.Type,
					Duration: p.Duration,
					Interval: p.Interval,
				}
			}
		}
	}

	// Set default MTU
	if cfg.Android.MTU <= 0 {
		cfg.Android.MTU = 1500
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
		DomainList: make([]DomainEntry, len(yamlCfg.DomainList)),
		Name:       yamlCfg.Name,
	}

	// Load compression config
	cfg.Compression = DefaultCompressionConfig()
	if yamlCfg.Compression.Level > 0 {
		cfg.Compression.Level = yamlCfg.Compression.Level
	}
	if yamlCfg.Compression.MinSize > 0 {
		cfg.Compression.MinSize = yamlCfg.Compression.MinSize
	}
	cfg.Compression.Enable = yamlCfg.Compression.Enable

	// Load buffer config
	cfg.Buffer = DefaultBufferConfig()
	if yamlCfg.Buffer.RelayBufferSize > 0 {
		cfg.Buffer.RelayBufferSize = yamlCfg.Buffer.RelayBufferSize
	}
	if yamlCfg.Buffer.ReadBufferSize > 0 {
		cfg.Buffer.ReadBufferSize = yamlCfg.Buffer.ReadBufferSize
	}
	cfg.Buffer.EnableOptimizedBuffers = yamlCfg.Buffer.EnableOptimizedBuffers

	// Load WebSocket config
	cfg.WebSocket = DefaultWebSocketConfig()
	if yamlCfg.WebSocket.Path != "" {
		cfg.WebSocket.Path = yamlCfg.WebSocket.Path
	}
	if yamlCfg.WebSocket.Host != "" {
		cfg.WebSocket.Host = yamlCfg.WebSocket.Host
	}
	cfg.WebSocket.TLS = yamlCfg.WebSocket.TLS

	// Convert domain_list from YAML format
	for i, d := range yamlCfg.DomainList {
		cfg.DomainList[i] = DomainEntry{
			Domain: d.Domain,
			Preset: d.Preset,
		}
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
