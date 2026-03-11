package server

import (
	"net/http"
	"os"
	"path/filepath"
)

// ServeJSON handles /pypi/{pkg}/json requests.
// Normalize name, redirect if needed, serve {repoPath}/json/{normalized} as application/json.
func (s *Server) ServeJSON(w http.ResponseWriter, r *http.Request) {
	pkg := r.PathValue("pkg")
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
