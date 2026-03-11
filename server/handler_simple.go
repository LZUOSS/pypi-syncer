package server

import (
	"fmt"
	"net/http"
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
		// Root index listing is disabled: it contains ~600 k packages and
		// would produce a response tens of MB in size on every request.
		// Per-package queries (/pypi/simple/{pkg}/) continue to work normally.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		prefix := s.cfg.Prefix
		fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>Index listing disabled</title></head>
<body>
<h1>403 — Index listing disabled</h1>
<p>The full package list at <code>%s/simple/</code> has been disabled because
it contains too many packages (&gt;600&thinsp;000) and the resulting index
file is tens of megabytes in size.</p>
<p>Per-package indexes still work normally:<br>
<code>%s/simple/{package-name}/</code></p>
<p>pip and uv use per-package lookups by default and are unaffected.</p>
</body>
</html>
`, prefix, prefix)
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
	switch suffix {
	case ".v1_json":
		w.Header().Set("Content-Type", "application/vnd.pypi.simple.v1+json")
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	// http.ServeFile handles If-Modified-Since, ETag, Range, streaming, and
	// 404 for missing files (it overrides our Content-Type only on error).
	http.ServeFile(w, r, filePath)
}
