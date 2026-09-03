package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/yansircc/llm-broker/internal/domain"
	"github.com/yansircc/llm-broker/internal/driver"
)

func (s *Server) registerRoutes(mux *http.ServeMux) {
	s.registerRelayRoutes(mux)
	s.registerAdminRoutes(mux)
	s.registerOperationalRoutes(mux)
	s.mountUIRoutes(mux)
}

func (s *Server) registerRelayRoutes(mux *http.ServeMux) {
	auth := s.authMw.Authenticate
	native := func(handler http.Handler) http.Handler {
		return auth(requireSurfaceHandler(domain.SurfaceNative, handler))
	}
	compat := func(handler http.Handler) http.Handler {
		return auth(requireSurfaceHandler(domain.SurfaceCompat, handler))
	}

	mux.Handle("GET /v1/models", native(http.HandlerFunc(s.handleListModels)))
	mux.Handle("GET /compat/v1/models", compat(http.HandlerFunc(s.handleCompatListModels)))
	mux.Handle("POST /compat/v1/chat/completions", compat(http.HandlerFunc(s.handleCompatOpenAIChatCompletions)))

	for _, provider := range sortedProviders(s.catalogDrivers) {
		drv := s.catalogDrivers[provider]
		for _, path := range drv.Info().RelayPaths {
			mux.Handle("POST "+path, native(http.HandlerFunc(s.relay.HandleProviderSurface(provider, domain.SurfaceNative))))
		}
	}
}

func (s *Server) registerAdminRoutes(mux *http.ServeMux) {
	auth := s.authMw.Authenticate
	admin := func(handler http.HandlerFunc) http.Handler {
		return auth(requireAdminHandler(http.HandlerFunc(handler)))
	}

	mux.Handle("GET /admin/providers", admin(s.handleListProviders))
	mux.Handle("POST /admin/accounts/generate-auth-url", admin(s.handleGenerateAuthURL))
	mux.Handle("POST /admin/accounts/exchange-code", admin(s.handleExchangeCode))
	mux.Handle("GET /admin/accounts", admin(s.handleListAccounts))
	mux.Handle("GET /admin/accounts/{id}", admin(s.handleGetAccount))
	mux.Handle("DELETE /admin/accounts/{id}", admin(s.handleDeleteAccount))
	mux.Handle("POST /admin/accounts/{id}/email", admin(s.handleUpdateAccountEmail))
	mux.Handle("POST /admin/accounts/{id}/status", admin(s.handleUpdateAccountStatus))
	mux.Handle("POST /admin/accounts/{id}/weight", admin(s.handleUpdateAccountWeight))
	mux.Handle("POST /admin/accounts/{id}/cell", admin(s.handleBindAccountCell))
	mux.Handle("POST /admin/accounts/{id}/refresh", admin(s.handleRefreshAccount))
	mux.Handle("POST /admin/accounts/{id}/test", admin(s.handleTestAccount))
	mux.Handle("GET /admin/egress/cells", admin(s.handleListEgressCells))
	mux.Handle("POST /admin/egress/cells", admin(s.handleUpsertEgressCell))
	mux.Handle("POST /admin/egress/cells/test-proxy", admin(s.handleTestProxy))
	mux.Handle("POST /admin/egress/cells/{id}/clear-cooldown", admin(s.handleClearEgressCellCooldown))
	mux.Handle("POST /admin/egress/cells/{id}/test", admin(s.handleTestEgressCell))

	mux.Handle("DELETE /admin/events", admin(s.handleClearEvents))
	mux.HandleFunc("POST /admin/login", s.handleLogin)

	mux.Handle("POST /admin/users", admin(s.handleCreateUser))
	mux.Handle("GET /admin/users", admin(s.handleListUsers))
	mux.Handle("GET /admin/users/total-costs", admin(s.handleListUserTotalCosts))
	mux.Handle("GET /admin/users/{id}", admin(s.handleGetUser))
	mux.Handle("DELETE /admin/users/{id}", admin(s.handleDeleteUser))
	mux.Handle("POST /admin/users/{id}/regenerate", admin(s.handleRegenerateUserToken))
	mux.Handle("POST /admin/users/{id}/status", admin(s.handleUpdateUserStatus))
	mux.Handle("POST /admin/users/{id}/policy", admin(s.handleUpdateUserPolicy))

	mux.Handle("GET /admin/activity", admin(s.handleActivity))
	mux.Handle("GET /admin/activity/usage", admin(s.handleActivityUsage))
	mux.Handle("GET /admin/dashboard", admin(s.handleDashboard))
	mux.Handle("GET /admin/health", admin(s.handleHealth))
	mux.Handle("DELETE /admin/sessions/binding/{uuid}", admin(s.handleUnbindSession))
}

func (s *Server) registerOperationalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/event_logging/batch", s.handleTelemetryBatch)
	mux.HandleFunc("GET /api/status", s.handlePublicStatus)
	mux.HandleFunc("GET /health", s.handleHealthCheck)
}

func (s *Server) handleTelemetryBatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"success":true}`))
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"status":"error","store":"%s"}`, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

type modelsResponse struct {
	Object string         `json:"object"`
	Data   []driver.Model `json:"data"`
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	data := make([]driver.Model, 0)
	for _, provider := range sortedProviders(s.catalogDrivers) {
		data = append(data, s.catalogDrivers[provider].Models()...)
	}
	json.NewEncoder(w).Encode(modelsResponse{Object: "list", Data: data})
}
