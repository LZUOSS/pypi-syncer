package config

import (
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// NewTransport returns an http.RoundTripper cloned from http.DefaultTransport.
// If proxyURL is non-empty it is parsed and set as the transport proxy.
// Supports http://, https://, and socks5:// proxy URLs.
//
// ResponseHeaderTimeout is set to 60 s so stalled connections are detected
// quickly, without imposing a per-request body-read deadline. Callers that
// need a full-request timeout (e.g. the reverse-proxy handler) should set
// http.Client.Timeout themselves.
func NewTransport(proxyURL string) (http.RoundTripper, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport, nil
	}
	t := base.Clone()
	t.ResponseHeaderTimeout = 60 * time.Second

	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse proxy URL %q: %w", proxyURL, err)
		}
		t.Proxy = http.ProxyURL(u)
	}

	return t, nil
}
