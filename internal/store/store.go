package store

import (
	"context"
	"time"
)

// Store is the persistence interface for cc-relayer.
type Store interface {
	Ping(ctx context.Context) error
	Close() error

	// Account operations. Map keys use camelCase names (e.g. "expiresAt")
	// matching the original Redis hash field names for caller compatibility.
	GetAccount(ctx context.Context, id string) (map[string]string, error)
	SetAccount(ctx context.Context, id string, fields map[string]string) error
	SetAccountField(ctx context.Context, id, field, value string) error
	SetAccountFields(ctx context.Context, id string, fields map[string]string) error
	DeleteAccount(ctx context.Context, id string) error
	ListAccountIDs(ctx context.Context) ([]string, error)

	// Sticky session (in-memory with TTL)
	GetStickySession(ctx context.Context, hash string) (string, error)
	SetStickySession(ctx context.Context, hash, accountID string, ttl time.Duration) error

	// Session binding (in-memory with TTL)
	GetSessionBinding(ctx context.Context, sessionUUID string) (map[string]string, error)
	SetSessionBinding(ctx context.Context, sessionUUID, accountID string, ttl time.Duration) error
	RenewSessionBinding(ctx context.Context, sessionUUID string, ttl time.Duration) error

	// Stainless SDK header fingerprint (in-memory, permanent until restart)
	GetStainlessHeaders(ctx context.Context, accountID string) (string, error)
	SetStainlessHeadersNX(ctx context.Context, accountID, headersJSON string) (bool, error)

	// Token refresh lock (in-memory mutex, single process)
	AcquireRefreshLock(ctx context.Context, accountID, lockID string) (bool, error)
	ReleaseRefreshLock(ctx context.Context, accountID, lockID string) error

	// OAuth PKCE session (in-memory with TTL, replaces Redis Client() escape hatch)
	SetOAuthSession(ctx context.Context, sessionID, data string, ttl time.Duration) error
	GetDelOAuthSession(ctx context.Context, sessionID string) (string, error)

	// User management
	CreateUser(ctx context.Context, u *User) error
	GetUserByTokenHash(ctx context.Context, tokenHash string) (*User, error)
	ListUsers(ctx context.Context) ([]*User, error)
	DeleteUser(ctx context.Context, id string) error
	UpdateUserStatus(ctx context.Context, id, status string) error
	UpdateUserToken(ctx context.Context, id, tokenHash, tokenPrefix string) error
	UpdateUserLastActive(ctx context.Context, id string) error

	// Request log
	InsertRequestLog(ctx context.Context, log *RequestLog) error
	QueryRequestLogs(ctx context.Context, opts RequestLogQuery) ([]*RequestLog, int, error)
	PurgeOldLogs(ctx context.Context, before time.Time) (int64, error)

	// Dashboard & analytics
	QueryUsagePeriods(ctx context.Context, userID string) ([]UsagePeriod, error)
	QueryUserTotalCosts(ctx context.Context) (map[string]float64, error)
	QueryModelUsage(ctx context.Context, userID string) ([]ModelUsageRow, error)

	// Session bindings for account detail
	ListSessionBindingsForAccount(ctx context.Context, accountID string) ([]SessionBindingInfo, error)
}

// User represents an API user with a hashed token.
type User struct {
	ID           string
	Name         string
	TokenHash    string
	TokenPrefix  string
	Status       string
	CreatedAt    time.Time
	LastActiveAt *time.Time
}

// RequestLog represents a single API request log entry.
type RequestLog struct {
	ID                int64
	UserID            string
	AccountID         string
	Model             string
	InputTokens       int
	OutputTokens      int
	CacheReadTokens   int
	CacheCreateTokens int
	CostUSD           float64
	Status            string
	DurationMs        int64
	CreatedAt         time.Time
}

// RequestLogQuery is a paginated request log query.
type RequestLogQuery struct {
	UserID    string
	AccountID string
	Limit     int
	Offset    int
}

// UsagePeriod represents usage for a named period (today, yesterday, 3d, 7d, 30d).
type UsagePeriod struct {
	Label           string  `json:"label"`
	Requests        int     `json:"requests"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	CostUSD         float64 `json:"cost_usd"`
}

// ModelUsageRow represents per-model usage breakdown.
type ModelUsageRow struct {
	Model           string  `json:"model"`
	Requests        int     `json:"requests"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	CostUSD         float64 `json:"cost_usd"`
}

// SessionBindingInfo describes an active session binding.
type SessionBindingInfo struct {
	SessionUUID string    `json:"session_uuid"`
	AccountID   string    `json:"account_id"`
	CreatedAt   string    `json:"created_at"`
	LastUsedAt  string    `json:"last_used_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}
