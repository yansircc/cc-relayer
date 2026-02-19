package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/auth"
	"github.com/yansir/cc-relayer/internal/config"
	"github.com/yansir/cc-relayer/internal/identity"
	"github.com/yansir/cc-relayer/internal/ratelimit"
	"github.com/yansir/cc-relayer/internal/relay"
	"github.com/yansir/cc-relayer/internal/scheduler"
	"github.com/yansir/cc-relayer/internal/store"
	"github.com/yansir/cc-relayer/internal/transport"
)

// Server is the main HTTP server.
type Server struct {
	cfg          *config.Config
	store        *store.Store
	accounts     *account.AccountStore
	tokens       *account.TokenManager
	authMw       *auth.Middleware
	scheduler    *scheduler.Scheduler
	transformer  *identity.Transformer
	rateLimit    *ratelimit.Manager
	relay        *relay.Relay
	transportMgr *transport.Manager
	httpServer   *http.Server
}

func New(cfg *config.Config, s *store.Store, crypto *account.Crypto, tm *transport.Manager) *Server {
	as := account.NewAccountStore(s, crypto)
	tokMgr := account.NewTokenManager(s, as, cfg, tm)
	authMw := auth.NewMiddleware(cfg)
	sched := scheduler.New(s, as, cfg)
	trans := identity.NewTransformer(s, cfg)
	rl := ratelimit.NewManager(s)
	r := relay.New(s, as, tokMgr, sched, trans, rl, cfg, tm)

	srv := &Server{
		cfg:          cfg,
		store:        s,
		accounts:     as,
		tokens:       tokMgr,
		authMw:       authMw,
		scheduler:    sched,
		transformer:  trans,
		rateLimit:    rl,
		relay:        r,
		transportMgr: tm,
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	srv.httpServer = &http.Server{
		Addr:           fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:        mux,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   cfg.RequestTimeout + 30*time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB
	}

	return srv
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Relay endpoint (authenticated)
	mux.Handle("POST /v1/messages", s.authMw.Authenticate(http.HandlerFunc(s.relay.Handle)))

	// Telemetry sink — intercept without authentication
	mux.HandleFunc("POST /api/event_logging/batch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true}`))
	})

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"status":"error","redis":"%s"}`, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
}

// Run starts the server and blocks until shutdown.
func (s *Server) Run() error {
	// Start background cleanup goroutines
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.rateLimit.RunCleanup(ctx, 5*time.Minute)
	go s.transportMgr.RunCleanup(ctx)

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		slog.Info("server starting", "addr", s.httpServer.Addr)
		errCh <- s.httpServer.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		slog.Info("shutdown signal received", "signal", sig)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		return s.httpServer.Shutdown(shutdownCtx)
	}
}
