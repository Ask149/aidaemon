package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceDir_Default(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.WorkspaceDir != "" {
		t.Errorf("expected empty default WorkspaceDir, got %q", cfg.WorkspaceDir)
	}
}

func TestResolveWorkspaceDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{WorkspaceDir: dir}
	wsDir := cfg.ResolvedWorkspaceDir()
	if wsDir != dir {
		t.Errorf("expected %q, got %q", dir, wsDir)
	}
}

func TestResolveWorkspaceDir_Default(t *testing.T) {
	cfg := &Config{}
	wsDir := cfg.ResolvedWorkspaceDir()
	// Should end with .config/aidaemon/workspace
	want := filepath.Join(".config", "aidaemon", "workspace")
	if !strings.HasSuffix(wsDir, want) {
		t.Errorf("expected default workspace path ending with %q, got %q", want, wsDir)
	}
}

func TestValidate_NoTelegram_OK(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TelegramToken = ""
	cfg.TelegramUserID = 0
	// Should NOT error — Telegram is now optional
	if err := cfg.validate(); err != nil {
		t.Errorf("expected nil error without telegram, got: %v", err)
	}
}

func TestValidate_PartialTelegram_Error(t *testing.T) {
	tests := []struct {
		name  string
		token string
		uid   int64
	}{
		{"token without uid", "abc", 0},
		{"uid without token", "", 123},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.TelegramToken = tt.token
			cfg.TelegramUserID = tt.uid
			if err := cfg.validate(); err == nil {
				t.Error("expected error for partial telegram config")
			}
		})
	}
}

func TestTelegramEnabled(t *testing.T) {
	tests := []struct {
		name  string
		token string
		uid   int64
		want  bool
	}{
		{"both set", "abc", 123, true},
		{"both empty", "", 0, false},
		{"token only", "abc", 0, false},
		{"uid only", "", 123, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{TelegramToken: tt.token, TelegramUserID: tt.uid}
			if got := cfg.TelegramEnabled(); got != tt.want {
				t.Errorf("TelegramEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHeartbeatDuration(t *testing.T) {
	tests := []struct {
		name     string
		interval int
		want     time.Duration
	}{
		{"zero disabled", 0, 0},
		{"negative disabled", -1, 0},
		{"one minute", 1, 1 * time.Minute},
		{"thirty minutes", 30, 30 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{HeartbeatInterval: tt.interval}
			if got := cfg.HeartbeatDuration(); got != tt.want {
				t.Errorf("HeartbeatDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTokenLimit_Default(t *testing.T) {
	cfg := DefaultConfig()
	want := 128000
	if cfg.TokenLimit != want {
		t.Errorf("expected default TokenLimit=%d, got %d", want, cfg.TokenLimit)
	}
}

func TestTokenLimit_ZeroDefaultsTo128k(t *testing.T) {
	cfg := Config{
		ChatModel:  "test-model",
		TokenLimit: 0,
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() error: %v", err)
	}
	if cfg.TokenLimit != 128000 {
		t.Errorf("expected TokenLimit=128000 after validate(), got %d", cfg.TokenLimit)
	}
}

func TestDefaultProvider(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Provider != "copilot" {
		t.Errorf("expected default provider %q, got %q", "copilot", cfg.Provider)
	}
}

func TestValidate_OpenAI_RequiresBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "openai"
	cfg.ProviderConfig.APIKey = "sk-test"
	if err := cfg.validate(); err == nil {
		t.Error("expected error for openai provider without base_url")
	}
}

func TestValidate_OpenAI_RequiresAPIKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "openai"
	cfg.ProviderConfig.BaseURL = "https://api.openai.com/v1"
	if err := cfg.validate(); err == nil {
		t.Error("expected error for openai provider without api_key")
	}
}

func TestValidate_OpenAI_Valid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "openai"
	cfg.ProviderConfig.BaseURL = "https://api.openai.com/v1"
	cfg.ProviderConfig.APIKey = "sk-test"
	if err := cfg.validate(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidate_UnknownProvider(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "anthropic"
	if err := cfg.validate(); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestTeamsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
		tenantID string
		chatID   string
		want     bool
	}{
		{"all set", "cid", "tid", "chatid", true},
		{"no client", "", "tid", "chatid", false},
		{"no tenant", "cid", "", "chatid", false},
		{"no chat", "cid", "tid", "", false},
		{"all empty", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				TeamsClientID: tt.clientID,
				TeamsTenantID: tt.tenantID,
				TeamsChatID:   tt.chatID,
			}
			if got := cfg.TeamsEnabled(); got != tt.want {
				t.Errorf("TeamsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTeamsPollDuration(t *testing.T) {
	tests := []struct {
		name     string
		interval int
		want     time.Duration
	}{
		{"zero default", 0, 3 * time.Second},
		{"custom", 5, 5 * time.Second},
		{"negative default", -1, 3 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{TeamsPollInterval: tt.interval}
			if got := cfg.TeamsPollDuration(); got != tt.want {
				t.Errorf("TeamsPollDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidate_PartialTeams_Error(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
		tenantID string
		chatID   string
	}{
		{"client only", "cid", "", ""},
		{"tenant only", "", "tid", ""},
		{"chat only", "", "", "chatid"},
		{"missing chat", "cid", "tid", ""},
		{"missing tenant", "cid", "", "chatid"},
		{"missing client", "", "tid", "chatid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.TeamsClientID = tt.clientID
			cfg.TeamsTenantID = tt.tenantID
			cfg.TeamsChatID = tt.chatID
			if err := cfg.validate(); err == nil {
				t.Error("expected error for partial teams config")
			}
		})
	}
}
