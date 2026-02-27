package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/auth"
	"github.com/yansir/cc-relayer/internal/config"
	"github.com/yansir/cc-relayer/internal/ratelimit"
	"github.com/yansir/cc-relayer/internal/scheduler"
	"github.com/yansir/cc-relayer/internal/store"
)

// CodexRelay handles Codex CLI requests.
type CodexRelay struct {
	store     store.Store
	accounts  *account.AccountStore
	tokens    *account.TokenManager
	scheduler *scheduler.Scheduler
	rateLimit *ratelimit.Manager
	cfg       *config.Config
	transport TransportProvider
}

func NewCodexRelay(
	s store.Store,
	as *account.AccountStore,
	tm *account.TokenManager,
	sched *scheduler.Scheduler,
	rl *ratelimit.Manager,
	cfg *config.Config,
	tp TransportProvider,
) *CodexRelay {
	return &CodexRelay{
		store:     s,
		accounts:  as,
		tokens:    tm,
		scheduler: sched,
		rateLimit: rl,
		cfg:       cfg,
		transport: tp,
	}
}

// Handle processes a Codex relay request.
func (r *CodexRelay) Handle(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	keyInfo := auth.GetKeyInfo(ctx)
	if keyInfo == nil {
		writeCodexError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Enforce request body size limit
	req.Body = http.MaxBytesReader(w, req.Body, int64(r.cfg.MaxRequestBodyMB)<<20)

	// Read body
	rawBody, err := io.ReadAll(req.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeCodexError(w, http.StatusRequestEntityTooLarge, "request body exceeds size limit")
			return
		}
		writeCodexError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Extract model from body
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		writeCodexError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	model, _ := body["model"].(string)
	isStream := true // Codex responses API always streams

	// Retry loop
	var excludeIDs []string
	var lastErr error
	var lastUpstreamStatus int
	var lastUpstreamBody []byte

	for attempt := 0; attempt <= r.cfg.MaxRetryAccounts; attempt++ {
		if ctx.Err() != nil {
			return
		}

		selectOpts := scheduler.SelectOptions{
			BoundAccountID: keyInfo.BoundAccountID,
			Provider:       "codex",
			ExcludeIDs:     excludeIDs,
		}

		acct, err := r.scheduler.Select(ctx, selectOpts)
		if err != nil {
			lastErr = err
			break
		}

		accessToken, err := r.tokens.EnsureValidToken(ctx, acct.ID)
		if err != nil {
			slog.Warn("codex token invalid, excluding account", "accountId", acct.ID, "error", err)
			excludeIDs = append(excludeIDs, acct.ID)
			lastErr = err
			continue
		}

		// Build upstream request
		upReq, err := http.NewRequestWithContext(ctx, "POST", r.cfg.CodexAPIURL, strings.NewReader(string(rawBody)))
		if err != nil {
			lastErr = err
			break
		}

		// Forward original headers
		for _, h := range []string{"Content-Type", "Accept", "Codex-Version"} {
			if v := req.Header.Get(h); v != "" {
				upReq.Header.Set(h, v)
			}
		}
		if upReq.Header.Get("Content-Type") == "" {
			upReq.Header.Set("Content-Type", "application/json")
		}

		// Auth + identity headers
		upReq.Header.Set("Authorization", "Bearer "+accessToken)
		upReq.Header.Set("Host", "chatgpt.com")
		if acct.ExtInfo != nil {
			if accountID, ok := acct.ExtInfo["chatgptAccountId"].(string); ok && accountID != "" {
				upReq.Header.Set("Chatgpt-Account-Id", accountID)
			}
		}

		client := r.transport.GetClient(acct)
		resp, err := client.Do(upReq)
		if err != nil {
			slog.Error("codex upstream request failed", "accountId", acct.ID, "error", err)
			excludeIDs = append(excludeIDs, acct.ID)
			lastErr = err
			continue
		}

		// Handle retriable errors
		if shouldRetry(resp.StatusCode) && attempt < r.cfg.MaxRetryAccounts {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			r.handleCodexError(ctx, acct, resp.StatusCode, errBody, model)

			lastUpstreamStatus = resp.StatusCode
			lastUpstreamBody = errBody

			excludeIDs = append(excludeIDs, acct.ID)
			lastErr = fmt.Errorf("upstream %d", resp.StatusCode)
			continue
		}

		// Non-retriable error
		if resp.StatusCode != http.StatusOK {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			r.rateLimit.CaptureCodexHeaders(ctx, acct.ID, resp.Header)
			slog.Warn("codex upstream error", "status", resp.StatusCode, "accountId", acct.ID, "model", model,
				"body", truncate(string(errBody), 500))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(errBody)
			return
		}

		// Success — stream response
		defer resp.Body.Close()
		r.rateLimit.CaptureCodexHeaders(ctx, acct.ID, resp.Header)

		startTime := time.Now()
		var usage *codexUsageData
		if isStream {
			usage = r.streamCodexResponse(ctx, w, resp, acct)
		}

		now := time.Now().UTC().Format(time.RFC3339)
		_ = r.accounts.Update(context.Background(), acct.ID, map[string]string{"lastUsedAt": now})

		// Write request log
		if usage != nil {
			cost := calcCodexCost(model, usage.InputTokens, usage.OutputTokens, usage.InputTokensCached)
			go func() {
				_ = r.store.InsertRequestLog(context.Background(), &store.RequestLog{
					UserID:            keyInfo.ID,
					AccountID:         acct.ID,
					Model:             model,
					InputTokens:       usage.InputTokens,
					OutputTokens:      usage.OutputTokens,
					CacheReadTokens:   usage.InputTokensCached,
					CacheCreateTokens: 0,
					CostUSD:           cost,
					Status:            "ok",
					DurationMs:        time.Since(startTime).Milliseconds(),
					CreatedAt:         time.Now().UTC(),
				})
			}()
		}
		return
	}

	// All attempts failed
	if lastErr != nil {
		slog.Error("all codex relay attempts failed", "error", lastErr)
	}
	if lastUpstreamBody != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(lastUpstreamStatus)
		w.Write(lastUpstreamBody)
		return
	}
	writeCodexError(w, http.StatusServiceUnavailable, "no available codex accounts")
}

func (r *CodexRelay) streamCodexResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, acct *account.Account) *codexUsageData {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeCodexError(w, http.StatusInternalServerError, "streaming not supported")
		return nil
	}

	// Copy response headers
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var capturedUsage *codexUsageData

	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}

		line := scanner.Text()
		fmt.Fprintf(w, "%s\n", line)
		if line == "" {
			flusher.Flush()
		}

		// Capture usage from response.completed event
		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			if u := parseCodexUsage(data); u != nil {
				capturedUsage = u
			}
		}
	}
	flusher.Flush()

	return capturedUsage
}

func (r *CodexRelay) handleCodexError(ctx context.Context, acct *account.Account, statusCode int, errBody []byte, model string) {
	switch statusCode {
	case 429:
		r.rateLimit.CaptureCodexHeaders(ctx, acct.ID, http.Header{})
		until := time.Now().Add(r.cfg.ErrorPause429).UTC().Format(time.RFC3339)
		_ = r.accounts.Update(ctx, acct.ID, map[string]string{
			"overloadedAt":    time.Now().UTC().Format(time.RFC3339),
			"overloadedUntil": until,
		})
		slog.Warn("codex account rate limited (429)", "accountId", acct.ID, "model", model)

	case 401:
		until := time.Now().Add(r.cfg.ErrorPause401).UTC().Format(time.RFC3339)
		_ = r.accounts.Update(ctx, acct.ID, map[string]string{
			"status":          "error",
			"errorMessage":    "codex upstream 401: authentication failed",
			"overloadedUntil": until,
		})
		go func() {
			_, _ = r.tokens.ForceRefresh(context.Background(), acct.ID)
		}()
		slog.Warn("codex account auth failed (401)", "accountId", acct.ID)

	case 403:
		until := time.Now().Add(r.cfg.ErrorPause403).UTC().Format(time.RFC3339)
		_ = r.accounts.Update(ctx, acct.ID, map[string]string{
			"overloadedAt":    time.Now().UTC().Format(time.RFC3339),
			"overloadedUntil": until,
		})
		slog.Warn("codex account forbidden (403)", "accountId", acct.ID,
			"body", truncate(string(errBody), 500))
	}
}

type codexUsageData struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	InputTokensCached int `json:"input_tokens_cached"`
}

// parseCodexUsage extracts usage from a response.completed SSE data line.
// The format is: {"type":"response.completed","response":{...,"usage":{...}}}
func parseCodexUsage(data string) *codexUsageData {
	var wrapper struct {
		Type     string `json:"type"`
		Response struct {
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
				Details      *struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"input_tokens_details"`
			} `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal([]byte(data), &wrapper) != nil || wrapper.Response.Usage == nil {
		return nil
	}
	u := &codexUsageData{
		InputTokens:  wrapper.Response.Usage.InputTokens,
		OutputTokens: wrapper.Response.Usage.OutputTokens,
	}
	if wrapper.Response.Usage.Details != nil {
		u.InputTokensCached = wrapper.Response.Usage.Details.CachedTokens
	}
	return u
}

func writeCodexError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"message":"%s","type":"error","code":%d}}`, msg, status)
}
