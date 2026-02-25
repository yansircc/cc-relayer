package main

import (
	"log/slog"
	"os"

	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/config"
	"github.com/yansir/cc-relayer/internal/events"
	"github.com/yansir/cc-relayer/internal/server"
	"github.com/yansir/cc-relayer/internal/store"
	"github.com/yansir/cc-relayer/internal/transport"
)

var version = "dev"

func main() {
	// Load configuration
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		slog.Error("config validation failed", "error", err)
		os.Exit(1)
	}

	// Setup logging with ring buffer handler
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logHandler := events.NewLogHandler(level, 1000)
	slog.SetDefault(slog.New(logHandler))
	slog.Info("cc-relayer starting", "version", version)

	// Open SQLite database
	s, err := store.New(cfg.DBPath)
	if err != nil {
		slog.Error("database init failed", "error", err)
		os.Exit(1)
	}
	defer s.Close()
	slog.Info("database ready", "path", cfg.DBPath)

	// Initialize crypto (derive keys at startup)
	crypto := account.NewCrypto(cfg.EncryptionKey)
	if _, err := crypto.DeriveKey("salt"); err != nil {
		slog.Error("key derivation failed", "error", err)
		os.Exit(1)
	}
	slog.Info("encryption key derived")

	// Initialize transport manager (per-account utls + proxy)
	tm := transport.NewManager(cfg)
	defer tm.Close()

	// Initialize event bus
	bus := events.NewBus(200)

	// Start server
	srv := server.New(cfg, s, crypto, tm, bus, logHandler, version)
	if err := srv.Run(); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
