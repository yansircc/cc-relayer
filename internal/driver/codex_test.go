package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yansircc/llm-broker/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type trackedReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackedReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestCodexPlanRouteAffinityPrecedenceAndContinuity(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	tests := []struct {
		name           string
		body           map[string]interface{}
		sessionID      string
		wantKey        string
		wantKind       string
		wantContinuity AffinityContinuity
	}{
		{
			name: "prompt cache key wins",
			body: map[string]interface{}{
				"prompt_cache_key": "cache-key",
				"conversation":     map[string]interface{}{"id": "conv-id"},
			},
			sessionID:      "session-id",
			wantKey:        "cache-key",
			wantKind:       "prompt-cache-key",
			wantContinuity: AffinityRequire,
		},
		{
			name:           "conversation object is normalized",
			body:           map[string]interface{}{"conversation": map[string]interface{}{"id": " conv-id "}},
			wantKey:        "conv-id",
			wantKind:       "conversation",
			wantContinuity: AffinityRequire,
		},
		{
			name:           "session header is fallback",
			body:           map[string]interface{}{},
			sessionID:      " session-id ",
			wantKey:        "session-id",
			wantKind:       "session-id",
			wantContinuity: AffinityPrefer,
		},
		{
			name:           "previous response requires known owner",
			body:           map[string]interface{}{"previous_response_id": "resp_123"},
			sessionID:      "session-id",
			wantKey:        "session-id",
			wantKind:       "session-id",
			wantContinuity: AffinityRequire,
		},
		{
			name:           "previous response without stable key remains unresolved",
			body:           map[string]interface{}{"previous_response_id": "resp_123"},
			wantContinuity: AffinityRequire,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := make(http.Header)
			if tt.sessionID != "" {
				headers.Set("session-id", tt.sessionID)
			}
			plan := d.Plan(&RelayInput{Body: tt.body, Headers: headers})
			if plan.Affinity.RawKey != tt.wantKey || plan.Affinity.Kind != tt.wantKind || plan.Affinity.Continuity != tt.wantContinuity {
				t.Fatalf("Plan().Affinity = %#v, want key=%q kind=%q continuity=%q", plan.Affinity, tt.wantKey, tt.wantKind, tt.wantContinuity)
			}
		})
	}
}

func TestCodexPreflightStreamClassifiesCapacityBeforeCommit(t *testing.T) {
	d := NewCodexDriver(CodexConfig{Pauses: ErrorPauses{Pause429: time.Minute}})
	body := &trackedReadCloser{Reader: strings.NewReader("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"message\":\"Selected model is at capacity. Please try a different model.\"}}\n\n")}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}

	result, err := d.PreflightStream(context.Background(), resp, "gpt-5.5", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("PreflightStream: %v", err)
	}
	if result.Effect == nil {
		t.Fatal("PreflightStream effect = nil, want capacity cooldown")
	}
	if result.Effect.Kind != EffectCooldown || result.Effect.Scope != EffectScopeBucket || result.Effect.UpstreamStatus != http.StatusTooManyRequests {
		t.Fatalf("PreflightStream effect = %#v", *result.Effect)
	}
	if len(result.ErrorBody) == 0 {
		t.Fatal("PreflightStream ErrorBody is empty")
	}
	if !body.closed {
		t.Fatal("capacity stream body was not closed")
	}
}

func TestCodexPreflightStreamReplaysAcceptedPrefixByteForByte(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	want := "event: response.created\r\ndata: {\"type\":\"response.created\"}\r\n\r\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(want)),
	}

	result, err := d.PreflightStream(context.Background(), resp, "gpt-5.5", nil)
	if err != nil {
		t.Fatalf("PreflightStream: %v", err)
	}
	if result.Effect != nil {
		t.Fatalf("PreflightStream effect = %#v, want accepted", result.Effect)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != want {
		t.Fatalf("replayed body = %q, want exact %q", got, want)
	}
}

func TestCodexPreflightStreamDoesNotTreatOutputTextAsCapacityEffect(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	want := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Selected model is at capacity\"}\n\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(want)),
	}

	result, err := d.PreflightStream(context.Background(), resp, "gpt-5.5", nil)
	if err != nil {
		t.Fatalf("PreflightStream: %v", err)
	}
	if result.Effect != nil {
		t.Fatalf("output text was misclassified as capacity: %#v", result.Effect)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != want {
		t.Fatalf("replayed body = %q, want %q", got, want)
	}
}

func TestCodexBuildRequestUsesSubjectForAccountHeader(t *testing.T) {
	d := NewCodexDriver(CodexConfig{APIURL: "https://chatgpt.com/backend-api/codex"})
	input := &RelayInput{
		RawBody: []byte(`{"model":"gpt-5.1-codex"}`),
		Headers: make(http.Header),
	}
	acct := &domain.Account{
		Provider: domain.ProviderCodex,
		Subject:  "acct-subject-123",
		Identity: map[string]string{"chatgptAccountId": "stale-identity"},
	}

	req, err := d.BuildRequest(context.Background(), input, acct, "tok")
	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	if got := req.Header.Get("Chatgpt-Account-Id"); got != acct.Subject {
		t.Fatalf("Chatgpt-Account-Id = %q, want %q", got, acct.Subject)
	}
}

func TestCodexBuildRequestNormalizesUnsupportedResponsesFields(t *testing.T) {
	tests := []struct {
		name            string
		rawBody         string
		wantExactBody   bool
		wantInputFields []string
	}{
		{
			name:          "unchanged when unsupported fields are absent",
			rawBody:       `{ "model": "gpt-5.5", "stream": true, "store": false }`,
			wantExactBody: true,
		},
		{
			name:            "removes max output tokens",
			rawBody:         `{"model":"gpt-5.5","max_output_tokens":16384,"stream":true}`,
			wantInputFields: []string{"max_output_tokens"},
		},
		{
			name:            "removes prompt cache retention",
			rawBody:         `{"model":"gpt-5.5","prompt_cache_retention":"24h","store":false}`,
			wantInputFields: []string{"prompt_cache_retention"},
		},
		{
			name:            "removes both and preserves unrelated values",
			rawBody:         `{"model":"gpt-5.5","max_output_tokens":16384,"prompt_cache_retention":"24h","metadata":{"large":9007199254740993},"input":[{"role":"user","content":"hi"}]}`,
			wantInputFields: []string{"max_output_tokens", "prompt_cache_retention"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputBody := make(map[string]interface{})
			if err := json.Unmarshal([]byte(tt.rawBody), &inputBody); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}
			input := &RelayInput{
				Body:    inputBody,
				RawBody: []byte(tt.rawBody),
				Headers: make(http.Header),
			}
			originalRawBody := append([]byte(nil), input.RawBody...)

			d := NewCodexDriver(CodexConfig{APIURL: "https://chatgpt.com/backend-api/codex"})
			req, err := d.BuildRequest(context.Background(), input, &domain.Account{Subject: "acct-1"}, "tok")
			if err != nil {
				t.Fatalf("BuildRequest() error = %v", err)
			}
			gotBody, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}

			if tt.wantExactBody && string(gotBody) != tt.rawBody {
				t.Fatalf("request body = %q, want exact %q", gotBody, tt.rawBody)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(gotBody, &got); err != nil {
				t.Fatalf("unmarshal request body: %v", err)
			}
			for _, field := range codexUnsupportedRequestFields {
				if _, ok := got[field]; ok {
					t.Errorf("request body still contains %q", field)
				}
			}
			if gotModel := string(got["model"]); gotModel != `"gpt-5.5"` {
				t.Errorf("model = %s, want gpt-5.5", gotModel)
			}
			if strings.Contains(tt.name, "preserves unrelated") && !bytes.Contains(gotBody, []byte("9007199254740993")) {
				t.Errorf("request body lost exact unrelated numeric value: %s", gotBody)
			}

			if !bytes.Equal(input.RawBody, originalRawBody) {
				t.Fatalf("BuildRequest mutated RawBody: got %s, want %s", input.RawBody, originalRawBody)
			}
			for _, field := range tt.wantInputFields {
				if _, ok := input.Body[field]; !ok {
					t.Errorf("BuildRequest mutated input Body by removing %q", field)
				}
			}
		})
	}
}

func TestCodexBuildRequestRejectsInvalidJSON(t *testing.T) {
	d := NewCodexDriver(CodexConfig{APIURL: "https://chatgpt.com/backend-api/codex"})
	input := &RelayInput{RawBody: []byte(`{"model":`), Headers: make(http.Header)}

	_, err := d.BuildRequest(context.Background(), input, &domain.Account{Subject: "acct-1"}, "tok")
	if err == nil || !strings.Contains(err.Error(), "decode codex request body") {
		t.Fatalf("BuildRequest() error = %v, want decode error", err)
	}
}

func TestCodexBuildRequestPassesGPT6AstraFields(t *testing.T) {
	rawBody := `{"model":"gpt-6-astra","reasoning":{"effort":"max"},"service_tier":"fast","stream":true,"store":false}`
	input := &RelayInput{RawBody: []byte(rawBody), Headers: make(http.Header)}
	d := NewCodexDriver(CodexConfig{APIURL: "https://chatgpt.com/backend-api/codex"})

	req, err := d.BuildRequest(context.Background(), input, &domain.Account{Subject: "acct-1"}, "tok")
	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	gotBody, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if string(gotBody) != rawBody {
		t.Fatalf("request body = %q, want exact %q", gotBody, rawBody)
	}
}

func TestCodexProbeRequiresSubject(t *testing.T) {
	d := NewCodexDriver(CodexConfig{APIURL: "https://chatgpt.com/backend-api/codex"})
	acct := &domain.Account{Provider: domain.ProviderCodex}
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("event: response.output_text.delta\n")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	_, err := d.Probe(context.Background(), acct, "tok", client)
	if err == nil {
		t.Fatal("Probe() error = nil, want missing subject error")
	}
}

// --- Per-family rate limit tests ---

func multiFamilyHeaders() http.Header {
	h := make(http.Header)
	// Standard codex family
	h.Set("x-codex-primary-used-percent", "62")
	h.Set("x-codex-primary-reset-after-seconds", "7200")
	h.Set("x-codex-secondary-used-percent", "99")
	h.Set("x-codex-secondary-reset-after-seconds", "129000")
	// Spark (bengalfox) family
	h.Set("x-codex-bengalfox-primary-used-percent", "0")
	h.Set("x-codex-bengalfox-primary-reset-after-seconds", "18000")
	h.Set("x-codex-bengalfox-secondary-used-percent", "1")
	h.Set("x-codex-bengalfox-secondary-reset-after-seconds", "604000")
	h.Set("x-codex-bengalfox-limit-name", "GPT-5.3-Codex-Spark")
	return h
}

func TestCaptureHeaders_MultiFamilyParsing(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	state := d.captureHeaders(multiFamilyHeaders(), nil)

	var s CodexState
	if err := json.Unmarshal(state, &s); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if len(s.Families) != 2 {
		t.Fatalf("Families count = %d, want 2", len(s.Families))
	}

	std := s.family("")
	if std.PrimaryUtil != 0.62 {
		t.Errorf("standard PrimaryUtil = %v, want 0.62", std.PrimaryUtil)
	}
	if std.SecondaryUtil != 0.99 {
		t.Errorf("standard SecondaryUtil = %v, want 0.99", std.SecondaryUtil)
	}

	spark := s.family("bengalfox")
	if spark.PrimaryUtil != 0 {
		t.Errorf("spark PrimaryUtil = %v, want 0", spark.PrimaryUtil)
	}
	if spark.SecondaryUtil != 0.01 {
		t.Errorf("spark SecondaryUtil = %v, want 0.01", spark.SecondaryUtil)
	}
	if spark.LimitName != "GPT-5.3-Codex-Spark" {
		t.Errorf("spark LimitName = %q, want GPT-5.3-Codex-Spark", spark.LimitName)
	}
}

func TestCaptureHeaders_LegacyMigration(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	legacy := `{"primary_util":0.5,"primary_reset":9999999999,"secondary_util":0.8,"secondary_reset":9999999999}`

	// New headers arrive (only standard family)
	h := make(http.Header)
	h.Set("x-codex-primary-used-percent", "60")
	h.Set("x-codex-primary-reset-after-seconds", "3600")
	h.Set("x-codex-secondary-used-percent", "85")
	h.Set("x-codex-secondary-reset-after-seconds", "86400")

	state := d.captureHeaders(h, json.RawMessage(legacy))
	var s CodexState
	json.Unmarshal(state, &s)

	// Legacy flat fields should be zeroed
	if s.PrimaryUtil != 0 || s.SecondaryUtil != 0 {
		t.Fatalf("legacy flat fields not zeroed: primary=%v secondary=%v", s.PrimaryUtil, s.SecondaryUtil)
	}

	// New values should be in Families[""]
	std := s.family("")
	if std.PrimaryUtil != 0.6 {
		t.Errorf("migrated PrimaryUtil = %v, want 0.6", std.PrimaryUtil)
	}
	if std.SecondaryUtil != 0.85 {
		t.Errorf("migrated SecondaryUtil = %v, want 0.85", std.SecondaryUtil)
	}
}

func TestCanServe_PerFamily(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	now := time.Now()
	futureReset := now.Add(1 * time.Hour).Unix()

	state, _ := json.Marshal(CodexState{
		Families: map[string]CodexFamilyLimits{
			"":          {SecondaryUtil: 0.99, SecondaryReset: futureReset}, // standard exhausted
			"bengalfox": {SecondaryUtil: 0.01, SecondaryReset: futureReset}, // spark available
		},
	})

	tests := []struct {
		model string
		want  bool
	}{
		{"gpt-5.5", false},            // standard family
		{"gpt-5.3-codex", false},      // standard family exhausted
		{"gpt-5.4", false},            // standard family
		{"gpt-5.3-codex-spark", true}, // spark family has capacity
		{"codex-1", false},            // standard family
	}
	for _, tt := range tests {
		if got := d.CanServe(state, tt.model, now); got != tt.want {
			t.Errorf("CanServe(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestCanServe_ExpiredResetAllows(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	now := time.Now()
	pastReset := now.Add(-1 * time.Hour).Unix()

	state, _ := json.Marshal(CodexState{
		Families: map[string]CodexFamilyLimits{
			"": {SecondaryUtil: 0.99, SecondaryReset: pastReset},
		},
	})

	if !d.CanServe(state, "gpt-5.3-codex", now) {
		t.Error("CanServe should return true when reset time has passed")
	}
}

func TestComputeExhaustedCooldown_PartialExhaustion(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	now := time.Now()
	futureReset := now.Add(2 * time.Hour).Unix()

	// Standard exhausted, spark has capacity — no bucket cooldown
	state, _ := json.Marshal(CodexState{
		Families: map[string]CodexFamilyLimits{
			"":          {SecondaryUtil: 0.99, SecondaryReset: futureReset},
			"bengalfox": {SecondaryUtil: 0.01, SecondaryReset: futureReset},
		},
	})
	cd := d.ComputeExhaustedCooldown(state, now)
	if !cd.IsZero() {
		t.Errorf("expected no cooldown when spark has capacity, got %v", cd)
	}
}

func TestComputeExhaustedCooldown_AllExhausted(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	now := time.Now()
	stdReset := now.Add(2 * time.Hour).Unix()
	sparkReset := now.Add(6 * time.Hour).Unix()

	state, _ := json.Marshal(CodexState{
		Families: map[string]CodexFamilyLimits{
			"":          {SecondaryUtil: 0.99, SecondaryReset: stdReset},
			"bengalfox": {SecondaryUtil: 0.99, SecondaryReset: sparkReset},
		},
	})
	cd := d.ComputeExhaustedCooldown(state, now)
	if cd.IsZero() {
		t.Fatal("expected cooldown when all families exhausted")
	}
	// Should use earliest reset (standard @ 2h)
	if cd.Unix() != stdReset {
		t.Errorf("cooldown = %v, want earliest reset %v", cd.Unix(), stdReset)
	}
}

func TestAutoPriority_BestFamily(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})

	state, _ := json.Marshal(CodexState{
		Families: map[string]CodexFamilyLimits{
			"":          {SecondaryUtil: 0.99}, // 1% remaining
			"bengalfox": {SecondaryUtil: 0.01}, // 99% remaining
		},
	})
	pri := d.AutoPriority(state)
	if pri != 99 {
		t.Errorf("AutoPriority = %d, want 99 (best family capacity)", pri)
	}
}

func TestIsStale_PerFamily(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	now := time.Now()

	// Standard has expired reset, spark is fine
	state, _ := json.Marshal(CodexState{
		Families: map[string]CodexFamilyLimits{
			"":          {PrimaryUtil: 0.5, PrimaryReset: now.Add(-1 * time.Hour).Unix()},
			"bengalfox": {PrimaryUtil: 0.1, PrimaryReset: now.Add(1 * time.Hour).Unix()},
		},
	})
	if !d.IsStale(state, now) {
		t.Error("IsStale should return true when any family has expired reset")
	}
}

func TestGetUtilization_PerFamily(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	futureReset := time.Now().Add(1 * time.Hour).Unix()

	state, _ := json.Marshal(CodexState{
		Families: map[string]CodexFamilyLimits{
			"": {
				PrimaryUtil: 0.42, PrimaryReset: futureReset,
				SecondaryUtil: 0.99, SecondaryReset: futureReset,
			},
			"bengalfox": {
				PrimaryUtil: 0, PrimaryReset: futureReset,
				SecondaryUtil: 0.01, SecondaryReset: futureReset,
				LimitName: "GPT-5.3-Codex-Spark",
			},
		},
	})
	windows := d.GetUtilization(state)
	if len(windows) != 2 {
		t.Fatalf("GetUtilization returned %d windows, want 2 (merged)", len(windows))
	}
	// Primary window: standard 42%, spark 0%
	if windows[0].Label != "primary" {
		t.Errorf("windows[0].Label = %q, want 'primary'", windows[0].Label)
	}
	if windows[0].Pct != 42 {
		t.Errorf("windows[0].Pct = %d, want 42", windows[0].Pct)
	}
	if windows[0].SubPct != 0 {
		t.Errorf("windows[0].SubPct = %d, want 0", windows[0].SubPct)
	}
	if windows[0].SubLabel != "GPT-5.3-Codex-Spark" {
		t.Errorf("windows[0].SubLabel = %q, want 'GPT-5.3-Codex-Spark'", windows[0].SubLabel)
	}
	// Secondary window: standard 99%, spark 1%
	if windows[1].Pct != 99 {
		t.Errorf("windows[1].Pct = %d, want 99", windows[1].Pct)
	}
	if windows[1].SubPct != 1 {
		t.Errorf("windows[1].SubPct = %d, want 1", windows[1].SubPct)
	}
}

func TestCodexModelFamily(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"gpt-5.6-sol", ""},
		{"gpt-5.6-terra", ""},
		{"gpt-5.6-luna", ""},
		{"gpt-6-astra", ""},
		{"gpt-5.5", ""},
		{"gpt-5.3-codex", ""},
		{"gpt-5.4", ""},
		{"gpt-5.3-codex-spark", "bengalfox"},
		{"GPT-5.3-Codex-Spark", "bengalfox"},
		{"codex-1", ""},
		{"gpt-5.1-codex-mini", ""},
	}
	for _, tt := range tests {
		if got := codexModelFamily(tt.model); got != tt.want {
			t.Errorf("codexModelFamily(%q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

func TestCodexModelsIncludesGPT56Family(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	models := d.Models()
	want := map[string]int{
		"gpt-5.6-sol":   1050000,
		"gpt-5.6-terra": 1050000,
		"gpt-5.6-luna":  400000,
	}
	seen := make(map[string]int)
	for _, model := range models {
		if _, ok := want[model.ID]; ok {
			seen[model.ID] = model.ContextWindow
		}
		if model.ID == "gpt-5.6" {
			t.Fatal("gpt-5.6 alias should not be advertised without live Codex backend acceptance")
		}
	}
	for id, contextWindow := range want {
		got, ok := seen[id]
		if !ok {
			t.Fatalf("%s missing from codex model catalog", id)
		}
		if got != contextWindow {
			t.Fatalf("%s context_window = %d, want %d", id, got, contextWindow)
		}
	}
}

func TestCodexModelsIncludesGPT6Astra(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	for _, model := range d.Models() {
		if model.ID != "gpt-6-astra" {
			continue
		}
		if model.ContextWindow != 1050000 {
			t.Fatalf("gpt-6-astra context_window = %d, want %d", model.ContextWindow, 1050000)
		}
		return
	}
	t.Fatal("gpt-6-astra missing from codex model catalog")
}

func TestCodexModelsIncludesGPT55(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	models := d.Models()
	for _, model := range models {
		if model.ID != "gpt-5.5" {
			continue
		}
		if model.ContextWindow != 400000 {
			t.Fatalf("gpt-5.5 context_window = %d, want %d", model.ContextWindow, 400000)
		}
		return
	}
	t.Fatal("gpt-5.5 missing from codex model catalog")
}

func TestCodexProbeModelDefaultsToGPT56Sol(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	if got := d.probeModel(); got != "gpt-5.6-sol" {
		t.Fatalf("probeModel() before traffic = %q, want gpt-5.6-sol", got)
	}
}

func TestParseCodexUsageCapturesCacheWriteTokens(t *testing.T) {
	usage := parseCodexUsage(`{"type":"response.completed","response":{"usage":{"input_tokens":1000,"output_tokens":20,"input_tokens_details":{"cached_tokens":300,"cache_write_tokens":200}}}}`)
	if usage == nil {
		t.Fatal("parseCodexUsage() = nil")
	}
	if usage.InputTokens != 1000 {
		t.Fatalf("InputTokens = %d, want 1000", usage.InputTokens)
	}
	if usage.OutputTokens != 20 {
		t.Fatalf("OutputTokens = %d, want 20", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 300 {
		t.Fatalf("CacheReadTokens = %d, want 300", usage.CacheReadTokens)
	}
	if usage.CacheCreateTokens != 200 {
		t.Fatalf("CacheCreateTokens = %d, want 200", usage.CacheCreateTokens)
	}
}

func TestCodexCalcCostGPT56Pricing(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	usage := &Usage{InputTokens: 1000, OutputTokens: 10, CacheReadTokens: 300, CacheCreateTokens: 200}
	tests := []struct {
		model string
		want  float64
	}{
		{model: "gpt-5.6-sol", want: 0.0084},
		{model: "gpt-5.6", want: 0.0084},
		{model: "gpt-5.6-terra", want: 0.0042},
		{model: "gpt-5.6-luna", want: 0.00168},
	}
	for _, tt := range tests {
		if got := d.CalcCost(tt.model, usage); math.Abs(got-tt.want) > 1e-12 {
			t.Fatalf("CalcCost(%q) = %.12f, want %.12f", tt.model, got, tt.want)
		}
	}
}

func TestCodexCalcCostGPT6AstraPricing(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	usage := &Usage{InputTokens: 1000, OutputTokens: 10, CacheReadTokens: 300, CacheCreateTokens: 200}
	if got, want := d.CalcCost("gpt-6-astra", usage), 0.0166; math.Abs(got-want) > 1e-12 {
		t.Fatalf("CalcCost(%q) = %.12f, want %.12f", "gpt-6-astra", got, want)
	}
}

func TestDiscoverCodexFamilyPrefixes(t *testing.T) {
	h := multiFamilyHeaders()
	prefixes := discoverCodexFamilyPrefixes(h)
	if len(prefixes) != 2 {
		t.Fatalf("found %d prefixes, want 2", len(prefixes))
	}
	// Should contain "" and "bengalfox"
	foundStd, foundSpark := false, false
	for _, p := range prefixes {
		if p == "" {
			foundStd = true
		}
		if p == "bengalfox" {
			foundSpark = true
		}
	}
	if !foundStd || !foundSpark {
		t.Errorf("prefixes = %v, want [\"\", \"bengalfox\"]", prefixes)
	}
}

func TestCodexInterpretBadRequestRejects(t *testing.T) {
	d := NewCodexDriver(CodexConfig{APIURL: "https://chatgpt.com/backend-api/codex"})
	body := []byte(`{"detail":"The 'gpt-5.1-codex' model is not supported when using Codex with a ChatGPT account."}`)
	eff := d.Interpret(http.StatusBadRequest, make(http.Header), body, "gpt-5.1-codex", nil)
	if eff.Kind != EffectReject {
		t.Fatalf("Interpret(400).Kind = %v, want EffectReject", eff.Kind)
	}
	if !eff.CooldownUntil.IsZero() {
		t.Fatalf("Interpret(400) set cooldown %v, want zero (a 400 must not penalize the account)", eff.CooldownUntil)
	}
	if eff.UpstreamStatus != http.StatusBadRequest {
		t.Fatalf("Interpret(400).UpstreamStatus = %d, want 400", eff.UpstreamStatus)
	}
}

func TestCodexProbeModelFollowsObservedModel(t *testing.T) {
	d := NewCodexDriver(CodexConfig{APIURL: "https://chatgpt.com/backend-api/codex"})

	// Before any traffic, probe falls back to the catalog's primary model.
	if got, want := d.probeModel(), d.Models()[0].ID; got != want {
		t.Fatalf("probeModel() before traffic = %q, want catalog primary %q", got, want)
	}

	// A successful standard-family relay updates the observed model.
	d.Interpret(http.StatusOK, make(http.Header), nil, "gpt-5.6", nil)
	if got := d.probeModel(); got != "gpt-5.6" {
		t.Fatalf("probeModel() after observing gpt-5.6 = %q, want gpt-5.6", got)
	}

	// Spark-family and empty models are ignored so probes keep eliciting full headers.
	d.Interpret(http.StatusOK, make(http.Header), nil, "gpt-5.3-codex-spark", nil)
	d.Interpret(http.StatusOK, make(http.Header), nil, "", nil)
	if got := d.probeModel(); got != "gpt-5.6" {
		t.Fatalf("probeModel() after spark/empty = %q, want unchanged gpt-5.6", got)
	}
}

func TestCodexProbeUsesObservedModelInBody(t *testing.T) {
	d := NewCodexDriver(CodexConfig{APIURL: "https://chatgpt.com/backend-api/codex"})
	d.Interpret(http.StatusOK, make(http.Header), nil, "gpt-5.6", nil)

	var sentBody string
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(req.Body)
			sentBody = string(b)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("event: response.output_text.delta\n")),
				Header:     make(http.Header),
			}, nil
		}),
	}
	acct := &domain.Account{Provider: domain.ProviderCodex, Subject: "subj-1"}
	if _, err := d.Probe(context.Background(), acct, "tok", client); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if !strings.Contains(sentBody, `"model":"gpt-5.6"`) {
		t.Fatalf("probe body = %q, want it to use observed model gpt-5.6", sentBody)
	}
}
