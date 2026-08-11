package driver

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func (d *ClaudeDriver) GenerateAuthURL() (string, OAuthSession, error) {
	return generateClaudeAuthURL()
}

func (d *ClaudeDriver) ExchangeCode(ctx context.Context, client *http.Client, code, verifier, state string) (*ExchangeResult, error) {
	client = httpClientOrDefault(client, 30*time.Second)

	result, err := exchangeClaudeCode(ctx, client, code, verifier, state)
	if err != nil {
		return nil, err
	}

	orgUUID, email, orgName, err := fetchClaudeOrgWithToken(ctx, client, result.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("obtain Claude account identity: %w", err)
	}

	return &ExchangeResult{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    result.ExpiresIn,
		Subject:      orgUUID,
		Email:        email,
		Identity: map[string]string{
			"orgUUID": orgUUID,
			"orgName": orgName,
			"email":   email,
		},
	}, nil
}

func (d *ClaudeDriver) RefreshToken(ctx context.Context, client *http.Client, refreshToken string) (*TokenResponse, error) {
	return refreshClaudeToken(ctx, client, refreshToken)
}
