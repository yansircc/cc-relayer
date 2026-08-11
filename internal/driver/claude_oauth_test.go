package driver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestClaudeOAuthURLsUseExpectedHosts(t *testing.T) {
	for name, want := range map[string]struct {
		rawURL string
		host   string
	}{
		"authorize": {claudeOAuthAuthorizeURL, "claude.com"},
		"profile":   {claudeOAuthProfileURL, "api.anthropic.com"},
		"token":     {claudeOAuthTokenURL, "platform.claude.com"},
		"redirect":  {claudeOAuthRedirectURI, "platform.claude.com"},
	} {
		parsed, err := url.Parse(want.rawURL)
		if err != nil {
			t.Fatalf("%s URL parse failed: %v", name, err)
		}
		if parsed.Host != want.host {
			t.Fatalf("%s host = %q, want %s", name, parsed.Host, want.host)
		}
	}
}

func TestGenerateClaudeAuthURLUsesSubscriberManualFlow(t *testing.T) {
	rawURL, session, err := generateClaudeAuthURL()
	if err != nil {
		t.Fatalf("generateClaudeAuthURL() error = %v", err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	query := parsed.Query()
	if parsed.Scheme+"://"+parsed.Host+parsed.Path != claudeOAuthAuthorizeURL {
		t.Fatalf("authorize URL = %q", parsed)
	}
	if got := query.Get("redirect_uri"); got != claudeOAuthRedirectURI {
		t.Fatalf("redirect_uri = %q, want %q", got, claudeOAuthRedirectURI)
	}
	if got := query.Get("scope"); got != claudeOAuthAuthorizeScope {
		t.Fatalf("scope = %q, want %q", got, claudeOAuthAuthorizeScope)
	}
	if query.Get("state") != session.State || session.State == "" || session.CodeVerifier == "" {
		t.Fatalf("OAuth session and URL state do not match")
	}
}

type claudeOAuthRoundTripper func(*http.Request) (*http.Response, error)

func (f claudeOAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestExchangeClaudeCodeRejectsTokenWithoutInferenceScope(t *testing.T) {
	client := &http.Client{Transport: claudeOAuthRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != claudeOAuthTokenURL {
			t.Fatalf("token URL = %q, want %q", req.URL, claudeOAuthTokenURL)
		}
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode token request: %v", err)
		}
		if body["redirect_uri"] != claudeOAuthRedirectURI {
			t.Fatalf("redirect_uri = %q", body["redirect_uri"])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"access_token":"access-token",
				"refresh_token":"refresh-token",
				"expires_in":3600,
				"scope":"org:create_api_key user:profile"
			}`)),
		}, nil
	})}

	_, err := exchangeClaudeCode(context.Background(), client, "code", "verifier", "state")
	if err == nil || !strings.Contains(err.Error(), "missing user:inference") {
		t.Fatalf("exchangeClaudeCode() error = %v", err)
	}
}

func TestRefreshClaudeTokenRequestsSubscriberScopes(t *testing.T) {
	client := &http.Client{Transport: claudeOAuthRoundTripper(func(req *http.Request) (*http.Response, error) {
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode refresh request: %v", err)
		}
		if got := body["scope"]; got != claudeOAuthRefreshScope {
			t.Fatalf("refresh scope = %q, want %q", got, claudeOAuthRefreshScope)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"access_token":"new-access-token",
				"refresh_token":"new-refresh-token",
				"expires_in":3600,
				"scope":"user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
			}`)),
		}, nil
	})}

	result, err := refreshClaudeToken(context.Background(), client, "refresh-token")
	if err != nil {
		t.Fatalf("refreshClaudeToken() error = %v", err)
	}
	if result.AccessToken != "new-access-token" {
		t.Fatalf("access token was not decoded")
	}
}

func TestClaudeExchangeCodeDerivesSubjectFromOAuthProfile(t *testing.T) {
	client := &http.Client{Transport: claudeOAuthRoundTripper(func(req *http.Request) (*http.Response, error) {
		var responseBody string
		switch req.URL.String() {
		case claudeOAuthTokenURL:
			responseBody = `{
				"access_token":"access-token",
				"refresh_token":"refresh-token",
				"expires_in":3600,
				"scope":"org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
			}`
		case claudeOAuthProfileURL:
			responseBody = `{
				"account":{"uuid":"account-1","email":"person@example.com"},
				"organization":{"uuid":"org-1","name":"Example Org"}
			}`
		default:
			t.Fatalf("unexpected OAuth request URL: %s", req.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(responseBody)),
		}, nil
	})}
	drv := NewClaudeDriver(ClaudeConfig{}, nil, 0)

	result, err := drv.ExchangeCode(context.Background(), client, "code", "verifier", "state")
	if err != nil {
		t.Fatalf("ExchangeCode() error = %v", err)
	}
	if result.Subject != "org-1" || result.Email != "person@example.com" {
		t.Fatalf("exchange identity = subject %q, email %q", result.Subject, result.Email)
	}
	if result.Identity["orgUUID"] != result.Subject {
		t.Fatalf("identity orgUUID = %q, subject = %q", result.Identity["orgUUID"], result.Subject)
	}
}

func TestFetchClaudeOrgWithTokenUsesOAuthProfile(t *testing.T) {
	client := &http.Client{Transport: claudeOAuthRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != claudeOAuthProfileURL {
			t.Fatalf("profile URL = %q, want %q", req.URL, claudeOAuthProfileURL)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"account":{"uuid":"account-1","email":"person@example.com"},
				"organization":{"uuid":"org-1","name":"Example Org"}
			}`)),
		}, nil
	})}

	uuid, email, name, err := fetchClaudeOrgWithToken(context.Background(), client, "access-token")
	if err != nil {
		t.Fatalf("fetchClaudeOrgWithToken() error = %v", err)
	}
	if uuid != "org-1" || email != "person@example.com" || name != "Example Org" {
		t.Fatalf("identity = (%q, %q, %q)", uuid, email, name)
	}
}

func TestFetchClaudeOrgWithTokenRejectsMissingOrganizationUUID(t *testing.T) {
	client := &http.Client{Transport: claudeOAuthRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"account":{"uuid":"account-1"},"organization":{}}`)),
		}, nil
	})}

	_, _, _, err := fetchClaudeOrgWithToken(context.Background(), client, "access-token")
	if err == nil || !strings.Contains(err.Error(), "missing organization UUID") {
		t.Fatalf("fetchClaudeOrgWithToken() error = %v", err)
	}
}
