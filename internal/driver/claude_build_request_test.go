package driver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yansircc/llm-broker/internal/domain"
)

type noopClaudeStainlessBinder struct{}

func (noopClaudeStainlessBinder) GetStainless(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (noopClaudeStainlessBinder) SetStainlessNX(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}

func TestClaudeBuildRequestNormalizesSystemEnvelopeForSonnet(t *testing.T) {
	t.Run("missing system becomes Claude Code block", func(t *testing.T) {
		body := buildClaudeRequestBody(t, "claude-sonnet-4-6", map[string]interface{}{
			"max_tokens": 1,
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hello"},
			},
		})
		got := buildClaudeUpstreamBody(t, body, false)

		system := mustSystemBlocks(t, got["system"])
		if len(system) != 1 {
			t.Fatalf("len(system) = %d, want 1", len(system))
		}
		if text, _ := system[0]["text"].(string); text != claudeCodeSystemBlockText {
			t.Fatalf("system[0].text = %q", text)
		}
		if cc := system[0]["cache_control"]; cc == nil {
			t.Fatal("system[0].cache_control = nil, want ephemeral policy")
		}
	})

	t.Run("string system becomes Claude Code blocks", func(t *testing.T) {
		body := buildClaudeRequestBody(t, "claude-sonnet-4-6", map[string]interface{}{
			"max_tokens": 1,
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hello"},
			},
			"system": "You are a personal assistant running inside OpenClaw.",
		})
		got := buildClaudeUpstreamBody(t, body, false)

		system := mustSystemBlocks(t, got["system"])
		if len(system) != 2 {
			t.Fatalf("len(system) = %d, want 2", len(system))
		}
		if text, _ := system[0]["text"].(string); text != claudeCodeSystemBlockText {
			t.Fatalf("system[0].text = %q", text)
		}
		if text, _ := system[1]["text"].(string); text != "You are a personal assistant running inside OpenClaw." {
			t.Fatalf("system[1].text = %q", text)
		}
	})

	t.Run("existing array gets Claude Code prefix once", func(t *testing.T) {
		body := buildClaudeRequestBody(t, "claude-sonnet-4-6", map[string]interface{}{
			"max_tokens": 1,
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hello"},
			},
			"system": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": "You are a personal assistant running inside OpenClaw.",
					"cache_control": map[string]interface{}{
						"type": "ephemeral",
					},
				},
			},
		})
		got := buildClaudeUpstreamBody(t, body, false)

		system := mustSystemBlocks(t, got["system"])
		if len(system) != 2 {
			t.Fatalf("len(system) = %d, want 2", len(system))
		}
		if text, _ := system[0]["text"].(string); text != claudeCodeSystemBlockText {
			t.Fatalf("system[0].text = %q", text)
		}
		if text, _ := system[1]["text"].(string); text != "You are a personal assistant running inside OpenClaw." {
			t.Fatalf("system[1].text = %q", text)
		}
	})
}

func TestClaudeBuildRequestNormalizesSystemEnvelopeForCurrentOpus(t *testing.T) {
	for _, model := range []string{"claude-opus-4-8", "claude-opus-5"} {
		t.Run(model, func(t *testing.T) {
			body := buildClaudeRequestBody(t, model, map[string]interface{}{
				"max_tokens": 1,
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": "hello"},
				},
			})
			got := buildClaudeUpstreamBody(t, body, false)

			if got["model"] != model {
				t.Fatalf("model = %#v, want %q", got["model"], model)
			}
			system := mustSystemBlocks(t, got["system"])
			if len(system) != 1 {
				t.Fatalf("len(system) = %d, want 1", len(system))
			}
			if text, _ := system[0]["text"].(string); text != claudeCodeSystemBlockText {
				t.Fatalf("system[0].text = %q", text)
			}
		})
	}
}

func TestClaudeBuildRequestNormalizesSystemEnvelopeForFable(t *testing.T) {
	body := buildClaudeRequestBody(t, "claude-fable-5", map[string]interface{}{
		"max_tokens": 1,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	})
	got := buildClaudeUpstreamBody(t, body, false)

	if got["model"] != "claude-fable-5" {
		t.Fatalf("model = %#v", got["model"])
	}
	system := mustSystemBlocks(t, got["system"])
	if len(system) != 1 {
		t.Fatalf("len(system) = %d, want 1", len(system))
	}
	if text, _ := system[0]["text"].(string); text != claudeCodeSystemBlockText {
		t.Fatalf("system[0].text = %q", text)
	}
}

func TestClaudeBuildRequestNormalizesSystemEnvelopeForSonnet5(t *testing.T) {
	body := buildClaudeRequestBody(t, "claude-sonnet-5", map[string]interface{}{
		"max_tokens": 1,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	})
	got := buildClaudeUpstreamBody(t, body, false)

	if got["model"] != "claude-sonnet-5" {
		t.Fatalf("model = %#v", got["model"])
	}
	system := mustSystemBlocks(t, got["system"])
	if len(system) != 1 {
		t.Fatalf("len(system) = %d, want 1", len(system))
	}
	if text, _ := system[0]["text"].(string); text != claudeCodeSystemBlockText {
		t.Fatalf("system[0].text = %q", text)
	}
}

func TestClaudeBuildRequestLeavesHaikuAndCountTokensUnchanged(t *testing.T) {
	t.Run("haiku request stays untouched", func(t *testing.T) {
		body := buildClaudeRequestBody(t, "claude-haiku-4-5", map[string]interface{}{
			"max_tokens": 1,
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hello"},
			},
		})
		got := buildClaudeUpstreamBody(t, body, false)
		if got["model"] != "claude-haiku-4-5-20251001" {
			t.Fatalf("model = %#v, want haiku snapshot id", got["model"])
		}
		if _, ok := got["system"]; ok {
			t.Fatalf("system = %#v, want absent for haiku request", got["system"])
		}
	})

	t.Run("haiku 4.6 alias maps to official snapshot", func(t *testing.T) {
		body := buildClaudeRequestBody(t, "claude-haiku-4-6", map[string]interface{}{
			"max_tokens": 1,
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hello"},
			},
		})
		got := buildClaudeUpstreamBody(t, body, false)
		if got["model"] != "claude-haiku-4-5-20251001" {
			t.Fatalf("model = %#v, want official haiku snapshot", got["model"])
		}
	})

	t.Run("count_tokens request stays untouched", func(t *testing.T) {
		body := buildClaudeRequestBody(t, "claude-sonnet-4-6", map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hello"},
			},
		})
		got := buildClaudeUpstreamBody(t, body, true)
		if _, ok := got["system"]; ok {
			t.Fatalf("system = %#v, want absent for count_tokens request", got["system"])
		}
	})
}

func TestClaudeBuildRequestRejectsUnsupportedModelsLocally(t *testing.T) {
	t.Run("foreign OpenAI model is rejected", func(t *testing.T) {
		body := buildClaudeRequestBody(t, "gpt-5.4", map[string]interface{}{
			"max_tokens": 1,
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hello"},
			},
		})
		_, err := buildClaudeUpstreamBodyE(t, body, false)
		var requestErr *RequestValidationError
		if !errors.As(err, &requestErr) {
			t.Fatalf("error = %v, want RequestValidationError", err)
		}
		if requestErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("StatusCode = %d, want 400", requestErr.StatusCode)
		}
		if !strings.Contains(requestErr.Message, "does not belong to Claude") {
			t.Fatalf("Message = %q", requestErr.Message)
		}
	})

	t.Run("unknown Claude model is rejected", func(t *testing.T) {
		body := buildClaudeRequestBody(t, "claude-3-5-haiku-20241022", map[string]interface{}{
			"max_tokens": 1,
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hello"},
			},
		})
		_, err := buildClaudeUpstreamBodyE(t, body, false)
		var requestErr *RequestValidationError
		if !errors.As(err, &requestErr) {
			t.Fatalf("error = %v, want RequestValidationError", err)
		}
		if requestErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("StatusCode = %d, want 400", requestErr.StatusCode)
		}
		if !strings.Contains(requestErr.Message, "unsupported Claude model") {
			t.Fatalf("Message = %q", requestErr.Message)
		}
	})

	t.Run("compat-only alias remains rejected on native path", func(t *testing.T) {
		body := buildClaudeRequestBody(t, "claude-sonnet-4-5-20250929", map[string]interface{}{
			"max_tokens": 1,
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hello"},
			},
		})
		_, err := buildClaudeUpstreamBodyE(t, body, false)
		var requestErr *RequestValidationError
		if !errors.As(err, &requestErr) {
			t.Fatalf("error = %v, want RequestValidationError", err)
		}
		if requestErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("StatusCode = %d, want 400", requestErr.StatusCode)
		}
		if !strings.Contains(requestErr.Message, "unsupported Claude model") {
			t.Fatalf("Message = %q", requestErr.Message)
		}
	})
}

func buildClaudeRequestBody(t *testing.T, model string, body map[string]interface{}) []byte {
	t.Helper()
	body["model"] = model
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}

func buildClaudeUpstreamBody(t *testing.T, rawBody []byte, isCountTokens bool) map[string]interface{} {
	t.Helper()

	got, err := buildClaudeUpstreamBodyE(t, rawBody, isCountTokens)
	if err != nil {
		t.Fatalf("buildClaudeUpstreamBodyE() error = %v", err)
	}
	return got
}

func buildClaudeUpstreamBodyE(t *testing.T, rawBody []byte, isCountTokens bool) (map[string]interface{}, error) {
	t.Helper()

	d := NewClaudeDriver(ClaudeConfig{
		APIURL:     "https://claude.example/v1/messages",
		APIVersion: "2023-06-01",
		BetaHeader: "claude-code-20250219",
	}, noopClaudeStainlessBinder{}, 8)
	input := &RelayInput{
		RawBody:       rawBody,
		Headers:       make(http.Header),
		IsCountTokens: isCountTokens,
	}
	acct := &domain.Account{
		ID:       "acct-1",
		Provider: domain.ProviderClaude,
		Identity: map[string]string{"account_uuid": "org-1"},
	}

	req, err := d.BuildRequest(context.Background(), input, acct, "tok")
	if err != nil {
		return nil, err
	}

	payload, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return got, nil
}

func mustSystemBlocks(t *testing.T, v interface{}) []map[string]interface{} {
	t.Helper()

	rawBlocks, ok := v.([]interface{})
	if !ok {
		t.Fatalf("system = %#v, want []interface{}", v)
	}
	blocks := make([]map[string]interface{}, 0, len(rawBlocks))
	for _, raw := range rawBlocks {
		block, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("system block = %#v, want map[string]interface{}", raw)
		}
		blocks = append(blocks, block)
	}
	return blocks
}
