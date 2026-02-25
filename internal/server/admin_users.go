package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/yansir/cc-relayer/internal/store"
)

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

	writeJSON(w, http.StatusOK, users)
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
// User detail (admin only)
// ---------------------------------------------------------------------------

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()

	users, err := s.store.ListUsers(ctx)
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

	usage, _ := s.store.QueryUsagePeriods(ctx, id)
	modelUsage, _ := s.store.QueryModelUsage(ctx, id)
	recentRequests, _, _ := s.store.QueryRequestLogs(ctx, store.RequestLogQuery{
		UserID: id,
		Limit:  20,
	})

	if usage == nil {
		usage = []store.UsagePeriod{}
	}
	if modelUsage == nil {
		modelUsage = []store.ModelUsageRow{}
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
		"usage":           usage,
		"model_usage":     modelUsage,
		"recent_requests": recentRequests,
	})
}
