package routing

import (
	"net"
	"strings"
)

// Policy represents the routing decision
type Policy string

const (
	Direct Policy = "direct"
	Proxy  Policy = "proxy"
	Block  Policy = "block"
)

// Rule defines a single routing condition and its target policy
type Rule struct {
	Type   string // "domain_suffix", "domain_keyword", "ip_cidr"
	Value  string
	Policy Policy

	// internal parsed values
	ipNet *net.IPNet
}

// Router evaluates target addresses against a set of rules
type Router struct {
	rules         []*Rule
	defaultPolicy Policy
}

// NewRouter creates a new router with the given rules and default policy
func NewRouter(rules []*Rule, defaultPolicy Policy) *Router {
	if defaultPolicy == "" {
		defaultPolicy = Proxy
	}

	// Pre-parse CIDR rules for efficiency
	for _, rule := range rules {
		if rule.Type == "ip_cidr" {
			_, ipNet, err := net.ParseCIDR(rule.Value)
			if err == nil {
				rule.ipNet = ipNet
			}
		}
	}

	return &Router{
		rules:         rules,
		defaultPolicy: defaultPolicy,
	}
}

// Route resolves the target routing policy for a given host (IP or domain)
// targetAddr can be "example.com:443" or "1.2.3.4:80" or just the hostname.
func (r *Router) Route(targetAddr string) Policy {
	host, _, err := net.SplitHostPort(targetAddr)
	if err != nil {
		host = targetAddr // assume it's just a hostname/IP
	}

	isIP := false
	ip := net.ParseIP(host)
	if ip != nil {
		isIP = true
	}

	for _, rule := range r.rules {
		switch rule.Type {
		case "domain_suffix":
			if !isIP && (host == rule.Value || strings.HasSuffix(host, "."+rule.Value)) {
				return rule.Policy
			}
		case "domain_keyword":
			if !isIP && strings.Contains(host, rule.Value) {
				return rule.Policy
			}
		case "ip_cidr":
			if isIP && rule.ipNet != nil && rule.ipNet.Contains(ip) {
				return rule.Policy
			}
		}
	}

	return r.defaultPolicy
}
