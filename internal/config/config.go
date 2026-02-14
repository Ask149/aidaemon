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
	"strings"
	"time"
)

// Config holds all daemon configuration.
type Config struct {
	// Telegram bot token from BotFather.
	TelegramToken string `json:"telegram_token"`

	// Allowed Telegram user ID (your personal ID from @userinfobot).
	// Messages from other users are silently dropped.
	TelegramUserID int64 `json:"telegram_user_id"`

	// Model to use for chat (default: "claude-opus-4.6").
	ChatModel string `json:"chat_model"`

	// Maximum conversation messages to keep per chat (default: 20).
	MaxConversationMessages int `json:"max_conversation_messages"`

	// System prompt prepended to every conversation.
	SystemPrompt string `json:"system_prompt"`

	// HTTP API port (default: 8420). Set to 0 to disable.
	Port int `json:"port"`

	// Database path (default: ~/.config/aidaemon/aidaemon.db).
	DBPath string `json:"db_path"`

	// Data directory for media, logs, etc. (default: ~/.config/aidaemon/data).
	DataDir string `json:"data_dir"`

	// Brave Search API key (optional — enables higher-quality web search).
	// Get a free key at https://brave.com/search/api/ (2000 queries/month).
	// When empty, web_search falls back to DuckDuckGo HTML scraping.
	BraveAPIKey string `json:"brave_api_key"`

	// MCP servers to connect to on startup.
	// Each key is a server name; value describes how to launch it.
	// Example: {"playwright": {"command": "npx", "args": ["-y", "@playwright/mcp@latest", "--headless"]}}
	MCPServers map[string]MCPServerConfig `json:"mcp_servers"`

	// Log level: "debug", "info", "warn", "error" (default: "info").
	LogLevel string `json:"log_level"`
}

// MCPServerConfig describes how to launch an MCP server subprocess.
type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"`
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		ChatModel:               "claude-sonnet-4.5",
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

	// Resolve default data directory and ensure subdirectories exist.
	if cfg.DataDir == "" {
		dir, _ := configDir()
		cfg.DataDir = filepath.Join(dir, "data")
	}
	for _, sub := range []string{"media", "logs", "files"} {
		os.MkdirAll(filepath.Join(cfg.DataDir, sub), 0700)
	}

	// Load system prompt from file if it exists.
	cfg.SystemPrompt = loadSystemPrompt(cfg.SystemPrompt)

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

// loadSystemPrompt loads system prompt from file if it exists.
// If prompt is empty or starts with "@", tries to load from:
//   ~/.config/aidaemon/system_prompt.md
func loadSystemPrompt(prompt string) string {
	if prompt == "" || strings.HasPrefix(prompt, "@") {
		home, err := os.UserHomeDir()
		if err != nil {
			return prompt
		}
		
		promptPath := filepath.Join(home, ".config", "aidaemon", "system_prompt.md")
		data, err := os.ReadFile(promptPath)
		if err == nil {
			return string(data)
		}
	}
	
	return prompt
}
