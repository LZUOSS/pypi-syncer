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
	if pkgPath == "" {
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

	localPath := filepath.Join(s.cfg.RepoPath, "packages", pkgPath)
	if _, err := os.Stat(localPath); err == nil {
		// File exists locally.
		http.ServeFile(w, r, localPath)
		return
	}

	// File not cached locally; proxy or redirect to upstream.
	upstreamURL := strings.TrimSuffix(s.cfg.Upstream.PackagesURL, "/") + "/" + pkgPath

	clientIP := ExtractClientIP(r, s.trustedProxies)
	mode := s.cfg.IPModes.Default
	if clientIP != nil {
		mode = s.ipMatcher.Match(clientIP)
	}

	if mode == "proxy" {
		upstreamTimeout := parseDuration(s.cfg.Timeouts.Upstream)
		if err := ReverseProxy(w, r, upstreamURL, upstreamTimeout); err != nil {
			http.Error(w, "upstream error", http.StatusBadGateway)
		}
	} else {
		http.Redirect(w, r, upstreamURL, http.StatusFound)
	}
}
