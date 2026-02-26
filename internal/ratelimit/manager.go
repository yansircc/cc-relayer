package ratelimit

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/yansir/cc-relayer/internal/store"
)

// Manager tracks upstream rate limits from Anthropic response headers.
type Manager struct {
	store store.Store
}

func NewManager(s store.Store) *Manager {
	return &Manager{store: s}
}

// CaptureHeaders processes rate limit headers from an upstream response.
func (m *Manager) CaptureHeaders(ctx context.Context, accountID string, headers http.Header) {
	// 5-hour status
	status := headers.Get("anthropic-ratelimit-unified-5h-status")
	if status != "" {
		fiveHourReset := headers.Get("anthropic-ratelimit-unified-5h-reset")
		m.updateFiveHourStatus(ctx, accountID, status, fiveHourReset)
	}

	// Utilization + reset timestamps
	m.captureUtilization(ctx, accountID, headers)
}

func (m *Manager) updateFiveHourStatus(ctx context.Context, accountID, status, fiveHourReset string) {
	fields := map[string]string{
		"fiveHourStatus": status,
	}

	switch status {
	case "allowed", "allowed_warning":
		// Normal or warning — no action beyond recording the status.

	case "rejected":
		now := time.Now().UTC()
		fields["schedulable"] = "false"
		fields["overloadedAt"] = now.Format(time.RFC3339)

		// Use the 5h-reset header to compute overloadedUntil; fallback to now+5h.
		resetTime := now.Add(5 * time.Hour)
		if fiveHourReset != "" {
			if parsed, err := time.Parse(time.RFC3339, fiveHourReset); err == nil {
				resetTime = parsed
			}
		}
		fields["overloadedUntil"] = resetTime.Format(time.RFC3339)
		slog.Warn("account 5h limit rejected", "accountId", accountID, "until", resetTime)
	}

	_ = m.store.SetAccountFields(ctx, accountID, fields)
}

func (m *Manager) captureUtilization(ctx context.Context, accountID string, headers http.Header) {
	fields := map[string]string{}

	if v := headers.Get("anthropic-ratelimit-unified-5h-utilization"); v != "" {
		fields["fiveHourUtil"] = v
	}
	if v := headers.Get("anthropic-ratelimit-unified-5h-reset"); v != "" {
		fields["fiveHourReset"] = v
	}
	if v := headers.Get("anthropic-ratelimit-unified-7d-utilization"); v != "" {
		fields["sevenDayUtil"] = v
	}
	if v := headers.Get("anthropic-ratelimit-unified-7d-reset"); v != "" {
		fields["sevenDayReset"] = v
	}

	if len(fields) > 0 {
		_ = m.store.SetAccountFields(ctx, accountID, fields)
	}
}

// MarkOpusRateLimited records Opus-specific rate limiting.
func (m *Manager) MarkOpusRateLimited(ctx context.Context, accountID string, resetTime time.Time) {
	_ = m.store.SetAccountField(ctx, accountID, "opusRateLimitEndAt", resetTime.Format(time.RFC3339))
	slog.Info("account opus rate limited", "accountId", accountID, "until", resetTime)
}

// RunCleanup periodically checks for accounts that should be restored.
func (m *Manager) RunCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.cleanup(ctx)
		}
	}
}

func (m *Manager) cleanup(ctx context.Context) {
	ids, err := m.store.ListAccountIDs(ctx)
	if err != nil {
		slog.Error("cleanup list accounts", "error", err)
		return
	}

	now := time.Now()

	for _, id := range ids {
		data, err := m.store.GetAccount(ctx, id)
		if err != nil {
			continue
		}

		// Check overloaded recovery
		if overloadedUntil, err := time.Parse(time.RFC3339, data["overloadedUntil"]); err == nil {
			if now.After(overloadedUntil) {
				_ = m.store.SetAccountFields(ctx, id, map[string]string{
					"schedulable":     "true",
					"overloadedAt":    "",
					"overloadedUntil": "",
					"fiveHourStatus":  "",
				})
				slog.Info("account recovered from overload", "accountId", id)
			}
		}

		// Check Opus rate limit recovery
		if opusEnd, err := time.Parse(time.RFC3339, data["opusRateLimitEndAt"]); err == nil {
			if now.After(opusEnd) {
				_ = m.store.SetAccountField(ctx, id, "opusRateLimitEndAt", "")
				slog.Info("account Opus rate limit cleared", "accountId", id)
			}
		}

		// Check blocked account recovery (auto-unblock after pause expires)
		if data["status"] == "blocked" {
			if overloadedUntil, err := time.Parse(time.RFC3339, data["overloadedUntil"]); err == nil {
				if now.After(overloadedUntil) {
					_ = m.store.SetAccountFields(ctx, id, map[string]string{
						"status":          "active",
						"errorMessage":    "",
						"schedulable":     "true",
						"overloadedUntil": "",
					})
					slog.Info("blocked account recovered", "accountId", id)
				}
			}
		}

		// Self-heal stale schedulable=false on active accounts when no blocker exists.
		if data["status"] == "active" && data["schedulable"] == "false" {
			blockedByOverload := false
			if overloadedUntil, err := time.Parse(time.RFC3339, data["overloadedUntil"]); err == nil {
				if now.Before(overloadedUntil) {
					blockedByOverload = true
				}
			}

			if !blockedByOverload {
				_ = m.store.SetAccountField(ctx, id, "schedulable", "true")
				slog.Info("account schedulable flag self-healed", "accountId", id)
			}
		}
	}
}
