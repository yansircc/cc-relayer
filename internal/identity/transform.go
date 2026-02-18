package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yansir/claude-relay/internal/account"
	"github.com/yansir/claude-relay/internal/config"
	"github.com/yansir/claude-relay/internal/store"
)

// billingHeaderPattern matches x-anthropic-billing-header entries in system prompts.
var billingHeaderPattern = regexp.MustCompile(`(?i)x-anthropic-billing-header`)

// Transformer applies all identity transformations to a request.
type Transformer struct {
	store     *store.Store
	sigCache  *SignatureCache
	cfg       *config.Config
}

func NewTransformer(s *store.Store, sc *SignatureCache, cfg *config.Config) *Transformer {
	return &Transformer{store: s, sigCache: sc, cfg: cfg}
}

// TransformResult holds the results of a transformation.
type TransformResult struct {
	Body          map[string]interface{}
	Headers       http.Header
	SessionHash   string
	IsRealCC      bool
	ToolNameMap   map[string]string // transformed → original (for response restoration)
}

// Transform applies all identity transformations to a request.
func (t *Transformer) Transform(
	ctx context.Context,
	body map[string]interface{},
	reqHeaders http.Header,
	acct *account.Account,
) *TransformResult {
	result := &TransformResult{
		Body:    body,
		Headers: FilterHeaders(reqHeaders),
	}

	// 1. Strip billing headers from system prompt
	t.stripBillingHeaders(body)

	// 2. Enforce cache_control compliance (max 4 blocks, strip TTL)
	t.enforceCacheControl(body)

	// 3. Check if real Claude Code request
	result.IsRealCC = IsClaudeCodeRequest(body["system"])

	// 4. Inject system prompt if not real CC
	if !result.IsRealCC {
		body["system"] = InjectClaudeCodePrompt(body["system"])
	}

	// 5. Rewrite metadata.user_id
	accountUUID := GetAccountUUID(acct.ExtInfo)
	if metadata, ok := body["metadata"].(map[string]interface{}); ok {
		if origUserID, ok := metadata["user_id"].(string); ok {
			metadata["user_id"] = RewriteUserID(origUserID, acct.ID, accountUUID)
		}
	}

	// 6. Compute session hash
	result.SessionHash = t.computeSessionHashFromBody(body)

	// 7. Bind/restore stainless headers
	RemoveAllStainless(result.Headers) // Clear client's stainless first
	BindStainlessHeaders(ctx, t.store, acct.ID, reqHeaders, result.Headers)

	// 8. Restore thinking signatures
	t.restoreSignatures(body, acct.ID)

	// 9. Tool name normalization (non-CC only)
	if !result.IsRealCC {
		result.ToolNameMap = t.normalizeToolNames(body)
	}

	return result
}

// RestoreToolNamesInResponse reverses tool name transformation in response data.
func (t *Transformer) RestoreToolNamesInResponse(data []byte, nameMap map[string]string) []byte {
	if len(nameMap) == 0 {
		return data
	}
	s := string(data)
	for transformed, original := range nameMap {
		s = strings.ReplaceAll(s, `"`+transformed+`"`, `"`+original+`"`)
	}
	return []byte(s)
}

// CaptureSignatures extracts and caches thinking signatures from a response event.
func (t *Transformer) CaptureSignatures(sessionID string, event map[string]interface{}) {
	// Look for content_block_stop with thinking block
	if event["type"] != "content_block_stop" {
		return
	}
	contentBlock, ok := event["content_block"].(map[string]interface{})
	if !ok {
		return
	}
	if contentBlock["type"] != "thinking" {
		return
	}
	sig, _ := contentBlock["signature"].(string)
	text, _ := contentBlock["thinking"].(string)
	if sig != "" && text != "" {
		t.sigCache.Store(sessionID, text, sig)
	}
}

// --- Internal methods ---

func (t *Transformer) stripBillingHeaders(body map[string]interface{}) {
	system, ok := body["system"]
	if !ok {
		return
	}

	switch s := system.(type) {
	case []interface{}:
		filtered := make([]interface{}, 0, len(s))
		for _, entry := range s {
			if m, ok := entry.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					if billingHeaderPattern.MatchString(text) {
						continue // Strip billing header entries
					}
				}
			}
			filtered = append(filtered, entry)
		}
		body["system"] = filtered
	}
}

func (t *Transformer) enforceCacheControl(body map[string]interface{}) {
	maxBlocks := t.cfg.MaxCacheControls

	// Strip TTL from all cache_control entries and count them
	total := 0
	total += stripAndCountCacheControl(body, "system")
	total += stripAndCountCacheControl(body, "messages")

	if total <= maxBlocks {
		return
	}

	// Remove excess: from messages first, then system
	excess := total - maxBlocks
	excess = removeCacheControls(body, "messages", excess)
	if excess > 0 {
		removeCacheControls(body, "system", excess)
	}
}

func stripAndCountCacheControl(body map[string]interface{}, field string) int {
	count := 0
	walkContentBlocks(body[field], func(block map[string]interface{}) {
		if cc, ok := block["cache_control"]; ok {
			count++
			// Strip TTL if present
			if ccMap, ok := cc.(map[string]interface{}); ok {
				delete(ccMap, "ttl")
			}
		}
	})
	return count
}

func removeCacheControls(body map[string]interface{}, field string, toRemove int) int {
	if toRemove <= 0 {
		return 0
	}
	removed := 0
	walkContentBlocks(body[field], func(block map[string]interface{}) {
		if removed >= toRemove {
			return
		}
		if _, ok := block["cache_control"]; ok {
			delete(block, "cache_control")
			removed++
		}
	})
	return toRemove - removed
}

func walkContentBlocks(v interface{}, fn func(map[string]interface{})) {
	switch s := v.(type) {
	case []interface{}:
		for _, item := range s {
			if m, ok := item.(map[string]interface{}); ok {
				fn(m)
				// Recurse into content arrays
				if content, ok := m["content"]; ok {
					walkContentBlocks(content, fn)
				}
			}
		}
	case string:
		// System prompt as string — no cache_control to process
	}
}

func (t *Transformer) computeSessionHashFromBody(body map[string]interface{}) string {
	var userID, systemPrompt, firstMsg string

	if metadata, ok := body["metadata"].(map[string]interface{}); ok {
		userID, _ = metadata["user_id"].(string)
	}
	if sys, ok := body["system"].(string); ok {
		systemPrompt = sys
	} else if sysList, ok := body["system"].([]interface{}); ok && len(sysList) > 0 {
		if m, ok := sysList[0].(map[string]interface{}); ok {
			systemPrompt, _ = m["text"].(string)
		}
	}
	if msgs, ok := body["messages"].([]interface{}); ok && len(msgs) > 0 {
		if m, ok := msgs[0].(map[string]interface{}); ok {
			if content, ok := m["content"].(string); ok {
				firstMsg = content
			}
		}
	}

	return computeSessionHashInline(userID, systemPrompt, firstMsg)
}

func (t *Transformer) restoreSignatures(body map[string]interface{}, accountID string) {
	messages, ok := body["messages"].([]interface{})
	if !ok {
		return
	}

	sessionID := ""
	if metadata, ok := body["metadata"].(map[string]interface{}); ok {
		if uid, ok := metadata["user_id"].(string); ok {
			sessionID = ExtractSessionUUID(uid)
		}
	}
	if sessionID == "" {
		return
	}

	for _, msg := range messages {
		m, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}
		content, ok := m["content"].([]interface{})
		if !ok {
			continue
		}
		for _, block := range content {
			b, ok := block.(map[string]interface{})
			if !ok {
				continue
			}
			if b["type"] != "thinking" {
				continue
			}
			// If signature is missing, try to restore from cache
			if _, hasSig := b["signature"]; hasSig {
				continue
			}
			text, _ := b["thinking"].(string)
			if text == "" {
				continue
			}
			if sig := t.sigCache.Lookup(sessionID, text); sig != "" {
				b["signature"] = sig
				slog.Debug("restored thinking signature", "sessionId", sessionID)
			}
		}
	}
}

func (t *Transformer) normalizeToolNames(body map[string]interface{}) map[string]string {
	nameMap := make(map[string]string)

	// Transform tools array
	if tools, ok := body["tools"].([]interface{}); ok {
		for _, tool := range tools {
			if m, ok := tool.(map[string]interface{}); ok {
				if name, ok := m["name"].(string); ok {
					newName := toPascalCase(name)
					if newName != name {
						nameMap[newName] = name
						m["name"] = newName
					}
				}
			}
		}
	}

	// Transform tool_choice
	if tc, ok := body["tool_choice"].(map[string]interface{}); ok {
		if name, ok := tc["name"].(string); ok {
			if newName, exists := findTransformed(nameMap, name); exists {
				tc["name"] = newName
			}
		}
	}

	// Transform tool_use in messages
	if messages, ok := body["messages"].([]interface{}); ok {
		for _, msg := range messages {
			if m, ok := msg.(map[string]interface{}); ok {
				if content, ok := m["content"].([]interface{}); ok {
					for _, block := range content {
						if b, ok := block.(map[string]interface{}); ok {
							if b["type"] == "tool_use" {
								if name, ok := b["name"].(string); ok {
									if newName, exists := findTransformed(nameMap, name); exists {
										b["name"] = newName
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return nameMap
}

func toPascalCase(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return name
	}

	var b strings.Builder
	for _, part := range parts {
		if len(part) > 0 {
			b.WriteByte(byte(strings.ToUpper(string(part[0]))[0]))
			b.WriteString(strings.ToLower(part[1:]))
		}
	}
	b.WriteString("_tool")
	return b.String()
}

func findTransformed(nameMap map[string]string, original string) (string, bool) {
	for transformed, orig := range nameMap {
		if orig == original {
			return transformed, true
		}
	}
	return "", false
}

// IsWarmupRequest checks if the request is a warmup/non-productive request.
func IsWarmupRequest(body map[string]interface{}) bool {
	// Check for warmup ping
	if messages, ok := body["messages"].([]interface{}); ok && len(messages) == 1 {
		if m, ok := messages[0].(map[string]interface{}); ok {
			// String content: "Warmup"
			if content, ok := m["content"].(string); ok && content == "Warmup" {
				return true
			}
			// Array content: [{"type":"text","text":"Warmup"}]
			if content, ok := m["content"].([]interface{}); ok && len(content) == 1 {
				if block, ok := content[0].(map[string]interface{}); ok {
					if text, ok := block["text"].(string); ok && text == "Warmup" {
						return true
					}
				}
			}
		}
	}

	// Check system prompt for title/analysis patterns
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

// BuildWarmupResponse creates a synthetic SSE response for warmup requests (no delay, for tests).
func BuildWarmupResponse(model string) []byte {
	var buf []byte
	for _, e := range WarmupEvents(model) {
		buf = append(buf, []byte(e)...)
	}
	return buf
}

func generateShortID() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return hex.EncodeToString(h[:8])
}

func computeSessionHashInline(userID, systemPrompt, firstMessage string) string {
	if idx := strings.LastIndex(userID, "session_"); idx >= 0 {
		session := userID[idx:]
		h := sha256.Sum256([]byte("session:" + session))
		return hex.EncodeToString(h[:16])
	}
	if systemPrompt != "" {
		end := len(systemPrompt)
		if end > 200 {
			end = 200
		}
		h := sha256.Sum256([]byte("system:" + systemPrompt[:end]))
		return hex.EncodeToString(h[:16])
	}
	if firstMessage != "" {
		end := len(firstMessage)
		if end > 200 {
			end = 200
		}
		h := sha256.Sum256([]byte("msg:" + firstMessage[:end]))
		return hex.EncodeToString(h[:16])
	}
	return ""
}
