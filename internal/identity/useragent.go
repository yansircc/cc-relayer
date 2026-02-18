package identity

import (
	"regexp"
	"strconv"
	"strings"
)

// ccUserAgentPattern matches Claude Code User-Agent: claude-cli/1.0.110 ...
var ccUserAgentPattern = regexp.MustCompile(`^claude-cli/([\d.]+)`)

const defaultUserAgent = "claude-cli/1.0.69 (external, cli)"

// ParseCCUserAgent extracts the full UA string if it matches Claude Code format.
// Returns ("claude-cli/1.0.110 ...", "1.0.110", true) on match.
func ParseCCUserAgent(ua string) (full string, version string, ok bool) {
	m := ccUserAgentPattern.FindStringSubmatch(ua)
	if len(m) < 2 {
		return "", "", false
	}
	return ua, m[1], true
}

// IsNewerVersion returns true if a is semantically newer than b.
// Compares dot-separated numeric segments left to right.
func IsNewerVersion(a, b string) bool {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		av, bv := 0, 0
		if i < len(aParts) {
			av, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bv, _ = strconv.Atoi(bParts[i])
		}
		if av > bv {
			return true
		}
		if av < bv {
			return false
		}
	}
	return false
}

// DefaultUserAgent returns the hardcoded fallback User-Agent.
func DefaultUserAgent() string {
	return defaultUserAgent
}
