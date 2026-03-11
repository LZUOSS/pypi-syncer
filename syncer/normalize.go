package syncer

import (
	"regexp"
	"strings"
)

var normalizeRe = regexp.MustCompile(`[-_.]+`)

func normalizeName(name string) string {
	return strings.ToLower(normalizeRe.ReplaceAllString(name, "-"))
}
