package driver

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClaudeCalcCost(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{}, NoopStainlessStore{}, 4)

	tests := []struct {
		name  string
		model string
		usage *Usage
		want  float64
	}{
		{
			name:  "haiku",
			model: "claude-haiku-4-5-20251001",
			usage: &Usage{InputTokens: 1923, OutputTokens: 214},
			want:  0.002993,
		},
		{
			name:  "sonnet",
			model: "claude-sonnet-4-6",
			usage: &Usage{InputTokens: 1488, OutputTokens: 101},
			want:  0.005979,
		},
		{
			name:  "opus",
			model: "claude-opus-4-6",
			usage: &Usage{InputTokens: 1460, OutputTokens: 59},
			want:  0.008775,
		},
		{
			name:  "opus 5",
			model: "claude-opus-5",
			usage: &Usage{InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 300, CacheCreateTokens: 400},
			want:  0.01265,
		},
		{
			name:  "fable",
			model: "claude-fable-5",
			usage: &Usage{InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 300, CacheCreateTokens: 400},
			want:  0.0253,
		},
		{
			name:  "haiku cache",
			model: "claude-haiku-4-5-20251001",
			usage: &Usage{InputTokens: 3, OutputTokens: 13, CacheCreateTokens: 75436},
			want:  0.094363,
		},
		{
			name:  "sonnet cache",
			model: "claude-sonnet-4-6",
			usage: &Usage{InputTokens: 3, OutputTokens: 760, CacheReadTokens: 147856, CacheCreateTokens: 299},
			want:  0.05688705,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := d.CalcCost(tc.model, tc.usage); math.Abs(got-tc.want) > 1e-12 {
				t.Fatalf("CalcCost(%q) = %v, want %v", tc.model, got, tc.want)
			}
		})
	}
}

func TestClaudeModelsIncludeCurrentOpus(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{}, NoopStainlessStore{}, 4)
	models := make(map[string]Model, len(d.Models()))
	for _, model := range d.Models() {
		models[model.ID] = model
	}

	for _, id := range []string{"claude-opus-4-8", "claude-opus-5"} {
		model, ok := models[id]
		if !ok {
			t.Fatalf("%s missing from Claude model catalog", id)
		}
		if model.ContextWindow != 1000000 {
			t.Fatalf("%s context_window = %d, want 1000000", id, model.ContextWindow)
		}
	}
}

func TestClaudeModelsIncludesFable5(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{}, NoopStainlessStore{}, 4)
	for _, model := range d.Models() {
		if model.ID == "claude-mythos-5" {
			t.Fatal("restricted Claude Mythos 5 should not be advertised")
		}
		if model.ID != "claude-fable-5" {
			continue
		}
		if model.ContextWindow != 1000000 {
			t.Fatalf("claude-fable-5 context_window = %d, want 1000000", model.ContextWindow)
		}
		return
	}
	t.Fatal("claude-fable-5 missing from Claude model catalog")
}

func TestClaudeModelsIncludesSonnet5(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{}, NoopStainlessStore{}, 4)
	for _, model := range d.Models() {
		if model.ID != "claude-sonnet-5" {
			continue
		}
		if model.ContextWindow != 1000000 {
			t.Fatalf("claude-sonnet-5 context_window = %d, want 1000000", model.ContextWindow)
		}
		return
	}
	t.Fatal("claude-sonnet-5 missing from Claude model catalog")
}

func TestClaudeInterpret_400DisabledOrganizationBlocks(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{
		Pauses: ErrorPauses{Pause401: time.Minute},
	}, NoopStainlessStore{}, 4)

	before := time.Now()
	body := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"This organization has been disabled."}}`)
	effect := d.Interpret(http.StatusBadRequest, make(http.Header), body, "claude-sonnet-4-6", json.RawMessage(`{}`))

	if effect.Kind != EffectBlock {
		t.Fatalf("Kind = %v, want block", effect.Kind)
	}
	if effect.Scope != EffectScopeBucket {
		t.Fatalf("Scope = %v, want bucket", effect.Scope)
	}
	if effect.UpstreamStatus != http.StatusBadRequest {
		t.Fatalf("UpstreamStatus = %d, want %d", effect.UpstreamStatus, http.StatusBadRequest)
	}
	if effect.CooldownUntil.Before(before.Add(50 * time.Second)) {
		t.Fatalf("CooldownUntil = %s, want around now+1m", effect.CooldownUntil.Format(time.RFC3339))
	}
	if !strings.Contains(effect.ErrorMessage, "organization has been disabled") {
		t.Fatalf("ErrorMessage = %q, want disabled signal", effect.ErrorMessage)
	}
	if !json.Valid(effect.UpdatedState) {
		t.Fatalf("UpdatedState = %q, want valid JSON", string(effect.UpdatedState))
	}
}

func TestClaudeInterpret_403NonBanReturnsReject(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{
		Pauses: ErrorPauses{Pause403: 10 * time.Minute},
	}, NoopStainlessStore{}, 4)

	effect := d.Interpret(http.StatusForbidden, make(http.Header), []byte(`{"error":{"message":"request rejected"}}`), "claude-sonnet-4-6", json.RawMessage(`{}`))

	if effect.Kind != EffectReject {
		t.Fatalf("Kind = %v, want reject", effect.Kind)
	}
	if effect.UpstreamStatus != http.StatusForbidden {
		t.Fatalf("UpstreamStatus = %d, want %d", effect.UpstreamStatus, http.StatusForbidden)
	}
	if !effect.CooldownUntil.IsZero() {
		t.Fatalf("CooldownUntil = %v, want zero", effect.CooldownUntil)
	}
}

func TestClaudeInterpret_400NonBanReturnsReject(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{
		Pauses: ErrorPauses{Pause403: 10 * time.Minute},
	}, NoopStainlessStore{}, 4)

	effect := d.Interpret(http.StatusBadRequest, make(http.Header), []byte(`{"error":{"message":"request rejected"}}`), "claude-sonnet-4-6", json.RawMessage(`{}`))

	if effect.Kind != EffectReject {
		t.Fatalf("Kind = %v, want reject", effect.Kind)
	}
	if effect.UpstreamStatus != http.StatusBadRequest {
		t.Fatalf("UpstreamStatus = %d, want %d", effect.UpstreamStatus, http.StatusBadRequest)
	}
	if !effect.CooldownUntil.IsZero() {
		t.Fatalf("CooldownUntil = %v, want zero", effect.CooldownUntil)
	}
}

func TestClaudeInterpret_404ReturnsReject(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{}, NoopStainlessStore{}, 4)

	effect := d.Interpret(http.StatusNotFound, make(http.Header), []byte(`{"error":{"type":"not_found_error","message":"model: claude-haiku-4-6"}}`), "claude-haiku-4-6", json.RawMessage(`{}`))

	if effect.Kind != EffectReject {
		t.Fatalf("Kind = %v, want reject", effect.Kind)
	}
	if effect.UpstreamStatus != http.StatusNotFound {
		t.Fatalf("UpstreamStatus = %d, want %d", effect.UpstreamStatus, http.StatusNotFound)
	}
	if effect.UpstreamErrorType != "not_found_error" {
		t.Fatalf("UpstreamErrorType = %q, want not_found_error", effect.UpstreamErrorType)
	}
}

func TestClaudeInterpret_502ReturnsServerError(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{}, NoopStainlessStore{}, 4)

	effect := d.Interpret(http.StatusBadGateway, make(http.Header), []byte(`{"error":{"type":"api_error","message":"upstream bad gateway"}}`), "claude-sonnet-4-6", json.RawMessage(`{}`))

	if effect.Kind != EffectServerError {
		t.Fatalf("Kind = %v, want server_error", effect.Kind)
	}
	if effect.UpstreamStatus != http.StatusBadGateway {
		t.Fatalf("UpstreamStatus = %d, want %d", effect.UpstreamStatus, http.StatusBadGateway)
	}
}

func TestClaudePlanTreatsFullHistoryAsPortableSessionAffinity(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{}, NoopStainlessStore{}, 4)
	plan := d.Plan(&RelayInput{Body: map[string]interface{}{
		"metadata": map[string]interface{}{
			"user_id": "user_account__session_123e4567-e89b-12d3-a456-426614174000",
		},
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
			map[string]interface{}{"role": "assistant", "content": "hi"},
		},
	}})
	if plan.Affinity.RawKey != "123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("Plan().Affinity.RawKey = %q", plan.Affinity.RawKey)
	}
	if plan.Affinity.Continuity != AffinityPrefer {
		t.Fatalf("Plan().Affinity.Continuity = %q, want prefer", plan.Affinity.Continuity)
	}
}
