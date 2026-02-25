package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/config"
	"github.com/yansir/cc-relayer/internal/store"
)

// --- Header filtering ---

// StainlessPrefix identifies x-stainless-* headers.
const StainlessPrefix = "x-stainless-"

var allowedHeaders = map[string]bool{
	"accept":            true,
	"content-type":      true,
	"user-agent":        true,
	"anthropic-version": true,
	"anthropic-beta":    true,
	"anthropic-dangerous-direct-browser-access": true,
	"x-app": true,
}

// FilterHeaders builds a clean header set with only allowed headers.
// Stainless headers are handled separately (via fingerprint binding).
func FilterHeaders(original http.Header) http.Header {
	clean := make(http.Header)

	for key, vals := range original {
		lower := strings.ToLower(key)

		if allowedHeaders[lower] || strings.HasPrefix(lower, StainlessPrefix) {
			for _, v := range vals {
				clean.Add(key, v)
			}
		}
	}

	return clean
}

// SetRequiredHeaders sets the required headers for the upstream request.
func SetRequiredHeaders(h http.Header, accessToken, apiVersion, betaHeader string) {
	// Strip client auth headers — the relay's static token must never reach upstream.
	h.Del("x-api-key")
	h.Del("Authorization")

	h.Set("Authorization", "Bearer "+accessToken)
	if h.Get("anthropic-version") == "" {
		h.Set("anthropic-version", apiVersion)
	}
	if mergedBeta := mergeBetaHeaders(h.Get("anthropic-beta"), betaHeader); mergedBeta != "" {
		h.Set("anthropic-beta", mergedBeta)
	}
	h.Set("Content-Type", "application/json")
}

func mergeBetaHeaders(clientBeta, relayBeta string) string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, raw := range []string{clientBeta, relayBeta} {
		if raw == "" {
			continue
		}
		for _, part := range strings.Split(raw, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

// --- User ID rewriting ---

var userIDPattern = regexp.MustCompile(`^user_([a-fA-F0-9]{64})_account__session_([\w-]+)$`)
var sessionUUIDPattern = regexp.MustCompile(`session_([a-f0-9-]{36})$`)

// RewriteUserID replaces the user_id to match the account's real identity
// while maintaining session consistency.
func RewriteUserID(originalUserID, accountID, accountUUID string) string {
	matches := userIDPattern.FindStringSubmatch(originalUserID)
	if len(matches) < 3 {
		return buildUserID(accountID, accountUUID, "default")
	}
	return buildUserID(accountID, accountUUID, matches[2])
}

// ExtractSessionUUID extracts the session UUID from a user_id string.
func ExtractSessionUUID(userID string) string {
	matches := sessionUUIDPattern.FindStringSubmatch(userID)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// GetAccountUUID extracts account_uuid from the account's extInfo.
func GetAccountUUID(extInfo map[string]interface{}) string {
	if extInfo == nil {
		return ""
	}
	if v, ok := extInfo["account_uuid"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func buildUserID(accountID, accountUUID, sessionTail string) string {
	accountHash := deriveAccountHash(accountUUID, accountID)
	stableSession := deriveSessionUUID(accountID, sessionTail)
	return fmt.Sprintf("user_%s_account__session_%s", accountHash, stableSession)
}

func deriveAccountHash(accountUUID, accountID string) string {
	source := accountUUID
	if source == "" {
		source = accountID
	}
	h := sha256.Sum256([]byte(source))
	return hex.EncodeToString(h[:])
}

func deriveSessionUUID(accountID, sessionTail string) string {
	h := sha256.Sum256([]byte(accountID + ":" + sessionTail))
	hx := hex.EncodeToString(h[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hx[0:8], hx[8:12], hx[12:16], hx[16:20], hx[20:32])
}

// --- Transformer ---

var billingHeaderPattern = regexp.MustCompile(`(?i)x-anthropic-billing-header`)

// Transformer applies all identity transformations to a request.
type Transformer struct {
	store store.Store
	cfg   *config.Config
}

func NewTransformer(s store.Store, cfg *config.Config) *Transformer {
	return &Transformer{store: s, cfg: cfg}
}

// TransformResult holds the results of a transformation.
type TransformResult struct {
	Body        map[string]interface{}
	Headers     http.Header
	SessionHash string
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

	// 2. Enforce cache_control compliance (max N blocks, strip TTL)
	t.enforceCacheControl(body)

	// 3. Rewrite metadata.user_id
	accountUUID := GetAccountUUID(acct.ExtInfo)
	if metadata, ok := body["metadata"].(map[string]interface{}); ok {
		if origUserID, ok := metadata["user_id"].(string); ok {
			metadata["user_id"] = RewriteUserID(origUserID, acct.ID, accountUUID)
		}
	}

	// 4. Compute session hash
	result.SessionHash = t.computeSessionHashFromBody(body)

	// 5. Bind/restore stainless headers
	RemoveAllStainless(result.Headers)
	BindStainlessHeaders(ctx, t.store, acct.ID, reqHeaders, result.Headers)

	return result
}

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
						continue
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

	total := 0
	total += stripAndCountCacheControl(body, "system")
	total += stripAndCountCacheControl(body, "messages")

	if total <= maxBlocks {
		return
	}

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

	return computeSessionHash(userID, systemPrompt, firstMsg)
}

func computeSessionHash(userID, systemPrompt, firstMessage string) string {
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

// --- Warmup detection ---

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
