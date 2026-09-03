package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yansircc/llm-broker/internal/domain"
	"github.com/yansircc/llm-broker/internal/driver"
)

func TestPublicStatusRouteReturnsMinimalProviderProjection(t *testing.T) {
	srv := newTestServer(t)
	claude := driver.NewClaudeDriver(driver.ClaudeConfig{}, driver.NoopStainlessStore{}, 4)
	codex := driver.NewCodexDriver(driver.CodexConfig{})
	srv.adminDrivers = map[domain.Provider]driver.AdminDriver{
		domain.ProviderClaude: claude,
		domain.ProviderCodex:  codex,
	}
	srv.pool.SetDrivers(map[domain.Provider]driver.SchedulerDriver{
		domain.ProviderClaude: claude,
		domain.ProviderCodex:  codex,
	})

	now := time.Now().UTC()
	claudeReset := now.Add(5 * time.Hour).Unix()
	claudeCooldown := now.Add(3 * time.Minute)
	codexReset := now.Add(7 * 24 * time.Hour).Unix()
	accounts := []*domain.Account{
		{
			ID: "claude-newer", Email: "secret-newer@example.com", Provider: domain.ProviderClaude,
			Subject: "claude-subject-newer", Status: domain.StatusBlocked, CreatedAt: now.Add(-time.Hour),
			ProviderStateJSON: `{"five_hour_util":0.5,"five_hour_reset":` + jsonInt64(claudeReset) + `}`,
		},
		{
			ID: "claude-older", Email: "secret-older@example.com", Provider: domain.ProviderClaude,
			Subject: "claude-subject-older", Status: domain.StatusActive, CreatedAt: now.Add(-2 * time.Hour),
			CooldownUntil:     &claudeCooldown,
			ProviderStateJSON: `{"five_hour_util":0.25,"five_hour_reset":` + jsonInt64(claudeReset) + `}`,
		},
		{
			ID: "codex-active", Email: "codex-secret@example.com", Provider: domain.ProviderCodex,
			Subject: "codex-subject", Status: domain.StatusActive, CreatedAt: now,
			ProviderStateJSON: `{"families":{"":{"pu":0.5,"pr":` + jsonInt64(codexReset) + `,"su":0.25,"sr":` + jsonInt64(codexReset) + `},"bengalfox":{"pu":0.99,"pr":` + jsonInt64(codexReset) + `,"su":0.98,"sr":` + jsonInt64(codexReset) + `,"name":"GPT-5.3-Codex-Spark"}}}`,
		},
		{
			ID: "codex-disabled", Email: "disabled-secret@example.com", Provider: domain.ProviderCodex,
			Subject: "codex-disabled-subject", Status: domain.StatusDisabled, CreatedAt: now.Add(time.Hour),
		},
		{
			ID: "gemini-active", Email: "gemini-secret@example.com", Provider: domain.ProviderGemini,
			Subject: "gemini-subject", Status: domain.StatusActive, CreatedAt: now.Add(2 * time.Hour),
		},
	}
	for _, account := range accounts {
		if err := srv.pool.Add(account); err != nil {
			t.Fatalf("pool.Add(%s): %v", account.ID, err)
		}
	}

	mux := http.NewServeMux()
	srv.registerOperationalRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	var response map[string][]publicStatusAccount
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(response["claude"]) != 2 {
		t.Fatalf("claude accounts = %d, want 2", len(response["claude"]))
	}
	if len(response["codex"]) != 1 {
		t.Fatalf("codex accounts = %d, want 1", len(response["codex"]))
	}
	if _, ok := response["gemini"]; ok {
		t.Fatalf("public response includes gemini: %#v", response["gemini"])
	}
	if got := response["claude"][0]; got.Status != "active" || got.CooldownUntil != claudeCooldown.Unix() {
		t.Fatalf("first claude account = %#v, want older active account with cooldown", got)
	}
	if got := response["claude"][0].Limits; len(got) != 1 || got[0].Name != "5h" || got[0].RemainingPct != 75 || got[0].ResetAt != claudeReset {
		t.Fatalf("claude limits = %#v", got)
	}
	if got := response["codex"][0].Limits; len(got) != 2 || got[0].Name != "primary" || got[0].RemainingPct != 50 || got[1].Name != "secondary" || got[1].RemainingPct != 75 {
		t.Fatalf("codex limits = %#v", got)
	}

	body := w.Body.String()
	for _, forbidden := range []string{
		`"id"`, `"email"`, `"subject"`, `"cell_id"`, `"weight"`, `"last_used_at"`,
		`"available_native"`, `"available_compat"`, `"sub_label"`, `"sub_pct"`, `"sub_reset"`,
		"spark", "gemini", "secret@example.com", "codex-secret@example.com", "disabled-secret@example.com",
	} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("public response contains forbidden value %q: %s", forbidden, body)
		}
	}
}

func TestPublicStatusRouteReturnsEmptyObject(t *testing.T) {
	srv := newTestServer(t)
	mux := http.NewServeMux()
	srv.registerOperationalRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != "{}" {
		t.Fatalf("body = %s, want {}", got)
	}
}

func jsonInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
