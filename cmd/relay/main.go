package main

import (
	"log/slog"
	"os"

	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/config"
	"github.com/yansir/cc-relayer/internal/server"
	"github.com/yansir/cc-relayer/internal/store"
	"github.com/yansir/cc-relayer/internal/transport"
)

func main() {
	// Load configuration
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		slog.Error("config validation failed", "error", err)
		os.Exit(1)
	}

	// Setup logging
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	// Connect to Redis
	s, err := store.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		slog.Error("redis connection failed", "error", err)
		os.Exit(1)
	}
	defer s.Close()
	slog.Info("redis connected", "addr", cfg.RedisAddr)

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

	// Start server
	srv := server.New(cfg, s, crypto, tm)
	if err := srv.Run(); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
