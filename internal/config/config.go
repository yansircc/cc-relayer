package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	// Server
	Host string
	Port int

	// Redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// Security
	EncryptionKey string
	JWTSecret     string
	APIKeyPrefix  string

	// Admin
	AdminUsername string
	AdminPassword string

	// Claude API
	ClaudeAPIURL     string
	ClaudeAPIVersion string
	ClaudeBetaHeader string
	OAuthClientID    string
	OAuthTokenURL    string

	// Proxy
	DefaultProxyTimeout time.Duration

	// Scheduling
	StickySessionTTL    time.Duration
	SessionBindingTTL   time.Duration
	OverloadedCooldown  time.Duration
	TokenRefreshAdvance time.Duration

	// Rate limiting
	ConcurrencySlotTTL time.Duration

	// Request
	RequestTimeout    time.Duration
	MaxRequestBodyMB  int
	MaxRetryAccounts  int
	MaxCacheControls  int

	// Logging
	LogLevel string
}

func Load() *Config {
	return &Config{
		Host: envOr("HOST", "0.0.0.0"),
		Port: envInt("PORT", 3000),

		RedisAddr:     envOr("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword: envOr("REDIS_PASSWORD", ""),
		RedisDB:       envInt("REDIS_DB", 0),

		EncryptionKey: os.Getenv("ENCRYPTION_KEY"),
		JWTSecret:     os.Getenv("JWT_SECRET"),
		APIKeyPrefix:  envOr("API_KEY_PREFIX", "cr_"),

		AdminUsername: envOr("ADMIN_USERNAME", "admin"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),

		ClaudeAPIURL:     envOr("CLAUDE_API_URL", "https://api.anthropic.com/v1/messages"),
		ClaudeAPIVersion: envOr("CLAUDE_API_VERSION", "2023-06-01"),
		ClaudeBetaHeader: envOr("CLAUDE_BETA_HEADER", "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"),
		OAuthClientID:    envOr("OAUTH_CLIENT_ID", "9d1c250a-e61b-44d9-88ed-5944d1962f5e"),
		OAuthTokenURL:    envOr("OAUTH_TOKEN_URL", "https://console.anthropic.com/v1/oauth/token"),

		DefaultProxyTimeout: envDuration("DEFAULT_PROXY_TIMEOUT", 60*time.Second),

		StickySessionTTL:    envDuration("STICKY_SESSION_TTL", time.Hour),
		SessionBindingTTL:   envDuration("SESSION_BINDING_TTL", 24*time.Hour),
		OverloadedCooldown:  envDuration("OVERLOADED_COOLDOWN", 5*time.Minute),
		TokenRefreshAdvance: envDuration("TOKEN_REFRESH_ADVANCE", 60*time.Second),

		ConcurrencySlotTTL: envDuration("CONCURRENCY_SLOT_TTL", 300*time.Second),

		RequestTimeout:   envDuration("REQUEST_TIMEOUT", 5*time.Minute),
		MaxRequestBodyMB: envInt("REQUEST_MAX_SIZE_MB", 60),
		MaxRetryAccounts: envInt("MAX_RETRY_ACCOUNTS", 2),
		MaxCacheControls: envInt("MAX_CACHE_CONTROLS", 4),

		LogLevel: envOr("LOG_LEVEL", "info"),
	}
}

func (c *Config) Validate() error {
	if c.EncryptionKey == "" {
		return errMissing("ENCRYPTION_KEY")
	}
	if c.JWTSecret == "" {
		return errMissing("JWT_SECRET")
	}
	if c.AdminPassword == "" {
		return errMissing("ADMIN_PASSWORD")
	}
	return nil
}

type configError struct{ field string }

func (e *configError) Error() string { return "missing required env: " + e.field }
func errMissing(f string) error      { return &configError{field: f} }

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return fallback
}
