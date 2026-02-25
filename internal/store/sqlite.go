package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strconv"
	"strings"
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
	sticky        *TTLMap[string]
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
		sticky:        NewTTLMap[string](),
		bindings:      NewTTLMap[bindingEntry](),
		oauthSessions: NewTTLMap[string](),
		cleanupCancel: cancel,
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sticky.Cleanup()
				s.bindings.Cleanup()
				s.oauthSessions.Cleanup()
			}
		}
	}()

	return s, nil
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
	"name":                {"name", sqlStr},
	"status":              {"status", sqlStr},
	"schedulable":         {"schedulable", sqlBool},
	"priority":            {"priority", sqlInt},
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
	"fiveHourAutoStopped": {"five_hour_auto_stopped", sqlBool},
	"fiveHourStoppedAt":   {"five_hour_stopped_at", sqlTimeNullable},
	"sessionWindowStart":  {"session_window_start", sqlTimeNullable},
	"sessionWindowEnd":    {"session_window_end", sqlTimeNullable},
	"autoStopOnWarning":   {"auto_stop_on_warning", sqlBool},
	"opusRateLimitEndAt":  {"opus_rate_limit_end_at", sqlTimeNullable},
	"overloadedAt":        {"overloaded_at", sqlTimeNullable},
	"overloadedUntil":     {"overloaded_until", sqlTimeNullable},
	"rateLimitedAt":       {"rate_limited_at", sqlTimeNullable},
}

func sqlStr(s string) interface{}  { return s }
func sqlBool(s string) interface{} { return boolInt(s == "true") }
func sqlInt(s string) interface{}  { n, _ := strconv.Atoi(s); return n }
func sqlInt64(s string) interface{} {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
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
// Account operations
// ---------------------------------------------------------------------------

const accountCols = `id, name, status, schedulable, priority, error_message,
	refresh_token_enc, access_token_enc, expires_at, created_at,
	last_used_at, last_refresh_at, proxy_json, ext_info_json,
	five_hour_status, five_hour_auto_stopped, five_hour_stopped_at,
	session_window_start, session_window_end, auto_stop_on_warning,
	opus_rate_limit_end_at, overloaded_at, overloaded_until, rate_limited_at`

func scanAccountRow(scanner interface{ Scan(...any) error }) (map[string]string, error) {
	var (
		id, name, status, errMsg         string
		refreshEnc, accessEnc            string
		proxyJSON, extInfoJSON, fhStatus string
		sched, prio                      int
		fhAutoStopped, autoStop          int
		expiresAt, createdAt             int64
		lastUsedAt, lastRefreshAt        sql.NullInt64
		fhStoppedAt                      sql.NullInt64
		winStart, winEnd                 sql.NullInt64
		opusEnd                          sql.NullInt64
		olAt, olUntil                    sql.NullInt64
		rlAt                             sql.NullInt64
	)
	err := scanner.Scan(
		&id, &name, &status, &sched, &prio, &errMsg,
		&refreshEnc, &accessEnc, &expiresAt, &createdAt,
		&lastUsedAt, &lastRefreshAt, &proxyJSON, &extInfoJSON,
		&fhStatus, &fhAutoStopped, &fhStoppedAt,
		&winStart, &winEnd, &autoStop,
		&opusEnd, &olAt, &olUntil, &rlAt,
	)
	if err != nil {
		return nil, err
	}

	m := map[string]string{
		"id":                  id,
		"name":                name,
		"status":              status,
		"schedulable":         boolStr(sched),
		"priority":            strconv.Itoa(prio),
		"errorMessage":        errMsg,
		"refreshToken":        refreshEnc,
		"accessToken":         accessEnc,
		"expiresAt":           strconv.FormatInt(expiresAt, 10),
		"createdAt":           time.Unix(createdAt, 0).UTC().Format(time.RFC3339),
		"proxy":               proxyJSON,
		"extInfo":             extInfoJSON,
		"fiveHourStatus":      fhStatus,
		"fiveHourAutoStopped": boolStr(fhAutoStopped),
		"autoStopOnWarning":   boolStr(autoStop),
	}
	setTimeField(m, "lastUsedAt", lastUsedAt)
	setTimeField(m, "lastRefreshAt", lastRefreshAt)
	setTimeField(m, "fiveHourStoppedAt", fhStoppedAt)
	setTimeField(m, "sessionWindowStart", winStart)
	setTimeField(m, "sessionWindowEnd", winEnd)
	setTimeField(m, "opusRateLimitEndAt", opusEnd)
	setTimeField(m, "overloadedAt", olAt)
	setTimeField(m, "overloadedUntil", olUntil)
	setTimeField(m, "rateLimitedAt", rlAt)
	return m, nil
}

func setTimeField(m map[string]string, key string, v sql.NullInt64) {
	if v.Valid && v.Int64 > 0 {
		m[key] = time.Unix(v.Int64, 0).UTC().Format(time.RFC3339)
	}
}

func (s *SQLiteStore) GetAccount(ctx context.Context, id string) (map[string]string, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+accountCols+" FROM accounts WHERE id = ?", id)
	m, err := scanAccountRow(row)
	if err == sql.ErrNoRows {
		return map[string]string{}, nil
	}
	return m, err
}

func (s *SQLiteStore) SetAccount(ctx context.Context, id string, fields map[string]string) error {
	// Check existence to decide INSERT vs UPDATE.
	var exists int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM accounts WHERE id = ?", id).Scan(&exists)
	if err == sql.ErrNoRows {
		return s.insertAccount(ctx, id, fields)
	}
	if err != nil {
		return err
	}
	return s.SetAccountFields(ctx, id, fields)
}

func (s *SQLiteStore) insertAccount(ctx context.Context, id string, fields map[string]string) error {
	cols := []string{"id"}
	vals := []interface{}{id}

	for redisKey, val := range fields {
		if redisKey == "id" {
			continue
		}
		info, ok := fieldMap[redisKey]
		if !ok {
			continue
		}
		cols = append(cols, info.col)
		vals = append(vals, info.conv(val))
	}

	// Ensure created_at is present.
	hasCreatedAt := false
	for _, c := range cols {
		if c == "created_at" {
			hasCreatedAt = true
			break
		}
	}
	if !hasCreatedAt {
		cols = append(cols, "created_at")
		vals = append(vals, time.Now().Unix())
	}

	placeholders := strings.Repeat("?,", len(cols))
	placeholders = placeholders[:len(placeholders)-1]

	query := fmt.Sprintf("INSERT INTO accounts (%s) VALUES (%s)", strings.Join(cols, ", "), placeholders)
	_, err := s.db.ExecContext(ctx, query, vals...)
	return err
}

func (s *SQLiteStore) SetAccountField(ctx context.Context, id, field, value string) error {
	return s.SetAccountFields(ctx, id, map[string]string{field: value})
}

func (s *SQLiteStore) SetAccountFields(ctx context.Context, id string, fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	var sets []string
	var vals []interface{}
	for redisKey, val := range fields {
		info, ok := fieldMap[redisKey]
		if !ok {
			continue
		}
		sets = append(sets, info.col+" = ?")
		vals = append(vals, info.conv(val))
	}
	if len(sets) == 0 {
		return nil
	}
	vals = append(vals, id)
	query := fmt.Sprintf("UPDATE accounts SET %s WHERE id = ?", strings.Join(sets, ", "))
	_, err := s.db.ExecContext(ctx, query, vals...)
	return err
}

func (s *SQLiteStore) DeleteAccount(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM accounts WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) ListAccountIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM accounts")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ---------------------------------------------------------------------------
// Sticky session (in-memory)
// ---------------------------------------------------------------------------

func (s *SQLiteStore) GetStickySession(_ context.Context, hash string) (string, error) {
	v, ok := s.sticky.Get(hash)
	if !ok {
		return "", nil
	}
	return v, nil
}

func (s *SQLiteStore) SetStickySession(_ context.Context, hash, accountID string, ttl time.Duration) error {
	s.sticky.Set(hash, accountID, ttl)
	return nil
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

// ---------------------------------------------------------------------------
// User operations
// ---------------------------------------------------------------------------

func (s *SQLiteStore) CreateUser(ctx context.Context, u *User) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, name, token_hash, token_prefix, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.Name, u.TokenHash, u.TokenPrefix, u.Status, u.CreatedAt.Unix())
	return err
}

func (s *SQLiteStore) GetUserByTokenHash(ctx context.Context, tokenHash string) (*User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, token_hash, token_prefix, status, created_at, last_active_at FROM users WHERE token_hash = ?`,
		tokenHash)
	return scanUser(row)
}

func (s *SQLiteStore) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, token_hash, token_prefix, status, created_at, last_active_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) DeleteUser(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) UpdateUserStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE users SET status = ? WHERE id = ?", status, id)
	return err
}

func (s *SQLiteStore) UpdateUserToken(ctx context.Context, id, tokenHash, tokenPrefix string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE users SET token_hash = ?, token_prefix = ? WHERE id = ?", tokenHash, tokenPrefix, id)
	return err
}

func (s *SQLiteStore) UpdateUserLastActive(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE users SET last_active_at = ? WHERE id = ?", time.Now().Unix(), id)
	return err
}

func scanUser(scanner interface{ Scan(...any) error }) (*User, error) {
	var (
		id, name, tokenHash, tokenPrefix, status string
		createdAt                                 int64
		lastActiveAt                              sql.NullInt64
	)
	err := scanner.Scan(&id, &name, &tokenHash, &tokenPrefix, &status, &createdAt, &lastActiveAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u := &User{
		ID:          id,
		Name:        name,
		TokenHash:   tokenHash,
		TokenPrefix: tokenPrefix,
		Status:      status,
		CreatedAt:   time.Unix(createdAt, 0).UTC(),
	}
	if lastActiveAt.Valid {
		t := time.Unix(lastActiveAt.Int64, 0).UTC()
		u.LastActiveAt = &t
	}
	return u, nil
}

// ---------------------------------------------------------------------------
// Request log
// ---------------------------------------------------------------------------

func (s *SQLiteStore) InsertRequestLog(ctx context.Context, l *RequestLog) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO request_log (user_id, account_id, model, input_tokens, output_tokens,
			cache_read_tokens, cache_create_tokens, status, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.UserID, l.AccountID, l.Model, l.InputTokens, l.OutputTokens,
		l.CacheReadTokens, l.CacheCreateTokens, l.Status, l.DurationMs, l.CreatedAt.Unix())
	return err
}

func (s *SQLiteStore) QueryUsageSummary(ctx context.Context, opts UsageQueryOpts) ([]*UsageSummaryRow, error) {
	var groupExpr string
	switch opts.GroupBy {
	case "day":
		groupExpr = "date(created_at, 'unixepoch')"
	case "user":
		groupExpr = "user_id"
	case "account":
		groupExpr = "account_id"
	case "model":
		groupExpr = "model"
	default:
		groupExpr = "'all'"
	}

	where, args := buildLogWhere(opts.UserID, opts.AccountID, opts.Since, opts.Until)
	query := fmt.Sprintf(`SELECT %s as grp, COUNT(*), SUM(input_tokens), SUM(output_tokens),
		SUM(cache_read_tokens), SUM(cache_create_tokens)
		FROM request_log WHERE %s GROUP BY grp ORDER BY grp`, groupExpr, where)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*UsageSummaryRow
	for rows.Next() {
		r := &UsageSummaryRow{}
		if err := rows.Scan(&r.Key, &r.RequestCount, &r.InputTokens, &r.OutputTokens,
			&r.CacheReadTokens, &r.CacheCreateTokens); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) QueryRequestLogs(ctx context.Context, opts RequestLogQuery) ([]*RequestLog, int, error) {
	where, args := buildLogWhere(opts.UserID, opts.AccountID, time.Time{}, time.Time{})

	var total int
	_ = s.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM request_log WHERE %s", where), args...).Scan(&total)

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	fetchArgs := make([]interface{}, len(args))
	copy(fetchArgs, args)
	fetchArgs = append(fetchArgs, limit, opts.Offset)

	query := fmt.Sprintf(`SELECT id, user_id, account_id, model, input_tokens, output_tokens,
		cache_read_tokens, cache_create_tokens, status, duration_ms, created_at
		FROM request_log WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, where)

	rows, err := s.db.QueryContext(ctx, query, fetchArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var logs []*RequestLog
	for rows.Next() {
		l := &RequestLog{}
		var ts int64
		if err := rows.Scan(&l.ID, &l.UserID, &l.AccountID, &l.Model,
			&l.InputTokens, &l.OutputTokens, &l.CacheReadTokens, &l.CacheCreateTokens,
			&l.Status, &l.DurationMs, &ts); err != nil {
			return nil, 0, err
		}
		l.CreatedAt = time.Unix(ts, 0).UTC()
		logs = append(logs, l)
	}
	return logs, total, rows.Err()
}

func (s *SQLiteStore) GetDashboardData(ctx context.Context) (*DashboardData, error) {
	d := &DashboardData{}
	now := time.Now().UTC()

	// Account summary
	_ = s.db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END),
		SUM(CASE WHEN status = 'blocked' THEN 1 ELSE 0 END),
		SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END),
		SUM(CASE WHEN overloaded_until IS NOT NULL AND overloaded_until > ? THEN 1 ELSE 0 END)
		FROM accounts`, now.Unix()).Scan(
		&d.AccountSummary.Total, &d.AccountSummary.Active,
		&d.AccountSummary.Blocked, &d.AccountSummary.Error, &d.AccountSummary.Overloaded)

	// 7-day daily usage
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour).Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT
		date(created_at, 'unixepoch') as day, COUNT(*),
		SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_create_tokens)
		FROM request_log WHERE created_at >= ? GROUP BY day ORDER BY day`, sevenDaysAgo)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			du := &DailyUsage{}
			rows.Scan(&du.Date, &du.RequestCount, &du.InputTokens, &du.OutputTokens,
				&du.CacheReadTokens, &du.CacheCreateTokens)
			d.DailyUsage = append(d.DailyUsage, du)
		}
	}

	// Today's top users
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	d.TopUsers = s.queryTopN(ctx, "user_id", todayStart, 10)
	d.TopAccounts = s.queryTopN(ctx, "account_id", todayStart, 10)

	return d, nil
}

func (s *SQLiteStore) queryTopN(ctx context.Context, groupCol string, since int64, limit int) []*UsageSummaryRow {
	query := fmt.Sprintf(`SELECT %s, COUNT(*), SUM(input_tokens), SUM(output_tokens),
		SUM(cache_read_tokens), SUM(cache_create_tokens)
		FROM request_log WHERE created_at >= ?
		GROUP BY %s ORDER BY SUM(input_tokens + output_tokens) DESC LIMIT ?`, groupCol, groupCol)
	rows, err := s.db.QueryContext(ctx, query, since, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var result []*UsageSummaryRow
	for rows.Next() {
		r := &UsageSummaryRow{}
		rows.Scan(&r.Key, &r.RequestCount, &r.InputTokens, &r.OutputTokens,
			&r.CacheReadTokens, &r.CacheCreateTokens)
		result = append(result, r)
	}
	return result
}

func (s *SQLiteStore) PurgeOldLogs(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM request_log WHERE created_at < ?", before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func buildLogWhere(userID, accountID string, since, until time.Time) (string, []interface{}) {
	where := "1=1"
	var args []interface{}
	if userID != "" {
		where += " AND user_id = ?"
		args = append(args, userID)
	}
	if accountID != "" {
		where += " AND account_id = ?"
		args = append(args, accountID)
	}
	if !since.IsZero() {
		where += " AND created_at >= ?"
		args = append(args, since.Unix())
	}
	if !until.IsZero() {
		where += " AND created_at < ?"
		args = append(args, until.Unix())
	}
	return where, args
}

// ---------------------------------------------------------------------------
// WebUI: in-memory state views
// ---------------------------------------------------------------------------

func (s *SQLiteStore) ListSessionBindings(_ context.Context) ([]SessionBindingInfo, error) {
	entries := s.bindings.Entries()
	result := make([]SessionBindingInfo, 0, len(entries))
	for _, e := range entries {
		result = append(result, SessionBindingInfo{
			SessionUUID: e.Key,
			AccountID:   e.Value.AccountID,
			CreatedAt:   e.Value.CreatedAt,
			LastUsedAt:  e.Value.LastUsedAt,
			ExpiresAt:   e.ExpiresAt,
		})
	}
	return result, nil
}

func (s *SQLiteStore) ListStickySessions(_ context.Context) ([]StickySessionInfo, error) {
	entries := s.sticky.Entries()
	result := make([]StickySessionInfo, 0, len(entries))
	for _, e := range entries {
		result = append(result, StickySessionInfo{
			Hash:      e.Key,
			AccountID: e.Value,
			ExpiresAt: e.ExpiresAt,
		})
	}
	return result, nil
}

func (s *SQLiteStore) DeleteSessionBinding(_ context.Context, sessionUUID string) error {
	s.bindings.Delete(sessionUUID)
	return nil
}

func (s *SQLiteStore) DeleteStickySession(_ context.Context, hash string) error {
	s.sticky.Delete(hash)
	return nil
}

func (s *SQLiteStore) ListOAuthSessions(_ context.Context) ([]OAuthSessionInfo, error) {
	entries := s.oauthSessions.Entries()
	result := make([]OAuthSessionInfo, 0, len(entries))
	for _, e := range entries {
		result = append(result, OAuthSessionInfo{
			SessionID: e.Key,
			ExpiresAt: e.ExpiresAt,
		})
	}
	return result, nil
}
