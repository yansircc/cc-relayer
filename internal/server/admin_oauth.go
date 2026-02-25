package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/identity"
)

// handleGenerateAuthURL generates a PKCE-secured auth URL for manual browser-based OAuth.
// Returns session_id and auth_url. PKCE params are stored with 10 min TTL.
func (s *Server) handleGenerateAuthURL(w http.ResponseWriter, r *http.Request) {
	authURL, session, err := account.GenerateAuthURL()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	sessionID := uuid.New().String()
	sessionJSON, _ := json.Marshal(session)

	if err := s.store.SetOAuthSession(r.Context(), sessionID, string(sessionJSON), 10*time.Minute); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to store oauth session")
		return
	}

	slog.Info("oauth auth URL generated", "sessionId", sessionID)
	writeJSON(w, http.StatusOK, map[string]string{
		"session_id": sessionID,
		"auth_url":   authURL,
	})
}

// handleExchangeCode accepts an auth code and exchanges it for tokens.
// Supports two modes:
//   - session_id mode: pass session_id + code (or callback_url). PKCE params from store.
//   - direct mode: pass code + code_verifier + state directly.
func (s *Server) handleExchangeCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		// Session mode
		SessionID   string `json:"session_id"`
		CallbackURL string `json:"callback_url"`
		// Direct mode
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
		State        string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}

	// Session mode: look up PKCE from store
	if req.SessionID != "" {
		sessionJSON, err := s.store.GetDelOAuthSession(r.Context(), req.SessionID)
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, "invalid_request", "invalid or expired session_id")
			return
		}
		var session account.OAuthSession
		if err := json.Unmarshal([]byte(sessionJSON), &session); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "internal_error", "corrupt session data")
			return
		}
		req.CodeVerifier = session.CodeVerifier
		req.State = session.State
		// Extract code from callback URL if provided
		if req.CallbackURL != "" && req.Code == "" {
			req.Code = account.ExtractCodeFromCallback(req.CallbackURL)
		}
	}
	if req.Code != "" {
		req.Code = account.ExtractCodeFromCallback(req.Code)
	}

	if req.Code == "" || req.CodeVerifier == "" || req.State == "" {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "code, code_verifier, and state are required")
		return
	}

	result, err := account.ExchangeCode(r.Context(), req.Code, req.CodeVerifier, req.State)
	if err != nil {
		slog.Error("exchange code failed", "error", err)
		writeAdminError(w, http.StatusBadGateway, "oauth_error", err.Error())
		return
	}

	// Auto-fetch org info using the new access token
	orgUUID, email, orgName, err := account.FetchOrgWithToken(r.Context(), result.AccessToken)
	if err != nil {
		slog.Warn("fetch org info via claude.ai failed, trying API header", "error", err)
		// Fallback: extract org UUID from API response header
		orgUUID = fetchOrgUUIDFromAPIHeader(r.Context(), s.cfg.ClaudeAPIURL, result.AccessToken, s.cfg.ClaudeAPIVersion, s.cfg.ClaudeBetaHeader)
		email = "account-" + time.Now().Format("0102-1504")
	}

	// Dedup: find existing account by orgUUID
	existing, err := s.findAccountByOrgUUID(r, orgUUID)
	if err != nil {
		slog.Error("list accounts failed", "error", err)
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to list accounts")
		return
	}

	extInfo := map[string]interface{}{
		"orgUUID": orgUUID,
		"orgName": orgName,
		"email":   email,
	}
	extInfoJSON, _ := json.Marshal(extInfo)

	if existing != nil {
		if err := s.accounts.StoreTokens(r.Context(), existing.ID, result.AccessToken, result.RefreshToken, result.ExpiresIn); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to store tokens")
			return
		}
		_ = s.accounts.Update(r.Context(), existing.ID, map[string]string{
			"email":   email,
			"status":  "active",
			"extInfo": string(extInfoJSON),
		})

		slog.Info("account updated via code exchange", "id", existing.ID, "email", email)
		writeJSON(w, http.StatusOK, map[string]string{
			"id":     existing.ID,
			"email":  email,
			"status": "active",
		})
		return
	}

	acct, err := s.accounts.Create(r.Context(), email, result.RefreshToken, nil, 50)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to create account")
		return
	}

	if err := s.accounts.StoreTokens(r.Context(), acct.ID, result.AccessToken, result.RefreshToken, result.ExpiresIn); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to store tokens")
		return
	}
	_ = s.accounts.Update(r.Context(), acct.ID, map[string]string{
		"extInfo": string(extInfoJSON),
	})

	slog.Info("account created via code exchange", "id", acct.ID, "email", email)
	writeJSON(w, http.StatusOK, map[string]string{
		"id":     acct.ID,
		"email":  email,
		"status": "active",
	})
}

// findAccountByOrgUUID looks for an existing account matching the given orgUUID.
func (s *Server) findAccountByOrgUUID(r *http.Request, orgUUID string) (*account.Account, error) {
	accounts, err := s.accounts.List(r.Context())
	if err != nil {
		return nil, err
	}
	for _, a := range accounts {
		if a.ExtInfo != nil {
			if uuid, ok := a.ExtInfo["orgUUID"].(string); ok && uuid == orgUUID {
				return a, nil
			}
		}
	}
	return nil, nil
}

// fetchOrgUUIDFromAPIHeader makes a minimal Anthropic API call and extracts
// the org UUID from the Anthropic-Organization-Id response header.
func fetchOrgUUIDFromAPIHeader(ctx context.Context, apiURL, accessToken, apiVersion, betaHeader string) string {
	body := `{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	identity.SetRequiredHeaders(req.Header, accessToken, apiVersion, betaHeader)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	return resp.Header.Get("Anthropic-Organization-Id")
}
