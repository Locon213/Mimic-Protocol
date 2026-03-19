package presets

import (
	"fmt"
	"strings"
	"time"
)

// PresetType defines the category of the preset
type PresetType string

const (
	TypeSocial    PresetType = "social"
	TypeVideo     PresetType = "video"
	TypeMessenger PresetType = "messenger"
	TypeWeb       PresetType = "web"
	TypeGaming    PresetType = "gaming"
	TypeVoIP      PresetType = "voip"
	TypeCustom    PresetType = "custom"
)

// RangeInt represents a min-max integer range
type RangeInt struct {
	Min int `yaml:"min" json:"min"`
	Max int `yaml:"max" json:"max"`
}

// RangeDuration represents a min-max duration range
type RangeDuration struct {
	Min time.Duration `yaml:"min" json:"min"`
	Max time.Duration `yaml:"max" json:"max"`
}

// TrafficPattern defines a specific behavior within a preset (e.g., "burst")
type TrafficPattern struct {
	Type     string        `yaml:"type" json:"type"`         // e.g., "burst", "constant", "idle"
	Duration RangeDuration `yaml:"duration" json:"duration"` // How long this pattern lasts
	Interval RangeDuration `yaml:"interval" json:"interval"` // Time between repetitions
}

// Preset defines a complete behavior profile for mimicking a service
type Preset struct {
	Name                string           `yaml:"name" json:"name"`
	Type                PresetType       `yaml:"type" json:"type"`
	PacketSize          RangeInt         `yaml:"packet_size" json:"packet_size"`                     // Packet size in bytes
	PacketsPerSecond    RangeInt         `yaml:"packets_per_sec" json:"packets_per_sec"`             // PPS rate
	UploadDownloadRatio float64          `yaml:"upload_download_ratio" json:"upload_download_ratio"` // e.g. 0.5 for 1:2
	SessionDuration     RangeDuration    `yaml:"session_duration" json:"session_duration"`           // Time before switching
	Patterns            []TrafficPattern `yaml:"patterns,omitempty" json:"patterns,omitempty"`       // Specific traffic patterns
}

// DefaultPresets returns the built-in optimized presets
func DefaultPresets() map[string]Preset {
	return map[string]Preset{
		// Optimized for modern web browsing - FAST!
		"web_generic": {
			Name:                "web_generic",
			Type:                TypeWeb,
			PacketSize:          RangeInt{Min: 500, Max: 1420}, // Larger packets for modern HTTP/2
			PacketsPerSecond:    RangeInt{Min: 10, Max: 150},   // Higher PPS for fast loading
			UploadDownloadRatio: 0.1,                           // Mostly download (web pages, images)
			SessionDuration:     RangeDuration{Min: 30 * time.Second, Max: 300 * time.Second},
			Patterns: []TrafficPattern{
				{
					Type:     "burst",
					Duration: RangeDuration{Min: 1 * time.Second, Max: 5 * time.Second},
					Interval: RangeDuration{Min: 2 * time.Second, Max: 10 * time.Second},
				},
			},
		},

		// Social media traffic (VK, Instagram, Facebook, Twitter)
		"social": {
			Name:                "social",
			Type:                TypeSocial,
			PacketSize:          RangeInt{Min: 500, Max: 1400},
			PacketsPerSecond:    RangeInt{Min: 5, Max: 50},
			UploadDownloadRatio: 0.3, // More download (images, videos) than upload
			SessionDuration:     RangeDuration{Min: 60 * time.Second, Max: 300 * time.Second},
			Patterns: []TrafficPattern{
				{
					Type:     "burst",
					Duration: RangeDuration{Min: 3 * time.Second, Max: 10 * time.Second},
					Interval: RangeDuration{Min: 15 * time.Second, Max: 60 * time.Second},
				},
			},
		},

		// Video streaming (YouTube, Twitch, Netflix)
		"video": {
			Name:                "video",
			Type:                TypeVideo,
			PacketSize:          RangeInt{Min: 1000, Max: 1450}, // Max MTU for video chunks
			PacketsPerSecond:    RangeInt{Min: 50, Max: 200},    // Very high PPS for HD/4K
			UploadDownloadRatio: 0.05,                           // Almost all download
			SessionDuration:     RangeDuration{Min: 300 * time.Second, Max: 3600 * time.Second},
			Patterns: []TrafficPattern{
				{
					Type:     "constant",
					Duration: RangeDuration{Min: 60 * time.Second, Max: 300 * time.Second},
					Interval: RangeDuration{Min: 5 * time.Second, Max: 30 * time.Second},
				},
			},
		},

		// Messengers (Telegram, WhatsApp, Signal)
		"messenger": {
			Name:                "messenger",
			Type:                TypeMessenger,
			PacketSize:          RangeInt{Min: 50, Max: 400}, // Small messages
			PacketsPerSecond:    RangeInt{Min: 1, Max: 10},   // Low PPS, keep-alive
			UploadDownloadRatio: 1.0,                         // Symmetric (chat messages)
			SessionDuration:     RangeDuration{Min: 300 * time.Second, Max: 3600 * time.Second},
			Patterns: []TrafficPattern{
				{
					Type:     "idle",
					Duration: RangeDuration{Min: 30 * time.Second, Max: 120 * time.Second},
					Interval: RangeDuration{Min: 5 * time.Second, Max: 30 * time.Second},
				},
			},
		},

		// Gaming preset - low latency, small packets, high PPS
		// Optimized for: CS2, Dota 2, Valorant, Apex Legends
		"gaming": {
			Name:                "gaming",
			Type:                TypeGaming,
			PacketSize:          RangeInt{Min: 64, Max: 512}, // Small packets for low latency
			PacketsPerSecond:    RangeInt{Min: 30, Max: 120}, // High PPS for real-time updates
			UploadDownloadRatio: 0.8,                         // Nearly symmetric (game state sync)
			SessionDuration:     RangeDuration{Min: 600 * time.Second, Max: 7200 * time.Second},
			Patterns: []TrafficPattern{
				{
					Type:     "constant",
					Duration: RangeDuration{Min: 10 * time.Second, Max: 60 * time.Second},
					Interval: RangeDuration{Min: 1 * time.Second, Max: 5 * time.Second},
				},
			},
		},

		// VoIP preset - WebRTC/Discord/Zoom traffic simulation
		// Optimized for: Discord, Zoom, Skype, Google Meet
		"voip": {
			Name:                "voip",
			Type:                TypeVoIP,
			PacketSize:          RangeInt{Min: 80, Max: 300}, // Typical VoIP packet size
			PacketsPerSecond:    RangeInt{Min: 20, Max: 50},  // 20-50 PPS (typical for Opus codec)
			UploadDownloadRatio: 1.0,                         // Symmetric (bidirectional audio/video)
			SessionDuration:     RangeDuration{Min: 300 * time.Second, Max: 7200 * time.Second},
			Patterns: []TrafficPattern{
				{
					Type:     "constant",
					Duration: RangeDuration{Min: 30 * time.Second, Max: 300 * time.Second},
					Interval: RangeDuration{Min: 5 * time.Second, Max: 30 * time.Second},
				},
			},
		},
	}
}

// CustomPresetRegistry holds user-defined custom presets
type CustomPresetRegistry struct {
	presets map[string]Preset
}

// NewCustomPresetRegistry creates a new custom preset registry
func NewCustomPresetRegistry() *CustomPresetRegistry {
	return &CustomPresetRegistry{
		presets: make(map[string]Preset),
	}
}

// AddPreset adds a custom preset to the registry
func (r *CustomPresetRegistry) AddPreset(name string, preset Preset) {
	preset.Name = name
	preset.Type = TypeCustom
	r.presets[name] = preset
}

// GetPreset returns a preset by name from custom registry
func (r *CustomPresetRegistry) GetPreset(name string) (Preset, bool) {
	preset, ok := r.presets[name]
	return preset, ok
}

// GetAllPresets returns all custom presets
func (r *CustomPresetRegistry) GetAllPresets() map[string]Preset {
	result := make(map[string]Preset, len(r.presets))
	for k, v := range r.presets {
		result[k] = v
	}
	return result
}

// DetectPreset finds the best preset for a domain
// Priority: 1) Custom presets by domain, 2) Default presets by domain, 3) web_generic
func DetectPreset(domain string, customRegistry *CustomPresetRegistry) Preset {
	defaults := DefaultPresets()

	// Normalize domain
	domain = strings.ToLower(domain)
	domain = strings.TrimPrefix(domain, "www.")

	// Step 1: Check custom presets for exact domain match
	if customRegistry != nil {
		if preset, ok := customRegistry.GetPreset(domain); ok {
			return preset
		}

		// Check custom presets for keyword match
		for name, preset := range customRegistry.GetAllPresets() {
			if strings.Contains(domain, name) {
				return preset
			}
		}
	}

	// Step 2: Check default presets by domain mapping
	switch {
	// Social media
	case containsAny(domain, []string{"vk.com", "instagram.com", "facebook.com", "twitter.com", "tiktok.com", "reddit.com"}):
		return defaults["social"]

	// Video streaming
	case containsAny(domain, []string{"youtube.com", "youtu.be", "twitch.tv", "netflix.com", "rutube.ru", "vimeo.com"}):
		return defaults["video"]

	// Messengers
	case containsAny(domain, []string{"telegram.org", "telegram.me", "whatsapp.com", "signal.org", "viber.com"}):
		return defaults["messenger"]

	// Gaming platforms
	case containsAny(domain, []string{"steampowered.com", "steamcommunity.com", "epicgames.com", "origin.com", "xbox.com", "playstation.com"}):
		return defaults["gaming"]

	// VoIP services
	case containsAny(domain, []string{"discord.com", "discord.gg", "zoom.us", "skype.com", "meet.google.com", "teams.microsoft.com"}):
		return defaults["voip"]
	}

	// Step 3: Default to optimized web_generic for all other domains
	return defaults["web_generic"]
}

// DetectPresetByService detects preset by service name
func DetectPresetByService(serviceName string, customRegistry *CustomPresetRegistry) Preset {
	defaults := DefaultPresets()
	serviceName = strings.ToLower(serviceName)

	// Check custom presets first
	if customRegistry != nil {
		if preset, ok := customRegistry.GetPreset(serviceName); ok {
			return preset
		}
	}

	// Check default presets
	if preset, ok := defaults[serviceName]; ok {
		return preset
	}

	// Fallback to web_generic
	return defaults["web_generic"]
}

// containsAny checks if domain contains any of the substrings
func containsAny(domain string, substrings []string) bool {
	for _, sub := range substrings {
		if strings.Contains(domain, sub) {
			return true
		}
	}
	return false
}

// ValidatePreset validates a preset configuration
func ValidatePreset(preset Preset) error {
	if preset.PacketSize.Min <= 0 || preset.PacketSize.Max <= 0 {
		return fmt.Errorf("invalid packet_size: min and max must be positive")
	}
	if preset.PacketSize.Min > preset.PacketSize.Max {
		return fmt.Errorf("invalid packet_size: min cannot be greater than max")
	}
	if preset.PacketsPerSecond.Min < 0 || preset.PacketsPerSecond.Max < 0 {
		return fmt.Errorf("invalid packets_per_sec: values cannot be negative")
	}
	if preset.PacketsPerSecond.Min > preset.PacketsPerSecond.Max {
		return fmt.Errorf("invalid packets_per_sec: min cannot be greater than max")
	}
	if preset.UploadDownloadRatio < 0 || preset.UploadDownloadRatio > 1 {
		return fmt.Errorf("invalid upload_download_ratio: must be between 0 and 1")
	}
	if preset.SessionDuration.Min < 0 || preset.SessionDuration.Max < 0 {
		return fmt.Errorf("invalid session_duration: values cannot be negative")
	}
	if preset.SessionDuration.Min > preset.SessionDuration.Max {
		return fmt.Errorf("invalid session_duration: min cannot be greater than max")
	}
	return nil
}

// GetOptimalPacketSize returns optimal packet size for a given bandwidth
func GetOptimalPacketSize(bandwidthMbps float64) RangeInt {
	// Higher bandwidth = larger packets for efficiency
	if bandwidthMbps >= 100 {
		return RangeInt{Min: 1200, Max: 1450}
	} else if bandwidthMbps >= 50 {
		return RangeInt{Min: 900, Max: 1350}
	} else if bandwidthMbps >= 10 {
		return RangeInt{Min: 600, Max: 1200}
	} else {
		return RangeInt{Min: 400, Max: 900}
	}
}

// GetOptimalPPS returns optimal packets per second for a given latency
func GetOptimalPPS(latencyMs int) RangeInt {
	// Lower latency = higher PPS for responsiveness
	if latencyMs <= 20 {
		return RangeInt{Min: 50, Max: 200}
	} else if latencyMs <= 50 {
		return RangeInt{Min: 30, Max: 120}
	} else if latencyMs <= 100 {
		return RangeInt{Min: 20, Max: 80}
	} else {
		return RangeInt{Min: 10, Max: 50}
	}
}

func (r RangeInt) String() string {
	return fmt.Sprintf("%d-%d", r.Min, r.Max)
}

func (r RangeDuration) String() string {
	return fmt.Sprintf("%v-%v", r.Min, r.Max)
}
