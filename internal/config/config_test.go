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
