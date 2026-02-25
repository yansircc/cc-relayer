package account

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	oauthRedirectURI  = "https://platform.claude.com/oauth/code/callback"
	oauthScope        = "org:create_api_key user:profile user:inference user:sessions:claude_code"
	oauthAuthorizeURL = "https://claude.ai/oauth/authorize"
	claudeAIBaseURL   = "https://claude.ai"
)

// OAuthSession holds PKCE parameters for a pending manual OAuth flow.
type OAuthSession struct {
	CodeVerifier string `json:"code_verifier"`
	State        string `json:"state"`
}

// GenerateAuthURL creates a PKCE-secured authorization URL for manual browser-based OAuth.
func GenerateAuthURL() (authURL string, session OAuthSession, err error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", OAuthSession{}, fmt.Errorf("generate PKCE: %w", err)
	}
	state := generateState()

	params := url.Values{
		"code":                  {"true"},
		"client_id":             {oauthClientID},
		"response_type":         {"code"},
		"redirect_uri":          {oauthRedirectURI},
		"scope":                 {oauthScope},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}

	return oauthAuthorizeURL + "?" + params.Encode(), OAuthSession{
		CodeVerifier: verifier,
		State:        state,
	}, nil
}

// ExtractCodeFromCallback extracts the authorization code from a callback URL or raw code string.
func ExtractCodeFromCallback(callbackURL string) string {
	s := strings.TrimSpace(callbackURL)
	if s == "" {
		return ""
	}

	parsed, err := url.Parse(s)
	if err != nil || parsed.Scheme == "" {
		// Raw code input may include URL fragments/params like "code#state" or "code&..."
		if i := strings.Index(s, "#"); i >= 0 {
			s = s[:i]
		}
		if i := strings.Index(s, "&"); i >= 0 {
			s = s[:i]
		}
		if i := strings.Index(s, "?"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimPrefix(s, "code=")
		return strings.TrimSpace(s)
	}
	if code := parsed.Query().Get("code"); code != "" {
		return code
	}
	return strings.TrimSpace(s)
}

type orgResponse struct {
	UUID         string   `json:"uuid"`
	Name         string   `json:"name"`
	EmailAddress string   `json:"email_address"`
	Capabilities []string `json:"capabilities"`
}

// ExchangeCodeResult holds the tokens returned from an authorization code exchange.
type ExchangeCodeResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

// ExchangeCode exchanges an authorization code for tokens at the Anthropic token endpoint.
func ExchangeCode(ctx context.Context, code, verifier, state string) (*ExchangeCodeResult, error) {
	resp, err := exchangeCode(ctx, &http.Client{Timeout: 30 * time.Second}, code, verifier, state)
	if err != nil {
		return nil, err
	}
	return &ExchangeCodeResult{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ExpiresIn:    resp.ExpiresIn,
	}, nil
}

func exchangeCode(ctx context.Context, client *http.Client, code, verifier, state string) (*tokenResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     oauthClientID,
		"code":          code,
		"redirect_uri":  oauthRedirectURI,
		"code_verifier": verifier,
		"state":         state,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", oauthTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "claude-cli/1.0.69 (external, cli)")
	req.Header.Set("Referer", "https://claude.ai/")
	req.Header.Set("Origin", "https://claude.ai")

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
		return nil, fmt.Errorf("token API returned %d: %s", resp.StatusCode, truncate(respBody, 200))
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in response")
	}
	return &tokenResp, nil
}

// FetchOrgWithToken fetches organization info using an OAuth access token.
// Used after manual OAuth code exchange to auto-populate account email/org.
func FetchOrgWithToken(ctx context.Context, accessToken string) (uuid, email, name string, err error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", claudeAIBaseURL+"/api/organizations", nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("organizations API returned %d: %s", resp.StatusCode, truncate(body, 200))
	}

	var orgs []orgResponse
	if err := json.Unmarshal(body, &orgs); err != nil {
		return "", "", "", fmt.Errorf("parse organizations: %w", err)
	}
	if len(orgs) == 0 {
		return "", "", "", fmt.Errorf("no organizations found")
	}

	best := -1
	bestCaps := -1
	for i, org := range orgs {
		hasChat := false
		for _, cap := range org.Capabilities {
			if cap == "chat" {
				hasChat = true
				break
			}
		}
		if hasChat && len(org.Capabilities) > bestCaps {
			best = i
			bestCaps = len(org.Capabilities)
		}
	}
	if best == -1 {
		best = 0
	}

	return orgs[best].UUID, orgs[best].EmailAddress, orgs[best].Name, nil
}

// --- PKCE helpers ---

func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return verifier, challenge, nil
}

func generateState() string {
	// Match Node relay / claude-code-login behavior (32 bytes -> ~43 chars base64url).
	// Some upstream validators appear stricter and reject short state values.
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func truncate(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
