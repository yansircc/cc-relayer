package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/yansir/cc-relayer/internal/store"
)

type contextKey string

const KeyInfoKey contextKey = "keyInfo"

// KeyInfo is attached to the request context after authentication.
type KeyInfo struct {
	ID             string
	Name           string
	BoundAccountID string
	IsAdmin        bool
}

// Middleware validates API tokens against the admin token and user store.
type Middleware struct {
	adminToken string
	store      store.Store
}

func NewMiddleware(adminToken string, s store.Store) *Middleware {
	return &Middleware{adminToken: adminToken, store: s}
}

// Authenticate is the HTTP middleware that validates tokens.
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "authentication_error", "missing or invalid API key")
			return
		}

		keyInfo, err := m.validateToken(r.Context(), token)
		if err != nil {
			slog.Warn("auth failed", "error", err)
			writeError(w, http.StatusUnauthorized, "authentication_error", err.Error())
			return
		}

		ctx := context.WithValue(r.Context(), KeyInfoKey, keyInfo)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ValidateToken validates a token and returns KeyInfo if valid.
func (m *Middleware) ValidateToken(ctx context.Context, token string) (*KeyInfo, bool) {
	ki, err := m.validateToken(ctx, token)
	return ki, err == nil && ki != nil
}

func (m *Middleware) validateToken(ctx context.Context, token string) (*KeyInfo, error) {
	// Check admin token with constant-time comparison.
	if subtle.ConstantTimeCompare([]byte(token), []byte(m.adminToken)) == 1 {
		return &KeyInfo{
			ID:      "admin",
			Name:    "admin",
			IsAdmin: true,
		}, nil
	}

	// Hash token and look up user.
	hash := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(hash[:])

	user, err := m.store.GetUserByTokenHash(ctx, hashHex)
	if err != nil {
		return nil, fmt.Errorf("token lookup failed: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("invalid API key")
	}
	if user.Status != "active" {
		return nil, fmt.Errorf("user %s is %s", user.Name, user.Status)
	}

	go m.store.UpdateUserLastActive(context.Background(), user.ID)

	return &KeyInfo{
		ID:   user.ID,
		Name: user.Name,
	}, nil
}

// --- Helpers ---

func extractToken(r *http.Request) string {
	if key := r.Header.Get("x-api-key"); key != "" {
		return key
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if c, err := r.Cookie("cc_session"); err == nil && c.Value != "" {
		return c.Value
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
	fmt.Fprintf(w, `{"type":"error","error":{"type":"%s","message":"%s"}}`, errType, msg)
}
