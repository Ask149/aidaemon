package auth

import (
	"testing"
	"time"
)

func TestEntraToken_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"nil-like zero", time.Time{}, true},
		{"past", time.Now().Add(-1 * time.Hour), true},
		{"within margin", time.Now().Add(2 * time.Minute), true},
		{"future", time.Now().Add(1 * time.Hour), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := &EntraToken{ExpiresAt: tt.expiresAt}
			if got := tok.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEntraToken_IsExpired_Nil(t *testing.T) {
	var tok *EntraToken
	if !tok.IsExpired() {
		t.Error("nil token should be expired")
	}
}

func TestEntraTokenManager_GetToken_Cached(t *testing.T) {
	tm := &EntraTokenManager{
		token: &EntraToken{
			AccessToken:  "cached-token",
			RefreshToken: "refresh",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
		},
	}
	tok, err := tm.GetToken()
	if err != nil {
		t.Fatalf("GetToken() error: %v", err)
	}
	if tok.AccessToken != "cached-token" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "cached-token")
	}
}

func TestEntraTokenManager_GetToken_Expired_NoRefreshToken(t *testing.T) {
	tm := &EntraTokenManager{
		token: &EntraToken{
			AccessToken: "old-token",
			ExpiresAt:   time.Now().Add(-1 * time.Hour),
		},
	}
	_, err := tm.GetToken()
	if err == nil {
		t.Fatal("GetToken() should error when token is expired and no refresh token")
	}
}

func TestEntraTokenManager_HasToken(t *testing.T) {
	tm := &EntraTokenManager{}
	if tm.HasToken() {
		t.Error("HasToken() should be false with no token")
	}

	tm.token = &EntraToken{AccessToken: "tok"}
	if !tm.HasToken() {
		t.Error("HasToken() should be true with a token")
	}
}

const testDefaultScopes = "Chat.Read ChatMessage.Send User.Read offline_access"

func TestEntraTokenManager_DefaultScopes(t *testing.T) {
	tm := &EntraTokenManager{}
	if tm.scopes != "" {
		t.Errorf("zero-value scopes should be empty, got %q", tm.scopes)
	}
}

func TestEntraTokenManager_CustomScopes(t *testing.T) {
	tm := &EntraTokenManager{scopes: "Chat.ReadWrite User.Read offline_access"}
	if tm.scopes != "Chat.ReadWrite User.Read offline_access" {
		t.Errorf("scopes = %q, want custom value", tm.scopes)
	}
}

func TestResolveScopes_Default(t *testing.T) {
	got := resolveScopes("")
	if got != testDefaultScopes {
		t.Errorf("resolveScopes(\"\") = %q, want %q", got, testDefaultScopes)
	}
}

func TestNewEntraTokenManager_ScopesWiring(t *testing.T) {
	// Custom scopes flow through constructor.
	custom := "Chat.ReadWrite User.Read offline_access"
	tm, err := NewEntraTokenManager("client-id", "tenant-id", custom)
	if err != nil {
		t.Fatalf("NewEntraTokenManager() error: %v", err)
	}
	if tm.scopes != custom {
		t.Errorf("scopes = %q, want %q", tm.scopes, custom)
	}

	// Empty scopes default.
	tm2, err := NewEntraTokenManager("client-id", "tenant-id", "")
	if err != nil {
		t.Fatalf("NewEntraTokenManager() error: %v", err)
	}
	if tm2.scopes != defaultEntraScopes {
		t.Errorf("scopes = %q, want default %q", tm2.scopes, defaultEntraScopes)
	}
}

func TestResolveScopes_Custom(t *testing.T) {
	custom := "Chat.ReadWrite User.Read offline_access"
	got := resolveScopes(custom)
	if got != custom {
		t.Errorf("resolveScopes(%q) = %q, want same", custom, got)
	}
}

func TestParseTokenResponse(t *testing.T) {
	body := []byte(`{
		"access_token": "eyJ0eXAiOi...",
		"token_type": "Bearer",
		"expires_in": 3600,
		"refresh_token": "0.AXYA..."
	}`)

	tok, err := parseTokenResponse(body)
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}
	if tok.AccessToken != "eyJ0eXAiOi..." {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "eyJ0eXAiOi...")
	}
	if tok.RefreshToken != "0.AXYA..." {
		t.Errorf("RefreshToken = %q, want %q", tok.RefreshToken, "0.AXYA...")
	}
	// ExpiresAt should be roughly 1 hour from now
	if tok.ExpiresAt.Before(time.Now().Add(59 * time.Minute)) {
		t.Error("ExpiresAt should be ~1 hour from now")
	}
}

func TestParseTokenResponse_Error(t *testing.T) {
	body := []byte(`{
		"error": "invalid_grant",
		"error_description": "The refresh token has expired"
	}`)

	_, err := parseTokenResponse(body)
	if err == nil {
		t.Fatal("parseTokenResponse() should error on error response")
	}
}
