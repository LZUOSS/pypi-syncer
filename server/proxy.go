package server

import (
	"context"
	"io"
	"net/http"
	"time"
)

// hopByHop lists headers that should not be forwarded by proxies.
var hopByHop = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Proxy-Authenticate":  true,
}

// ReverseProxy forwards r to targetURL and writes the response to w.
// It strips hop-by-hop headers and streams the response body.
func ReverseProxy(w http.ResponseWriter, r *http.Request, targetURL string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, r.Method, targetURL, r.Body)
	if err != nil {
		return err
	}

	// Forward original headers, skipping hop-by-hop.
	for key, vals := range r.Header {
		if hopByHop[http.CanonicalHeaderKey(key)] {
			continue
		}
		for _, v := range vals {
			req.Header.Add(key, v)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Copy response headers, skipping hop-by-hop.
	for key, vals := range resp.Header {
		if hopByHop[http.CanonicalHeaderKey(key)] {
			continue
		}
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}

	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	return err
}
