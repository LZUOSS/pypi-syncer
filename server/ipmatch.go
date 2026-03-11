package server

import (
	"fmt"
	"net"

	"github.com/kexi/pypi-mirror/config"
)

type ipRule struct {
	network *net.IPNet
	mode    string
}

// IPMatcher maps IP addresses to modes ("302" or "proxy").
type IPMatcher struct {
	rules       []ipRule
	defaultMode string
}

// NewIPMatcher creates an IPMatcher from config.
func NewIPMatcher(cfg config.IPModesConfig) (*IPMatcher, error) {
	m := &IPMatcher{
		defaultMode: cfg.Default,
	}
	for _, r := range cfg.Rules {
		_, network, err := net.ParseCIDR(r.CIDR)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", r.CIDR, err)
		}
		m.rules = append(m.rules, ipRule{
			network: network,
			mode:    r.Mode,
		})
	}
	return m, nil
}

// Match returns the mode for the given IP.
func (m *IPMatcher) Match(ip net.IP) string {
	for _, r := range m.rules {
		if r.network.Contains(ip) {
			return r.mode
		}
	}
	return m.defaultMode
}
