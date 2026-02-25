package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// IsWarmupRequest checks if the request is a warmup/non-productive request.
func IsWarmupRequest(body map[string]interface{}) bool {
	if messages, ok := body["messages"].([]interface{}); ok && len(messages) == 1 {
		if m, ok := messages[0].(map[string]interface{}); ok {
			if content, ok := m["content"].(string); ok && content == "Warmup" {
				return true
			}
			if content, ok := m["content"].([]interface{}); ok && len(content) == 1 {
				if block, ok := content[0].(map[string]interface{}); ok {
					if text, ok := block["text"].(string); ok && text == "Warmup" {
						return true
					}
				}
			}
		}
	}

	systemText := extractSystemText(body)
	if strings.Contains(systemText, "Please write a 5-10 word title") {
		return true
	}
	if strings.Contains(systemText, "nalyze if this message indicates a new conversation topic") {
		return true
	}

	return false
}

func extractSystemText(body map[string]interface{}) string {
	switch s := body["system"].(type) {
	case string:
		return s
	case []interface{}:
		var texts []string
		for _, entry := range s {
			if m, ok := entry.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
		return strings.Join(texts, " ")
	}
	return ""
}

// WarmupEvents returns the synthetic SSE events for a warmup response.
func WarmupEvents(model string) []string {
	id := "msg_warmup_" + generateShortID()
	return []string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"` + id + `","type":"message","role":"assistant","content":[],"model":"` + model + `","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":1}}}` + "\n\n",
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n",
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}` + "\n\n",
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}` + "\n\n",
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}` + "\n\n",
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}` + "\n\n",
	}
}

func generateShortID() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return hex.EncodeToString(h[:8])
}
