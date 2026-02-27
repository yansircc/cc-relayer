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

	// Database
	DBPath string

	// Security
	EncryptionKey string
	StaticToken   string

	// Claude API
	ClaudeAPIURL     string
	ClaudeAPIVersion string
	ClaudeBetaHeader string

	// Codex API
	CodexAPIURL        string
	CodexRequestTimeout time.Duration

	// Scheduling
	SessionBindingTTL   time.Duration
	TokenRefreshAdvance time.Duration

	// Error pause durations
	ErrorPause401 time.Duration
	ErrorPause403 time.Duration
	ErrorPause429 time.Duration
	ErrorPause529 time.Duration

	// Request
	RequestTimeout   time.Duration
	MaxRequestBodyMB int
	MaxRetryAccounts int
	MaxCacheControls int

	// Logging
	LogLevel string
}

func Load() *Config {
	return &Config{
		Host: envOr("HOST", "0.0.0.0"),
		Port: envInt("PORT", 3000),

		DBPath: envOr("DB_PATH", "./cc-relayer.db"),

		EncryptionKey: os.Getenv("ENCRYPTION_KEY"),
		StaticToken:   os.Getenv("API_TOKEN"),

		ClaudeAPIURL:     envOr("CLAUDE_API_URL", "https://api.anthropic.com/v1/messages"),
		ClaudeAPIVersion: envOr("CLAUDE_API_VERSION", "2023-06-01"),
		ClaudeBetaHeader: envOr("CLAUDE_BETA_HEADER", "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"),

		CodexAPIURL:         envOr("CODEX_API_URL", "https://chatgpt.com/backend-api/codex/responses"),
		CodexRequestTimeout: envDuration("CODEX_REQUEST_TIMEOUT", 10*time.Minute),

		SessionBindingTTL:   envDuration("SESSION_BINDING_TTL", 24*time.Hour),
		TokenRefreshAdvance: envDuration("TOKEN_REFRESH_ADVANCE", 60*time.Second),

		ErrorPause401: envDuration("ERROR_PAUSE_401", 30*time.Minute),
		ErrorPause403: envDuration("ERROR_PAUSE_403", 10*time.Minute),
		ErrorPause429: envDuration("ERROR_PAUSE_429", 60*time.Second),
		ErrorPause529: envDuration("ERROR_PAUSE_529", 5*time.Minute),

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
	if c.StaticToken == "" {
		return errMissing("API_TOKEN")
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
