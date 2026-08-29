package quickbooks

import (
	"bytes"
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
	// revokeURL is Intuit's OAuth2 revocation endpoint (from the published
	// OpenID discovery document). Revoking a refresh token kills the grant
	// server-side and invalidates any access tokens minted from it.
	revokeURL = "https://developer.api.intuit.com/v2/oauth2/tokens/revoke"
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
	ExpiresIn        int    `json:"expires_in"`                 // seconds (typically 3600)
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

// RevokeToken revokes a refresh (or access) token with Intuit's revocation
// endpoint, terminating the grant server-side. Revoking a refresh token also
// invalidates every access token derived from it. It performs an external HTTP
// call and must therefore run outside any database transaction.
func RevokeToken(ctx context.Context, httpClient *http.Client, clientID, clientSecret, token string) error {
	return revokeTokenAt(ctx, revokeURL, httpClient, clientID, clientSecret, token)
}

// revokeTokenAt is the testable core of RevokeToken, parameterized on the
// endpoint URL so tests can point it at an httptest server.
func revokeTokenAt(ctx context.Context, endpoint string, httpClient *http.Client, clientID, clientSecret, token string) error {
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return fmt.Errorf("marshal revoke request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create revoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(clientID, clientSecret)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke endpoint returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
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
