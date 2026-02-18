package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/yansir/claude-relay/internal/config"
	"github.com/yansir/claude-relay/internal/store"
)

// HTTPTransportProvider returns per-account HTTP transports.
type HTTPTransportProvider interface {
	GetHTTPTransport(acct *Account) *http.Transport
}

// TokenManager handles OAuth token refresh with distributed locking.
type TokenManager struct {
	store     *store.Store
	accounts  *AccountStore
	cfg       *config.Config
	client    *http.Client // default client (no proxy)
	transport HTTPTransportProvider
}

func NewTokenManager(s *store.Store, as *AccountStore, cfg *config.Config, tp HTTPTransportProvider) *TokenManager {
	return &TokenManager{
		store:     s,
		accounts:  as,
		cfg:       cfg,
		client:    &http.Client{Timeout: 30 * time.Second},
		transport: tp,
	}
}

// tokenResponse is the OAuth refresh response from Anthropic.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// EnsureValidToken checks if the account's access token is valid.
// If expired (within 60s), triggers a refresh.
// Returns the decrypted access token.
func (tm *TokenManager) EnsureValidToken(ctx context.Context, accountID string) (string, error) {
	data, err := tm.store.GetAccount(ctx, accountID)
	if err != nil {
		return "", fmt.Errorf("get account: %w", err)
	}

	expiresAt := atoi64(data["expiresAt"], 0)
	now := time.Now().UnixMilli()

	// Token still valid
	if expiresAt > 0 && now < expiresAt-tm.cfg.TokenRefreshAdvance.Milliseconds() {
		token, err := tm.accounts.GetDecryptedAccessToken(ctx, accountID)
		if err != nil {
			return "", fmt.Errorf("decrypt access token: %w", err)
		}
		if token != "" {
			return token, nil
		}
	}

	// Token expired or about to expire — refresh
	return tm.refresh(ctx, accountID)
}

// refresh performs the OAuth token refresh with distributed locking.
func (tm *TokenManager) refresh(ctx context.Context, accountID string) (string, error) {
	lockID := uuid.New().String()

	acquired, err := tm.store.AcquireRefreshLock(ctx, accountID, lockID)
	if err != nil {
		return "", fmt.Errorf("acquire lock: %w", err)
	}

	if !acquired {
		// Another goroutine is refreshing — wait and re-read
		slog.Info("token refresh locked, waiting", "accountId", accountID)
		time.Sleep(2 * time.Second)

		token, err := tm.accounts.GetDecryptedAccessToken(ctx, accountID)
		if err != nil {
			return "", fmt.Errorf("get token after wait: %w", err)
		}
		if token != "" {
			// Check if it's now valid
			data, _ := tm.store.GetAccount(ctx, accountID)
			exp := atoi64(data["expiresAt"], 0)
			if exp > time.Now().UnixMilli() {
				return token, nil
			}
		}
		return "", fmt.Errorf("token refresh in progress by another process")
	}

	// We hold the lock — do the refresh
	defer func() {
		if err := tm.store.ReleaseRefreshLock(ctx, accountID, lockID); err != nil {
			slog.Error("release refresh lock failed", "accountId", accountID, "error", err)
		}
	}()

	refreshToken, err := tm.accounts.GetDecryptedRefreshToken(ctx, accountID)
	if err != nil {
		tm.markError(ctx, accountID, "decrypt refresh token: "+err.Error())
		return "", fmt.Errorf("decrypt refresh token: %w", err)
	}
	if refreshToken == "" {
		tm.markError(ctx, accountID, "empty refresh token")
		return "", fmt.Errorf("empty refresh token for account %s", accountID)
	}

	slog.Info("refreshing token", "accountId", accountID)

	resp, err := tm.callOAuthRefresh(ctx, accountID, refreshToken)
	if err != nil {
		tm.markError(ctx, accountID, err.Error())
		return "", fmt.Errorf("oauth refresh: %w", err)
	}

	// Store new tokens
	if err := tm.accounts.StoreTokens(ctx, accountID, resp.AccessToken, resp.RefreshToken, resp.ExpiresIn); err != nil {
		return "", fmt.Errorf("store tokens: %w", err)
	}

	slog.Info("token refreshed", "accountId", accountID, "expiresIn", resp.ExpiresIn)
	return resp.AccessToken, nil
}

// callOAuthRefresh sends the OAuth refresh request to Anthropic.
func (tm *TokenManager) callOAuthRefresh(ctx context.Context, accountID, refreshToken string) (*tokenResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     tm.cfg.OAuthClientID,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", tm.cfg.OAuthTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "claude-cli/1.0.69 (external, cli)")
	req.Header.Set("Referer", "https://claude.ai/")
	req.Header.Set("Origin", "https://claude.ai")

	// Use account-specific proxy transport if available
	client := tm.client
	if tm.transport != nil {
		acct, err := tm.accounts.Get(ctx, accountID)
		if err == nil && acct != nil && acct.Proxy != nil {
			client = &http.Client{
				Transport: tm.transport.GetHTTPTransport(acct),
				Timeout:   30 * time.Second,
			}
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth returned %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in response")
	}

	return &tokenResp, nil
}

func (tm *TokenManager) markError(ctx context.Context, accountID, msg string) {
	slog.Error("token refresh failed", "accountId", accountID, "error", msg)
	_ = tm.accounts.Update(ctx, accountID, map[string]string{
		"status":       "error",
		"errorMessage": msg,
	})
}

// ForceRefresh triggers an immediate token refresh, ignoring expiry.
func (tm *TokenManager) ForceRefresh(ctx context.Context, accountID string) (string, error) {
	return tm.refresh(ctx, accountID)
}
