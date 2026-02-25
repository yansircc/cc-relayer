package identity

import (
	"net/http"
	"strings"
)

// StainlessPrefix identifies x-stainless-* headers.
const StainlessPrefix = "x-stainless-"

var allowedHeaders = map[string]bool{
	"accept":            true,
	"content-type":      true,
	"user-agent":        true,
	"anthropic-version": true,
	"anthropic-beta":    true,
	"anthropic-dangerous-direct-browser-access": true,
	"x-app": true,
}

// FilterHeaders builds a clean header set with only allowed headers.
// Stainless headers are handled separately (via fingerprint binding).
func FilterHeaders(original http.Header) http.Header {
	clean := make(http.Header)

	for key, vals := range original {
		lower := strings.ToLower(key)

		if allowedHeaders[lower] || strings.HasPrefix(lower, StainlessPrefix) {
			for _, v := range vals {
				clean.Add(key, v)
			}
		}
	}

	return clean
}

// SetRequiredHeaders sets the required headers for the upstream request.
func SetRequiredHeaders(h http.Header, accessToken, apiVersion, betaHeader string) {
	// Strip client auth headers — the relay's static token must never reach upstream.
	h.Del("x-api-key")
	h.Del("Authorization")

	h.Set("Authorization", "Bearer "+accessToken)
	if h.Get("anthropic-version") == "" {
		h.Set("anthropic-version", apiVersion)
	}
	if mergedBeta := mergeBetaHeaders(h.Get("anthropic-beta"), betaHeader); mergedBeta != "" {
		h.Set("anthropic-beta", mergedBeta)
	}
	h.Set("Content-Type", "application/json")
}

func mergeBetaHeaders(clientBeta, relayBeta string) string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, raw := range []string{clientBeta, relayBeta} {
		if raw == "" {
			continue
		}
		for _, part := range strings.Split(raw, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}
