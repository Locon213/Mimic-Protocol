package presets

import (
	"fmt"
	"time"
)

// PresetType defines the category of the preset
type PresetType string

const (
	TypeSocial    PresetType = "social"
	TypeVideo     PresetType = "video"
	TypeMessenger PresetType = "messenger"
	TypeWeb       PresetType = "web"
)

// RangeInt represents a min-max integer range
type RangeInt struct {
	Min int `yaml:"min"`
	Max int `yaml:"max"`
}

// RangeDuration represents a min-max duration range
type RangeDuration struct {
	Min time.Duration `yaml:"min"`
	Max time.Duration `yaml:"max"`
}

// TrafficPattern defines a specific behavior within a preset (e.g., "burst")
type TrafficPattern struct {
	Type     string        `yaml:"type"`     // e.g., "burst", "constant", "idle"
	Duration RangeDuration `yaml:"duration"` // How long this pattern lasts
	Interval RangeDuration `yaml:"interval"` // Time between repetitions
}

// Preset defines a complete behavior profile for mimicking a service
type Preset struct {
	Name                string           `yaml:"name"`
	Type                PresetType       `yaml:"type"`
	PacketSize          RangeInt         `yaml:"packet_size"`           // Packet size in bytes
	PacketsPerSecond    RangeInt         `yaml:"packets_per_sec"`       // PPS rate
	UploadDownloadRatio float64          `yaml:"upload_download_ratio"` // e.g. 0.5 for 1:2
	SessionDuration     RangeDuration    `yaml:"session_duration"`      // Time before switching
	Patterns            []TrafficPattern `yaml:"patterns,omitempty"`    // Specific traffic patterns
}

// DefaultPresets returns the built-in presets
func DefaultPresets() map[string]Preset {
	return map[string]Preset{
		"social": {
			Name:                "social",
			Type:                TypeSocial,
			PacketSize:          RangeInt{Min: 500, Max: 1400},
			PacketsPerSecond:    RangeInt{Min: 1, Max: 10},
			UploadDownloadRatio: 0.3, // More download than upload
			SessionDuration:     RangeDuration{Min: 60 * time.Second, Max: 300 * time.Second},
			Patterns: []TrafficPattern{
				{
					Type:     "burst",
					Duration: RangeDuration{Min: 5 * time.Second, Max: 15 * time.Second},
					Interval: RangeDuration{Min: 30 * time.Second, Max: 120 * time.Second},
				},
			},
		},
		"video": {
			Name:                "video",
			Type:                TypeVideo,
			PacketSize:          RangeInt{Min: 1000, Max: 1450}, // Larger packets
			PacketsPerSecond:    RangeInt{Min: 30, Max: 100},    // High PPS
			UploadDownloadRatio: 0.05,                           // Mostly download
			SessionDuration:     RangeDuration{Min: 300 * time.Second, Max: 1200 * time.Second},
		},
		"messenger": {
			Name:                "messenger",
			Type:                TypeMessenger,
			PacketSize:          RangeInt{Min: 50, Max: 400}, // Small packets
			PacketsPerSecond:    RangeInt{Min: 0, Max: 5},    // Low PPS, keep-alive
			UploadDownloadRatio: 1.0,                         // Symmetric
			SessionDuration:     RangeDuration{Min: 600 * time.Second, Max: 3600 * time.Second},
		},
	}
}

// DetectPreset attempts to guess the best preset based on the domain name
func DetectPreset(domain string) (Preset, error) {
	defaults := DefaultPresets()

	// Simple mapping logic (can be improved with regex or map lookup)
	switch domain {
	case "vk.com", "instagram.com", "facebook.com", "twitter.com":
		return defaults["social"], nil
	case "rutube.ru", "youtube.com", "twitch.tv":
		return defaults["video"], nil
	case "telegram.org", "whatsapp.com":
		return defaults["messenger"], nil
	default:
		// Default to generic web browsing behavior
		return Preset{
			Name:                "web_generic",
			Type:                TypeWeb,
			PacketSize:          RangeInt{Min: 300, Max: 1200},
			PacketsPerSecond:    RangeInt{Min: 1, Max: 20},
			UploadDownloadRatio: 0.2,
			SessionDuration:     RangeDuration{Min: 60 * time.Second, Max: 180 * time.Second},
		}, nil
	}
}

func (r RangeInt) String() string {
	return fmt.Sprintf("%d-%d", r.Min, r.Max)
}
