package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/auth"
	"github.com/yansir/cc-relayer/internal/store"
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
		slog.Warn("fetch org info failed, using fallback", "error", err)
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
			"name":    email,
			"status":  "active",
			"extInfo": string(extInfoJSON),
		})

		slog.Info("account updated via code exchange", "id", existing.ID, "email", email)
		writeJSON(w, http.StatusOK, map[string]string{
			"id":     existing.ID,
			"name":   email,
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
		"name":   email,
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
		ID                 string                 `json:"id"`
		Name               string                 `json:"name"`
		Status             string                 `json:"status"`
		Priority           int                    `json:"priority"`
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
			Name:               a.Name,
			Status:             a.Status,
			Priority:           a.Priority,
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

// ---------------------------------------------------------------------------
// Auth helpers
// ---------------------------------------------------------------------------

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	ki := auth.GetKeyInfo(r.Context())
	if ki == nil || !ki.IsAdmin {
		writeAdminError(w, http.StatusForbidden, "forbidden", "admin access required")
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Login
// ---------------------------------------------------------------------------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}

	// Quick validation: try admin first, then user
	ki, valid := s.authMw.ValidateToken(r.Context(), req.Token)
	if !valid || ki == nil {
		writeAdminError(w, http.StatusUnauthorized, "authentication_error", "invalid token")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "cc_session",
		Value:    req.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 30,
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ok",
		"is_admin": ki.IsAdmin,
		"name":     ki.Name,
	})
}

// ---------------------------------------------------------------------------
// User CRUD (admin only)
// ---------------------------------------------------------------------------

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}

	plaintext, hashStr, prefix := generateUserToken(req.Name)
	u := &store.User{
		ID:          uuid.New().String(),
		Name:        req.Name,
		TokenHash:   hashStr,
		TokenPrefix: prefix,
		Status:      "active",
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.store.CreateUser(r.Context(), u); err != nil {
		slog.Error("create user failed", "error", err)
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to create user")
		return
	}

	slog.Info("user created", "id", u.ID, "name", u.Name)
	writeJSON(w, http.StatusOK, map[string]string{
		"id":    u.ID,
		"name":  u.Name,
		"token": plaintext,
	})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to list users")
		return
	}

	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)

	type userView struct {
		ID           string                 `json:"id"`
		Name         string                 `json:"name"`
		TokenPrefix  string                 `json:"token_prefix"`
		Status       string                 `json:"status"`
		CreatedAt    time.Time              `json:"created_at"`
		LastActiveAt *time.Time             `json:"last_active_at,omitempty"`
		TodayUsage   *store.UsageSummaryRow `json:"today_usage"`
		WeekUsage    *store.UsageSummaryRow `json:"week_usage"`
	}
	views := make([]userView, 0, len(users))
	for _, u := range users {
		v := userView{
			ID:           u.ID,
			Name:         u.Name,
			TokenPrefix:  u.TokenPrefix,
			Status:       u.Status,
			CreatedAt:    u.CreatedAt,
			LastActiveAt: u.LastActiveAt,
		}

		todayRows, _ := s.store.QueryUsageSummary(r.Context(), store.UsageQueryOpts{
			UserID: u.ID, Since: todayStart, GroupBy: "all",
		})
		if len(todayRows) > 0 {
			v.TodayUsage = todayRows[0]
		}

		weekRows, _ := s.store.QueryUsageSummary(r.Context(), store.UsageQueryOpts{
			UserID: u.ID, Since: sevenDaysAgo, GroupBy: "all",
		})
		if len(weekRows) > 0 {
			v.WeekUsage = weekRows[0]
		}

		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to delete user")
		return
	}
	slog.Info("user deleted", "id", id)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleRegenerateUserToken(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")

	// We need the user's name for the token format
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to lookup user")
		return
	}
	var userName string
	for _, u := range users {
		if u.ID == id {
			userName = u.Name
			break
		}
	}
	if userName == "" {
		writeAdminError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}

	plaintext, hashStr, prefix := generateUserToken(userName)
	if err := s.store.UpdateUserToken(r.Context(), id, hashStr, prefix); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to update token")
		return
	}

	slog.Info("user token regenerated", "id", id)
	writeJSON(w, http.StatusOK, map[string]string{
		"id":    id,
		"token": plaintext,
	})
}

func (s *Server) handleUpdateUserStatus(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Status != "active" && req.Status != "disabled") {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "status must be 'active' or 'disabled'")
		return
	}
	if err := s.store.UpdateUserStatus(r.Context(), id, req.Status); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to update user status")
		return
	}
	slog.Info("user status updated", "id", id, "status", req.Status)
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": req.Status})
}

func generateUserToken(name string) (plaintext, hashStr, prefix string) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	hexStr := hex.EncodeToString(b)
	plaintext = fmt.Sprintf("tk_%s_%s", name, hexStr)
	h := sha256.Sum256([]byte(plaintext))
	hashStr = hex.EncodeToString(h[:])
	prefix = fmt.Sprintf("tk_%s_%s...", name, hexStr[:4])
	return
}

// ---------------------------------------------------------------------------
// Dashboard & Usage (admin only)
// ---------------------------------------------------------------------------

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	data, err := s.store.GetDashboardData(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to get dashboard data")
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	since := time.Now().Add(-7 * 24 * time.Hour)
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	opts := store.UsageQueryOpts{
		UserID:    r.URL.Query().Get("user_id"),
		AccountID: r.URL.Query().Get("account_id"),
		Since:     since,
		GroupBy:   r.URL.Query().Get("group_by"),
	}
	if opts.GroupBy == "" {
		opts.GroupBy = "day"
	}
	rows, err := s.store.QueryUsageSummary(r.Context(), opts)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to query usage")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleRequestLog(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	opts := store.RequestLogQuery{
		UserID:    r.URL.Query().Get("user_id"),
		AccountID: r.URL.Query().Get("account_id"),
		Limit:     limit,
		Offset:    offset,
	}
	logs, total, err := s.store.QueryRequestLogs(r.Context(), opts)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to query request logs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total": total,
		"items": logs,
	})
}

// ---------------------------------------------------------------------------
// Sessions (admin only)
// ---------------------------------------------------------------------------

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	bindings, _ := s.store.ListSessionBindings(r.Context())
	sticky, _ := s.store.ListStickySessions(r.Context())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"bindings": bindings,
		"sticky":   sticky,
	})
}

func (s *Server) handleDeleteSessionBinding(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	_ = s.store.DeleteSessionBinding(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleDeleteStickySession(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	_ = s.store.DeleteStickySession(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// ---------------------------------------------------------------------------
// SSE Events stream (admin only)
// ---------------------------------------------------------------------------

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Catch-up: send recent events
	eventID, eventCh, recentEvents := s.bus.Subscribe()
	defer s.bus.Unsubscribe(eventID)
	for _, e := range recentEvents {
		data, _ := json.Marshal(e)
		fmt.Fprintf(w, "event: event\ndata: %s\n\n", data)
	}

	// Catch-up: send recent logs
	logID, logCh, recentLogs := s.logHandler.Subscribe()
	defer s.logHandler.Unsubscribe(logID)
	for _, l := range recentLogs {
		data, _ := json.Marshal(l)
		fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
	}
	flusher.Flush()

	// Stream new events and logs
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-eventCh:
			if !ok {
				return
			}
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: event\ndata: %s\n\n", data)
			flusher.Flush()
		case l, ok := <-logCh:
			if !ok {
				return
			}
			data, _ := json.Marshal(l)
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// ---------------------------------------------------------------------------
// Health (authenticated)
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	sqliteStatus := "ok"
	if err := s.store.Ping(r.Context()); err != nil {
		sqliteStatus = err.Error()
	}
	d := time.Since(s.startTime)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	uptime := fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	writeJSON(w, http.StatusOK, map[string]string{
		"sqlite":  sqliteStatus,
		"uptime":  uptime,
		"version": s.version,
	})
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

	// Filter session bindings for this account
	allBindings, _ := s.store.ListSessionBindings(r.Context())
	var sessions []store.SessionBindingInfo
	for _, b := range allBindings {
		if b.AccountID == id {
			sessions = append(sessions, b)
		}
	}
	if sessions == nil {
		sessions = []store.SessionBindingInfo{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":                 acct.ID,
		"name":               acct.Name,
		"status":             acct.Status,
		"priority":           acct.Priority,
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
		Priority int `json:"priority"`
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

	if err := s.accounts.Update(r.Context(), id, map[string]string{
		"priority": strconv.Itoa(req.Priority),
	}); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to update priority")
		return
	}
	slog.Info("account priority updated", "id", id, "priority", req.Priority)
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": id, "priority": req.Priority})
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

	if _, err := s.tokens.ForceRefresh(r.Context(), id); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "token refresh failed: "+err.Error())
		return
	}
	slog.Info("account token force refreshed", "id", id)
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "refreshed"})
}

// ---------------------------------------------------------------------------
// User detail (admin only)
// ---------------------------------------------------------------------------

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")

	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to list users")
		return
	}
	var user *store.User
	for _, u := range users {
		if u.ID == id {
			user = u
			break
		}
	}
	if user == nil {
		writeAdminError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}

	now := time.Now().UTC()
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)

	dailyUsage, _ := s.store.QueryUsageSummary(r.Context(), store.UsageQueryOpts{
		UserID:  id,
		Since:   sevenDaysAgo,
		GroupBy: "day",
	})
	modelUsage, _ := s.store.QueryUsageSummary(r.Context(), store.UsageQueryOpts{
		UserID:  id,
		Since:   sevenDaysAgo,
		GroupBy: "model",
	})
	recentRequests, _, _ := s.store.QueryRequestLogs(r.Context(), store.RequestLogQuery{
		UserID: id,
		Limit:  20,
	})

	if dailyUsage == nil {
		dailyUsage = []*store.UsageSummaryRow{}
	}
	if modelUsage == nil {
		modelUsage = []*store.UsageSummaryRow{}
	}
	if recentRequests == nil {
		recentRequests = []*store.RequestLog{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":              user.ID,
		"name":            user.Name,
		"token_prefix":    user.TokenPrefix,
		"status":          user.Status,
		"created_at":      user.CreatedAt,
		"last_active_at":  user.LastActiveAt,
		"daily_usage":     dailyUsage,
		"model_usage":     modelUsage,
		"recent_requests": recentRequests,
		"stainless":       nil,
		"sessions":        []interface{}{},
	})
}

// ---------------------------------------------------------------------------
// OAuth sessions (admin only)
// ---------------------------------------------------------------------------

func (s *Server) handleListOAuthSessions(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	sessions, err := s.store.ListOAuthSessions(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", "failed to list oauth sessions")
		return
	}
	if sessions == nil {
		sessions = []store.OAuthSessionInfo{}
	}
	writeJSON(w, http.StatusOK, sessions)
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
