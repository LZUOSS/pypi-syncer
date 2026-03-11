package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ExtractClientIP extracts the real client IP from the request.
// It respects X-Forwarded-For and X-Real-IP headers when the
// direct connection is from a trusted proxy.
func ExtractClientIP(r *http.Request, trustedProxies []*net.IPNet) net.IP {
	remoteIP := parseAddrIP(r.RemoteAddr)
	if remoteIP == nil {
		return nil
	}

	if !isTrusted(remoteIP, trustedProxies) {
		return remoteIP
	}

	// Check X-Forwarded-For chain, walking rightmost-first.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		// Walk from right to left; stop at first non-trusted IP.
		for i := len(parts) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(parts[i]))
			if ip == nil {
				continue
			}
			if !isTrusted(ip, trustedProxies) {
				return ip
			}
		}
	}

	// Fall back to X-Real-IP.
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := net.ParseIP(strings.TrimSpace(xri)); ip != nil {
			return ip
		}
	}

	return remoteIP
}

// ParseTrustedProxies parses a list of CIDR strings.
func ParseTrustedProxies(cidrs []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", cidr, err)
		}
		nets = append(nets, network)
	}
	return nets, nil
}

func parseAddrIP(addr string) net.IP {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Maybe addr is just an IP without port.
		return net.ParseIP(addr)
	}
	return net.ParseIP(host)
}

func isTrusted(ip net.IP, trusted []*net.IPNet) bool {
	for _, n := range trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
