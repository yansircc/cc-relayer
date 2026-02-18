package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/config"
	"github.com/yansir/cc-relayer/internal/store"
)

// Scheduler selects accounts for requests.
type Scheduler struct {
	store    *store.Store
	accounts *account.AccountStore
	cfg      *config.Config
}

func New(s *store.Store, as *account.AccountStore, cfg *config.Config) *Scheduler {
	return &Scheduler{store: s, accounts: as, cfg: cfg}
}

// SelectOptions provides context for account selection.
type SelectOptions struct {
	BoundAccountID string   // API Key bound account
	SessionHash    string   // For sticky session lookup
	IsOpusRequest  bool     // Whether this is an Opus model request
	ExcludeIDs     []string // Accounts to skip (failed on this request)
}

// Select picks the best available account for a request.
func (s *Scheduler) Select(ctx context.Context, opts SelectOptions) (*account.Account, error) {
	// 1. API Key bound account — highest priority
	if opts.BoundAccountID != "" {
		acct, err := s.accounts.Get(ctx, opts.BoundAccountID)
		if err == nil && acct != nil && s.isAvailable(acct, opts) {
			return acct, nil
		}
		// Bound account unavailable — don't fall through, return error
		if acct != nil {
			return nil, fmt.Errorf("bound account %s is %s", opts.BoundAccountID, acct.Status)
		}
	}

	// 2. Sticky session — check Redis for existing binding
	if opts.SessionHash != "" {
		accountID, err := s.store.GetStickySession(ctx, opts.SessionHash)
		if err == nil && accountID != "" && !contains(opts.ExcludeIDs, accountID) {
			acct, err := s.accounts.Get(ctx, accountID)
			if err == nil && acct != nil && s.isAvailable(acct, opts) {
				// Renew sticky session TTL
				_ = s.store.SetStickySession(ctx, opts.SessionHash, accountID, s.cfg.StickySessionTTL)
				return acct, nil
			}
		}
	}

	// 3. Pool selection — filter, sort, pick best
	all, err := s.accounts.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}

	var candidates []*account.Account
	for _, acct := range all {
		if contains(opts.ExcludeIDs, acct.ID) {
			continue
		}
		if !s.isAvailable(acct, opts) {
			continue
		}
		candidates = append(candidates, acct)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no available accounts")
	}

	// Sort: priority DESC, then lastUsedAt ASC (round-robin effect)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		ti := timeOrZero(candidates[i].LastUsedAt)
		tj := timeOrZero(candidates[j].LastUsedAt)
		return ti.Before(tj)
	})

	selected := candidates[0]

	// Bind to sticky session
	if opts.SessionHash != "" {
		_ = s.store.SetStickySession(ctx, opts.SessionHash, selected.ID, s.cfg.StickySessionTTL)
	}

	slog.Debug("account selected", "accountId", selected.ID, "name", selected.Name, "priority", selected.Priority)
	return selected, nil
}

// isAvailable checks if an account can handle a request right now.
func (s *Scheduler) isAvailable(acct *account.Account, opts SelectOptions) bool {
	if acct.Status != "active" {
		return false
	}
	if !acct.Schedulable {
		return false
	}

	// Overloaded check
	if acct.OverloadedUntil != nil && time.Now().Before(*acct.OverloadedUntil) {
		return false
	}

	// 5-hour window check
	if acct.FiveHourAutoStopped {
		if acct.SessionWindowEnd != nil && time.Now().Before(acct.SessionWindowEnd.Add(time.Minute)) {
			return false
		}
	}

	// Opus rate limit check
	if opts.IsOpusRequest && acct.OpusRateLimitEndAt != nil {
		if time.Now().Before(*acct.OpusRateLimitEndAt) {
			return false
		}
	}

	return true
}

// ComputeSessionHash generates a hash from request content for sticky session binding.
// Priority: metadata.user_id session UUID > system prompt hash > first message hash
func ComputeSessionHash(userID string, systemPrompt string, firstMessage string) string {
	// Try to extract session UUID from user_id
	if idx := strings.LastIndex(userID, "session_"); idx >= 0 {
		session := userID[idx:]
		return hashStr("session:" + session)
	}

	// Fall back to system prompt hash
	if systemPrompt != "" {
		return hashStr("system:" + systemPrompt[:min(len(systemPrompt), 200)])
	}

	// Fall back to first message
	if firstMessage != "" {
		return hashStr("msg:" + firstMessage[:min(len(firstMessage), 200)])
	}

	return ""
}

func hashStr(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16]) // 32 hex chars
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
