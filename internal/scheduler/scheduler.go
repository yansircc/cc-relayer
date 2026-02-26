package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"time"

	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/config"
)

// Scheduler selects accounts for requests.
type Scheduler struct {
	accounts *account.AccountStore
	cfg      *config.Config
}

func New(as *account.AccountStore, cfg *config.Config) *Scheduler {
	return &Scheduler{accounts: as, cfg: cfg}
}

// SelectOptions provides context for account selection.
type SelectOptions struct {
	BoundAccountID string   // API Key bound account
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
			reason := fmt.Sprintf("bound account %s unavailable (status=%s", opts.BoundAccountID, acct.Status)
			if !acct.Schedulable {
				reason += ", unschedulable"
			}
			if acct.OverloadedUntil != nil {
				reason += fmt.Sprintf(", overloaded until %s", acct.OverloadedUntil.Format(time.RFC3339))
			}
			if acct.ErrorMessage != "" {
				reason += ": " + acct.ErrorMessage
			}
			reason += ")"
			return nil, fmt.Errorf("%s", reason)
		}
	}

	// 2. Pool selection — filter, sort, pick best
	all, err := s.accounts.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}

	var candidates []*account.Account
	for _, acct := range all {
		if slices.Contains(opts.ExcludeIDs, acct.ID) {
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

	// Compute effective priority for each candidate
	type scored struct {
		acct     *account.Account
		priority int
	}
	scoredCandidates := make([]scored, len(candidates))
	for i, acct := range candidates {
		pri := acct.Priority
		if acct.PriorityMode == "auto" {
			// Auto priority: derive from FiveHourStatus header
			// "allowed_warning" = nearing limit → lower priority
			switch acct.FiveHourStatus {
			case "allowed_warning":
				pri = 30
			default:
				pri = 100
			}
		}
		scoredCandidates[i] = scored{acct: acct, priority: pri}
	}

	// Sort: priority DESC, then lastUsedAt ASC (round-robin effect)
	sort.Slice(scoredCandidates, func(i, j int) bool {
		if scoredCandidates[i].priority != scoredCandidates[j].priority {
			return scoredCandidates[i].priority > scoredCandidates[j].priority
		}
		ti := timeOrZero(scoredCandidates[i].acct.LastUsedAt)
		tj := timeOrZero(scoredCandidates[j].acct.LastUsedAt)
		return ti.Before(tj)
	})

	selected := scoredCandidates[0].acct

	slog.Debug("account selected", "accountId", selected.ID, "email", selected.Email, "priority", scoredCandidates[0].priority, "mode", selected.PriorityMode)
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

	// Opus rate limit check
	if opts.IsOpusRequest && acct.OpusRateLimitEndAt != nil {
		if time.Now().Before(*acct.OpusRateLimitEndAt) {
			return false
		}
	}

	return true
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
