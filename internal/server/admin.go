package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/auth"
	"github.com/yansir/cc-relayer/internal/config"
	"github.com/yansir/cc-relayer/internal/store"
)

// AdminHandler handles admin API endpoints.
type AdminHandler struct {
	cfg      *config.Config
	accounts *account.AccountStore
	tokens   *account.TokenManager
	authMw   *auth.Middleware
	store    *store.Store
}

func (a *AdminHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/login", a.login)

	// Protected admin routes
	mux.Handle("GET /admin/accounts", a.requireAdmin(http.HandlerFunc(a.listAccounts)))
	mux.Handle("POST /admin/accounts", a.requireAdmin(http.HandlerFunc(a.createAccount)))
	mux.Handle("PUT /admin/accounts/{id}", a.requireAdmin(http.HandlerFunc(a.updateAccount)))
	mux.Handle("DELETE /admin/accounts/{id}", a.requireAdmin(http.HandlerFunc(a.deleteAccount)))
	mux.Handle("POST /admin/accounts/{id}/refresh", a.requireAdmin(http.HandlerFunc(a.refreshAccount)))
	mux.Handle("POST /admin/accounts/{id}/toggle", a.requireAdmin(http.HandlerFunc(a.toggleAccount)))

	mux.Handle("GET /admin/keys", a.requireAdmin(http.HandlerFunc(a.listKeys)))
	mux.Handle("POST /admin/keys", a.requireAdmin(http.HandlerFunc(a.createKey)))
	mux.Handle("DELETE /admin/keys/{id}", a.requireAdmin(http.HandlerFunc(a.deleteKey)))

	mux.Handle("GET /admin/status", a.requireAdmin(http.HandlerFunc(a.status)))
}

// --- Auth middleware ---

func (a *AdminHandler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tokenStr == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing token"})
			return
		}

		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			return []byte(a.cfg.JWTSecret), nil
		})
		if err != nil || !token.Valid {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// --- Login ---

func (a *AdminHandler) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	if req.Username != a.cfg.AdminUsername || !verifyPassword(req.Password, a.cfg.AdminPassword) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": req.Username,
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	})
	tokenStr, err := token.SignedString([]byte(a.cfg.JWTSecret))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":     tokenStr,
		"expiresIn": 86400,
	})
}

// --- Account handlers ---

func (a *AdminHandler) listAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := a.accounts.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Build safe response (no tokens)
	var result []map[string]interface{}
	for _, acct := range accounts {
		result = append(result, map[string]interface{}{
			"id":                acct.ID,
			"name":              acct.Name,
			"status":            acct.Status,
			"errorMessage":      acct.ErrorMessage,
			"schedulable":       acct.Schedulable,
			"priority":          acct.Priority,
			"maxConcurrency":    acct.MaxConcurrency,
			"autoStopOnWarning": acct.AutoStopOnWarning,
			"lastUsedAt":        acct.LastUsedAt,
			"lastRefreshAt":     acct.LastRefreshAt,
			"expiresAt":         acct.ExpiresAt,
			"hasProxy":          acct.Proxy != nil,
			"fiveHourStatus":    acct.FiveHourStatus,
			"overloaded":        acct.OverloadedUntil != nil && time.Now().Before(*acct.OverloadedUntil),
			"opusRateLimited":   acct.OpusRateLimitEndAt != nil && time.Now().Before(*acct.OpusRateLimitEndAt),
		})
	}

	writeJSON(w, http.StatusOK, result)
}

func (a *AdminHandler) createAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name              string                `json:"name"`
		RefreshToken      string                `json:"refreshToken"`
		Proxy             *account.ProxyConfig  `json:"proxy,omitempty"`
		Priority          int                   `json:"priority"`
		AutoStopOnWarning bool                  `json:"autoStopOnWarning"`
		MaxConcurrency    int                   `json:"maxConcurrency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.RefreshToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refreshToken required"})
		return
	}
	if req.Priority == 0 {
		req.Priority = 50
	}

	acct, err := a.accounts.Create(r.Context(), req.Name, req.RefreshToken, req.Proxy, req.Priority, req.AutoStopOnWarning, req.MaxConcurrency)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Trigger immediate token refresh to validate
	go func() {
		ctx := r.Context()
		if _, err := a.tokens.ForceRefresh(ctx, acct.ID); err != nil {
			slog.Error("initial token refresh failed", "accountId", acct.ID, "error", err)
		}
	}()

	writeJSON(w, http.StatusCreated, map[string]string{
		"id":     acct.ID,
		"name":   acct.Name,
		"status": acct.Status,
	})
}

func (a *AdminHandler) updateAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var fields map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	updates := make(map[string]string)
	for k, v := range fields {
		switch k {
		case "name", "priority", "maxConcurrency", "autoStopOnWarning", "schedulable":
			updates[k] = fmt.Sprintf("%v", v)
		case "proxy":
			if v == nil {
				updates[k] = ""
			} else {
				b, _ := json.Marshal(v)
				updates[k] = string(b)
			}
		}
	}

	if err := a.accounts.Update(r.Context(), id, updates); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (a *AdminHandler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.accounts.Delete(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *AdminHandler) refreshAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := a.tokens.ForceRefresh(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Read back expiresAt after refresh
	resp := map[string]interface{}{"success": true}
	data, getErr := a.store.GetAccount(r.Context(), id)
	if getErr == nil {
		if exp := data["expiresAt"]; exp != "" {
			resp["expiresAt"] = exp
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *AdminHandler) toggleAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	acct, err := a.accounts.Get(r.Context(), id)
	if err != nil || acct == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "account not found"})
		return
	}

	newVal := "true"
	if acct.Schedulable {
		newVal = "false"
	}
	_ = a.accounts.Update(r.Context(), id, map[string]string{"schedulable": newVal})
	writeJSON(w, http.StatusOK, map[string]interface{}{"schedulable": newVal == "true"})
}

// --- Key handlers ---

func (a *AdminHandler) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.authMw.ListAPIKeys(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (a *AdminHandler) createKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name               string  `json:"name"`
		ConcurrencyLimit   int     `json:"concurrencyLimit"`
		WeeklyOpusCostLim  float64 `json:"weeklyOpusCostLimit"`
		BoundAccountID     string  `json:"boundAccountId"`
		ExpiresInDays      int     `json:"expiresInDays"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	keyID, plaintext, err := a.authMw.CreateAPIKey(r.Context(), req.Name, req.ConcurrencyLimit, req.WeeklyOpusCostLim, req.BoundAccountID, req.ExpiresInDays)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"id":   keyID,
		"key":  plaintext,
		"name": req.Name,
	})
}

func (a *AdminHandler) deleteKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.authMw.DeleteAPIKey(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Status ---

func (a *AdminHandler) status(w http.ResponseWriter, r *http.Request) {
	accounts, _ := a.accounts.List(r.Context())
	total := len(accounts)
	active, errCount, overloaded := 0, 0, 0
	for _, acct := range accounts {
		switch acct.Status {
		case "active":
			active++
		case "error":
			errCount++
		}
		if acct.OverloadedUntil != nil && time.Now().Before(*acct.OverloadedUntil) {
			overloaded++
		}
	}

	redisStatus := "connected"
	if err := a.store.Ping(r.Context()); err != nil {
		redisStatus = "error: " + err.Error()
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"accounts": map[string]int{
			"total":      total,
			"active":     active,
			"error":      errCount,
			"overloaded": overloaded,
		},
		"redis": redisStatus,
	})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func verifyPassword(input, stored string) bool {
	// Support plain text comparison (for env var config)
	if input == stored {
		return true
	}
	// Support SHA-256 hashed password
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:]) == stored
}
