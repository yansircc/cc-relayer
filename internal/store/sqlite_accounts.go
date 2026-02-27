package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Account operations
// ---------------------------------------------------------------------------

const accountCols = `id, email, status, schedulable, priority, priority_mode, error_message,
	refresh_token_enc, access_token_enc, expires_at, created_at,
	last_used_at, last_refresh_at, proxy_json, ext_info_json,
	five_hour_status,
	opus_rate_limit_end_at, overloaded_at, overloaded_until, rate_limited_at,
	five_hour_util, five_hour_reset, seven_day_util, seven_day_reset,
	provider, codex_primary_util, codex_primary_reset, codex_secondary_util, codex_secondary_reset`

func scanAccountRow(scanner interface{ Scan(...any) error }) (map[string]string, error) {
	var (
		id, email, status, priMode, errMsg  string
		refreshEnc, accessEnc               string
		proxyJSON, extInfoJSON, fhStatus    string
		sched, prio                         int
		expiresAt, createdAt                int64
		lastUsedAt, lastRefreshAt           sql.NullInt64
		opusEnd                             sql.NullInt64
		olAt, olUntil                       sql.NullInt64
		rlAt                                sql.NullInt64
		fhUtil, sdUtil                      float64
		fhReset, sdReset                    int64
		provider                            string
		cpUtil, csUtil                      float64
		cpReset, csReset                    int64
	)
	err := scanner.Scan(
		&id, &email, &status, &sched, &prio, &priMode, &errMsg,
		&refreshEnc, &accessEnc, &expiresAt, &createdAt,
		&lastUsedAt, &lastRefreshAt, &proxyJSON, &extInfoJSON,
		&fhStatus,
		&opusEnd, &olAt, &olUntil, &rlAt,
		&fhUtil, &fhReset, &sdUtil, &sdReset,
		&provider, &cpUtil, &cpReset, &csUtil, &csReset,
	)
	if err != nil {
		return nil, err
	}

	if priMode == "" {
		priMode = "auto"
	}
	if provider == "" {
		provider = "claude"
	}

	m := map[string]string{
		"id":                  id,
		"email":               email,
		"provider":            provider,
		"status":              status,
		"schedulable":         boolStr(sched),
		"priority":            strconv.Itoa(prio),
		"priorityMode":        priMode,
		"errorMessage":        errMsg,
		"refreshToken":        refreshEnc,
		"accessToken":         accessEnc,
		"expiresAt":           strconv.FormatInt(expiresAt, 10),
		"createdAt":           time.Unix(createdAt, 0).UTC().Format(time.RFC3339),
		"proxy":               proxyJSON,
		"extInfo":             extInfoJSON,
		"fiveHourStatus":      fhStatus,
		"fiveHourUtil":        strconv.FormatFloat(fhUtil, 'f', -1, 64),
		"fiveHourReset":       strconv.FormatInt(fhReset, 10),
		"sevenDayUtil":        strconv.FormatFloat(sdUtil, 'f', -1, 64),
		"sevenDayReset":       strconv.FormatInt(sdReset, 10),
		"codexPrimaryUtil":    strconv.FormatFloat(cpUtil, 'f', -1, 64),
		"codexPrimaryReset":   strconv.FormatInt(cpReset, 10),
		"codexSecondaryUtil":  strconv.FormatFloat(csUtil, 'f', -1, 64),
		"codexSecondaryReset": strconv.FormatInt(csReset, 10),
	}
	setTimeField(m, "lastUsedAt", lastUsedAt)
	setTimeField(m, "lastRefreshAt", lastRefreshAt)
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
