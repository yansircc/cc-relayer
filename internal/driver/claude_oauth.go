package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	claudeOAuthClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeOAuthTokenURL     = "https://platform.claude.com/v1/oauth/token"
	claudeOAuthRedirectURI  = "https://platform.claude.com/oauth/code/callback"
	claudeOAuthAuthorizeURL = "https://claude.com/cai/oauth/authorize"
	claudeOAuthProfileURL   = "https://api.anthropic.com/api/oauth/profile"

	claudeOAuthAuthorizeScope = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	claudeOAuthRefreshScope   = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
)

type claudeOAuthProfile struct {
	Account struct {
		UUID  string `json:"uuid"`
		Email string `json:"email"`
	} `json:"account"`
	Organization struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"organization"`
}

func generateClaudeAuthURL() (string, OAuthSession, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", OAuthSession{}, fmt.Errorf("generate PKCE: %w", err)
	}
	state := generateState()

	params := url.Values{
		"code":                  {"true"},
		"client_id":             {claudeOAuthClientID},
		"response_type":         {"code"},
		"redirect_uri":          {claudeOAuthRedirectURI},
		"scope":                 {claudeOAuthAuthorizeScope},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}

	return claudeOAuthAuthorizeURL + "?" + params.Encode(), OAuthSession{
		CodeVerifier: verifier,
		State:        state,
	}, nil
}

func exchangeClaudeCode(ctx context.Context, client *http.Client, code, verifier, state string) (*TokenResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     claudeOAuthClientID,
		"code":          code,
		"redirect_uri":  claudeOAuthRedirectURI,
		"code_verifier": verifier,
		"state":         state,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", claudeOAuthTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setClaudeOAuthControlPlaneHeaders(req.Header)

	client = httpClientOrDefault(client, 30*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token API returned %d: %s", resp.StatusCode, truncateBytes(respBody, 200))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in response")
	}
	if err := requireClaudeOAuthScopes(tokenResp.Scope, "user:profile", "user:inference"); err != nil {
		return nil, fmt.Errorf("token exchange scope validation: %w", err)
	}
	return &tokenResp, nil
}

func setClaudeOAuthControlPlaneHeaders(headers http.Header) {
	headers.Set("Accept", "application/json, text/plain, */*")
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", "axios/1.15.2")
}

func requireClaudeOAuthScopes(granted string, required ...string) error {
	grantedSet := make(map[string]struct{})
	for _, scope := range strings.Fields(granted) {
		grantedSet[scope] = struct{}{}
	}
	for _, scope := range required {
		if _, ok := grantedSet[scope]; !ok {
			return fmt.Errorf("granted scope missing %s", scope)
		}
	}
	return nil
}

func fetchClaudeOrgWithToken(ctx context.Context, client *http.Client, accessToken string) (uuid, email, name string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeOAuthProfileURL, nil)
	if err != nil {
		return "", "", "", fmt.Errorf("create OAuth profile request: %w", err)
	}
	setClaudeOAuthControlPlaneHeaders(req.Header)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Cache-Control", "no-cache")

	client = httpClientOrDefault(client, 15*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("fetch OAuth profile: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", fmt.Errorf("read OAuth profile response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("OAuth profile API returned %d: %s", resp.StatusCode, truncateBytes(body, 200))
	}

	var profile claudeOAuthProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return "", "", "", fmt.Errorf("parse OAuth profile response: %w", err)
	}
	if profile.Organization.UUID == "" {
		return "", "", "", fmt.Errorf("OAuth profile response missing organization UUID")
	}
	return profile.Organization.UUID, profile.Account.Email, profile.Organization.Name, nil
}

func refreshClaudeToken(ctx context.Context, client *http.Client, refreshToken string) (*TokenResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     claudeOAuthClientID,
		"scope":         claudeOAuthRefreshScope,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", claudeOAuthTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setClaudeOAuthControlPlaneHeaders(req.Header)

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

	var tokenResp TokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in response")
	}
	if err := requireClaudeOAuthScopes(tokenResp.Scope, "user:inference"); err != nil {
		return nil, fmt.Errorf("token refresh scope validation: %w", err)
	}
	return &tokenResp, nil
}
