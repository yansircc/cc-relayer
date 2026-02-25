package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/identity"
)

// handleListAccounts returns all accounts (without tokens).
func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.accounts.List(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to list accounts")
		return
	}

	type accountView struct {
		ID                 string                 `json:"id"`
		Email              string                 `json:"email"`
		Status             string                 `json:"status"`
		Priority           int                    `json:"priority"`
		PriorityMode       string                 `json:"priority_mode"`
		Schedulable        bool                   `json:"schedulable"`
		ExtInfo            map[string]interface{} `json:"extInfo,omitempty"`
		LastUsedAt         *time.Time             `json:"lastUsedAt,omitempty"`
		OverloadedUntil    *time.Time             `json:"overloadedUntil,omitempty"`
		FiveHourStatus     string                 `json:"fiveHourStatus"`
		OpusRateLimitEndAt *time.Time             `json:"opusRateLimitEndAt,omitempty"`
	}

	views := make([]accountView, 0, len(accounts))
	for _, a := range accounts {
		views = append(views, accountView{
			ID:                 a.ID,
			Email:              a.Email,
			Status:             a.Status,
			Priority:           a.Priority,
			PriorityMode:       a.PriorityMode,
			Schedulable:        a.Schedulable,
			ExtInfo:            a.ExtInfo,
			LastUsedAt:         a.LastUsedAt,
			OverloadedUntil:    a.OverloadedUntil,
			FiveHourStatus:     a.FiveHourStatus,
			OpusRateLimitEndAt: a.OpusRateLimitEndAt,
		})
	}
	writeJSON(w, http.StatusOK, views)
}

// handleDeleteAccount removes an account by ID.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "account id is required")
		return
	}

	acct, err := s.accounts.Get(r.Context(), id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to get account")
		return
	}
	if acct == nil {
		writeAdminError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}

	if err := s.accounts.Delete(r.Context(), id); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to delete account")
		return
	}

	slog.Info("account deleted", "id", id, "email", acct.Email)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// ---------------------------------------------------------------------------
// Account detail (authenticated)
// ---------------------------------------------------------------------------

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "account id is required")
		return
	}

	acct, err := s.accounts.Get(r.Context(), id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to get account")
		return
	}
	if acct == nil {
		writeAdminError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}

	// Parse stainless headers
	var stainless map[string]interface{}
	if hdrs, err := s.store.GetStainlessHeaders(r.Context(), id); err == nil && hdrs != "" {
		json.Unmarshal([]byte(hdrs), &stainless)
	}

	// Session bindings for this account
	sessions, _ := s.store.ListSessionBindingsForAccount(r.Context(), id)

	// Compute auto priority score
	var autoScore int
	if acct.PriorityMode == "auto" && s.cfg.Limit5HCost > 0 {
		costs, _ := s.store.QueryAccountCosts(r.Context())
		if info, ok := costs[acct.ID]; ok {
			remaining := 1.0 - info.FiveHourCost/s.cfg.Limit5HCost
			if remaining < 0 {
				remaining = 0
			}
			autoScore = int(remaining * 100)
		} else {
			autoScore = 100
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":                 acct.ID,
		"email":              acct.Email,
		"status":             acct.Status,
		"priority":           acct.Priority,
		"priority_mode":      acct.PriorityMode,
		"auto_score":         autoScore,
		"schedulable":        acct.Schedulable,
		"errorMessage":       acct.ErrorMessage,
		"extInfo":            acct.ExtInfo,
		"createdAt":          acct.CreatedAt,
		"lastUsedAt":         acct.LastUsedAt,
		"lastRefreshAt":      acct.LastRefreshAt,
		"expiresAt":          acct.ExpiresAt,
		"fiveHourStatus":     acct.FiveHourStatus,
		"overloadedUntil":    acct.OverloadedUntil,
		"opusRateLimitEndAt": acct.OpusRateLimitEndAt,
		"stainless":          stainless,
		"sessions":           sessions,
	})
}

// ---------------------------------------------------------------------------
// Account actions (authenticated)
// ---------------------------------------------------------------------------

func (s *Server) handleUpdateAccountEmail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || len(req.Email) > 100 {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "email must be 1-100 characters")
		return
	}

	acct, err := s.accounts.Get(r.Context(), id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to get account")
		return
	}
	if acct == nil {
		writeAdminError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}

	if err := s.accounts.Update(r.Context(), id, map[string]string{"email": req.Email}); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to update account email")
		return
	}
	slog.Info("account email updated", "id", id, "email", req.Email)
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "email": req.Email})
}

func (s *Server) handleUpdateAccountStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Status != "active" && req.Status != "disabled") {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "status must be 'active' or 'disabled'")
		return
	}

	acct, err := s.accounts.Get(r.Context(), id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to get account")
		return
	}
	if acct == nil {
		writeAdminError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}

	fields := map[string]string{"status": req.Status}
	if req.Status == "disabled" {
		fields["schedulable"] = "false"
	} else {
		fields["schedulable"] = "true"
		fields["errorMessage"] = ""
	}
	if err := s.accounts.Update(r.Context(), id, fields); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to update account status")
		return
	}
	slog.Info("account status updated", "id", id, "status", req.Status)
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": req.Status})
}

func (s *Server) handleUpdateAccountPriority(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Mode     string `json:"mode"`
		Priority int    `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}

	acct, err := s.accounts.Get(r.Context(), id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to get account")
		return
	}
	if acct == nil {
		writeAdminError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}

	if req.Mode == "" {
		req.Mode = "manual"
	}
	if req.Mode != "auto" && req.Mode != "manual" {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "mode must be 'auto' or 'manual'")
		return
	}

	fields := map[string]string{"priorityMode": req.Mode}
	if req.Mode == "manual" {
		fields["priority"] = strconv.Itoa(req.Priority)
	}

	if err := s.accounts.Update(r.Context(), id, fields); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to update priority")
		return
	}
	slog.Info("account priority updated", "id", id, "mode", req.Mode, "priority", req.Priority)
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": id, "mode": req.Mode, "priority": req.Priority})
}

func (s *Server) handleRefreshAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	acct, err := s.accounts.Get(r.Context(), id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to get account")
		return
	}
	if acct == nil {
		writeAdminError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}

	accessToken, err := s.tokens.ForceRefresh(r.Context(), id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "token refresh failed: "+err.Error())
		return
	}
	slog.Info("account token force refreshed", "id", id)

	// Back-fill org UUID if missing — extract from API response header
	if acct.ExtInfo == nil || acct.ExtInfo["orgUUID"] == nil || acct.ExtInfo["orgUUID"] == "" {
		orgUUID := s.fetchOrgUUIDViaAPI(r.Context(), acct, accessToken)
		if orgUUID != "" {
			extInfo := map[string]interface{}{
				"orgUUID": orgUUID,
				"orgName": acct.ExtInfo["orgName"],
				"email":   acct.ExtInfo["email"],
			}
			extInfoJSON, _ := json.Marshal(extInfo)
			_ = s.accounts.Update(r.Context(), id, map[string]string{
				"extInfo": string(extInfoJSON),
			})
			slog.Info("account org UUID back-filled", "id", id, "orgUUID", orgUUID)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "refreshed"})
}

// ---------------------------------------------------------------------------
// Account test endpoint
// ---------------------------------------------------------------------------

func (s *Server) handleTestAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	acct, err := s.accounts.Get(r.Context(), id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to get account")
		return
	}
	if acct == nil {
		writeAdminError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}

	accessToken, err := s.tokens.EnsureValidToken(r.Context(), acct.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":    false,
			"error": "token unavailable: " + err.Error(),
		})
		return
	}

	// Build test request: minimal messages call
	testBody := `{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	testURL := s.cfg.ClaudeAPIURL
	testReq, err := http.NewRequestWithContext(r.Context(), "POST", testURL, strings.NewReader(testBody))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":    false,
			"error": "failed to create request",
		})
		return
	}
	testReq.Header.Set("Content-Type", "application/json")
	identity.SetRequiredHeaders(testReq.Header, accessToken, s.cfg.ClaudeAPIVersion, s.cfg.ClaudeBetaHeader)

	client := s.transportMgr.GetClient(acct)
	start := time.Now()
	resp, err := client.Do(testReq)
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":         false,
			"latency_ms": latencyMs,
			"error":      err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body) // drain

	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":         false,
			"latency_ms": latencyMs,
			"error":      fmt.Sprintf("upstream returned %d", resp.StatusCode),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"latency_ms": latencyMs,
	})
}

// fetchOrgUUIDViaAPI makes a minimal API call and extracts the org UUID
// from the Anthropic-Organization-Id response header.
func (s *Server) fetchOrgUUIDViaAPI(ctx context.Context, acct *account.Account, accessToken string) string {
	testBody := `{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequestWithContext(ctx, "POST", s.cfg.ClaudeAPIURL, strings.NewReader(testBody))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	identity.SetRequiredHeaders(req.Header, accessToken, s.cfg.ClaudeAPIVersion, s.cfg.ClaudeBetaHeader)

	client := s.transportMgr.GetClient(acct)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	return resp.Header.Get("Anthropic-Organization-Id")
}
