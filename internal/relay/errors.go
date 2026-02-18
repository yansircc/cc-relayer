package relay

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// routeTagPattern strips internal route tags like [relay/claude] from error messages.
var routeTagPattern = regexp.MustCompile(`\[relay/[^\]]+\]\s*`)

// ErrorCode defines a standardised error response.
type ErrorCode struct {
	Code    string
	Status  int
	Type    string
	Message string
	Pattern *regexp.Regexp // matches upstream error body
}

// Predefined error codes.
var errorCodes = []ErrorCode{
	{Code: "E001", Status: 400, Type: "invalid_request_error", Message: "bad request format", Pattern: regexp.MustCompile(`(?i)invalid.?request|bad request|malformed`)},
	{Code: "E002", Status: 401, Type: "authentication_error", Message: "authentication failed", Pattern: regexp.MustCompile(`(?i)unauthorized|invalid.*key|auth.*fail|invalid.*token`)},
	{Code: "E003", Status: 403, Type: "permission_error", Message: "access denied", Pattern: regexp.MustCompile(`(?i)forbidden|permission|access.?denied`)},
	{Code: "E004", Status: 404, Type: "not_found_error", Message: "resource not found", Pattern: regexp.MustCompile(`(?i)not.?found`)},
	{Code: "E005", Status: 413, Type: "request_too_large", Message: "request payload too large", Pattern: regexp.MustCompile(`(?i)too.?large|payload|content.?length`)},
	{Code: "E006", Status: 429, Type: "rate_limit_error", Message: "rate limited, please retry later", Pattern: regexp.MustCompile(`(?i)rate.?limit|too.?many|throttl`)},
	{Code: "E007", Status: 500, Type: "api_error", Message: "internal server error", Pattern: regexp.MustCompile(`(?i)internal.?server`)},
	{Code: "E008", Status: 502, Type: "api_error", Message: "bad gateway", Pattern: regexp.MustCompile(`(?i)bad.?gateway`)},
	{Code: "E009", Status: 503, Type: "overloaded_error", Message: "service temporarily overloaded", Pattern: regexp.MustCompile(`(?i)overloaded|unavailable`)},
	{Code: "E010", Status: 529, Type: "overloaded_error", Message: "API overloaded, please retry later", Pattern: regexp.MustCompile(`(?i)529|overloaded`)},
	{Code: "E011", Status: 400, Type: "invalid_request_error", Message: "model not available", Pattern: regexp.MustCompile(`(?i)model.*not.*available|unsupported.*model|does not support`)},
	{Code: "E012", Status: 400, Type: "invalid_request_error", Message: "context window exceeded", Pattern: regexp.MustCompile(`(?i)context.?window|token.?limit.*exceed|too.?long|max.*tokens.*input`)},
	{Code: "E013", Status: 400, Type: "invalid_request_error", Message: "output token limit exceeded", Pattern: regexp.MustCompile(`(?i)max.*output|output.*token.*limit`)},
	{Code: "E014", Status: 400, Type: "invalid_request_error", Message: "content policy violation", Pattern: regexp.MustCompile(`(?i)content.?policy|safety|moderation|harmful`)},
	{Code: "E015", Status: 500, Type: "api_error", Message: "unexpected upstream error", Pattern: nil},
}

// statusCodeMap maps HTTP status codes to their primary error code.
var statusCodeMap = map[int]*ErrorCode{}

func init() {
	// Build status-based lookup for codes that map 1:1 to HTTP status.
	directMap := map[int]string{
		401: "E002",
		403: "E003",
		404: "E004",
		413: "E005",
		429: "E006",
		502: "E008",
		503: "E009",
		529: "E010",
	}
	for _, ec := range errorCodes {
		if code, ok := directMap[ec.Status]; ok && ec.Code == code {
			cp := ec
			statusCodeMap[ec.Status] = &cp
		}
	}
}

// SanitizeError maps an upstream error response to a sanitised client-facing error.
// It strips internal route tags and attempts to match known error patterns.
func SanitizeError(statusCode int, body []byte) (int, []byte) {
	bodyStr := stripRouteTags(string(body))

	// Try direct status code mapping first
	if ec, ok := statusCodeMap[statusCode]; ok {
		return ec.Status, buildErrorJSON(ec.Type, ec.Message)
	}

	// Try pattern matching against body content
	for i := range errorCodes {
		ec := &errorCodes[i]
		if ec.Pattern != nil && ec.Pattern.MatchString(bodyStr) {
			return ec.Status, buildErrorJSON(ec.Type, ec.Message)
		}
	}

	// Try to preserve the original error structure if it's valid JSON
	var parsed struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(bodyStr), &parsed) == nil && parsed.Error.Type != "" {
		msg := stripRouteTags(parsed.Error.Message)
		return statusCode, buildErrorJSON(parsed.Error.Type, msg)
	}

	// Fallback to E015
	e015 := errorCodes[len(errorCodes)-1]
	return e015.Status, buildErrorJSON(e015.Type, e015.Message)
}

// SanitizeSSEError wraps a sanitised error as an SSE event.
func SanitizeSSEError(statusCode int, body []byte) string {
	_, sanitized := SanitizeError(statusCode, body)
	return fmt.Sprintf("event: error\ndata: %s\n\n", sanitized)
}

func stripRouteTags(s string) string {
	return strings.TrimSpace(routeTagPattern.ReplaceAllString(s, ""))
}

func buildErrorJSON(errType, msg string) []byte {
	resp := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errType,
			"message": msg,
		},
	}
	data, _ := json.Marshal(resp)
	return data
}
