package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/yansir/cc-relayer/internal/config"
)

type contextKey string

const KeyInfoKey contextKey = "keyInfo"

// KeyInfo is attached to the request context after authentication.
type KeyInfo struct {
	ID             string
	Name           string
	BoundAccountID string
}

// Middleware validates the static API token.
type Middleware struct {
	cfg *config.Config
}

func NewMiddleware(cfg *config.Config) *Middleware {
	return &Middleware{cfg: cfg}
}

// Authenticate is the HTTP middleware that validates the static API token.
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := extractAPIKey(r)
		if apiKey == "" {
			writeError(w, http.StatusUnauthorized, "authentication_error", "missing or invalid API key")
			return
		}

		keyInfo, err := m.validateKey(apiKey)
		if err != nil {
			slog.Warn("auth failed", "error", err)
			writeError(w, http.StatusUnauthorized, "authentication_error", err.Error())
			return
		}

		ctx := context.WithValue(r.Context(), KeyInfoKey, keyInfo)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) validateKey(apiKey string) (*KeyInfo, error) {
	if apiKey != m.cfg.StaticToken {
		return nil, fmt.Errorf("invalid API key")
	}
	return &KeyInfo{
		ID:   "default",
		Name: "default",
	}, nil
}

// --- Helpers ---

func extractAPIKey(r *http.Request) string {
	// x-api-key header
	if key := r.Header.Get("x-api-key"); key != "" {
		return key
	}
	// Authorization: Bearer
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func GetKeyInfo(ctx context.Context) *KeyInfo {
	v, _ := ctx.Value(KeyInfoKey).(*KeyInfo)
	return v
}

func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"type":"%s","message":"%s"}}`, errType, msg)
}
