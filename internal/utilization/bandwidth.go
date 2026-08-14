package utilization

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ParseBitsPerSecond accepts an integer or a value with a K/M/G suffix
// (case-insensitive), e.g. "500M", "1.5G", "1000".
func ParseBitsPerSecond(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("bandwidth is empty")
	}
	mult := float64(1)
	switch {
	case hasSuffixFold(s, "GBPS"), hasSuffixFold(s, "G"):
		mult = 1e9
		s = trimSuffixFold(s, "GBPS")
		s = trimSuffixFold(s, "G")
	case hasSuffixFold(s, "MBPS"), hasSuffixFold(s, "M"):
		mult = 1e6
		s = trimSuffixFold(s, "MBPS")
		s = trimSuffixFold(s, "M")
	case hasSuffixFold(s, "KBPS"), hasSuffixFold(s, "K"):
		mult = 1e3
		s = trimSuffixFold(s, "KBPS")
		s = trimSuffixFold(s, "K")
	case hasSuffixFold(s, "BPS"):
		s = trimSuffixFold(s, "BPS")
	}
	s = strings.TrimSpace(s)
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < 0 || !isFinite(n) {
		return 0, fmt.Errorf("invalid bandwidth %q", s)
	}
	return uint64(n * mult), nil
}

func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

func trimSuffixFold(s, suffix string) string {
	if hasSuffixFold(s, suffix) {
		return strings.TrimRightFunc(s[:len(s)-len(suffix)], unicode.IsSpace)
	}
	return s
}

func isFinite(n float64) bool {
	return n == n && n < 1e20
}
