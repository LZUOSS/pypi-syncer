package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// botUAs contains User-Agent substrings for bots whose downloads
// should not be recorded as votes.
var botUAs = []string{
	"bandersnatch",
	"Googlebot",
	"bingbot",
	"YandexBot",
	"Baiduspider",
}

func isBot(ua string) bool {
	for _, bot := range botUAs {
		if strings.Contains(ua, bot) {
			return true
		}
	}
	return false
}

// ServePackages handles /pypi/packages/{hash_prefix}/{hash}/{filename} requests.
func (s *Server) ServePackages(w http.ResponseWriter, r *http.Request) {
	// Extract path after /pypi/packages/
	pathPrefix := s.cfg.Prefix + "/packages/"
	pkgPath := strings.TrimPrefix(r.URL.Path, pathPrefix)
	if pkgPath == "" || strings.Contains(pkgPath, "..") || strings.HasPrefix(pkgPath, "/") {
		http.NotFound(w, r)
		return
	}
	// Validate path shape: {2-char-hex}/{hex-hash}/{filename}
	parts := strings.Split(pkgPath, "/")
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}

	// Async vote recording (non-blocking).
	ua := r.Header.Get("User-Agent")
	if !isBot(ua) {
		clientIP := ExtractClientIP(r, s.trustedProxies)
		ipStr := ""
		if clientIP != nil {
			ipStr = clientIP.String()
		}
		select {
		case s.voteCh <- voteRequest{
			FilePath:  pkgPath,
			IPAddress: ipStr,
			UserAgent: ua,
		}:
		default:
		}
	}

	for _, tier := range s.tiers {
		localPath := filepath.Join(tier.Path, pkgPath)
		if _, err := os.Stat(localPath); err == nil {
			http.ServeFile(w, r, localPath)
			return
		}
	}

	// File not cached locally; proxy or redirect to upstream.
	clientIP := ExtractClientIP(r, s.trustedProxies)
	mode := s.cfg.IPModes.Default
	if clientIP != nil {
		mode = s.ipMatcher.Match(clientIP)
	}

	if mode == "proxy" {
		proxyURL := strings.TrimSuffix(s.cfg.Upstream.PackagesURL, "/") + "/" + pkgPath
		upstreamTimeout := parseDuration(s.cfg.Timeouts.Upstream)
		if err := ReverseProxy(w, r, proxyURL, upstreamTimeout, s.upstreamClient); err != nil {
			http.Error(w, "upstream error", http.StatusBadGateway)
		}
	} else {
		redirectBase := s.cfg.Upstream.RedirectURL
		if redirectBase == "" {
			redirectBase = s.cfg.Upstream.PackagesURL
		}
		http.Redirect(w, r, strings.TrimSuffix(redirectBase, "/")+"/"+pkgPath, http.StatusFound)
	}
}
