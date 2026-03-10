package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	authBaseURL  = "https://appcenter.intuit.com/connect/oauth2"
	tokenBaseURL = "https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer"
)

// AuthorizationURL generates the URL to redirect the user to for QBO authorization.
func AuthorizationURL(clientID, redirectURI, state string) string {
	params := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {"com.intuit.quickbooks.accounting"},
		"state":         {state},
	}
	return authBaseURL + "?" + params.Encode()
}

// TokenResponse represents the response from the QBO token endpoint.
type TokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`         // seconds (typically 3600)
	RefreshExpiresIn int    `json:"x_refresh_token_expires_in"` // seconds (typically 8726400 = 100 days)
}

// ExchangeCode exchanges an authorization code for access and refresh tokens.
func ExchangeCode(ctx context.Context, httpClient *http.Client, clientID, clientSecret, code, redirectURI string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
	}
	return postTokenRequest(ctx, httpClient, clientID, clientSecret, data)
}

// exchangeRefreshToken uses a refresh token to obtain new access and refresh tokens.
func exchangeRefreshToken(ctx context.Context, httpClient *http.Client, clientID, clientSecret, refreshToken string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	return postTokenRequest(ctx, httpClient, clientID, clientSecret, data)
}

func postTokenRequest(ctx context.Context, httpClient *http.Client, clientID, clientSecret string, data url.Values) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenBaseURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(clientID, clientSecret)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("unmarshal token response: %w", err)
	}

	return &tokenResp, nil
}
