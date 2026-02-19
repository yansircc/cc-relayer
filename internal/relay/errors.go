package relay

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// errorCode defines a standardised error response.
type errorCode struct {
	Status  int
	Type    string
	Message string
	Pattern *regexp.Regexp // matches upstream error body
}

// Predefined error codes.
var errorCodes = []errorCode{
	{Status: 400, Type: "invalid_request_error", Message: "bad request format", Pattern: regexp.MustCompile(`(?i)invalid.?request|bad request|malformed`)},
	{Status: 401, Type: "authentication_error", Message: "authentication failed", Pattern: regexp.MustCompile(`(?i)unauthorized|invalid.*key|auth.*fail|invalid.*token`)},
	{Status: 403, Type: "permission_error", Message: "access denied", Pattern: regexp.MustCompile(`(?i)forbidden|permission|access.?denied`)},
	{Status: 404, Type: "not_found_error", Message: "resource not found", Pattern: regexp.MustCompile(`(?i)not.?found`)},
	{Status: 413, Type: "request_too_large", Message: "request payload too large", Pattern: regexp.MustCompile(`(?i)too.?large|payload|content.?length`)},
	{Status: 429, Type: "rate_limit_error", Message: "rate limited, please retry later", Pattern: regexp.MustCompile(`(?i)rate.?limit|too.?many|throttl`)},
	{Status: 500, Type: "api_error", Message: "internal server error", Pattern: regexp.MustCompile(`(?i)internal.?server`)},
	{Status: 502, Type: "api_error", Message: "bad gateway", Pattern: regexp.MustCompile(`(?i)bad.?gateway`)},
	{Status: 503, Type: "overloaded_error", Message: "service temporarily overloaded", Pattern: regexp.MustCompile(`(?i)overloaded|unavailable`)},
	{Status: 529, Type: "overloaded_error", Message: "API overloaded, please retry later", Pattern: regexp.MustCompile(`(?i)529|overloaded`)},
	{Status: 400, Type: "invalid_request_error", Message: "model not available", Pattern: regexp.MustCompile(`(?i)model.*not.*available|unsupported.*model|does not support`)},
	{Status: 400, Type: "invalid_request_error", Message: "context window exceeded", Pattern: regexp.MustCompile(`(?i)context.?window|token.?limit.*exceed|too.?long|max.*tokens.*input`)},
	{Status: 400, Type: "invalid_request_error", Message: "output token limit exceeded", Pattern: regexp.MustCompile(`(?i)max.*output|output.*token.*limit`)},
	{Status: 400, Type: "invalid_request_error", Message: "content policy violation", Pattern: regexp.MustCompile(`(?i)content.?policy|safety|moderation|harmful`)},
	{Status: 500, Type: "api_error", Message: "unexpected upstream error", Pattern: nil},
}

// statusCodeMap maps HTTP status codes to their primary error code (1:1 statuses only).
var statusCodeMap map[int]*errorCode

func init() {
	directStatuses := map[int]bool{
		401: true, 403: true, 404: true, 413: true,
		429: true, 502: true, 503: true, 529: true,
	}
	statusCodeMap = make(map[int]*errorCode, len(directStatuses))
	for i := range errorCodes {
		if directStatuses[errorCodes[i].Status] && statusCodeMap[errorCodes[i].Status] == nil {
			statusCodeMap[errorCodes[i].Status] = &errorCodes[i]
		}
	}
}

// fallbackError is the last entry in errorCodes (unexpected upstream error).
var fallbackError = &errorCodes[len(errorCodes)-1]

// SanitizeError maps an upstream error response to a sanitised client-facing error.
func SanitizeError(statusCode int, body []byte) (int, []byte) {
	bodyStr := string(body)

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
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Type != "" {
		return statusCode, buildErrorJSON(parsed.Error.Type, parsed.Error.Message)
	}

	return fallbackError.Status, buildErrorJSON(fallbackError.Type, fallbackError.Message)
}

// SanitizeSSEError wraps a sanitised error as an SSE event.
func SanitizeSSEError(statusCode int, body []byte) string {
	_, sanitized := SanitizeError(statusCode, body)
	return fmt.Sprintf("event: error\ndata: %s\n\n", sanitized)
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
