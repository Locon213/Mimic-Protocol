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

	domainsStr := q.Get("domains")
	var domains []string
	if domainsStr != "" {
		domains = strings.Split(domainsStr, ",")
	}

	transport := q.Get("transport")
	if transport == "" {
		transport = "mtp"
	}

	dns := q.Get("dns")
	serverName := u.Fragment

	cfg := &ClientConfig{
		Server:     server,
		ServerName: serverName,
		UUID:       uuid,
		Domains:    domains,
		Transport:  transport,
		DNS:        dns,
		Settings: ClientSettings{
			SwitchMin: 60 * time.Second,
			SwitchMax: 300 * time.Second,
		},
		Proxies: []ProxyConfig{
			{Type: "socks5", Port: 1080},
			{Type: "http", Port: 1081},
		},
		Routing: RoutingConfig{
			DefaultPolicy: "proxy",
		},
	}

	return cfg, nil
}

// GenerateMimicURL generates a mimic:// URI from connection parameters.
func GenerateMimicURL(uuid, serverAddr, serverName string, domains []string, transport, dns string) string {
	u := &url.URL{
		Scheme:   SchemeMimic,
		User:     url.User(uuid),
		Host:     serverAddr,
		Fragment: serverName,
	}

	q := u.Query()
	if len(domains) > 0 {
		q.Set("domains", strings.Join(domains, ","))
	}
	if transport != "" && transport != "mtp" {
		q.Set("transport", transport)
	}
	if dns != "" {
		q.Set("dns", dns)
	}

	u.RawQuery = q.Encode()
	return u.String()
}
