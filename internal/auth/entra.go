// Package auth — Entra ID (Azure AD) token management for Microsoft Graph API.
//
// Uses a simple mutex (not atomic.Value + singleflight like Copilot) because:
//   - Entra tokens have a 1-hour TTL (vs Copilot's 30-min)
//   - Only one Teams channel polling goroutine (no concurrent access pressure)
//   - Token is persisted to disk (unlike Copilot which re-fetches from GitHub)
//
// Flow:
//  1. Device code flow for initial auth (interactive, one-time).
//  2. Persist token to ~/.config/aidaemon/entra_token.json.
//  3. On startup, load persisted token and refresh via refresh_token.
package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// entraRefreshMargin is how far before expiry we proactively refresh.
	// 5 minutes gives buffer on a 1-hour TTL.
	entraRefreshMargin = 5 * time.Minute

	// entraTokenFile is the filename for persisted Entra tokens.
	entraTokenFile = "entra_token.json"

	// defaultEntraScopes are the Microsoft Graph permissions requested by default.
	// Chat.Read (delegated, no admin consent) + ChatMessage.Send (delegated).
	defaultEntraScopes = "Chat.Read ChatMessage.Send User.Read offline_access"
)

// entraEndpoints builds Microsoft identity platform URLs for a tenant.
func entraTokenURL(tenantID string) string {
	return fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID)
}

func entraDeviceCodeURL(tenantID string) string {
	return fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/devicecode", tenantID)
}

// EntraToken is an OAuth2 token from Microsoft Entra ID.
type EntraToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// IsExpired returns true if the token is expired or within the refresh margin.
func (t *EntraToken) IsExpired() bool {
	if t == nil {
		return true
	}
	return time.Now().After(t.ExpiresAt.Add(-entraRefreshMargin))
}

// EntraTokenManager manages the lifecycle of Entra ID tokens.
// It persists tokens to disk and refreshes them via refresh_token.
type EntraTokenManager struct {
	clientID string
	tenantID string
	scopes   string // resolved scopes (never empty after construction)
	mu       sync.Mutex
	token    *EntraToken
	path     string // path to persisted token file
	client   *http.Client
}

// resolveScopes returns scopes if non-empty, otherwise the default.
func resolveScopes(scopes string) string {
	if scopes == "" {
		return defaultEntraScopes
	}
	return scopes
}

// NewEntraTokenManager creates an EntraTokenManager and loads any persisted token.
func NewEntraTokenManager(clientID, tenantID, scopes string) (*EntraTokenManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find home directory: %w", err)
	}
	tokenPath := filepath.Join(home, ".config", "aidaemon", entraTokenFile)

	tm := &EntraTokenManager{
		clientID: clientID,
		tenantID: tenantID,
		path:     tokenPath,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
	tm.scopes = resolveScopes(scopes)

	// Try to load persisted token.
	if data, err := os.ReadFile(tokenPath); err == nil {
		var tok EntraToken
		if err := json.Unmarshal(data, &tok); err == nil && tok.AccessToken != "" {
			tm.token = &tok
		}
	}

	return tm, nil
}

// GetToken returns a valid Entra token, refreshing if needed.
func (tm *EntraTokenManager) GetToken() (*EntraToken, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Fast path: cached token still valid. Return a copy to avoid mutation.
	if tm.token != nil && !tm.token.IsExpired() {
		tok := *tm.token
		return &tok, nil
	}

	// Need to refresh.
	if tm.token == nil || tm.token.RefreshToken == "" {
		return nil, fmt.Errorf("entra: no valid token and no refresh token — run device flow to authenticate")
	}

	tok, err := tm.doRefresh(tm.token.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("entra: refresh failed: %w", err)
	}

	tm.token = tok
	if err := tm.persist(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Could not save Entra token: %v\n", err)
	}

	result := *tm.token
	return &result, nil
}

// HasToken returns true if a token exists (may be expired).
func (tm *EntraTokenManager) HasToken() bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.token != nil && tm.token.AccessToken != ""
}

// SetToken stores a token (e.g. after device code flow) and persists it to disk.
func (tm *EntraTokenManager) SetToken(tok *EntraToken) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.token = tok
	return tm.persist()
}

// doRefresh exchanges a refresh token for a new access + refresh token.
func (tm *EntraTokenManager) doRefresh(refreshToken string) (*EntraToken, error) {
	form := url.Values{
		"client_id":     {tm.clientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {tm.scopes},
	}

	req, err := http.NewRequest("POST", entraTokenURL(tm.tenantID), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tm.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token endpoint HTTP %d: %s", resp.StatusCode, string(body))
	}

	return parseTokenResponse(body)
}

// persist saves the current token to disk as JSON.
func (tm *EntraTokenManager) persist() error {
	dir := filepath.Dir(tm.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(tm.token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	return os.WriteFile(tm.path, data, 0600)
}

// parseTokenResponse parses an OAuth2 token response into an EntraToken.
func parseTokenResponse(body []byte) (*EntraToken, error) {
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	if raw.Error != "" {
		return nil, fmt.Errorf("entra oauth error: %s: %s", raw.Error, raw.ErrorDesc)
	}

	if raw.AccessToken == "" {
		return nil, fmt.Errorf("entra: empty access token in response")
	}

	return &EntraToken{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second),
	}, nil
}

// RunEntraDeviceFlow performs the Microsoft Entra ID device code flow.
// It prints instructions for the user, polls until auth completes, and returns the token.
func RunEntraDeviceFlow(clientID, tenantID, scopes string) (*EntraToken, error) {
	resolved := resolveScopes(scopes)
	client := &http.Client{Timeout: 10 * time.Second}

	// Step 1: Request device code.
	form := url.Values{
		"client_id": {clientID},
		"scope":     {resolved},
	}
	req, err := http.NewRequest("POST", entraDeviceCodeURL(tenantID), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device code request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read device code response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("device code failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var dcr struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Message         string `json:"message"`
	}
	if err := json.Unmarshal(body, &dcr); err != nil {
		return nil, fmt.Errorf("parse device code response: %w", err)
	}

	// Step 2: Instruct user.
	fmt.Printf("\n🔐 Microsoft Entra ID Authorization\n")
	fmt.Printf("   1. Open: %s\n", dcr.VerificationURI)
	fmt.Printf("   2. Enter code: %s\n", dcr.UserCode)
	fmt.Printf("   Waiting for authorization...\n\n")

	// Step 3: Poll for token.
	interval := time.Duration(dcr.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dcr.ExpiresIn) * time.Second)

	tokenURL := entraTokenURL(tenantID)
	for time.Now().Before(deadline) {
		time.Sleep(interval)

		pollForm := url.Values{
			"client_id":   {clientID},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {dcr.DeviceCode},
		}

		pollReq, err := http.NewRequest("POST", tokenURL, strings.NewReader(pollForm.Encode()))
		if err != nil {
			return nil, fmt.Errorf("build poll request: %w", err)
		}
		pollReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		pollResp, err := client.Do(pollReq)
		if err != nil {
			return nil, fmt.Errorf("poll token: %w", err)
		}

		pollBody, err := io.ReadAll(pollResp.Body)
		pollResp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read poll response: %w", err)
		}

		// Check for pending/error states.
		var result struct {
			Error     string `json:"error"`
			ErrorDesc string `json:"error_description"`
		}
		if err := json.Unmarshal(pollBody, &result); err == nil && result.Error != "" {
			switch result.Error {
			case "authorization_pending":
				continue
			case "slow_down":
				interval += 5 * time.Second
				continue
			case "expired_token":
				return nil, fmt.Errorf("device code expired — please try again")
			case "access_denied":
				return nil, fmt.Errorf("authorization denied by user")
			default:
				return nil, fmt.Errorf("entra oauth error: %s: %s", result.Error, result.ErrorDesc)
			}
		}

		// Success — parse the full token.
		tok, err := parseTokenResponse(pollBody)
		if err != nil {
			return nil, fmt.Errorf("parse token: %w", err)
		}

		fmt.Printf("✅ Authorized with Microsoft Entra ID!\n\n")
		return tok, nil
	}

	return nil, fmt.Errorf("device authorization timed out after %d seconds", dcr.ExpiresIn)
}
