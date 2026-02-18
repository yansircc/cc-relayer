package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/config"
	"github.com/yansir/cc-relayer/internal/store"
)

type contextKey string

const KeyInfoKey contextKey = "keyInfo"

// KeyInfo is attached to the request context after authentication.
type KeyInfo struct {
	ID                string
	Name              string
	ConcurrencyLimit  int
	WeeklyOpusCostLim float64
	BoundAccountID    string
}

// Middleware validates API keys and enforces limits.
type Middleware struct {
	store  *store.Store
	crypto *account.Crypto
	cfg    *config.Config
}

func NewMiddleware(s *store.Store, c *account.Crypto, cfg *config.Config) *Middleware {
	return &Middleware{store: s, crypto: c, cfg: cfg}
}

// Authenticate is the HTTP middleware that validates API keys.
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := extractAPIKey(r, m.cfg.APIKeyPrefix)
		if apiKey == "" {
			writeError(w, http.StatusUnauthorized, "authentication_error", "missing or invalid API key")
			return
		}

		keyInfo, err := m.validateKey(r.Context(), apiKey)
		if err != nil {
			slog.Warn("auth failed", "error", err)
			writeError(w, http.StatusUnauthorized, "authentication_error", err.Error())
			return
		}

		ctx := context.WithValue(r.Context(), KeyInfoKey, keyInfo)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) validateKey(ctx context.Context, apiKey string) (*KeyInfo, error) {
	hash := m.crypto.HashAPIKey(apiKey)

	keyID, err := m.store.GetAPIKeyIDByHash(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("lookup key: %w", err)
	}
	if keyID == "" {
		return nil, fmt.Errorf("invalid API key")
	}

	data, err := m.store.GetAPIKey(ctx, keyID)
	if err != nil {
		return nil, fmt.Errorf("get key data: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("key not found")
	}

	if data["isActive"] != "true" {
		return nil, fmt.Errorf("API key is inactive")
	}
	if data["isDeleted"] == "true" {
		return nil, fmt.Errorf("API key is deleted")
	}

	// Check expiry
	if exp := data["expiresAt"]; exp != "" {
		if t, err := time.Parse(time.RFC3339, exp); err == nil {
			if time.Now().After(t) {
				return nil, fmt.Errorf("API key expired")
			}
		}
	}

	// Update lastUsedAt
	_ = m.store.SetAPIKey(ctx, keyID, map[string]string{
		"lastUsedAt": time.Now().UTC().Format(time.RFC3339),
	})

	return &KeyInfo{
		ID:                data["id"],
		Name:              data["name"],
		ConcurrencyLimit:  atoi(data["concurrencyLimit"]),
		WeeklyOpusCostLim: atof(data["weeklyOpusCostLimit"]),
		BoundAccountID:    data["boundAccountId"],
	}, nil
}

// AcquireConcurrency atomically checks and acquires a concurrency slot using a Lua script.
func (m *Middleware) AcquireConcurrency(ctx context.Context, keyInfo *KeyInfo) (requestID string, err error) {
	if keyInfo.ConcurrencyLimit <= 0 {
		return uuid.New().String(), nil // unlimited
	}

	reqID := uuid.New().String()
	acquired, err := m.store.TryAcquireConcurrencySlot(ctx, keyInfo.ID, reqID, keyInfo.ConcurrencyLimit, m.cfg.ConcurrencySlotTTL)
	if err != nil {
		return "", fmt.Errorf("acquire concurrency: %w", err)
	}
	if !acquired {
		return "", fmt.Errorf("concurrency limit exceeded (%d)", keyInfo.ConcurrencyLimit)
	}

	return reqID, nil
}

// ReleaseConcurrency releases a concurrency slot.
func (m *Middleware) ReleaseConcurrency(ctx context.Context, keyID, requestID string) {
	if err := m.store.ReleaseConcurrencySlot(ctx, keyID, requestID); err != nil {
		slog.Error("release concurrency slot failed", "keyId", keyID, "error", err)
	}
}

// CheckWeeklyOpusCost checks if the API key has exceeded its weekly Opus cost limit.
func (m *Middleware) CheckWeeklyOpusCost(ctx context.Context, keyInfo *KeyInfo) error {
	if keyInfo.WeeklyOpusCostLim <= 0 {
		return nil // unlimited
	}

	weekStr := isoWeekString(time.Now())
	cost, err := m.store.GetWeeklyOpusCost(ctx, keyInfo.ID, weekStr)
	if err != nil {
		return fmt.Errorf("get weekly cost: %w", err)
	}

	if cost >= keyInfo.WeeklyOpusCostLim {
		return fmt.Errorf("weekly Opus cost limit exceeded (%.2f/%.2f)", cost, keyInfo.WeeklyOpusCostLim)
	}
	return nil
}

// --- API Key management ---

// CreateAPIKey generates a new API key and stores its hash.
func (m *Middleware) CreateAPIKey(ctx context.Context, name string, concLimit int, opusLimit float64, boundAccountID string, expiresInDays int) (keyID, plaintext string, err error) {
	keyID = uuid.New().String()
	rawKey := generateRandomHex(64)
	plaintext = m.cfg.APIKeyPrefix + rawKey
	hash := m.crypto.HashAPIKey(plaintext)

	now := time.Now().UTC()
	fields := map[string]string{
		"id":                 keyID,
		"name":               name,
		"apiKey":             hash,
		"isActive":           "true",
		"concurrencyLimit":   fmt.Sprintf("%d", concLimit),
		"weeklyOpusCostLimit": fmt.Sprintf("%.2f", opusLimit),
		"boundAccountId":     boundAccountID,
		"createdAt":          now.Format(time.RFC3339),
		"lastUsedAt":         "",
		"isDeleted":          "false",
		"deletedAt":          "",
	}

	if expiresInDays > 0 {
		fields["expiresAt"] = now.AddDate(0, 0, expiresInDays).Format(time.RFC3339)
	}

	if err := m.store.SetAPIKey(ctx, keyID, fields); err != nil {
		return "", "", err
	}
	if err := m.store.SetAPIKeyHash(ctx, hash, keyID); err != nil {
		return "", "", err
	}

	return keyID, plaintext, nil
}

// DeleteAPIKey soft-deletes an API key.
func (m *Middleware) DeleteAPIKey(ctx context.Context, keyID string) error {
	data, err := m.store.GetAPIKey(ctx, keyID)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("key not found")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := m.store.SetAPIKey(ctx, keyID, map[string]string{
		"isDeleted": "true",
		"isActive":  "false",
		"deletedAt": now,
	}); err != nil {
		return err
	}

	// Remove from hash map
	if hash := data["apiKey"]; hash != "" {
		_ = m.store.DeleteAPIKeyHash(ctx, hash)
	}
	return nil
}

// ListAPIKeys returns all non-deleted API keys.
func (m *Middleware) ListAPIKeys(ctx context.Context) ([]map[string]string, error) {
	hashes, err := m.store.ListAPIKeyHashes(ctx)
	if err != nil {
		return nil, err
	}

	var keys []map[string]string
	seen := make(map[string]bool)
	for _, keyID := range hashes {
		if seen[keyID] {
			continue
		}
		seen[keyID] = true

		data, err := m.store.GetAPIKey(ctx, keyID)
		if err != nil || len(data) == 0 {
			continue
		}
		if data["isDeleted"] == "true" {
			continue
		}
		// Remove sensitive hash from output
		delete(data, "apiKey")
		keys = append(keys, data)
	}
	return keys, nil
}

// --- Helpers ---

func extractAPIKey(r *http.Request, prefix string) string {
	// x-api-key header
	if key := r.Header.Get("x-api-key"); strings.HasPrefix(key, prefix) {
		return key
	}
	// Authorization: Bearer
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		key := strings.TrimPrefix(auth, "Bearer ")
		if strings.HasPrefix(key, prefix) {
			return key
		}
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

func isoWeekString(t time.Time) string {
	year, week := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}

func generateRandomHex(length int) string {
	b := make([]byte, length/2)
	_, _ = uuid.New().MarshalBinary() // just for entropy
	for i := range b {
		b[i] = byte(uuid.New().ID() & 0xFF)
	}
	return fmt.Sprintf("%x", b)
}

func atoi(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

func atof(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}
