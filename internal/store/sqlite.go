package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// bindingEntry holds session binding data in memory.
type bindingEntry struct {
	AccountID  string
	CreatedAt  string
	LastUsedAt string
}

// SQLiteStore implements Store using SQLite for persistence and in-memory maps
// for ephemeral data (sticky sessions, bindings, stainless fingerprints, locks).
type SQLiteStore struct {
	db            *sql.DB
	bindings      *TTLMap[bindingEntry]
	oauthSessions *TTLMap[string]
	stainless     sync.Map // accountID → headersJSON
	refreshLocks  sync.Map // accountID → *sync.Mutex
	cleanupCancel context.CancelFunc
}

// New creates a SQLiteStore, initializes the schema, and starts background cleanup.
func New(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}

	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &SQLiteStore{
		db:            db,
		bindings:      NewTTLMap[bindingEntry](),
		oauthSessions: NewTTLMap[string](),
		cleanupCancel: cancel,
	}

	// Run migrations for existing databases.
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		cancel()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.bindings.Cleanup()
				s.oauthSessions.Cleanup()
			}
		}
	}()

	return s, nil
}

// migrate adds columns that may not exist in older databases.
func (s *SQLiteStore) migrate(ctx context.Context) error {
	migrations := []struct {
		table, column, ddl string
	}{
		{"request_log", "cost_usd", "ALTER TABLE request_log ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0"},
		{"accounts", "priority_mode", "ALTER TABLE accounts ADD COLUMN priority_mode TEXT NOT NULL DEFAULT 'auto'"},
		{"accounts", "email", "ALTER TABLE accounts RENAME COLUMN name TO email"},
		{"accounts", "five_hour_util", "ALTER TABLE accounts ADD COLUMN five_hour_util REAL NOT NULL DEFAULT 0"},
		{"accounts", "five_hour_reset", "ALTER TABLE accounts ADD COLUMN five_hour_reset INTEGER NOT NULL DEFAULT 0"},
		{"accounts", "seven_day_util", "ALTER TABLE accounts ADD COLUMN seven_day_util REAL NOT NULL DEFAULT 0"},
		{"accounts", "seven_day_reset", "ALTER TABLE accounts ADD COLUMN seven_day_reset INTEGER NOT NULL DEFAULT 0"},
	}
	for _, m := range migrations {
		if !s.columnExists(ctx, m.table, m.column) {
			if _, err := s.db.ExecContext(ctx, m.ddl); err != nil {
				return fmt.Errorf("add %s.%s: %w", m.table, m.column, err)
			}
		}
	}
	return nil
}

func (s *SQLiteStore) columnExists(ctx context.Context, table, column string) bool {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typeName string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

func (s *SQLiteStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *SQLiteStore) Close() error                    { s.cleanupCancel(); return s.db.Close() }

// ---------------------------------------------------------------------------
// Field mapping: Redis camelCase key ↔ SQLite snake_case column
// ---------------------------------------------------------------------------

type colInfo struct {
	col  string
	conv func(string) interface{}
}

var fieldMap = map[string]colInfo{
	"id":                  {"id", sqlStr},
	"email":               {"email", sqlStr},
	"status":              {"status", sqlStr},
	"schedulable":         {"schedulable", sqlBool},
	"priority":            {"priority", sqlInt},
	"priorityMode":        {"priority_mode", sqlStr},
	"errorMessage":        {"error_message", sqlStr},
	"refreshToken":        {"refresh_token_enc", sqlStr},
	"accessToken":         {"access_token_enc", sqlStr},
	"expiresAt":           {"expires_at", sqlInt64},
	"createdAt":           {"created_at", sqlTime},
	"lastUsedAt":          {"last_used_at", sqlTimeNullable},
	"lastRefreshAt":       {"last_refresh_at", sqlTimeNullable},
	"proxy":               {"proxy_json", sqlStr},
	"extInfo":             {"ext_info_json", sqlStr},
	"fiveHourStatus":      {"five_hour_status", sqlStr},
	"opusRateLimitEndAt":  {"opus_rate_limit_end_at", sqlTimeNullable},
	"overloadedAt":        {"overloaded_at", sqlTimeNullable},
	"overloadedUntil":     {"overloaded_until", sqlTimeNullable},
	"rateLimitedAt":       {"rate_limited_at", sqlTimeNullable},
	"fiveHourUtil":        {"five_hour_util", sqlFloat},
	"fiveHourReset":       {"five_hour_reset", sqlInt64},
	"sevenDayUtil":        {"seven_day_util", sqlFloat},
	"sevenDayReset":       {"seven_day_reset", sqlInt64},
}

func sqlStr(s string) interface{}  { return s }
func sqlBool(s string) interface{} { return boolInt(s == "true") }
func sqlInt(s string) interface{}  { n, _ := strconv.Atoi(s); return n }
func sqlInt64(s string) interface{} {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
func sqlFloat(s string) interface{} {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
func sqlTime(s string) interface{} {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Now().Unix()
	}
	return t.Unix()
}
func sqlTimeNullable(s string) interface{} {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return t.Unix()
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func boolStr(v int) string {
	if v != 0 {
		return "true"
	}
	return "false"
}

// ---------------------------------------------------------------------------
// Session binding (in-memory)
// ---------------------------------------------------------------------------

func (s *SQLiteStore) GetSessionBinding(_ context.Context, sessionUUID string) (map[string]string, error) {
	e, ok := s.bindings.Get(sessionUUID)
	if !ok {
		return nil, nil
	}
	return map[string]string{
		"accountId":  e.AccountID,
		"createdAt":  e.CreatedAt,
		"lastUsedAt": e.LastUsedAt,
	}, nil
}

func (s *SQLiteStore) SetSessionBinding(_ context.Context, sessionUUID, accountID string, ttl time.Duration) error {
	now := time.Now().UTC().Format(time.RFC3339)
	s.bindings.Set(sessionUUID, bindingEntry{
		AccountID:  accountID,
		CreatedAt:  now,
		LastUsedAt: now,
	}, ttl)
	return nil
}

func (s *SQLiteStore) RenewSessionBinding(_ context.Context, sessionUUID string, ttl time.Duration) error {
	s.bindings.Update(sessionUUID, func(e *bindingEntry) {
		e.LastUsedAt = time.Now().UTC().Format(time.RFC3339)
	}, ttl)
	return nil
}

// ListSessionBindingsForAccount returns active session bindings for a specific account.
func (s *SQLiteStore) ListSessionBindingsForAccount(_ context.Context, accountID string) ([]SessionBindingInfo, error) {
	entries := s.bindings.Entries()
	var result []SessionBindingInfo
	for _, e := range entries {
		if e.Value.AccountID == accountID {
			result = append(result, SessionBindingInfo{
				SessionUUID: e.Key,
				AccountID:   e.Value.AccountID,
				CreatedAt:   e.Value.CreatedAt,
				LastUsedAt:  e.Value.LastUsedAt,
				ExpiresAt:   e.ExpiresAt,
			})
		}
	}
	if result == nil {
		result = []SessionBindingInfo{}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Stainless headers (in-memory, permanent)
// ---------------------------------------------------------------------------

func (s *SQLiteStore) GetStainlessHeaders(_ context.Context, accountID string) (string, error) {
	v, ok := s.stainless.Load(accountID)
	if !ok {
		return "", nil
	}
	return v.(string), nil
}

func (s *SQLiteStore) SetStainlessHeadersNX(_ context.Context, accountID, headersJSON string) (bool, error) {
	_, loaded := s.stainless.LoadOrStore(accountID, headersJSON)
	return !loaded, nil
}

// ---------------------------------------------------------------------------
// Token refresh lock (in-memory mutex)
// ---------------------------------------------------------------------------

func (s *SQLiteStore) AcquireRefreshLock(_ context.Context, accountID, _ string) (bool, error) {
	mu, _ := s.refreshLocks.LoadOrStore(accountID, &sync.Mutex{})
	return mu.(*sync.Mutex).TryLock(), nil
}

func (s *SQLiteStore) ReleaseRefreshLock(_ context.Context, accountID, _ string) error {
	mu, ok := s.refreshLocks.Load(accountID)
	if ok {
		mu.(*sync.Mutex).Unlock()
	}
	return nil
}

// ---------------------------------------------------------------------------
// OAuth session (in-memory with TTL)
// ---------------------------------------------------------------------------

func (s *SQLiteStore) SetOAuthSession(_ context.Context, sessionID, data string, ttl time.Duration) error {
	s.oauthSessions.Set(sessionID, data, ttl)
	return nil
}

func (s *SQLiteStore) GetDelOAuthSession(_ context.Context, sessionID string) (string, error) {
	v, ok := s.oauthSessions.GetAndDelete(sessionID)
	if !ok {
		return "", fmt.Errorf("invalid or expired session")
	}
	return v, nil
}
