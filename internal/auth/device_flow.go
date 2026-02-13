// Package auth handles GitHub Copilot authentication, including
// the OAuth device code flow for initial setup.
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
	"time"
)

const (
	// VS Code Copilot extension's registered OAuth App client ID.
	// Used by OpenCode, Crush, CopilotForXcode, and other third-party tools.
	copilotClientID = "Iv1.b507a08c87ecfe98"

	deviceCodeURL    = "https://github.com/login/device/code"
	accessTokenURL   = "https://github.com/login/oauth/access_token"
	deviceCodeScope  = "read:user"
)

// DeviceCodeResponse is the response from POST /login/device/code.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// RunDeviceFlow performs the GitHub OAuth device code flow and returns a GitHub token.
// It prints instructions for the user and polls until auth is complete.
func RunDeviceFlow() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	// Step 1: Request device code.
	form := url.Values{
		"client_id": {copilotClientID},
		"scope":     {deviceCodeScope},
	}
	req, err := http.NewRequest("POST", deviceCodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("device code request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("device code failed: HTTP %d: %s", resp.StatusCode, body)
	}

	var dcr DeviceCodeResponse
	if err := json.Unmarshal(body, &dcr); err != nil {
		return "", fmt.Errorf("parse device code response: %w", err)
	}

	// Step 2: Instruct user.
	fmt.Printf("\n🔐 GitHub Device Authorization\n")
	fmt.Printf("   1. Open: %s\n", dcr.VerificationURI)
	fmt.Printf("   2. Enter code: %s\n", dcr.UserCode)
	fmt.Printf("   Waiting for authorization...\n\n")

	// Step 3: Poll for access token.
	interval := time.Duration(dcr.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dcr.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(interval)

		token, done, err := pollAccessToken(client, dcr.DeviceCode)
		if err != nil {
			return "", err
		}
		if done {
			fmt.Printf("✅ Authorized!\n\n")
			// Save the token for future use.
			if err := saveToken(token); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Could not save token: %v\n", err)
			}
			return token, nil
		}
	}

	return "", fmt.Errorf("device authorization timed out after %d seconds", dcr.ExpiresIn)
}

// pollAccessToken checks if the user has completed authorization.
func pollAccessToken(client *http.Client, deviceCode string) (string, bool, error) {
	form := url.Values{
		"client_id":   {copilotClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	req, err := http.NewRequest("POST", accessTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("poll access token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", false, fmt.Errorf("parse poll response: %w", err)
	}

	switch result.Error {
	case "":
		// Success!
		return result.AccessToken, true, nil
	case "authorization_pending":
		// User hasn't entered code yet — keep polling.
		return "", false, nil
	case "slow_down":
		// We're polling too fast — back off (handled by caller's interval).
		return "", false, nil
	case "expired_token":
		return "", false, fmt.Errorf("device code expired — please try again")
	case "access_denied":
		return "", false, fmt.Errorf("authorization denied by user")
	default:
		return "", false, fmt.Errorf("oauth error: %s", result.Error)
	}
}

// saveToken persists the GitHub token to ~/.config/aidaemon/auth.json.
func saveToken(token string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "aidaemon")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data := map[string]string{"github_token": token}
	b, _ := json.MarshalIndent(data, "", "  ")
	return os.WriteFile(filepath.Join(dir, "auth.json"), b, 0600)
}

// loadSavedToken reads a previously saved token from ~/.config/aidaemon/auth.json.
func loadSavedToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "aidaemon", "auth.json"))
	if err != nil {
		return "", err
	}
	var auth map[string]string
	if err := json.Unmarshal(data, &auth); err != nil {
		return "", err
	}
	if token, ok := auth["github_token"]; ok && token != "" {
		return token, nil
	}
	return "", fmt.Errorf("no token in auth.json")
}
