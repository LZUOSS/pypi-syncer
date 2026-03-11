package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ServeJSON handles /pypi/{pkg}/json requests.
// Normalize name, redirect if needed, serve {repoPath}/json/{normalized} as application/json.
func (s *Server) ServeJSON(w http.ResponseWriter, r *http.Request) {
	// Extract pkg from path: strip prefix and trailing "/json".
	rel := strings.TrimPrefix(r.URL.Path, s.cfg.Prefix+"/")
	pkg := strings.TrimSuffix(rel, "/json")
	if pkg == "" {
		http.NotFound(w, r)
		return
	}

	normalized := NormalizeName(pkg)
	if pkg != normalized {
		http.Redirect(w, r, s.cfg.Prefix+"/"+normalized+"/json", http.StatusMovedPermanently)
		return
	}

	filePath := filepath.Join(s.cfg.RepoPath, "json", normalized)
	data, err := os.ReadFile(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}
