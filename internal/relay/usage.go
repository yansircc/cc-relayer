package relay

import (
	"encoding/json"
	"strings"
)

// Usage tracks token consumption from a relay response.
type Usage struct {
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	Model                    string
}

// ParseMessageStart extracts input token counts and model from a message_start SSE event.
func ParseMessageStart(data []byte, u *Usage) {
	var event struct {
		Type    string `json:"type"`
		Message struct {
			Model string `json:"model"`
			Usage struct {
				InputTokens              int `json:"input_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return
	}
	if event.Type != "message_start" {
		return
	}
	u.InputTokens = event.Message.Usage.InputTokens
	u.CacheCreationInputTokens = event.Message.Usage.CacheCreationInputTokens
	u.CacheReadInputTokens = event.Message.Usage.CacheReadInputTokens
	if event.Message.Model != "" {
		u.Model = event.Message.Model
	}
}

// ParseMessageDelta extracts output token count from a message_delta SSE event.
func ParseMessageDelta(data []byte, u *Usage) {
	var event struct {
		Type  string `json:"type"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return
	}
	if event.Type != "message_delta" {
		return
	}
	u.OutputTokens += event.Usage.OutputTokens
}

// ParseJSONUsage extracts usage from a non-streaming JSON response body.
func ParseJSONUsage(body []byte) *Usage {
	var resp struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	return &Usage{
		InputTokens:              resp.Usage.InputTokens,
		OutputTokens:             resp.Usage.OutputTokens,
		CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		Model:                    resp.Model,
	}
}

// IsOpus returns true if the model name indicates an Opus model.
func IsOpus(model string) bool {
	return strings.Contains(strings.ToLower(model), "opus")
}
