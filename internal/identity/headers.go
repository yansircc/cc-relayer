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
func SetRequiredHeaders(h http.Header, accessToken, apiVersion, betaHeader, userAgent string) {
	h.Set("Authorization", "Bearer "+accessToken)
	h.Set("anthropic-version", apiVersion)
	h.Set("anthropic-beta", betaHeader)
	h.Set("Content-Type", "application/json")
	if userAgent != "" {
		h.Set("User-Agent", userAgent)
	}
}
