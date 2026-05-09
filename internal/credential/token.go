/*-------------------------------------------------------------------------
 *
 * token.go
 *	  TokenProvider obtains OAuth2 access tokens for database
 *	  authentication. Used with cloud databases (e.g., Oracle Cloud
 *	  Infrastructure Autonomous DB). It uses the client_credentials
 *	  grant type and caches tokens until expiry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/token.go
 *
 *-------------------------------------------------------------------------
 */
package credential

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const tokenHTTPTimeout = 30 * time.Second

// TokenProvider obtains OAuth2 access tokens for database authentication.
// Used with cloud databases (e.g., Oracle Cloud Infrastructure Autonomous DB).
// It uses the client_credentials grant type and caches tokens until expiry.
type TokenProvider struct {
	tokenURL     string
	scope        string
	clientID     string
	clientSecret string
	client       *http.Client

	mu          sync.Mutex
	cachedToken string
	expiresAt   time.Time
}

// NewTokenProvider creates a TokenProvider for the given OAuth2 endpoint.
func NewTokenProvider(tokenURL, scope, clientID, clientSecret string) *TokenProvider {
	return &TokenProvider{
		tokenURL:     tokenURL,
		scope:        scope,
		clientID:     clientID,
		clientSecret: clientSecret,
		client:       &http.Client{Timeout: tokenHTTPTimeout},
	}
}

// Resolve returns an access token to use as the database password.
// Cached tokens are returned if still valid. Otherwise, a new token is obtained.
func (p *TokenProvider) Resolve(connName string) (string, error) {
	if p.tokenURL == "" {
		return "", fmt.Errorf("token URL not configured for connection %q", connName)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Return cached token if still valid (with 30s buffer).
	if p.cachedToken != "" && time.Now().Add(30*time.Second).Before(p.expiresAt) {
		return p.cachedToken, nil
	}

	token, expiresIn, err := p.fetchToken()
	if err != nil {
		return "", fmt.Errorf("obtaining token for %q: %w", connName, err)
	}

	p.cachedToken = token
	p.expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)

	return token, nil
}

// tokenResponse represents the OAuth2 token endpoint response.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// fetchToken obtains a new access token from the OAuth2 endpoint
// using client_credentials grant.
func (p *TokenProvider) fetchToken() (token string, expiresIn int, err error) {
	form := url.Values{
		"grant_type": {"client_credentials"},
	}
	if p.scope != "" {
		form.Set("scope", p.scope)
	}
	if p.clientID != "" {
		form.Set("client_id", p.clientID)
		form.Set("client_secret", p.clientSecret)
	}

	resp, err := p.client.PostForm(p.tokenURL, form)
	if err != nil {
		return "", 0, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", 0, fmt.Errorf("decoding token response: %w", err)
	}

	if tokenResp.Error != "" {
		return "", 0, fmt.Errorf("token error: %s (%s)", tokenResp.Error, tokenResp.ErrorDesc)
	}

	if tokenResp.AccessToken == "" {
		return "", 0, fmt.Errorf("empty access token in response")
	}

	return tokenResp.AccessToken, tokenResp.ExpiresIn, nil
}

// TokenURL returns the configured OAuth2 token endpoint.
func (p *TokenProvider) TokenURL() string {
	return p.tokenURL
}
