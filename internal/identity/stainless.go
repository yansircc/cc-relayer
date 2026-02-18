package identity

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/yansir/claude-relay/internal/store"
)

// Bound stainless headers (captured once, replayed always).
var boundStainlessKeys = []string{
	"x-stainless-os",
	"x-stainless-arch",
	"x-stainless-runtime",
	"x-stainless-runtime-version",
	"x-stainless-lang",
	"x-stainless-package-version",
}

// Pass-through stainless headers (dynamic, not bound).
var passthroughStainlessKeys = []string{
	"x-stainless-retry-count",
	"x-stainless-read-timeout",
}

// BindStainlessHeaders captures x-stainless-* headers from the first request
// and replays them on all subsequent requests for the same account.
func BindStainlessHeaders(ctx context.Context, s *store.Store, accountID string, reqHeaders http.Header, outHeaders http.Header) {
	// Try to get stored fingerprint
	stored, err := s.GetStainlessHeaders(ctx, accountID)
	if err != nil {
		slog.Error("get stainless headers", "error", err)
	}

	if stored != "" {
		// Apply stored fingerprint
		var headers map[string]string
		if json.Unmarshal([]byte(stored), &headers) == nil {
			for k, v := range headers {
				outHeaders.Set(k, v)
			}
		}
	} else {
		// Capture from this request (first time)
		captured := make(map[string]string)
		for _, key := range boundStainlessKeys {
			if v := reqHeaders.Get(key); v != "" {
				captured[key] = v
				outHeaders.Set(key, v)
			}
		}

		if len(captured) > 0 {
			data, _ := json.Marshal(captured)
			ok, err := s.SetStainlessHeadersNX(ctx, accountID, string(data))
			if err != nil {
				slog.Error("set stainless headers", "error", err)
			}
			if !ok {
				// Another request beat us — re-read and apply stored version
				stored, _ := s.GetStainlessHeaders(ctx, accountID)
				if stored != "" {
					var headers map[string]string
					if json.Unmarshal([]byte(stored), &headers) == nil {
						for k, v := range headers {
							outHeaders.Set(k, v)
						}
					}
				}
			}
		}
	}

	// Always pass through dynamic headers from the current request
	for _, key := range passthroughStainlessKeys {
		if v := reqHeaders.Get(key); v != "" {
			outHeaders.Set(key, v)
		}
	}
}

// RemoveAllStainless strips all x-stainless-* headers from a header set.
func RemoveAllStainless(h http.Header) {
	for key := range h {
		if strings.HasPrefix(strings.ToLower(key), StainlessPrefix) {
			h.Del(key)
		}
	}
}
