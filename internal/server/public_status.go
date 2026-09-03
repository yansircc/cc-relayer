package server

import (
	"net/http"
	"sort"
	"time"

	"github.com/yansircc/llm-broker/internal/domain"
)

type publicStatusLimit struct {
	Name         string `json:"name"`
	RemainingPct int    `json:"remaining_pct"`
	ResetAt      int64  `json:"reset_at,omitempty"`
}

type publicStatusAccount struct {
	Status        string              `json:"status"`
	CooldownUntil int64               `json:"cooldown_until,omitempty"`
	Limits        []publicStatusLimit `json:"limits"`
}

var publicStatusProviders = map[domain.Provider]struct{}{
	domain.ProviderClaude: {},
	domain.ProviderCodex:  {},
}

func (s *Server) handlePublicStatus(w http.ResponseWriter, r *http.Request) {
	accounts := s.pool.List()
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Provider != accounts[j].Provider {
			return accounts[i].Provider < accounts[j].Provider
		}
		if !accounts[i].CreatedAt.Equal(accounts[j].CreatedAt) {
			return accounts[i].CreatedAt.Before(accounts[j].CreatedAt)
		}
		return accounts[i].ID < accounts[j].ID
	})

	now := time.Now()
	response := make(map[string][]publicStatusAccount)
	for _, account := range accounts {
		if _, public := publicStatusProviders[account.Provider]; !public || account.Status == domain.StatusDisabled {
			continue
		}

		projection := s.projectAccount(account)
		limits := make([]publicStatusLimit, 0, len(projection.windows))
		for _, window := range projection.windows {
			limits = append(limits, publicStatusLimit{
				Name:         window.Label,
				RemainingPct: 100 - window.Pct,
				ResetAt:      window.Reset,
			})
		}

		view := publicStatusAccount{
			Status: string(account.Status),
			Limits: limits,
		}
		if account.CooldownUntil != nil && account.CooldownUntil.After(now) {
			view.CooldownUntil = account.CooldownUntil.Unix()
		}

		provider := string(account.Provider)
		if _, ok := response[provider]; !ok {
			response[provider] = []publicStatusAccount{}
		}
		response[provider] = append(response[provider], view)
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}
