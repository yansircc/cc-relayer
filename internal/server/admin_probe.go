package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/identity"
)

// doClaudeProbe sends a minimal haiku request to refresh rate limit headers.
func (s *Server) doClaudeProbe(ctx context.Context, acct *account.Account, accessToken string) (*http.Response, error) {
	body := `{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequestWithContext(ctx, "POST", s.cfg.ClaudeAPIURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	identity.SetRequiredHeaders(req.Header, accessToken, s.cfg.ClaudeAPIVersion, s.cfg.ClaudeBetaHeader)
	return s.transportMgr.GetClient(acct).Do(req)
}

// doCodexProbe sends a minimal codex streaming request to refresh rate limit headers.
// Codex only returns rate limit headers on streaming responses.
func (s *Server) doCodexProbe(ctx context.Context, acct *account.Account, accessToken string) (*http.Response, error) {
	body := `{"model":"gpt-5-codex","stream":true,"store":false,"instructions":"Reply: ok","input":[{"role":"user","content":"t"}]}`
	req, err := http.NewRequestWithContext(ctx, "POST", s.cfg.CodexAPIURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Host", "chatgpt.com")
	req.Header.Set("Accept", "text/event-stream")
	if acct.ExtInfo != nil {
		if accountID, ok := acct.ExtInfo["chatgptAccountId"].(string); ok && accountID != "" {
			req.Header.Set("Chatgpt-Account-Id", accountID)
		}
	}
	return s.transportMgr.GetClient(acct).Do(req)
}

// runRateLimitRefresh periodically probes accounts whose reset window has expired
// so the dashboard always shows fresh rate limit data.
func (s *Server) runRateLimitRefresh(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshStaleAccounts(ctx)
		}
	}
}

func (s *Server) refreshStaleAccounts(ctx context.Context) {
	accounts, err := s.accounts.List(ctx)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	var wg sync.WaitGroup
	for _, acct := range accounts {
		if acct.Status != "active" {
			continue
		}

		// Check if account should be on cooldown based on existing data
		if acct.Schedulable {
			s.enforceExhaustedCooldown(ctx, acct, now)
		}

		// Stale = reset expired OR has utilization data but missing reset timestamp
		stale := false
		if acct.Provider == "codex" {
			stale = (acct.CodexPrimaryReset > 0 && acct.CodexPrimaryReset < now) ||
				(acct.CodexSecondaryReset > 0 && acct.CodexSecondaryReset < now) ||
				(acct.CodexPrimaryUtil > 0 && acct.CodexPrimaryReset == 0) ||
				(acct.CodexSecondaryUtil > 0 && acct.CodexSecondaryReset == 0)
		} else {
			stale = (acct.FiveHourReset > 0 && acct.FiveHourReset < now) ||
				(acct.SevenDayReset > 0 && acct.SevenDayReset < now) ||
				(acct.FiveHourUtil > 0 && acct.FiveHourReset == 0) ||
				(acct.SevenDayUtil > 0 && acct.SevenDayReset == 0)
		}
		if !stale {
			continue
		}

		wg.Add(1)
		go func(a *account.Account) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			accessToken, err := s.tokens.EnsureValidToken(probeCtx, a.ID)
			if err != nil {
				slog.Warn("probe token failed", "account", a.Email, "error", err)
				return
			}

			var resp *http.Response
			if a.Provider == "codex" {
				resp, err = s.doCodexProbe(probeCtx, a, accessToken)
			} else {
				resp, err = s.doClaudeProbe(probeCtx, a, accessToken)
			}
			if err != nil {
				slog.Warn("probe request failed", "account", a.Email, "error", err)
				return
			}

			if a.Provider == "codex" {
				s.rateLimit.CaptureCodexHeaders(probeCtx, a.ID, resp.Header)
			} else {
				s.rateLimit.CaptureHeaders(probeCtx, a.ID, resp.Header)
			}
			resp.Body.Close()
			slog.Debug("probe refreshed", "account", a.Email, "status", resp.StatusCode)
		}(acct)
	}
	wg.Wait()
}

// enforceExhaustedCooldown checks existing rate limit data and sets cooldown
// for accounts that are exhausted but not yet on cooldown.
func (s *Server) enforceExhaustedCooldown(ctx context.Context, acct *account.Account, now int64) {
	var cooldownUntil int64

	if acct.Provider == "codex" {
		if acct.CodexPrimaryUtil >= 0.99 && acct.CodexPrimaryReset > now {
			cooldownUntil = acct.CodexPrimaryReset
		}
		if acct.CodexSecondaryUtil >= 0.99 && acct.CodexSecondaryReset > now && acct.CodexSecondaryReset > cooldownUntil {
			cooldownUntil = acct.CodexSecondaryReset
		}
	} else {
		if acct.FiveHourUtil >= 0.99 && acct.FiveHourReset > now {
			cooldownUntil = acct.FiveHourReset
		}
		if acct.SevenDayUtil >= 0.99 && acct.SevenDayReset > now && acct.SevenDayReset > cooldownUntil {
			cooldownUntil = acct.SevenDayReset
		}
	}

	if cooldownUntil > 0 {
		resetTime := time.Unix(cooldownUntil, 0).UTC()
		_ = s.store.SetAccountFields(ctx, acct.ID, map[string]string{
			"schedulable":     "false",
			"overloadedAt":    time.Now().UTC().Format(time.RFC3339),
			"overloadedUntil": resetTime.Format(time.RFC3339),
		})
		slog.Warn("enforced cooldown on exhausted account", "account", acct.Email, "until", resetTime)
	}
}

// fetchOrgUUIDViaAPI makes a minimal API call and extracts the org UUID
// from the Anthropic-Organization-Id response header.
func (s *Server) fetchOrgUUIDViaAPI(ctx context.Context, acct *account.Account, accessToken string) string {
	resp, err := s.doClaudeProbe(ctx, acct, accessToken)
	if err != nil {
		return ""
	}
	resp.Body.Close()
	return resp.Header.Get("Anthropic-Organization-Id")
}
