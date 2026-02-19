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
	store *store.Store
}

func NewManager(s *store.Store) *Manager {
	return &Manager{store: s}
}

// CaptureHeaders processes rate limit headers from an upstream response.
func (m *Manager) CaptureHeaders(ctx context.Context, accountID string, headers http.Header) {
	// 5-hour status
	status := headers.Get("anthropic-ratelimit-unified-5h-status")
	if status != "" {
		m.updateFiveHourStatus(ctx, accountID, status)
	}

	// Reset timestamp (from 429 responses)
	resetStr := headers.Get("anthropic-ratelimit-unified-reset")
	if resetStr != "" {
		m.updateResetTime(ctx, accountID, resetStr)
	}
}

func (m *Manager) updateFiveHourStatus(ctx context.Context, accountID, status string) {
	fields := map[string]string{
		"fiveHourStatus": status,
	}

	now := time.Now().UTC()

	switch status {
	case "allowed":
		// Normal — clear any auto-stop
		fields["fiveHourAutoStopped"] = "false"

	case "allowed_warning":
		// Check if autoStopOnWarning is enabled
		data, err := m.store.GetAccount(ctx, accountID)
		if err != nil {
			return
		}
		if data["autoStopOnWarning"] == "true" {
			fields["schedulable"] = "false"
			fields["fiveHourAutoStopped"] = "true"
			fields["fiveHourStoppedAt"] = now.Format(time.RFC3339)

			// Compute window
			windowStart := now.Truncate(time.Hour)
			windowEnd := windowStart.Add(5 * time.Hour)
			fields["sessionWindowStart"] = windowStart.Format(time.RFC3339)
			fields["sessionWindowEnd"] = windowEnd.Format(time.RFC3339)

			slog.Info("account auto-stopped on warning", "accountId", accountID)
		}

	case "rejected":
		fields["schedulable"] = "false"
		fields["fiveHourAutoStopped"] = "true"
		fields["fiveHourStoppedAt"] = now.Format(time.RFC3339)
		slog.Warn("account 5h limit rejected", "accountId", accountID)
	}

	_ = m.store.SetAccountFields(ctx, accountID, fields)
}

func (m *Manager) updateResetTime(ctx context.Context, accountID, resetStr string) {
	resetTime, err := time.Parse(time.RFC3339, resetStr)
	if err != nil {
		slog.Warn("parse reset time", "error", err, "value", resetStr)
		return
	}

	windowEnd := resetTime
	windowStart := resetTime.Add(-5 * time.Hour)

	fields := map[string]string{
		"sessionWindowStart": windowStart.Format(time.RFC3339),
		"sessionWindowEnd":   windowEnd.Format(time.RFC3339),
		"rateLimitedAt":      time.Now().UTC().Format(time.RFC3339),
	}

	_ = m.store.SetAccountFields(ctx, accountID, fields)
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

		// Check 5-hour auto-stop recovery
		if data["fiveHourAutoStopped"] == "true" {
			if windowEnd, err := time.Parse(time.RFC3339, data["sessionWindowEnd"]); err == nil {
				if now.After(windowEnd.Add(time.Minute)) {
					_ = m.store.SetAccountFields(ctx, id, map[string]string{
						"schedulable":         "true",
						"fiveHourAutoStopped": "false",
						"fiveHourStatus":      "",
					})
					slog.Info("account restored from 5h auto-stop", "accountId", id)
				}
			} else {
				// No window end — check stoppedAt + 5h1m
				if stoppedAt, err := time.Parse(time.RFC3339, data["fiveHourStoppedAt"]); err == nil {
					if now.After(stoppedAt.Add(5*time.Hour + time.Minute)) {
						_ = m.store.SetAccountFields(ctx, id, map[string]string{
							"schedulable":         "true",
							"fiveHourAutoStopped": "false",
							"fiveHourStatus":      "",
						})
						slog.Info("account restored from 5h auto-stop (fallback)", "accountId", id)
					}
				}
			}
		}

		// Check overloaded recovery
		if overloadedUntil, err := time.Parse(time.RFC3339, data["overloadedUntil"]); err == nil {
			if now.After(overloadedUntil) {
				_ = m.store.SetAccountFields(ctx, id, map[string]string{
					"overloadedAt":    "",
					"overloadedUntil": "",
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
	}
}
