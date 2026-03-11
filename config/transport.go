package config

import (
	"fmt"
	"net/http"
	"net/url"
)

// NewTransport returns an http.RoundTripper cloned from http.DefaultTransport.
// If proxyURL is non-empty it is parsed and set as the transport proxy.
// Supports http://, https://, and socks5:// proxy URLs.
func NewTransport(proxyURL string) (http.RoundTripper, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport, nil
	}
	t := base.Clone()

	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse proxy URL %q: %w", proxyURL, err)
		}
		t.Proxy = http.ProxyURL(u)
	}

	return t, nil
}
