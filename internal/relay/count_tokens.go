package relay

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/yansir/cc-relayer/internal/auth"
	"github.com/yansir/cc-relayer/internal/identity"
	"github.com/yansir/cc-relayer/internal/scheduler"
)

// HandleCountTokens proxies token counting requests to the upstream API.
func (r *Relay) HandleCountTokens(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	keyInfo := auth.GetKeyInfo(ctx)
	if keyInfo == nil {
		writeError(w, http.StatusUnauthorized, "authentication_error", "not authenticated")
		return
	}

	req.Body = http.MaxBytesReader(w, req.Body, int64(r.cfg.MaxRequestBodyMB)<<20)
	rawBody, err := io.ReadAll(req.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "failed to read body")
		return
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body")
		return
	}
	model, _ := body["model"].(string)

	acct, err := r.scheduler.Select(ctx, scheduler.SelectOptions{
		BoundAccountID: keyInfo.BoundAccountID,
		IsOpusRequest:  isOpusModel(model),
	})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "overloaded_error", "no available accounts")
		return
	}

	accessToken, err := r.tokens.EnsureValidToken(ctx, acct.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "api_error", "token unavailable")
		return
	}

	result := r.transformer.Transform(ctx, body, req.Header, acct)
	upstreamBody, _ := json.Marshal(result.Body)

	upstreamURL, err := appendRawQuery(r.cfg.ClaudeAPIURL+"/count_tokens", req.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "api_error", "failed to build upstream url")
		return
	}

	upReq, err := http.NewRequestWithContext(ctx, "POST", upstreamURL, strings.NewReader(string(upstreamBody)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "api_error", "failed to create request")
		return
	}
	for k, vals := range result.Headers {
		for _, v := range vals {
			upReq.Header.Add(k, v)
		}
	}
	identity.SetRequiredHeaders(upReq.Header, accessToken, r.cfg.ClaudeAPIVersion, r.cfg.ClaudeBetaHeader)

	client := r.transport.GetClient(acct)
	resp, err := client.Do(upReq)
	if err != nil {
		slog.Error("count_tokens upstream failed", "error", err)
		writeError(w, http.StatusBadGateway, "api_error", "upstream request failed")
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}
