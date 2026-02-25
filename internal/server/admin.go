package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/yansir/cc-relayer/internal/auth"
)

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
// Dashboard (admin only) — single unified endpoint
// ---------------------------------------------------------------------------

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ctx := r.Context()

	// Health
	sqliteStatus := "ok"
	if err := s.store.Ping(ctx); err != nil {
		sqliteStatus = err.Error()
	}
	d := time.Since(s.startTime)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	uptime := fmt.Sprintf("%dd %dh %dm", days, hours, mins)

	// Usage periods
	usage, _ := s.store.QueryUsagePeriods(ctx, "")

	// Accounts with cost percentages
	acctList, _ := s.accounts.List(ctx)
	costs, _ := s.store.QueryAccountCosts(ctx)

	type accountView struct {
		ID              string     `json:"id"`
		Email           string     `json:"email"`
		Status          string     `json:"status"`
		PriorityMode    string     `json:"priority_mode"`
		Priority        int        `json:"priority"`
		OverloadedUntil *time.Time `json:"overloaded_until,omitempty"`
		LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
		FiveHourPct     float64    `json:"five_hour_pct"`
		SevenDayPct     float64    `json:"seven_day_pct"`
	}

	acctViews := make([]accountView, 0, len(acctList))
	for _, a := range acctList {
		fhPct := 100.0
		sdPct := 100.0
		if info, ok := costs[a.ID]; ok {
			if s.cfg.Limit5HCost > 0 {
				fhPct = math.Max(0, (1-info.FiveHourCost/s.cfg.Limit5HCost)*100)
			}
			if s.cfg.Limit7DCost > 0 {
				sdPct = math.Max(0, (1-info.SevenDayCost/s.cfg.Limit7DCost)*100)
			}
		}
		acctViews = append(acctViews, accountView{
			ID:              a.ID,
			Email:           a.Email,
			Status:          a.Status,
			PriorityMode:    a.PriorityMode,
			Priority:        a.Priority,
			OverloadedUntil: a.OverloadedUntil,
			LastUsedAt:      a.LastUsedAt,
			FiveHourPct:     math.Round(fhPct*10) / 10,
			SevenDayPct:     math.Round(sdPct*10) / 10,
		})
	}

	// Users with total cost
	users, _ := s.store.ListUsers(ctx)
	userCosts, _ := s.store.QueryUserTotalCosts(ctx)

	type userView struct {
		ID           string     `json:"id"`
		Name         string     `json:"name"`
		Status       string     `json:"status"`
		LastActiveAt *time.Time `json:"last_active_at,omitempty"`
		TotalCost    float64    `json:"total_cost"`
	}

	userViews := make([]userView, 0, len(users))
	for _, u := range users {
		userViews = append(userViews, userView{
			ID:           u.ID,
			Name:         u.Name,
			Status:       u.Status,
			LastActiveAt: u.LastActiveAt,
			TotalCost:    userCosts[u.ID],
		})
	}

	// Events from bus ring buffer
	recentEvents := s.bus.Recent(20)
	type eventInfo struct {
		Type      string `json:"type"`
		AccountID string `json:"account_id,omitempty"`
		Message   string `json:"message"`
		Timestamp string `json:"ts"`
	}
	evViews := make([]eventInfo, 0, len(recentEvents))
	for i := len(recentEvents) - 1; i >= 0; i-- {
		e := recentEvents[i]
		evViews = append(evViews, eventInfo{
			Type:      string(e.Type),
			AccountID: e.AccountID,
			Message:   e.Message,
			Timestamp: e.Timestamp.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"health": map[string]string{
			"sqlite":  sqliteStatus,
			"uptime":  uptime,
			"version": s.version,
		},
		"usage":    usage,
		"accounts": acctViews,
		"users":    userViews,
		"events":   evViews,
	})
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
// JSON helpers
// ---------------------------------------------------------------------------

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
