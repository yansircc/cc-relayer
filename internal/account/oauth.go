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
	"time"
)

const (
	oauthRedirectURI = "https://platform.claude.com/oauth/code/callback"
	oauthScope       = "user:profile user:inference"
	claudeAIBaseURL  = "https://claude.ai"
)

// OAuthResult is the result of a Cookie OAuth flow.
type OAuthResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Email        string
	OrgUUID      string
	OrgName      string
}

// CookieOAuth completes the full OAuth flow using a sessionKey cookie.
// Step 1: GET /api/organizations → pick best org
// Step 2: POST /v1/oauth/{org}/authorize → get authorization code
// Step 3: POST token endpoint → exchange code for tokens
func CookieOAuth(ctx context.Context, sessionKey string) (*OAuthResult, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Step 1: get organizations
	orgUUID, email, orgName, err := fetchOrganization(ctx, client, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("fetch organization: %w", err)
	}

	// PKCE
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, fmt.Errorf("generate PKCE: %w", err)
	}
	state := generateState()

	// Step 2: authorize
	code, err := authorize(ctx, client, sessionKey, orgUUID, verifier, challenge, state)
	if err != nil {
		return nil, fmt.Errorf("authorize: %w", err)
	}

	// Step 3: exchange code for tokens
	tokens, err := exchangeCode(ctx, client, code, verifier, state)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	return &OAuthResult{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresIn:    tokens.ExpiresIn,
		Email:        email,
		OrgUUID:      orgUUID,
		OrgName:      orgName,
	}, nil
}

type orgResponse struct {
	UUID         string   `json:"uuid"`
	Name         string   `json:"name"`
	EmailAddress string   `json:"email_address"`
	Capabilities []string `json:"capabilities"`
}

func fetchOrganization(ctx context.Context, client *http.Client, sessionKey string) (uuid, email, name string, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", claudeAIBaseURL+"/api/organizations", nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Cookie", "sessionKey="+sessionKey)
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

	// Pick the org with "chat" capability and most capabilities
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
		// Fallback to first org
		best = 0
	}

	return orgs[best].UUID, orgs[best].EmailAddress, orgs[best].Name, nil
}

type authorizeRequest struct {
	ResponseType        string `json:"response_type"`
	ClientID            string `json:"client_id"`
	OrganizationUUID    string `json:"organization_uuid"`
	RedirectURI         string `json:"redirect_uri"`
	Scope               string `json:"scope"`
	State               string `json:"state"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
}

type authorizeResponse struct {
	RedirectURI string `json:"redirect_uri"`
}

func authorize(ctx context.Context, client *http.Client, sessionKey, orgUUID, verifier, challenge, state string) (string, error) {
	reqBody := authorizeRequest{
		ResponseType:        "code",
		ClientID:            oauthClientID,
		OrganizationUUID:    orgUUID,
		RedirectURI:         oauthRedirectURI,
		Scope:               oauthScope,
		State:               state,
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", claudeAIBaseURL+"/v1/oauth/"+orgUUID+"/authorize", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Cookie", "sessionKey="+sessionKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("authorize API returned %d: %s", resp.StatusCode, truncate(body, 200))
	}

	var authResp authorizeResponse
	if err := json.Unmarshal(body, &authResp); err != nil {
		return "", fmt.Errorf("parse authorize response: %w", err)
	}

	parsed, err := url.Parse(authResp.RedirectURI)
	if err != nil {
		return "", fmt.Errorf("parse redirect_uri: %w", err)
	}
	code := parsed.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("no code in redirect_uri: %s", authResp.RedirectURI)
	}
	return code, nil
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
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func truncate(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
