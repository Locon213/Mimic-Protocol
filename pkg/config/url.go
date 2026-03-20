package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

const SchemeMimic = "mimic"

// ParseMimicURL parses a mimic:// URI into a ClientConfig.
// Format: mimic://<uuid>@<server_ip>:<port>?domains=d1,d2&transport=mtp&dns=8.8.8.8:53
// Extended format: mimic://<uuid>@<server_ip>:<port>?domains=d1:preset1,d2:preset2&transport=mtp&dns=8.8.8.8:53&compression=true&compression_level=2&switch_time=60s-300s&randomize=true
func ParseMimicURL(uri string) (*ClientConfig, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid mimic URL: %w", err)
	}

	if u.Scheme != SchemeMimic {
		return nil, fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}

	uuid := u.User.Username()
	if uuid == "" {
		return nil, fmt.Errorf("missing UUID in URL")
	}

	server := u.Host
	if server == "" {
		return nil, fmt.Errorf("missing server address in URL")
	}

	q := u.Query()

	// Parse domains with optional presets (format: domain:preset)
	domainsStr := q.Get("domains")
	var domains []DomainEntry
	if domainsStr != "" {
		domainEntries := strings.Split(domainsStr, ",")
		domains = make([]DomainEntry, len(domainEntries))
		for i, entry := range domainEntries {
			parts := strings.SplitN(entry, ":", 2)
			domain := parts[0]
			preset := ""
			if len(parts) > 1 {
				preset = parts[1]
			}
			domains[i] = DomainEntry{Domain: domain, Preset: preset}
		}
	}

	transport := q.Get("transport")
	if transport == "" {
		transport = "mtp"
	}

	dns := q.Get("dns")
	serverName := u.Fragment

	// Parse settings
	settings := ClientSettings{
		SwitchMin: 60 * time.Second,
		SwitchMax: 300 * time.Second,
	}
	if switchTime := q.Get("switch_time"); switchTime != "" {
		settings.SwitchMin, settings.SwitchMax = parseSwitchTime(switchTime)
	}
	if randomize := q.Get("randomize"); randomize == "true" {
		settings.Randomize = true
	}

	// Parse compression
	compression := DefaultCompressionConfig()
	if compEnable := q.Get("compression"); compEnable == "true" {
		compression.Enable = true
	}
	if compLevel := q.Get("compression_level"); compLevel != "" {
		level := 2
		fmt.Sscanf(compLevel, "%d", &level)
		if level >= 1 && level <= 3 {
			compression.Level = level
		}
	}
	if compMinSize := q.Get("compression_min_size"); compMinSize != "" {
		minSize := 64
		fmt.Sscanf(compMinSize, "%d", &minSize)
		if minSize > 0 {
			compression.MinSize = minSize
		}
	}

	// Parse Android settings
	android := AndroidConfig{
		MTU: 1500,
	}
	if enableTUN := q.Get("android_tun"); enableTUN == "true" {
		android.EnableTUN = true
	}
	if useProtected := q.Get("android_protected"); useProtected == "true" {
		android.UseProtectedSockets = true
	}
	if mtu := q.Get("android_mtu"); mtu != "" {
		mtuVal := 1500
		fmt.Sscanf(mtu, "%d", &mtuVal)
		if mtuVal > 0 {
			android.MTU = mtuVal
		}
	}

	// Parse routing
	routing := RoutingConfig{
		DefaultPolicy: "proxy",
	}
	if defaultPolicy := q.Get("routing_policy"); defaultPolicy != "" {
		routing.DefaultPolicy = defaultPolicy
	}

	cfg := &ClientConfig{
		Server:      server,
		ServerName:  serverName,
		UUID:        uuid,
		Domains:     domains,
		Transport:   transport,
		DNS:         dns,
		Settings:    settings,
		Compression: compression,
		Android:     android,
		Routing:     routing,
		Proxies: []ProxyConfig{
			{Type: "socks5", Port: 1080},
			{Type: "http", Port: 1081},
		},
	}

	return cfg, nil
}

// GenerateMimicURL generates a mimic:// URI from full ClientConfig.
func GenerateMimicURL(cfg *ClientConfig) string {
	u := &url.URL{
		Scheme:   SchemeMimic,
		User:     url.User(cfg.UUID),
		Host:     cfg.Server,
		Fragment: cfg.ServerName,
	}

	q := u.Query()

	// Encode domains with presets (format: domain:preset)
	if len(cfg.Domains) > 0 {
		domainEntries := make([]string, len(cfg.Domains))
		for i, d := range cfg.Domains {
			if d.Preset != "" {
				domainEntries[i] = d.Domain + ":" + d.Preset
			} else {
				domainEntries[i] = d.Domain
			}
		}
		q.Set("domains", strings.Join(domainEntries, ","))
	}

	// Transport
	if cfg.Transport != "" && cfg.Transport != "mtp" {
		q.Set("transport", cfg.Transport)
	}

	// DNS
	if cfg.DNS != "" {
		q.Set("dns", cfg.DNS)
	}

	// Settings
	if cfg.Settings.SwitchTimeRangeStr != "" {
		q.Set("switch_time", cfg.Settings.SwitchTimeRangeStr)
	} else if cfg.Settings.SwitchMin > 0 || cfg.Settings.SwitchMax > 0 {
		q.Set("switch_time", fmt.Sprintf("%s-%s", cfg.Settings.SwitchMin, cfg.Settings.SwitchMax))
	}
	if cfg.Settings.Randomize {
		q.Set("randomize", "true")
	}

	// Compression
	if cfg.Compression.Enable {
		q.Set("compression", "true")
		if cfg.Compression.Level != 2 {
			q.Set("compression_level", fmt.Sprintf("%d", cfg.Compression.Level))
		}
		if cfg.Compression.MinSize != 64 {
			q.Set("compression_min_size", fmt.Sprintf("%d", cfg.Compression.MinSize))
		}
	}

	// Android
	if cfg.Android.EnableTUN {
		q.Set("android_tun", "true")
	}
	if cfg.Android.UseProtectedSockets {
		q.Set("android_protected", "true")
	}
	if cfg.Android.MTU != 1500 {
		q.Set("android_mtu", fmt.Sprintf("%d", cfg.Android.MTU))
	}

	// Routing
	if cfg.Routing.DefaultPolicy != "" && cfg.Routing.DefaultPolicy != "proxy" {
		q.Set("routing_policy", cfg.Routing.DefaultPolicy)
	}

	u.RawQuery = q.Encode()
	return u.String()
}
