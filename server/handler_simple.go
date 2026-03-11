package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ServeSimple handles /pypi/simple/* requests.
// Routes:
//
//	GET /pypi/simple/         -> serve {repoPath}/simple/index{suffix}
//	GET /pypi/simple/{pkg}/   -> normalize name, redirect if not normalized,
//	                             serve {repoPath}/simple/{normalized}/index{suffix}
func (s *Server) ServeSimple(w http.ResponseWriter, r *http.Request) {
	suffix := NegotiateSimpleFormat(r)

	// Strip prefix to get the path after /pypi/simple/
	trimmed := strings.TrimPrefix(r.URL.Path, s.cfg.Prefix+"/simple")
	trimmed = strings.TrimPrefix(trimmed, "/")
	trimmed = strings.TrimSuffix(trimmed, "/")

	if trimmed == "" {
		// Root index: /pypi/simple/
		filePath := filepath.Join(s.cfg.RepoPath, "simple", "index"+suffix)
		serveSimpleFile(w, r, filePath, suffix)
		return
	}

	// Package-specific: /pypi/simple/{pkg}/
	pkg := trimmed
	normalized := NormalizeName(pkg)
	if pkg != normalized {
		http.Redirect(w, r, s.cfg.Prefix+"/simple/"+normalized+"/", http.StatusMovedPermanently)
		return
	}

	filePath := filepath.Join(s.cfg.RepoPath, "simple", normalized, "index"+suffix)
	serveSimpleFile(w, r, filePath, suffix)
}

func serveSimpleFile(w http.ResponseWriter, r *http.Request, filePath, suffix string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	switch suffix {
	case ".v1_json":
		w.Header().Set("Content-Type", "application/vnd.pypi.simple.v1+json")
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.Write(data)
}
