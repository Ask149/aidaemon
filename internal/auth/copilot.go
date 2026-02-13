// Package auth handles GitHub Copilot authentication.
//
// The flow is:
//  1. Read a long-lived GitHub OAuth token (from disk or env).
//  2. Exchange it for a short-lived Copilot bearer token (~30 min TTL).
//  3. Refresh proactively (before expiry) and reactively (on 401).
//
// Concurrency: atomic.Value for lock-free reads, singleflight.Group
// to deduplicate concurrent refresh attempts.
package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// copilotTokenURL is the endpoint to exchange a GitHub token for a Copilot token.
	copilotTokenURL = "https://api.github.com/copilot_internal/v2/token"

	// refreshMargin is how far before expiry we proactively refresh.
	// 3 minutes gives plenty of buffer on a 30-minute TTL.
	refreshMargin = 3 * time.Minute
)

// CopilotToken is the short-lived bearer token for api.githubcopilot.com.
type CopilotToken struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"` // Unix timestamp
}

// IsExpired returns true if the token is expired or within the refresh margin.
func (t *CopilotToken) IsExpired() bool {
	if t == nil {
		return true
	}
	return time.Now().After(time.Unix(t.ExpiresAt, 0).Add(-refreshMargin))
}

// ExpiresIn returns the duration until the token expires.
func (t *CopilotToken) ExpiresIn() time.Duration {
	if t == nil {
		return 0
	}
	return time.Until(time.Unix(t.ExpiresAt, 0))
}

// TokenManager handles the lifecycle of Copilot authentication tokens.
// It reads a long-lived GitHub token from disk/env and exchanges it for
// short-lived Copilot bearer tokens, refreshing them automatically.
type TokenManager struct {
	githubToken  string
	copilotToken atomic.Value    // stores *CopilotToken
	refreshGroup singleflight.Group
	client       *http.Client
}

// NewTokenManager creates a TokenManager by finding the GitHub token.
// Token source priority:
//  1. GITHUB_TOKEN environment variable
//  2. ~/.config/github-copilot/hosts.json
//  3. ~/.config/github-copilot/apps.json
func NewTokenManager() (*TokenManager, error) {
	token, err := findGitHubToken()
	if err != nil {
		return nil, fmt.Errorf("github token: %w", err)
	}

	tm := &TokenManager{
		githubToken: token,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	return tm, nil
}

// ValidateGitHub checks that the GitHub token is valid by calling GET /user.
func (tm *TokenManager) ValidateGitHub() error {
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tm.githubToken)
	req.Header.Set("User-Agent", "aidaemon/0.1")

	resp, err := tm.client.Do(req)
	if err != nil {
		return fmt.Errorf("github API unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("github token invalid or expired (HTTP 401)")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("github API returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// GetToken returns a valid Copilot bearer token, refreshing if needed.
// Safe for concurrent use — singleflight deduplicates parallel refreshes.
func (tm *TokenManager) GetToken() (*CopilotToken, error) {
	// Fast path: atomic read, no locks.
	if tok := tm.loadToken(); tok != nil && !tok.IsExpired() {
		return tok, nil
	}

	// Slow path: refresh via singleflight (only one goroutine does the HTTP call).
	result, err, _ := tm.refreshGroup.Do("refresh", func() (interface{}, error) {
		// Double-check inside singleflight — another goroutine may have refreshed.
		if tok := tm.loadToken(); tok != nil && !tok.IsExpired() {
			return tok, nil
		}
		return tm.doRefresh()
	})
	if err != nil {
		return nil, err
	}
	return result.(*CopilotToken), nil
}

// ForceRefresh invalidates the current token and fetches a new one.
// Used on 401 from the completions API.
func (tm *TokenManager) ForceRefresh() (*CopilotToken, error) {
	// Clear the current token so the next singleflight call doesn't short-circuit.
	tm.copilotToken.Store((*CopilotToken)(nil))

	result, err, _ := tm.refreshGroup.Do("refresh", func() (interface{}, error) {
		return tm.doRefresh()
	})
	if err != nil {
		return nil, err
	}
	return result.(*CopilotToken), nil
}

// TokenExpiresIn returns how long until the current token expires.
// Returns 0 if no token is loaded.
func (tm *TokenManager) TokenExpiresIn() time.Duration {
	tok := tm.loadToken()
	if tok == nil {
		return 0
	}
	return tok.ExpiresIn()
}

// loadToken reads the current token from atomic storage.
func (tm *TokenManager) loadToken() *CopilotToken {
	v := tm.copilotToken.Load()
	if v == nil {
		return nil
	}
	return v.(*CopilotToken)
}

// doRefresh performs the actual HTTP call to exchange GitHub token for Copilot token.
func (tm *TokenManager) doRefresh() (*CopilotToken, error) {
	req, err := http.NewRequest("GET", copilotTokenURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tm.githubToken)
	req.Header.Set("User-Agent", "aidaemon/0.1")
	req.Header.Set("Accept", "application/json")

	resp, err := tm.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot token exchange: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("github token rejected by copilot (HTTP 401)")
	}
	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("copilot subscription not active for this account (HTTP 403)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("copilot token exchange failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var tok CopilotToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("parse copilot token: %w", err)
	}

	if tok.Token == "" {
		return nil, fmt.Errorf("copilot returned empty token")
	}

	tm.copilotToken.Store(&tok)
	return &tok, nil
}

// findGitHubToken tries multiple sources to find a GitHub OAuth token.
func findGitHubToken() (string, error) {
	// 1. Environment variable (highest priority).
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find home directory: %w", err)
	}

	// 2. Our own saved token from ~/.config/aidaemon/auth.json.
	if token, err := loadSavedToken(); err == nil && token != "" {
		return token, nil
	}

	// 3. OpenCode auth.json (written by `opencode` CLI).
	opencodePath := filepath.Join(home, ".local", "share", "opencode", "auth.json")
	if token, err := readOpenCodeToken(opencodePath); err == nil && token != "" {
		return token, nil
	}

	// 3. hosts.json (written by VS Code Copilot extension).
	hostsPath := filepath.Join(home, ".config", "github-copilot", "hosts.json")
	if token, err := readTokenFromJSON(hostsPath, "github.com"); err == nil && token != "" {
		return token, nil
	}

	// 4. apps.json (older Copilot installations).
	appsPath := filepath.Join(home, ".config", "github-copilot", "apps.json")
	if token, err := readTokenFromAppsJSON(appsPath); err == nil && token != "" {
		return token, nil
	}

	return "", fmt.Errorf(
		"no GitHub token found. Set GITHUB_TOKEN env var, run 'aidaemon auth', or sign in to GitHub Copilot in VS Code/OpenCode.\n"+
			"Checked: $GITHUB_TOKEN, ~/.config/aidaemon/auth.json, %s, %s, %s",
		opencodePath, hostsPath, appsPath,
	)
}

// readOpenCodeToken reads the GitHub token from OpenCode's auth.json.
// Format: {"github-copilot": {"access": "gho_xxx..."}}
func readOpenCodeToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var auth map[string]struct {
		Access string `json:"access"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return "", err
	}

	if entry, ok := auth["github-copilot"]; ok && entry.Access != "" {
		return entry.Access, nil
	}

	return "", fmt.Errorf("no github-copilot entry in %s", path)
}

// readTokenFromJSON reads an oauth_token from a JSON file like:
// {"github.com": {"oauth_token": "gho_xxx..."}}
func readTokenFromJSON(path string, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	// Try direct key → oauth_token structure.
	var hosts map[string]map[string]string
	if err := json.Unmarshal(data, &hosts); err != nil {
		return "", err
	}

	if entry, ok := hosts[key]; ok {
		if token, ok := entry["oauth_token"]; ok {
			return token, nil
		}
	}

	return "", fmt.Errorf("no oauth_token for %q in %s", key, path)
}

// readTokenFromAppsJSON reads a token from apps.json.
// Format varies: might be {"github.com:<client_id>": {"oauth_token": "..."}}
func readTokenFromAppsJSON(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var apps map[string]map[string]string
	if err := json.Unmarshal(data, &apps); err != nil {
		return "", err
	}

	// Find any key containing "github.com" and extract oauth_token.
	for key, entry := range apps {
		if len(key) > 10 { // "github.com" is 10 chars, key is "github.com:<client_id>"
			if token, ok := entry["oauth_token"]; ok {
				return token, nil
			}
		}
	}

	return "", fmt.Errorf("no token found in %s", path)
}
