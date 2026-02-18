package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/yansir/claude-relay/internal/account"
	"github.com/yansir/claude-relay/internal/auth"
	"github.com/yansir/claude-relay/internal/config"
	"github.com/yansir/claude-relay/internal/identity"
	"github.com/yansir/claude-relay/internal/ratelimit"
	"github.com/yansir/claude-relay/internal/scheduler"
	"github.com/yansir/claude-relay/internal/store"
)

// TransportProvider supplies per-account HTTP clients.
type TransportProvider interface {
	GetClient(acct *account.Account) *http.Client
}

// Relay orchestrates the request forwarding pipeline.
type Relay struct {
	store       *store.Store
	accounts    *account.AccountStore
	tokens      *account.TokenManager
	scheduler   *scheduler.Scheduler
	transformer *identity.Transformer
	rateLimit   *ratelimit.Manager
	cfg         *config.Config
	transport   TransportProvider
}

func New(
	s *store.Store,
	as *account.AccountStore,
	tm *account.TokenManager,
	sched *scheduler.Scheduler,
	trans *identity.Transformer,
	rl *ratelimit.Manager,
	cfg *config.Config,
	tp TransportProvider,
) *Relay {
	return &Relay{
		store:       s,
		accounts:    as,
		tokens:      tm,
		scheduler:   sched,
		transformer: trans,
		rateLimit:   rl,
		cfg:         cfg,
		transport:   tp,
	}
}

// Handle processes a relay request end-to-end.
func (r *Relay) Handle(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	keyInfo := auth.GetKeyInfo(ctx)
	if keyInfo == nil {
		writeError(w, http.StatusUnauthorized, "authentication_error", "not authenticated")
		return
	}

	// Capture latest Claude Code User-Agent version
	r.captureUserAgent(ctx, req.Header.Get("User-Agent"))

	// Parse request body
	body, rawBody, err := parseBody(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body")
		return
	}

	model, _ := body["model"].(string)
	isStream, _ := body["stream"].(bool)
	isOpus := isOpusModel(model)

	// Check weekly Opus cost
	if isOpus {
		authMw := auth.NewMiddleware(r.store, nil, r.cfg) // crypto not needed here
		if err := authMw.CheckWeeklyOpusCost(ctx, keyInfo); err != nil {
			writeError(w, http.StatusPaymentRequired, "billing_error", err.Error())
			return
		}
	}

	// Warmup interception — stream events with ~20ms delay to simulate network latency
	if identity.IsWarmupRequest(body) {
		slog.Debug("warmup intercepted", "model", model)
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		for _, event := range identity.WarmupEvents(model) {
			w.Write([]byte(event))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
		return
	}

	// Acquire concurrency slot
	authMw := auth.NewMiddleware(r.store, nil, r.cfg)
	requestID, err := authMw.AcquireConcurrency(ctx, keyInfo)
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "rate_limit_error", err.Error())
		return
	}
	// Use background context for cleanup so it runs even if client disconnects
	defer func() {
		authMw.ReleaseConcurrency(context.Background(), keyInfo.ID, requestID)
	}()

	// Session binding — try to look up a bound account from previous requests
	sessionUUID := extractSessionUUID(body)
	oldSession := isOldSession(body)
	var sessionBoundAccountID string
	if sessionUUID != "" {
		binding, err := r.store.GetSessionBinding(ctx, sessionUUID)
		if err == nil && binding != nil {
			if boundID := binding["accountId"]; boundID != "" {
				acct, err := r.accounts.Get(ctx, boundID)
				if err == nil && acct != nil && acct.Status == "active" && acct.Schedulable {
					sessionBoundAccountID = boundID
					// Renew the binding TTL on reuse
					_ = r.store.RenewSessionBinding(ctx, sessionUUID, r.cfg.SessionBindingTTL)
				} else if oldSession {
					// Pollution detection: bound account is unhealthy and this is a
					// continuation of an existing session. Switching accounts mid-conversation
					// would expose the relay pattern. Force client to start a new session.
					slog.Warn("session pollution detected", "sessionUUID", sessionUUID, "boundAccountId", boundID)
					writeError(w, http.StatusBadRequest, "session_binding_error",
						"bound account unavailable, please start a new session")
					return
				}
			}
		}
	}

	// Retry loop: try up to MaxRetryAccounts+1 different accounts
	var excludeIDs []string
	var lastErr error
	var forbiddenRetries int

	for attempt := 0; attempt <= r.cfg.MaxRetryAccounts; attempt++ {
		// Check for client disconnect before each attempt
		if ctx.Err() != nil {
			slog.Debug("client disconnected before attempt", "attempt", attempt)
			return
		}

		selectOpts := scheduler.SelectOptions{
			BoundAccountID: keyInfo.BoundAccountID,
			IsOpusRequest:  isOpus,
			ExcludeIDs:     excludeIDs,
		}
		// On first attempt, prefer the session-bound account
		if attempt == 0 && sessionBoundAccountID != "" && keyInfo.BoundAccountID == "" {
			selectOpts.BoundAccountID = sessionBoundAccountID
		}

		acct, err := r.scheduler.Select(ctx, selectOpts)
		if err != nil {
			lastErr = err
			break
		}

		// Ensure token is valid
		accessToken, err := r.tokens.EnsureValidToken(ctx, acct.ID)
		if err != nil {
			slog.Warn("token invalid, excluding account", "accountId", acct.ID, "error", err)
			excludeIDs = append(excludeIDs, acct.ID)
			lastErr = err
			continue
		}

		// Apply identity transformations (re-parse body each attempt for clean state)
		var attemptBody map[string]interface{}
		json.Unmarshal(rawBody, &attemptBody)

		result := r.transformer.Transform(ctx, attemptBody, req.Header, acct)

		// Build upstream request
		upstreamBody, _ := json.Marshal(result.Body)

		upReq, err := http.NewRequestWithContext(ctx, "POST", r.cfg.ClaudeAPIURL, strings.NewReader(string(upstreamBody)))
		if err != nil {
			lastErr = err
			break
		}

		// Set headers
		for k, vals := range result.Headers {
			for _, v := range vals {
				upReq.Header.Add(k, v)
			}
		}
		// For non-CC clients, override User-Agent with the latest captured version
		upstreamUA := ""
		if !result.IsRealCC {
			upstreamUA = r.getLatestUserAgent(ctx)
		}
		identity.SetRequiredHeaders(upReq.Header, accessToken, r.cfg.ClaudeAPIVersion, r.cfg.ClaudeBetaHeader, upstreamUA)
		if isStream {
			upReq.Header.Set("Accept", "text/event-stream")
		}

		// Send upstream request via per-account transport (utls + proxy)
		client := r.transport.GetClient(acct)
		resp, err := client.Do(upReq)
		if err != nil {
			slog.Error("upstream request failed", "accountId", acct.ID, "error", err)
			excludeIDs = append(excludeIDs, acct.ID)
			lastErr = err
			continue
		}

		// Handle retriable errors
		if shouldRetry(resp.StatusCode) && attempt < r.cfg.MaxRetryAccounts {
			r.handleUpstreamError(ctx, acct, resp, isOpus)
			resp.Body.Close()

			// 403 may be transient — retry same account up to 2x before switching
			if resp.StatusCode == 403 {
				forbiddenRetries++
				if forbiddenRetries <= 2 {
					lastErr = fmt.Errorf("upstream 403 (retry %d)", forbiddenRetries)
					continue // retry same account, don't exclude
				}
			}

			excludeIDs = append(excludeIDs, acct.ID)
			lastErr = fmt.Errorf("upstream %d", resp.StatusCode)
			continue
		}

		// Non-retriable error — sanitize and forward
		if resp.StatusCode != http.StatusOK {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			r.rateLimit.CaptureHeaders(ctx, acct.ID, resp.Header)

			if isStream {
				sanitizedStatus, _ := SanitizeError(resp.StatusCode, errBody)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(sanitizedStatus)
				fmt.Fprint(w, SanitizeSSEError(resp.StatusCode, errBody))
			} else {
				sanitizedStatus, sanitizedBody := SanitizeError(resp.StatusCode, errBody)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(sanitizedStatus)
				w.Write(sanitizedBody)
			}
			return
		}

		// Success — forward response
		defer resp.Body.Close()

		// Capture rate limit headers
		r.rateLimit.CaptureHeaders(ctx, acct.ID, resp.Header)

		// Save/renew session binding on success
		if result.SessionHash != "" && sessionUUID != "" {
			_ = r.store.SetSessionBinding(ctx, sessionUUID, acct.ID, r.cfg.SessionBindingTTL)
		}

		if isStream {
			completed := r.streamResponse(ctx, w, resp, acct, keyInfo, model, result)
			// Only update lastUsedAt on complete streams (not interrupted)
			if completed {
				now := time.Now().UTC().Format(time.RFC3339)
				_ = r.accounts.Update(context.Background(), acct.ID, map[string]string{"lastUsedAt": now})
			}
		} else {
			r.jsonResponse(ctx, w, resp, acct, keyInfo, model, result)
			now := time.Now().UTC().Format(time.RFC3339)
			_ = r.accounts.Update(context.Background(), acct.ID, map[string]string{"lastUsedAt": now})
		}
		return
	}

	// All attempts failed
	if lastErr != nil {
		slog.Error("all relay attempts failed", "error", lastErr)
	}
	writeError(w, http.StatusServiceUnavailable, "overloaded_error", "no available accounts")
}

// streamResponse streams the upstream SSE response to the client.
// Returns true if the stream completed normally, false if interrupted.
func (r *Relay) streamResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, acct *account.Account, keyInfo *auth.KeyInfo, model string, result *identity.TransformResult) bool {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "api_error", "streaming not supported")
		return false
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)

	scanner := NewSSEScanner(resp.Body)
	sessionID := identity.ExtractSessionUUID("")
	if metadata, ok := result.Body["metadata"].(map[string]interface{}); ok {
		if uid, ok := metadata["user_id"].(string); ok {
			sessionID = identity.ExtractSessionUUID(uid)
		}
	}

	var usage Usage

	completed := true
	for scanner.Scan() {
		// Check for client disconnect
		if ctx.Err() != nil {
			slog.Debug("client disconnected during stream", "accountId", acct.ID)
			completed = false
			break
		}

		line := scanner.Text()

		// Restore tool names if needed
		if len(result.ToolNameMap) > 0 && strings.HasPrefix(line, "data: ") {
			data := []byte(line[6:])
			data = r.transformer.RestoreToolNamesInResponse(data, result.ToolNameMap)
			line = "data: " + string(data)
		}

		// Parse usage and capture signatures from SSE data lines
		if strings.HasPrefix(line, "data: ") {
			data := []byte(line[6:])
			var event map[string]interface{}
			if json.Unmarshal(data, &event) == nil {
				// Capture thinking signatures
				if sessionID != "" {
					r.transformer.CaptureSignatures(sessionID, event)
				}
				// Track usage
				switch event["type"] {
				case "message_start":
					ParseMessageStart(data, &usage)
				case "message_delta":
					ParseMessageDelta(data, &usage)
				}
			}
		}

		fmt.Fprintf(w, "%s\n", line)
		if line == "" {
			flusher.Flush()
		}
	}
	flusher.Flush()

	// Accumulate Opus cost after stream completes (even partial)
	if IsOpus(model) && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		r.rateLimit.AccumulateOpusCost(context.Background(), keyInfo.ID, usage.InputTokens, usage.OutputTokens)
	}

	return completed
}

func (r *Relay) jsonResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, acct *account.Account, keyInfo *auth.KeyInfo, model string, result *identity.TransformResult) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "api_error", "failed to read upstream response")
		return
	}

	// Restore tool names
	if len(result.ToolNameMap) > 0 {
		body = r.transformer.RestoreToolNamesInResponse(body, result.ToolNameMap)
	}

	// Track usage for Opus cost accumulation
	if IsOpus(model) {
		if u := ParseJSONUsage(body); u != nil && (u.InputTokens > 0 || u.OutputTokens > 0) {
			r.rateLimit.AccumulateOpusCost(context.Background(), keyInfo.ID, u.InputTokens, u.OutputTokens)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

func (r *Relay) handleUpstreamError(ctx context.Context, acct *account.Account, resp *http.Response, isOpus bool) {
	switch resp.StatusCode {
	case 529:
		until := time.Now().Add(r.cfg.OverloadedCooldown).UTC().Format(time.RFC3339)
		_ = r.accounts.Update(ctx, acct.ID, map[string]string{
			"overloadedAt":    time.Now().UTC().Format(time.RFC3339),
			"overloadedUntil": until,
		})
		slog.Warn("account overloaded (529)", "accountId", acct.ID)

	case 429:
		r.rateLimit.CaptureHeaders(ctx, acct.ID, resp.Header)
		// Mark Opus-specific rate limit from reset header
		if isOpus {
			if resetStr := resp.Header.Get("anthropic-ratelimit-unified-reset"); resetStr != "" {
				if resetTime, err := time.Parse(time.RFC3339, resetStr); err == nil {
					r.rateLimit.MarkOpusRateLimited(ctx, acct.ID, resetTime)
				}
			}
		}
		slog.Warn("account rate limited (429)", "accountId", acct.ID)

	case 401:
		_ = r.accounts.Update(ctx, acct.ID, map[string]string{
			"status":       "error",
			"errorMessage": "upstream 401: authentication failed",
		})
		// Trigger async token refresh
		go func() {
			bgCtx := context.Background()
			_, _ = r.tokens.ForceRefresh(bgCtx, acct.ID)
		}()
		slog.Warn("account auth failed (401)", "accountId", acct.ID)
	}
}

func shouldRetry(statusCode int) bool {
	return statusCode == 529 || statusCode == 429 || statusCode == 401 || statusCode == 403
}

func parseBody(req *http.Request) (map[string]interface{}, []byte, error) {
	rawBody, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, nil, err
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, nil, err
	}
	return body, rawBody, nil
}

func isOpusModel(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "opus")
}

// extractSessionUUID extracts the session UUID from the request body's metadata.user_id.
func extractSessionUUID(body map[string]interface{}) string {
	if metadata, ok := body["metadata"].(map[string]interface{}); ok {
		if uid, ok := metadata["user_id"].(string); ok {
			return identity.ExtractSessionUUID(uid)
		}
	}
	return ""
}

// captureUserAgent captures the latest Claude Code User-Agent from incoming requests.
// Only updates Redis if the incoming version is newer than the stored one.
func (r *Relay) captureUserAgent(ctx context.Context, ua string) {
	fullUA, version, ok := identity.ParseCCUserAgent(ua)
	if !ok {
		return
	}

	cached, err := r.store.GetCachedUserAgent(ctx)
	if err != nil {
		return
	}

	if cached == "" {
		_ = r.store.SetCachedUserAgent(ctx, fullUA)
		return
	}

	_, cachedVersion, cachedOK := identity.ParseCCUserAgent(cached)
	if !cachedOK || identity.IsNewerVersion(version, cachedVersion) {
		_ = r.store.SetCachedUserAgent(ctx, fullUA)
		slog.Debug("user-agent updated", "from", cached, "to", fullUA)
	}
}

// getLatestUserAgent returns the cached User-Agent or a default fallback.
func (r *Relay) getLatestUserAgent(ctx context.Context) string {
	cached, err := r.store.GetCachedUserAgent(ctx)
	if err == nil && cached != "" {
		return cached
	}
	return identity.DefaultUserAgent()
}

// isOldSession detects requests that are continuations of existing sessions.
// If true, the request must not be silently routed to a different account.
func isOldSession(body map[string]interface{}) bool {
	messages, _ := body["messages"].([]interface{})

	// Multi-turn conversation
	if len(messages) > 1 {
		return true
	}

	// Single message but multi-part content (indicates prior context)
	if len(messages) == 1 {
		if m, ok := messages[0].(map[string]interface{}); ok {
			if content, ok := m["content"].([]interface{}); ok {
				// Count user text blocks (exclude system reminders / tool results)
				userTexts := 0
				for _, block := range content {
					if b, ok := block.(map[string]interface{}); ok {
						if b["type"] == "text" {
							userTexts++
						}
					}
				}
				if userTexts > 1 {
					return true
				}
			}
		}
	}

	// Single message but no tool definitions (new CC sessions always have tools)
	tools, _ := body["tools"].([]interface{})
	if len(tools) == 0 {
		return true
	}

	return false
}

func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"error","error":{"type":"%s","message":"%s"}}`, errType, msg)
}
