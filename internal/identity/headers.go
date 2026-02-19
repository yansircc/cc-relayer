package identity

import (
	"net/http"
	"strings"
)

// AllowedHeaders is the whitelist of headers forwarded to Anthropic.
var AllowedHeaders = map[string]bool{
	"accept":            true,
	"content-type":      true,
	"user-agent":        true,
	"anthropic-version": true,
	"anthropic-beta":    true,
	"x-api-key":         true,
	"authorization":     true,
	"x-app":             true,
}

// StainlessPrefix identifies x-stainless-* headers.
const StainlessPrefix = "x-stainless-"

// StrippedHeaders are explicitly removed even if somehow present.
var StrippedHeaders = []string{
	"x-real-ip", "x-forwarded-for", "x-forwarded-proto", "x-forwarded-host",
	"cf-ray", "cf-connecting-ip", "cf-ipcountry", "cf-visitor",
	"x-vercel-id", "x-vercel-deployment-url",
}

// FilterHeaders builds a clean header set with only allowed headers.
// Stainless headers are handled separately (via fingerprint binding).
func FilterHeaders(original http.Header) http.Header {
	clean := make(http.Header)

	for key, vals := range original {
		lower := strings.ToLower(key)

		// Allow whitelisted headers
		if AllowedHeaders[lower] {
			for _, v := range vals {
				clean.Add(key, v)
			}
			continue
		}

		// Allow x-stainless-* (will be overwritten by fingerprint binding)
		if strings.HasPrefix(lower, StainlessPrefix) {
			for _, v := range vals {
				clean.Add(key, v)
			}
			continue
		}
	}

	return clean
}

// SetRequiredHeaders sets the required headers for the upstream request.
// The model parameter is used to filter beta flags for non-CC models (e.g. Haiku).
func SetRequiredHeaders(h http.Header, accessToken, apiVersion, betaHeader, userAgent, model string) {
	beta := betaHeader
	if strings.Contains(strings.ToLower(model), "haiku") {
		beta = filterBetaForHaiku(betaHeader)
	}

	h.Set("Authorization", "Bearer "+accessToken)
	h.Set("anthropic-version", apiVersion)
	h.Set("anthropic-beta", beta)
	h.Set("Content-Type", "application/json")
	if userAgent != "" {
		h.Set("User-Agent", userAgent)
	}
}

// filterBetaForHaiku removes claude-code-* and fine-grained-tool-streaming-* beta flags
// that are not applicable to Haiku models.
func filterBetaForHaiku(betaHeader string) string {
	parts := strings.Split(betaHeader, ",")
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if strings.HasPrefix(p, "claude-code-") || strings.HasPrefix(p, "fine-grained-tool-streaming-") {
			continue
		}
		filtered = append(filtered, p)
	}
	return strings.Join(filtered, ",")
}
