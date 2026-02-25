package store

import (
	"context"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Request log
// ---------------------------------------------------------------------------

func (s *SQLiteStore) InsertRequestLog(ctx context.Context, l *RequestLog) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO request_log (user_id, account_id, model, input_tokens, output_tokens,
			cache_read_tokens, cache_create_tokens, cost_usd, status, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.UserID, l.AccountID, l.Model, l.InputTokens, l.OutputTokens,
		l.CacheReadTokens, l.CacheCreateTokens, l.CostUSD, l.Status, l.DurationMs, l.CreatedAt.Unix())
	return err
}

func (s *SQLiteStore) QueryRequestLogs(ctx context.Context, opts RequestLogQuery) ([]*RequestLog, int, error) {
	where, args := buildLogWhere(opts.UserID, opts.AccountID)

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
		cache_read_tokens, cache_create_tokens, cost_usd, status, duration_ms, created_at
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
			&l.CostUSD, &l.Status, &l.DurationMs, &ts); err != nil {
			return nil, 0, err
		}
		l.CreatedAt = time.Unix(ts, 0).UTC()
		logs = append(logs, l)
	}
	return logs, total, rows.Err()
}

func (s *SQLiteStore) PurgeOldLogs(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM request_log WHERE created_at < ?", before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func buildLogWhere(userID, accountID string) (string, []interface{}) {
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
	return where, args
}

// ---------------------------------------------------------------------------
// Dashboard & analytics queries
// ---------------------------------------------------------------------------

// QueryUsagePeriods returns usage for 5 periods: today, yesterday, 3d, 7d, 30d.
// If userID is non-empty, filters by that user.
func (s *SQLiteStore) QueryUsagePeriods(ctx context.Context, userID string) ([]UsagePeriod, error) {
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	yesterdayStart := todayStart.Add(-24 * time.Hour)

	periods := []struct {
		label string
		since time.Time
		until time.Time
	}{
		{"today", todayStart, now},
		{"yesterday", yesterdayStart, todayStart},
		{"3 days", now.Add(-3 * 24 * time.Hour), now},
		{"7 days", now.Add(-7 * 24 * time.Hour), now},
		{"30 days", now.Add(-30 * 24 * time.Hour), now},
	}

	result := make([]UsagePeriod, 0, len(periods))
	for _, p := range periods {
		var where string
		var args []interface{}
		if userID != "" {
			where = "user_id = ? AND created_at >= ? AND created_at < ?"
			args = []interface{}{userID, p.since.Unix(), p.until.Unix()}
		} else {
			where = "created_at >= ? AND created_at < ?"
			args = []interface{}{p.since.Unix(), p.until.Unix()}
		}
		row := s.db.QueryRowContext(ctx, fmt.Sprintf(
			`SELECT COALESCE(COUNT(*),0), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cost_usd),0)
			FROM request_log WHERE %s`, where), args...)
		up := UsagePeriod{Label: p.label}
		row.Scan(&up.Requests, &up.InputTokens, &up.OutputTokens, &up.CacheReadTokens, &up.CostUSD)
		result = append(result, up)
	}
	return result, nil
}

// QueryAccountCosts returns 5-hour and 7-day cost totals per account.
func (s *SQLiteStore) QueryAccountCosts(ctx context.Context) (map[string]AccountCostInfo, error) {
	now := time.Now().UTC()
	fiveHoursAgo := now.Add(-5 * time.Hour).Unix()
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour).Unix()

	result := make(map[string]AccountCostInfo)

	// 5-hour costs
	rows, err := s.db.QueryContext(ctx,
		`SELECT account_id, COALESCE(SUM(cost_usd),0)
		FROM request_log WHERE created_at >= ? GROUP BY account_id`, fiveHoursAgo)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var accountID string
			var cost float64
			rows.Scan(&accountID, &cost)
			info := result[accountID]
			info.FiveHourCost = cost
			result[accountID] = info
		}
	}

	// 7-day costs
	rows2, err := s.db.QueryContext(ctx,
		`SELECT account_id, COALESCE(SUM(cost_usd),0)
		FROM request_log WHERE created_at >= ? GROUP BY account_id`, sevenDaysAgo)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var accountID string
			var cost float64
			rows2.Scan(&accountID, &cost)
			info := result[accountID]
			info.SevenDayCost = cost
			result[accountID] = info
		}
	}

	return result, nil
}

// QueryUserTotalCosts returns total cost per user across all time.
func (s *SQLiteStore) QueryUserTotalCosts(ctx context.Context) (map[string]float64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, COALESCE(SUM(cost_usd),0) FROM request_log GROUP BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]float64)
	for rows.Next() {
		var userID string
		var cost float64
		rows.Scan(&userID, &cost)
		result[userID] = cost
	}
	return result, rows.Err()
}

// QueryModelUsage returns per-model usage breakdown filtered by user.
func (s *SQLiteStore) QueryModelUsage(ctx context.Context, userID string) ([]ModelUsageRow, error) {
	sevenDaysAgo := time.Now().UTC().Add(-7 * 24 * time.Hour).Unix()
	var where string
	var args []interface{}
	if userID != "" {
		where = "user_id = ? AND created_at >= ?"
		args = []interface{}{userID, sevenDaysAgo}
	} else {
		where = "created_at >= ?"
		args = []interface{}{sevenDaysAgo}
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT model, COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cost_usd),0)
		FROM request_log WHERE %s GROUP BY model ORDER BY SUM(input_tokens + output_tokens) DESC`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ModelUsageRow
	for rows.Next() {
		var m ModelUsageRow
		rows.Scan(&m.Model, &m.Requests, &m.InputTokens, &m.OutputTokens, &m.CacheReadTokens, &m.CostUSD)
		result = append(result, m)
	}
	return result, rows.Err()
}
