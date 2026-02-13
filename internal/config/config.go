// Package config handles loading and validating aidaemon configuration.
//
// Config is loaded from ~/.config/aidaemon/config.json.
// Missing config file → error with setup instructions.
// Missing optional fields → sensible defaults.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config holds all daemon configuration.
type Config struct {
	// Telegram bot token from BotFather.
	TelegramToken string `json:"telegram_token"`

	// Allowed Telegram user ID (your personal ID from @userinfobot).
	// Messages from other users are silently dropped.
	TelegramUserID int64 `json:"telegram_user_id"`

	// Model to use for chat (default: "gpt-4o").
	ChatModel string `json:"chat_model"`

	// Maximum conversation messages to keep per chat (default: 20).
	MaxConversationMessages int `json:"max_conversation_messages"`

	// System prompt prepended to every conversation.
	SystemPrompt string `json:"system_prompt"`

	// HTTP API port (default: 8420). Set to 0 to disable.
	Port int `json:"port"`

	// Database path (default: ~/.config/aidaemon/aidaemon.db).
	DBPath string `json:"db_path"`

	// Log level: "debug", "info", "warn", "error" (default: "info").
	LogLevel string `json:"log_level"`
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		ChatModel:               "gpt-4o",
		MaxConversationMessages: 20,
		SystemPrompt:            "You are a helpful personal assistant. Be concise and direct.",
		Port:                    8420,
		LogLevel:                "info",
	}
}

// Load reads config from ~/.config/aidaemon/config.json.
// Returns an error with setup instructions if the file doesn't exist.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"config not found at %s\n\nCreate it with:\n"+
					"  mkdir -p ~/.config/aidaemon\n"+
					"  cat > ~/.config/aidaemon/config.json << 'EOF'\n"+
					"{\n"+
					"  \"telegram_token\": \"YOUR_BOT_TOKEN_FROM_BOTFATHER\",\n"+
					"  \"telegram_user_id\": YOUR_TELEGRAM_USER_ID,\n"+
					"  \"chat_model\": \"gpt-4o\",\n"+
					"  \"system_prompt\": \"You are a helpful personal assistant.\"\n"+
					"}\n"+
					"EOF\n\n"+
					"Get your bot token: message @BotFather on Telegram\n"+
					"Get your user ID: message @userinfobot on Telegram",
				path,
			)
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	// Resolve default DB path.
	if cfg.DBPath == "" {
		dir, _ := configDir()
		cfg.DBPath = filepath.Join(dir, "aidaemon.db")
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.TelegramToken == "" {
		return fmt.Errorf("telegram_token is required")
	}
	if c.TelegramUserID == 0 {
		return fmt.Errorf("telegram_user_id is required (get it from @userinfobot on Telegram)")
	}
	if c.ChatModel == "" {
		return fmt.Errorf("chat_model is required")
	}
	if c.MaxConversationMessages < 2 {
		c.MaxConversationMessages = 2
	}
	return nil
}

// ConversationLimit returns MaxConversationMessages as a usable value.
func (c *Config) ConversationLimit() int {
	return c.MaxConversationMessages
}

// HeartbeatDuration is a placeholder for future heartbeat support.
// Currently unused but keeps the config extensible.
func (c *Config) HeartbeatDuration() time.Duration {
	return 30 * time.Minute
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "aidaemon"), nil
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}
