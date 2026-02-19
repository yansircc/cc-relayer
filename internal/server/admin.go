package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/yansir/cc-relayer/internal/account"
)

// handleOAuthAdd completes Cookie OAuth and adds/updates an account.
func (s *Server) handleOAuthAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionKey string `json:"sessionKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	if req.SessionKey == "" {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "sessionKey is required")
		return
	}

	result, err := account.CookieOAuth(r.Context(), req.SessionKey)
	if err != nil {
		slog.Error("cookie oauth failed", "error", err)
		writeAdminError(w, http.StatusBadGateway, "oauth_error", err.Error())
		return
	}

	// Dedup: find existing account by orgUUID
	existing, err := s.findAccountByOrgUUID(r, result.OrgUUID)
	if err != nil {
		slog.Error("list accounts failed", "error", err)
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to list accounts")
		return
	}

	extInfo := map[string]interface{}{
		"orgUUID": result.OrgUUID,
		"orgName": result.OrgName,
		"email":   result.Email,
	}
	extInfoJSON, _ := json.Marshal(extInfo)

	if existing != nil {
		// Update existing account tokens
		if err := s.accounts.StoreTokens(r.Context(), existing.ID, result.AccessToken, result.RefreshToken, result.ExpiresIn); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to store tokens")
			return
		}
		_ = s.accounts.Update(r.Context(), existing.ID, map[string]string{
			"name":    result.Email,
			"extInfo": string(extInfoJSON),
		})

		slog.Info("account updated via oauth", "id", existing.ID, "email", result.Email)
		writeJSON(w, http.StatusOK, map[string]string{
			"id":     existing.ID,
			"name":   result.Email,
			"email":  result.Email,
			"status": "active",
		})
		return
	}

	// Create new account
	acct, err := s.accounts.Create(r.Context(), result.Email, result.RefreshToken, nil, 50)
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

	slog.Info("account created via oauth", "id", acct.ID, "email", result.Email)
	writeJSON(w, http.StatusOK, map[string]string{
		"id":     acct.ID,
		"name":   result.Email,
		"email":  result.Email,
		"status": "active",
	})
}

// handleListAccounts returns all accounts (without tokens).
func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.accounts.List(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to list accounts")
		return
	}

	type accountView struct {
		ID       string                 `json:"id"`
		Name     string                 `json:"name"`
		Status   string                 `json:"status"`
		Priority int                    `json:"priority"`
		ExtInfo  map[string]interface{} `json:"extInfo,omitempty"`
	}

	views := make([]accountView, 0, len(accounts))
	for _, a := range accounts {
		views = append(views, accountView{
			ID:       a.ID,
			Name:     a.Name,
			Status:   a.Status,
			Priority: a.Priority,
			ExtInfo:  a.ExtInfo,
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

	slog.Info("account deleted", "id", id, "name", acct.Name)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
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

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeAdminError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"error","error":{"type":"%s","message":"%s"}}`, errType, msg)
}
