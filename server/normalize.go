package server

import (
	"regexp"
	"strings"
)

var normalizeRe = regexp.MustCompile(`[-_.]+`)

// NormalizeName normalizes a PyPI package name per PEP 503.
// Replaces runs of [-_.] with a single "-" and lowercases.
func NormalizeName(name string) string {
	return strings.ToLower(normalizeRe.ReplaceAllString(name, "-"))
}

// IsNormalized returns true if name is already in normalized form.
func IsNormalized(name string) bool {
	return NormalizeName(name) == name
}
