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
	QueryUsageSummary(ctx context.Context, opts UsageQueryOpts) ([]*UsageSummaryRow, error)
	QueryRequestLogs(ctx context.Context, opts RequestLogQuery) ([]*RequestLog, int, error)
	GetDashboardData(ctx context.Context) (*DashboardData, error)
	PurgeOldLogs(ctx context.Context, before time.Time) (int64, error)

	// WebUI: in-memory state views
	ListSessionBindings(ctx context.Context) ([]SessionBindingInfo, error)
	ListStickySessions(ctx context.Context) ([]StickySessionInfo, error)
	DeleteSessionBinding(ctx context.Context, sessionUUID string) error
	DeleteStickySession(ctx context.Context, hash string) error

	// OAuth sessions listing
	ListOAuthSessions(ctx context.Context) ([]OAuthSessionInfo, error)
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
	Status            string
	DurationMs        int64
	CreatedAt         time.Time
}

// UsageQueryOpts filters for usage summary queries.
type UsageQueryOpts struct {
	UserID    string
	AccountID string
	Since     time.Time
	Until     time.Time
	GroupBy   string // "day", "user", "account", "model"
}

// UsageSummaryRow is one row of aggregated usage data.
type UsageSummaryRow struct {
	Key               string `json:"key"`
	RequestCount      int    `json:"request_count"`
	InputTokens       int64  `json:"input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	CacheReadTokens   int64  `json:"cache_read_tokens"`
	CacheCreateTokens int64  `json:"cache_create_tokens"`
}

// RequestLogQuery is a paginated request log query.
type RequestLogQuery struct {
	UserID    string
	AccountID string
	Limit     int
	Offset    int
}

// DashboardData provides all data for the admin dashboard.
type DashboardData struct {
	AccountSummary AccountSummary     `json:"account_summary"`
	DailyUsage     []*DailyUsage      `json:"daily_usage"`
	TopUsers       []*UsageSummaryRow `json:"top_users"`
	TopAccounts    []*UsageSummaryRow `json:"top_accounts"`
}

// AccountSummary counts accounts by status.
type AccountSummary struct {
	Total      int `json:"total"`
	Active     int `json:"active"`
	Blocked    int `json:"blocked"`
	Error      int `json:"error"`
	Overloaded int `json:"overloaded"`
}

// DailyUsage is one day's aggregated usage.
type DailyUsage struct {
	Date              string `json:"date"`
	RequestCount      int    `json:"request_count"`
	InputTokens       int64  `json:"input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	CacheReadTokens   int64  `json:"cache_read_tokens"`
	CacheCreateTokens int64  `json:"cache_create_tokens"`
}

// SessionBindingInfo describes an active session binding.
type SessionBindingInfo struct {
	SessionUUID string    `json:"session_uuid"`
	AccountID   string    `json:"account_id"`
	CreatedAt   string    `json:"created_at"`
	LastUsedAt  string    `json:"last_used_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// StickySessionInfo describes an active sticky session.
type StickySessionInfo struct {
	Hash      string    `json:"hash"`
	AccountID string    `json:"account_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// OAuthSessionInfo describes a pending OAuth PKCE session.
type OAuthSessionInfo struct {
	SessionID string    `json:"session_id"`
	ExpiresAt time.Time `json:"expires_at"`
}
