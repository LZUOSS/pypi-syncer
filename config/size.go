package config

import (
	"fmt"
	"strconv"
	"strings"
)

// HumanSize is a size value that can be parsed from a human-readable string like "512g", "4g", "1t".
type HumanSize int64

func (s *HumanSize) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var str string
	if err := unmarshal(&str); err != nil {
		return err
	}
	v, err := ParseSize(str)
	if err != nil {
		return err
	}
	*s = HumanSize(v)
	return nil
}

func (s HumanSize) Bytes() int64 {
	return int64(s)
}

// ParseSize parses a human-readable size string into bytes.
// Supported suffixes: b, k, m, g, t (case-insensitive).
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size string")
	}
	s = strings.ToLower(s)

	multiplier := int64(1)
	numStr := s

	if len(s) > 0 {
		switch s[len(s)-1] {
		case 'b':
			numStr = s[:len(s)-1]
		case 'k':
			multiplier = 1024
			numStr = s[:len(s)-1]
		case 'm':
			multiplier = 1024 * 1024
			numStr = s[:len(s)-1]
		case 'g':
			multiplier = 1024 * 1024 * 1024
			numStr = s[:len(s)-1]
		case 't':
			multiplier = 1024 * 1024 * 1024 * 1024
			numStr = s[:len(s)-1]
		}
	}

	numStr = strings.TrimSpace(numStr)
	n, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative size %q", s)
	}
	return int64(n * float64(multiplier)), nil
}
