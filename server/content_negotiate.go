package server

import (
	"net/http"
	"strings"
)

// NegotiateSimpleFormat returns the file suffix for the simple API response
// based on the Accept header per PEP 691.
// Returns ".v1_json", ".v1_html", or ".html".
func NegotiateSimpleFormat(r *http.Request) string {
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/vnd.pypi.simple.v1+json") {
		return ".v1_json"
	}
	if strings.Contains(accept, "application/vnd.pypi.simple.v1+html") {
		return ".v1_html"
	}
	return ".html"
}
