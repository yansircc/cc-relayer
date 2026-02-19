package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/auth"
	"github.com/yansir/cc-relayer/internal/config"
	"github.com/yansir/cc-relayer/internal/identity"
	"github.com/yansir/cc-relayer/internal/ratelimit"
	"github.com/yansir/cc-relayer/internal/scheduler"
	"github.com/yansir/cc-relayer/internal/store"
)

// Ban signal patterns in 403 response bodies.
var banSignalPattern = regexp.MustCompile(`(?i)(organization has been disabled|account has been disabled|Too many active sessions|only authorized for use with claude code)`)

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

	// Parse request body
	body, rawBody, err := parseBody(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body")
		return
	}

	model, _ := body["model"].(string)
	isStream, _ := body["stream"].(bool)
	isOpus := isOpusModel(model)

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
		identity.SetRequiredHeaders(upReq.Header, accessToken, r.cfg.ClaudeAPIVersion, r.cfg.ClaudeBetaHeader, "", model)
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
			// Read body before closing for error analysis
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			r.handleUpstreamError(ctx, acct, resp, errBody, isOpus)

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
			completed := r.streamResponse(ctx, w, resp, acct, model)
			// Only update lastUsedAt on complete streams (not interrupted)
			if completed {
				now := time.Now().UTC().Format(time.RFC3339)
				_ = r.accounts.Update(context.Background(), acct.ID, map[string]string{"lastUsedAt": now})
			}
		} else {
			r.jsonResponse(w, resp)
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
func (r *Relay) streamResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, acct *account.Account, model string) bool {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "api_error", "streaming not supported")
		return false
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024) // 1MB max line

	completed := true
	for scanner.Scan() {
		// Check for client disconnect
		if ctx.Err() != nil {
			slog.Debug("client disconnected during stream", "accountId", acct.ID)
			completed = false
			break
		}

		line := scanner.Text()

		fmt.Fprintf(w, "%s\n", line)
		if line == "" {
			flusher.Flush()
		}
	}
	flusher.Flush()

	return completed
}

func (r *Relay) jsonResponse(w http.ResponseWriter, resp *http.Response) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "api_error", "failed to read upstream response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

func (r *Relay) handleUpstreamError(ctx context.Context, acct *account.Account, resp *http.Response, errBody []byte, isOpus bool) {
	switch resp.StatusCode {
	case 529:
		until := time.Now().Add(r.cfg.ErrorPause529).UTC().Format(time.RFC3339)
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

	case 403:
		bodyStr := string(errBody)
		if banSignalPattern.MatchString(bodyStr) {
			// Ban signal detected — mark blocked and pause for 30 minutes
			until := time.Now().Add(r.cfg.ErrorPause401).UTC().Format(time.RFC3339)
			_ = r.accounts.Update(ctx, acct.ID, map[string]string{
				"status":          "blocked",
				"errorMessage":    fmt.Sprintf("ban signal detected: %s", truncate(bodyStr, 200)),
				"schedulable":     "false",
				"overloadedUntil": until,
			})
			slog.Error("ban signal detected (403)", "accountId", acct.ID, "body", truncate(bodyStr, 200))
		} else {
			// Generic 403 — pause for 10 minutes to avoid rapid retries
			until := time.Now().Add(r.cfg.ErrorPause403).UTC().Format(time.RFC3339)
			_ = r.accounts.Update(ctx, acct.ID, map[string]string{
				"overloadedAt":    time.Now().UTC().Format(time.RFC3339),
				"overloadedUntil": until,
			})
			slog.Warn("account forbidden (403)", "accountId", acct.ID)
		}

	case 401:
		until := time.Now().Add(r.cfg.ErrorPause401).UTC().Format(time.RFC3339)
		_ = r.accounts.Update(ctx, acct.ID, map[string]string{
			"status":          "error",
			"errorMessage":    "upstream 401: authentication failed",
			"overloadedUntil": until,
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"error","error":{"type":"%s","message":"%s"}}`, errType, msg)
}
